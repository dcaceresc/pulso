package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type soloistFixtureEntry struct {
	name, data string
	kind       byte
}

type soloistFixtureTransport func(*http.Request) (*http.Response, error)

func (f soloistFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func soloistZipFixture(t *testing.T, entries ...soloistFixtureEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		if entry.kind == tar.TypeSymlink {
			header.SetMode(os.ModeSymlink | 0777)
		}
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func soloistTarFixture(t *testing.T, trim int, entries ...soloistFixtureEntry) []byte {
	t.Helper()
	var raw, buf bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, entry := range entries {
		h := &tar.Header{Name: entry.name, Mode: 0755, Size: int64(len(entry.data)), Typeflag: entry.kind}
		if entry.kind == tar.TypeSymlink || entry.kind == tar.TypeLink {
			h.Linkname = "other"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw.Bytes()[:raw.Len()-trim]); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInstallSoloist(t *testing.T) {
	t.Run("ZIP rejects incorrect zero CRC without descriptor", func(t *testing.T) {
		var raw bytes.Buffer
		zw := zip.NewWriter(&raw)
		const payload = "fixture executable"
		w, err := zw.CreateRaw(&zip.FileHeader{
			Name: "soloist.exe", Method: zip.Store,
			CompressedSize64:   uint64(len(payload)),
			UncompressedSize64: uint64(len(payload)),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, payload); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "soloist.exe")
		transport := soloistFixtureTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(raw.Bytes()))}, nil
		})
		err = installSoloistForPlatform(context.Background(), dest, "windows", "amd64", transport)
		if err == nil || !strings.Contains(err.Error(), "checksum") {
			t.Errorf("install error = %v, want checksum error", err)
		}
		if _, err := os.Lstat(dest); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("rejected archive left a destination: stat error = %v", err)
		}
	})

	const payload = "soloist fixture executable"
	win := soloistFixtureEntry{name: "release/soloist.exe", data: payload}
	linux := soloistFixtureEntry{name: "./release/soloist", data: payload}
	zipOK := soloistZipFixture(t, soloistFixtureEntry{name: "release/"}, win)
	tarOK := soloistTarFixture(t, 0, soloistFixtureEntry{name: "./release/", kind: tar.TypeDir}, linux)
	badZipChecksum := bytes.Replace(zipOK, []byte(payload), []byte(strings.Repeat("x", len(payload))), 1)
	badGzipChecksum := bytes.Clone(tarOK)
	badGzipChecksum[len(badGzipChecksum)-8] ^= 1
	manyDirs := make([]soloistFixtureEntry, 1025)
	for i := range manyDirs {
		manyDirs[i].name = "release/"
	}
	largeIndex := make([]soloistFixtureEntry, 600)
	for i := range largeIndex {
		largeIndex[i].name = strings.Repeat("a", 4096) + "/"
	}
	for _, tt := range []struct {
		name, goos, arch string
		archive          []byte
		status           int
		length           int64
		wantErr          string
		wantIs           error
		existing, racing bool
		cancel           bool
		cancelInFlight   bool
		wantRequests     int
	}{
		{name: "Windows ZIP", goos: "windows", archive: zipOK, wantRequests: 1},
		{name: "Linux tar.gz", goos: "linux", archive: tarOK, wantRequests: 1},
		{name: "unsupported OS", goos: "darwin", wantErr: "unsupported Soloist platform"},
		{name: "unsupported architecture", goos: "linux", arch: "arm64", wantErr: "unsupported Soloist platform"},
		{name: "existing destination", goos: "linux", existing: true, wantIs: os.ErrExist},
		{name: "destination created during download", goos: "windows", archive: zipOK, racing: true, wantIs: os.ErrExist, wantRequests: 1},
		{name: "canceled context", goos: "linux", cancel: true, wantIs: context.Canceled},
		{name: "canceled download", goos: "linux", archive: tarOK, cancelInFlight: true, wantIs: context.Canceled, wantRequests: 1},
		{name: "redirect", goos: "linux", status: http.StatusFound, wantErr: "HTTP 302", wantRequests: 1},
		{name: "HTTP error", goos: "linux", status: http.StatusNotFound, wantErr: "HTTP 404", wantRequests: 1},
		{name: "oversize response", goos: "linux", length: 1 << 30, wantErr: "download exceeds", wantRequests: 1},
		{name: "truncated ZIP", goos: "windows", archive: zipOK[:len(zipOK)-1], wantErr: "ZIP", wantRequests: 1},
		{name: "bad ZIP checksum", goos: "windows", archive: badZipChecksum, wantErr: "checksum", wantRequests: 1},
		{name: "duplicate ZIP executable", goos: "windows", archive: soloistZipFixture(t, win, win), wantErr: "multiple Soloist executables", wantRequests: 1},
		{name: "ZIP link", goos: "windows", archive: soloistZipFixture(t, win, soloistFixtureEntry{name: "link", data: "other", kind: tar.TypeSymlink}), wantErr: "non-regular", wantRequests: 1},
		{name: "ZIP traversal", goos: "windows", archive: soloistZipFixture(t, soloistFixtureEntry{name: "../soloist.exe", data: payload}), wantErr: "ZIP", wantRequests: 1},
		{name: "too many ZIP entries", goos: "windows", archive: soloistZipFixture(t, manyDirs...), wantErr: "too many ZIP entries", wantRequests: 1},
		{name: "oversize ZIP index", goos: "windows", archive: soloistZipFixture(t, largeIndex...), wantErr: "ZIP index exceeds", wantRequests: 1},
		{name: "tar symlink", goos: "linux", archive: soloistTarFixture(t, 0, linux, soloistFixtureEntry{name: "link", kind: tar.TypeSymlink}), wantErr: "non-regular", wantRequests: 1},
		{name: "tar hard link", goos: "linux", archive: soloistTarFixture(t, 0, linux, soloistFixtureEntry{name: "link", kind: tar.TypeLink}), wantErr: "non-regular", wantRequests: 1},
		{name: "missing executable", goos: "linux", archive: soloistTarFixture(t, 0), wantErr: "missing Soloist executable", wantRequests: 1},
		{name: "tar missing end marker", goos: "linux", archive: soloistTarFixture(t, 512, linux), wantErr: "tar end marker", wantRequests: 1},
		{name: "bad gzip checksum", goos: "linux", archive: badGzipChecksum, wantErr: "checksum", wantRequests: 1},
		{name: "trailing gzip member", goos: "linux", archive: append(bytes.Clone(tarOK), tarOK...), wantErr: "trailing gzip data", wantRequests: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			name, url := "soloist", "https://soloist-builds.spotifycdn.com/soloist_release_x86_64.tar.gz"
			if tt.goos == "windows" {
				name, url = "soloist.exe", "https://soloist-builds.spotifycdn.com/soloist_release_windows_x86_64.zip"
			}
			destination := filepath.Join(dir, "bin", name)
			putOriginal := func() {
				if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(destination, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.existing {
				putOriginal()
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancel {
				cancel()
			}
			calls := 0
			transport := soloistFixtureTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet || r.URL.String() != url {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
				}
				if _, ok := r.Context().Deadline(); !ok {
					t.Fatal("download has no deadline")
				}
				if tt.racing {
					putOriginal()
				}
				if tt.cancelInFlight {
					cancel()
				}
				status := tt.status
				if status == 0 {
					status = http.StatusOK
				}
				return &http.Response{
					StatusCode: status, ContentLength: tt.length,
					Body: io.NopCloser(bytes.NewReader(tt.archive)), Header: http.Header{"Location": {"https://example.invalid/redirect"}},
				}, nil
			})
			arch := tt.arch
			if arch == "" {
				arch = "amd64"
			}
			err := installSoloistForPlatform(ctx, destination, tt.goos, arch, transport)
			switch {
			case tt.wantIs != nil && !errors.Is(err, tt.wantIs):
				t.Fatalf("error = %v, want %v", err, tt.wantIs)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			case tt.wantIs == nil && tt.wantErr == "" && err != nil:
				t.Fatal(err)
			}
			if calls != tt.wantRequests {
				t.Fatalf("requests = %d, want %d", calls, tt.wantRequests)
			}
			contents, readErr := os.ReadFile(destination)
			switch {
			case tt.existing || tt.racing:
				if string(contents) != "original" || readErr != nil {
					t.Fatalf("existing destination changed: %q, %v", contents, readErr)
				}
			case err != nil:
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatalf("rejected input left a destination: %q, %v", contents, readErr)
				}
			default:
				if string(contents) != payload || readErr != nil {
					t.Fatalf("destination = %q, %v", contents, readErr)
				}
				info, err := os.Stat(destination)
				if err != nil {
					t.Fatal(err)
				}
				if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
					t.Fatalf("unexpected destination mode: %v", info.Mode())
				}
			}
			entries, err := os.ReadDir(filepath.Dir(destination))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() != name {
					t.Errorf("unexpected file left behind: %s", entry.Name())
				}
			}
		})
	}
}
