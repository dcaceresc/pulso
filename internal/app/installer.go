package app

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	soloistCDN             = "https://soloist-builds.spotifycdn.com/"
	soloistWindowsArchive  = soloistCDN + "soloist_release_windows_x86_64.zip"
	soloistLinuxArchive    = soloistCDN + "soloist_release_x86_64.tar.gz"
	soloistInstallTimeout  = 2 * time.Minute
	soloistDownloadLimit   = 64 << 20
	soloistExecutableLimit = 96 << 20
	soloistTarLimit        = 128 << 20
	soloistZIPIndexLimit   = 2 << 20
	soloistEntryLimit      = 1024
)

// installSoloist installs to an absent destination without launching Soloist.
func installSoloist(ctx context.Context, destination string) error {
	return installSoloistForPlatform(ctx, destination, runtime.GOOS, runtime.GOARCH, nil)
}

func installSoloistForPlatform(ctx context.Context, destination, goos, goarch string, transport http.RoundTripper) error {
	var url, executable string
	switch {
	case goos == "windows" && goarch == "amd64":
		url, executable = soloistWindowsArchive, "soloist.exe"
	case goos == "linux" && goarch == "amd64":
		url, executable = soloistLinuxArchive, "soloist"
	default:
		return fmt.Errorf("unsupported Soloist platform: %s/%s", goos, goarch)
	}

	ctx, cancel := context.WithTimeout(ctx, soloistInstallTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return &os.PathError{Op: "install Soloist", Path: destination, Err: fs.ErrExist}
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := downloadSoloist(ctx, url, transport)
	if err != nil {
		return err
	}

	dir := filepath.Dir(destination)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create Soloist install directory: %w", err)
	}
	stage, err := os.CreateTemp(dir, ".soloist-install-*")
	if err != nil {
		return fmt.Errorf("stage Soloist: %w", err)
	}
	defer os.Remove(stage.Name())
	defer stage.Close()

	if goos == "windows" {
		err = extractSoloistZIP(ctx, data, executable, stage)
	} else {
		err = extractSoloistTar(ctx, data, executable, stage)
	}
	if err != nil {
		return fmt.Errorf("inspect Soloist archive: %w", err)
	}
	if err := stage.Chmod(0700); err != nil {
		return fmt.Errorf("set Soloist permissions: %w", err)
	}
	if err := stage.Close(); err != nil {
		return fmt.Errorf("close staged Soloist: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// A same-directory hard link atomically publishes the file without replacing
	// an existing name. A filesystem without hard links fails safely.
	if err := os.Link(stage.Name(), destination); err != nil {
		return fmt.Errorf("publish Soloist: %w", err)
	}
	return nil
}

func downloadSoloist(ctx context.Context, url string, transport http.RoundTripper) ([]byte, error) {
	client := &http.Client{
		Transport: transport,
		// Never follow a redirect away from the pinned HTTPS release URL.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download Soloist: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download Soloist: HTTP %d", resp.StatusCode)
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return nil, fmt.Errorf("download Soloist: unexpected HTTP content encoding %q", encoding)
	}
	if resp.ContentLength > soloistDownloadLimit {
		return nil, fmt.Errorf("Soloist download exceeds %d bytes", soloistDownloadLimit)
	}
	data, err := io.ReadAll(io.LimitReader(soloistContextReader{ctx, resp.Body}, soloistDownloadLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read Soloist download: %w", err)
	}
	if len(data) > soloistDownloadLimit {
		return nil, fmt.Errorf("Soloist download exceeds %d bytes", soloistDownloadLimit)
	}
	return data, ctx.Err()
}

type soloistContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r soloistContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func soloistArchiveName(name string, dir bool) (string, error) {
	if dir {
		name = strings.TrimSuffix(name, "/")
	}
	name = strings.TrimPrefix(name, "./")
	if !fs.ValidPath(name) || strings.ContainsAny(name, "\\:\x00") || (name == "." && !dir) {
		return "", fmt.Errorf("unsafe archive name %q", name)
	}
	return path.Base(name), nil
}

// Call only after validating the archive name and regular entry type.
func isSoloistReleaseDoc(name string) bool {
	switch strings.TrimPrefix(name, "./") {
	case "CHANGELOG.md", "THIRD_PARTY_LICENSES.txt":
		return true
	}
	return false
}

// zip.NewReader materializes the whole index before callers can count entries.
// Cap index reads separately from compressed file data reads during extraction.
type soloistZIPReader struct {
	ctx          context.Context
	data         *bytes.Reader
	remaining    int
	readingIndex bool
}

func (r *soloistZIPReader) ReadAt(p []byte, off int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.readingIndex {
		if len(p) > r.remaining {
			return 0, fmt.Errorf("ZIP index exceeds %d bytes", soloistZIPIndexLimit)
		}
		r.remaining -= len(p)
	}
	return r.data.ReadAt(p, off)
}

func extractSoloistZIP(ctx context.Context, data []byte, executable string, out io.Writer) error {
	reader := &soloistZIPReader{ctx: ctx, data: bytes.NewReader(data), remaining: soloistZIPIndexLimit, readingIndex: true}
	archive, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return fmt.Errorf("open ZIP: %w", err)
	}
	reader.readingIndex = false
	if len(archive.File) > soloistEntryLimit {
		return fmt.Errorf("too many ZIP entries")
	}
	var match *zip.File
	for _, file := range archive.File {
		mode := file.Mode()
		dir := mode.Type() == fs.ModeDir && file.UncompressedSize64 == 0
		name, err := soloistArchiveName(file.Name, dir)
		if err != nil {
			return fmt.Errorf("ZIP: %w", err)
		}
		if file.Flags&1 != 0 || (!dir && !mode.IsRegular()) {
			return fmt.Errorf("non-regular or encrypted ZIP entry %q", file.Name)
		}
		if dir || isSoloistReleaseDoc(file.Name) {
			continue
		}
		if name != executable {
			return fmt.Errorf("unexpected ZIP file %q", file.Name)
		}
		if match != nil {
			return fmt.Errorf("multiple Soloist executables")
		}
		match = file
	}
	if match == nil {
		return fmt.Errorf("missing Soloist executable")
	}
	if match.UncompressedSize64 == 0 || match.UncompressedSize64 > soloistExecutableLimit {
		return fmt.Errorf("invalid Soloist executable size: %d bytes", match.UncompressedSize64)
	}
	entry, err := match.Open()
	if err != nil {
		return fmt.Errorf("open ZIP executable: %w", err)
	}
	defer entry.Close()
	// archive/zip may skip CRC comparison for a zero header CRC without a descriptor.
	checksum := crc32.NewIEEE()
	n, err := io.Copy(io.MultiWriter(out, checksum), io.LimitReader(soloistContextReader{ctx, entry}, soloistExecutableLimit+1))
	if err != nil {
		return fmt.Errorf("read ZIP executable: %w", err)
	}
	if checksum.Sum32() != match.CRC32 {
		return fmt.Errorf("ZIP executable checksum mismatch")
	}
	if n != int64(match.UncompressedSize64) {
		return fmt.Errorf("ZIP executable size does not match its header")
	}
	return nil
}

func extractSoloistTar(ctx context.Context, data []byte, executable string, out io.Writer) error {
	compressed := bytes.NewReader(data)
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	gz.Multistream(false)
	limit := &io.LimitedReader{R: soloistContextReader{ctx, gz}, N: soloistTarLimit + 1}
	archive := tar.NewReader(limit)
	var found bool
	var entrySize int64
	for count := 0; ; count++ {
		before := limit.N
		header, err := archive.Next()
		if limit.N == 0 {
			return fmt.Errorf("expanded Soloist tar exceeds %d bytes", soloistTarLimit)
		}
		if err == io.EOF {
			const block = 512
			// archive/tar also reports EOF for missing zero blocks. Require both.
			if before-limit.N != (block-entrySize%block)%block+2*block {
				return fmt.Errorf("missing or invalid tar end marker")
			}
			break
		}
		if err != nil {
			return fmt.Errorf("read tar header: %w", err)
		}
		if count == soloistEntryLimit {
			return fmt.Errorf("too many tar entries")
		}
		entrySize = header.Size
		dir := header.Typeflag == tar.TypeDir && entrySize == 0
		name, err := soloistArchiveName(header.Name, dir)
		if err != nil {
			return err
		}
		if dir {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return fmt.Errorf("non-regular tar entry %q", header.Name)
		}
		for key := range header.PAXRecords {
			if strings.HasPrefix(key, "GNU.sparse.") {
				return fmt.Errorf("non-regular sparse tar entry %q", header.Name)
			}
		}
		if isSoloistReleaseDoc(header.Name) {
			// Drain the body through the expanded-size limit so Next reads only
			// its block padding before the next header or end marker.
			_, err := io.CopyN(io.Discard, archive, entrySize)
			if limit.N == 0 {
				return fmt.Errorf("expanded Soloist tar exceeds %d bytes", soloistTarLimit)
			}
			if err != nil {
				return fmt.Errorf("skip tar release document: %w", err)
			}
			continue
		}
		if name != executable {
			return fmt.Errorf("unexpected tar file %q", header.Name)
		}
		if found {
			return fmt.Errorf("multiple Soloist executables")
		}
		if entrySize <= 0 || entrySize > soloistExecutableLimit {
			return fmt.Errorf("invalid Soloist executable size: %d bytes", entrySize)
		}
		if _, err := io.CopyN(out, archive, entrySize); err != nil {
			return fmt.Errorf("read tar executable: %w", err)
		}
		found = true
	}
	// Read through gzip EOF to validate its checksum, permitting only tar block
	// padding after the end marker and no extra compressed member or bytes.
	padding, err := io.Copy(soloistZeroPadding{}, limit)
	if err != nil {
		return fmt.Errorf("finish gzip: %w", err)
	}
	if limit.N == 0 {
		return fmt.Errorf("expanded Soloist tar exceeds %d bytes", soloistTarLimit)
	}
	if padding%512 != 0 || compressed.Len() != 0 {
		return fmt.Errorf("invalid tar padding or trailing gzip data")
	}
	if !found {
		return fmt.Errorf("missing Soloist executable")
	}
	return nil
}

type soloistZeroPadding struct{}

func (soloistZeroPadding) Write(p []byte) (int, error) {
	for i, b := range p {
		if b != 0 {
			return i, fmt.Errorf("non-zero tar padding")
		}
	}
	return len(p), nil
}
