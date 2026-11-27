package config

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

type Preferences struct {
	Language string `json:"language"`
	Theme    string `json:"theme"`
}

type Palette struct {
	Accent, AccentText, Muted, Error lipgloss.TerminalColor
}

func (p Preferences) Text(english, spanish string) string {
	if p.Language == "es" && spanish != "" {
		return spanish
	}
	return english
}

func (p Preferences) Palette() Palette {
	switch p.Theme {
	case "dark":
		return Palette{
			Accent: lipgloss.Color("#60A5FA"), AccentText: lipgloss.Color("#101827"),
			Muted: lipgloss.Color("#9CA3AF"), Error: lipgloss.Color("#FCA5A5"),
		}
	case "light":
		return Palette{
			Accent: lipgloss.Color("#1D4ED8"), AccentText: lipgloss.Color("#FFFFFF"),
			Muted: lipgloss.Color("#4B5563"), Error: lipgloss.Color("#B91C1C"),
		}
	default:
		return Palette{
			Accent: lipgloss.Color("14"), AccentText: lipgloss.Color("0"),
			Muted: lipgloss.AdaptiveColor{Light: "8", Dark: "7"},
			Error: lipgloss.AdaptiveColor{Light: "1", Dark: "9"},
		}
	}
}

func DefaultPreferences() Preferences {
	return Preferences{Language: "en", Theme: "standard"}
}

func (p Preferences) Normalized() Preferences {
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

// PreferencesPath expone la ruta resuelta para que otros comandos, como la
// desinstalación, la informen sin duplicar la resolución.
func PreferencesPath() (string, error) {
	return preferencesPath()
}

func Load() Preferences {
	path, err := preferencesPath()
	if err != nil {
		return DefaultPreferences()
	}
	return loadPreferencesAt(path)
}

func Save(p Preferences) error {
	path, err := preferencesPath()
	if err != nil {
		return err
	}
	return savePreferencesAt(path, p)
}

func loadPreferencesAt(path string) Preferences {
	if err := checkPreferencesDirectory(filepath.Dir(path)); err != nil {
		return DefaultPreferences()
	}
	entry, err := os.Lstat(path)
	if err != nil || !entry.Mode().IsRegular() || entry.Size() > preferencesReadLimit {
		return DefaultPreferences()
	}
	file, err := os.Open(path)
	if err != nil {
		return DefaultPreferences()
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(entry, opened) {
		return DefaultPreferences()
	}
	data, err := io.ReadAll(io.LimitReader(file, preferencesReadLimit+1))
	if err != nil || len(data) > preferencesReadLimit {
		return DefaultPreferences()
	}
	return decodePreferences(data)
}

// Read only one JSON object with unique, known keys and string values. A
// structurally valid object can default missing or unrecognized options independently.
func decodePreferences(data []byte) Preferences {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return DefaultPreferences()
	}
	var p Preferences
	var seenLanguage, seenTheme bool
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return DefaultPreferences()
		}
		value, err := decoder.Token()
		text, ok := value.(string)
		if err != nil || !ok {
			return DefaultPreferences()
		}
		switch {
		case key == "language" && !seenLanguage:
			p.Language, seenLanguage = text, true
		case key == "theme" && !seenTheme:
			p.Theme, seenTheme = text, true
		default:
			return DefaultPreferences()
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return DefaultPreferences()
	}
	if _, err := decoder.Token(); err != io.EOF {
		return DefaultPreferences()
	}
	return p.Normalized()
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

func savePreferencesAt(path string, p Preferences) error {
	if p != p.Normalized() {
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
