package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/charmbracelet/lipgloss"
)

const preferencesReadLimit = 4096

type preferences struct {
	Language string `json:"language"`
	Theme    string `json:"theme"`
}

type themePalette struct {
	Accent, AccentText, Muted, Error lipgloss.TerminalColor
}

func (p preferences) text(english, spanish string) string {
	if p.Language == "es" && spanish != "" {
		return spanish
	}
	return english
}

func (p preferences) palette() themePalette {
	switch p.Theme {
	case "dark":
		return themePalette{
			Accent: lipgloss.Color("#60A5FA"), AccentText: lipgloss.Color("#101827"),
			Muted: lipgloss.Color("#9CA3AF"), Error: lipgloss.Color("#FCA5A5"),
		}
	case "light":
		return themePalette{
			Accent: lipgloss.Color("#1D4ED8"), AccentText: lipgloss.Color("#FFFFFF"),
			Muted: lipgloss.Color("#4B5563"), Error: lipgloss.Color("#B91C1C"),
		}
	default:
		return themePalette{
			Accent: lipgloss.Color("14"), AccentText: lipgloss.Color("0"),
			Muted: lipgloss.AdaptiveColor{Light: "8", Dark: "7"},
			Error: lipgloss.AdaptiveColor{Light: "1", Dark: "9"},
		}
	}
}

func defaultPreferences() preferences {
	return preferences{Language: "en", Theme: "standard"}
}

func (p preferences) normalized() preferences {
	if p.Language != "en" && p.Language != "es" {
		p.Language = "en"
	}
	if p.Theme != "standard" && p.Theme != "dark" && p.Theme != "light" {
		p.Theme = "standard"
	}
	return p
}

func preferencesPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pulso", "preferences.json"), nil
}

func loadPreferences() preferences {
	path, err := preferencesPath()
	if err != nil {
		return defaultPreferences()
	}
	return loadPreferencesAt(path)
}

func savePreferences(p preferences) error {
	path, err := preferencesPath()
	if err != nil {
		return err
	}
	return savePreferencesAt(path, p)
}

func loadPreferencesAt(path string) preferences {
	if err := checkPreferencesDirectory(filepath.Dir(path)); err != nil {
		return defaultPreferences()
	}
	entry, err := os.Lstat(path)
	if err != nil || !entry.Mode().IsRegular() || entry.Size() > preferencesReadLimit {
		return defaultPreferences()
	}
	file, err := os.Open(path)
	if err != nil {
		return defaultPreferences()
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(entry, opened) {
		return defaultPreferences()
	}
	data, err := io.ReadAll(io.LimitReader(file, preferencesReadLimit+1))
	if err != nil || len(data) > preferencesReadLimit {
		return defaultPreferences()
	}
	return decodePreferences(data)
}

// Read only one JSON object with unique, known keys and string values. A
// structurally valid object can default missing or unrecognized options independently.
func decodePreferences(data []byte) preferences {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return defaultPreferences()
	}
	var p preferences
	var seenLanguage, seenTheme bool
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return defaultPreferences()
		}
		value, err := decoder.Token()
		text, ok := value.(string)
		if err != nil || !ok {
			return defaultPreferences()
		}
		switch {
		case key == "language" && !seenLanguage:
			p.Language, seenLanguage = text, true
		case key == "theme" && !seenTheme:
			p.Theme, seenTheme = text, true
		default:
			return defaultPreferences()
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return defaultPreferences()
	}
	if _, err := decoder.Token(); err != io.EOF {
		return defaultPreferences()
	}
	return p.normalized()
}

func checkPreferencesDirectory(dir string) error {
	entry, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !entry.IsDir() {
		return errors.New("preferences directory is not a regular directory")
	}
	return nil
}

func savePreferencesAt(path string, p preferences) error {
	if p != p.normalized() {
		return errors.New("unsupported language or theme preference")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := checkPreferencesDirectory(dir); err != nil {
		return err
	}
	if entry, err := os.Lstat(path); err == nil {
		if !entry.Mode().IsRegular() {
			return errors.New("preferences path is not a regular file")
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	// Stage the complete, bounded record next to its destination. A failed
	// write or rename leaves the previous preferences in place.
	stage, err := os.CreateTemp(dir, ".preferences-*")
	if err != nil {
		return err
	}
	defer os.Remove(stage.Name())
	defer stage.Close()
	if err := json.NewEncoder(stage).Encode(p); err != nil {
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
