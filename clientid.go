package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const clientIDLimit = 64

// loadClientID remembers only the public Spotify Client ID. Access tokens are
// never written; a missing or unusable file simply means "ask again".
func loadClientID() string {
	path, err := clientIDPath()
	if err != nil {
		return ""
	}
	return loadClientIDAt(path)
}

func saveClientID(id string) error {
	path, err := clientIDPath()
	if err != nil {
		return err
	}
	return saveClientIDAt(path, id)
}

func clientIDPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pulso", "client-id"), nil
}

func loadClientIDAt(path string) string {
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() || info.Size() > clientIDLimit {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	id, ok := normalizeClientID(string(data))
	if !ok {
		return ""
	}
	return id
}

func saveClientIDAt(path, raw string) error {
	id, ok := normalizeClientID(raw)
	if !ok {
		return errors.New("invalid Spotify Client ID")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	stage, err := os.CreateTemp(dir, ".client-id-*")
	if err != nil {
		return err
	}
	defer os.Remove(stage.Name())
	defer stage.Close()
	if err := stage.Chmod(0600); err != nil {
		return err
	}
	if _, err := stage.WriteString(id); err != nil {
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

// A Client ID is public, but it must still be a single safe token.
func normalizeClientID(raw string) (string, bool) {
	id := strings.TrimSpace(raw)
	if id == "" || len(id) > clientIDLimit {
		return "", false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return "", false
		}
	}
	return id, true
}
