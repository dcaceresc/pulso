package spotify

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The session record holds only what a silent restore needs. It is the one
// place Pulso writes a token, and it is private to the user.
const spotifySessionReadLimit = 4096

type spotifySession struct {
	access, refresh string
	expires         time.Time
}

// spotifySessionRecord is the on-disk shape. Keys are decoded one by one so an
// unknown, duplicated, or malformed field means "no session", never a partial one.
type spotifySessionRecord struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Expires string `json:"expires_at"`
}

func (s spotifySession) token() spotifyToken {
	return spotifyToken{access: s.access, refresh: s.refresh, expires: s.expires}
}

func (s spotifySession) normalized() (spotifySession, bool) {
	s.access = strings.TrimSpace(s.access)
	s.refresh = strings.TrimSpace(s.refresh)
	if !spotifyBearer.MatchString(s.access) || !spotifyBearer.MatchString(s.refresh) || s.expires.IsZero() {
		return spotifySession{}, false
	}
	return s, true
}

func spotifySessionPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pulso", "spotify-session.json"), nil
}

// loadSpotifySessionAt reads one bounded, regular file. Anything unexpected
// leaves the caller with "no session" so the interactive flow can run.
func loadSpotifySessionAt(path string) (spotifySession, bool) {
	entry, err := os.Lstat(path)
	if err != nil || !entry.Mode().IsRegular() || entry.Size() > spotifySessionReadLimit {
		return spotifySession{}, false
	}
	file, err := os.Open(path)
	if err != nil {
		return spotifySession{}, false
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(entry, opened) {
		return spotifySession{}, false
	}
	data, err := io.ReadAll(io.LimitReader(file, spotifySessionReadLimit+1))
	if err != nil || len(data) > spotifySessionReadLimit {
		return spotifySession{}, false
	}
	return decodeSpotifySession(data)
}

func decodeSpotifySession(data []byte) (spotifySession, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return spotifySession{}, false
	}
	var record spotifySessionRecord
	var seenAccess, seenRefresh, seenExpires bool
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return spotifySession{}, false
		}
		value, err := decoder.Token()
		text, ok := value.(string)
		if err != nil || !ok {
			return spotifySession{}, false
		}
		switch {
		case key == "access_token" && !seenAccess:
			record.Access, seenAccess = text, true
		case key == "refresh_token" && !seenRefresh:
			record.Refresh, seenRefresh = text, true
		case key == "expires_at" && !seenExpires:
			record.Expires, seenExpires = text, true
		default:
			return spotifySession{}, false
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return spotifySession{}, false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return spotifySession{}, false
	}
	expires, err := time.Parse(time.RFC3339Nano, record.Expires)
	if err != nil {
		return spotifySession{}, false
	}
	session := spotifySession{access: record.Access, refresh: record.Refresh, expires: expires}
	return session.normalized()
}

// saveSpotifySessionAt stages the complete record next to its destination so a
// failed write or rename leaves the previous session in place.
func saveSpotifySessionAt(path string, session spotifySession) error {
	session, ok := session.normalized()
	if !ok {
		return errors.New("invalid Spotify session")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if entry, err := os.Lstat(dir); err != nil || !entry.IsDir() {
		return errors.New("Spotify session directory is not a regular directory")
	}
	if entry, err := os.Lstat(path); err == nil {
		if !entry.Mode().IsRegular() {
			return errors.New("Spotify session path is not a regular file")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	stage, err := os.CreateTemp(dir, ".spotify-session-*")
	if err != nil {
		return err
	}
	defer os.Remove(stage.Name())
	defer stage.Close()
	if err := stage.Chmod(0o600); err != nil {
		return err
	}
	record := spotifySessionRecord{Access: session.access, Refresh: session.refresh, Expires: session.expires.UTC().Format(time.RFC3339Nano)}
	if err := json.NewEncoder(stage).Encode(record); err != nil {
		return err
	}
	if err := stage.Sync(); err != nil {
		return err
	}
	if err := stage.Close(); err != nil {
		return err
	}
	return os.Rename(stage.Name(), path)
}

// deleteSpotifySessionAt removes only the session file itself.
func deleteSpotifySessionAt(path string) error {
	if path == "" {
		return nil
	}
	entry, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !entry.Mode().IsRegular() {
		return errors.New("Spotify session path is not a regular file")
	}
	return os.Remove(path)
}
