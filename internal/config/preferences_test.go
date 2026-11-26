package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestPreferencesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pulso", "preferences.json")
	for _, want := range []Preferences{{"es", "dark"}, {"en", "light"}, {"en", "standard"}} {
		if err := savePreferencesAt(path, want); err != nil {
			t.Fatal(err)
		}
		if got := loadPreferencesAt(path); got != want {
			t.Fatalf("loaded %+v, want %+v", got, want)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		wantJSON := `{"language":"` + want.Language + `","theme":"` + want.Theme + `"}`
		if string(bytes.TrimSpace(data)) != wantJSON {
			t.Fatalf("file = %q, want %s", data, wantJSON)
		}
		entries, err := os.ReadDir(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != "preferences.json" {
			t.Fatalf("unexpected staged files: %v", entries)
		}
	}
}

func TestPreferencesInvalidDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, json string
		want       Preferences
	}{
		{"missing", "", Preferences{"en", "standard"}},
		{"empty object", `{}`, Preferences{"en", "standard"}},
		{"missing language", `{"theme":"dark"}`, Preferences{"en", "dark"}},
		{"missing theme", `{"language":"es"}`, Preferences{"es", "standard"}},
		{"unknown language", `{"language":"fr","theme":"light"}`, Preferences{"en", "light"}},
		{"unknown theme", `{"language":"es","theme":"custom"}`, Preferences{"es", "standard"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "preferences.json")
			if tc.json != "" {
				writePreferenceFixture(t, path, tc.json)
			}
			if got := loadPreferencesAt(path); got != tc.want {
				t.Fatalf("loaded %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestPreferencesUnsafeShape(t *testing.T) {
	for _, tc := range []struct{ name, json string }{
		{"malformed", `{"language":"es"`},
		{"array", `["es","dark"]`},
		{"null document", `null`},
		{"null value", `{"language":"es","theme":null}`},
		{"non-string value", `{"language":12,"theme":"dark"}`},
		{"extra key", `{"language":"es","theme":"dark","unrelated":true}`},
		{"duplicate key", `{"language":"en","language":"es","theme":"dark"}`},
		{"case variant", `{"Language":"es","theme":"dark"}`},
		{"trailing document", `{"language":"es","theme":"dark"}{}`},
		{"oversized", `{"language":"es","theme":"dark"}` + strings.Repeat(" ", 64<<10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "preferences.json")
			writePreferenceFixture(t, path, tc.json)
			if got, want := loadPreferencesAt(path), (Preferences{"en", "standard"}); got != want {
				t.Fatalf("loaded %+v, want %+v", got, want)
			}
			assertPreferenceFixture(t, path, tc.json)
		})
	}
}

func TestPreferencesRejectUnsafeDestination(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "preferences.json")
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if got, want := loadPreferencesAt(path), (Preferences{"en", "standard"}); got != want {
			t.Fatalf("loaded %+v, want %+v", got, want)
		}
		if err := savePreferencesAt(path, Preferences{"es", "dark"}); err == nil {
			t.Fatal("save accepted a directory destination")
		}
		if info, err := os.Lstat(path); err != nil || !info.IsDir() {
			t.Fatalf("destination changed: %v, %v", info, err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.json")
		const original = `{"language":"es","theme":"dark"}`
		writePreferenceFixture(t, target, original)
		path := filepath.Join(dir, "preferences.json")
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("cannot create symlink: %v", err)
		}
		if got, want := loadPreferencesAt(path), (Preferences{"en", "standard"}); got != want {
			t.Fatalf("loaded %+v, want %+v", got, want)
		}
		if err := savePreferencesAt(path, Preferences{"en", "light"}); err == nil {
			t.Fatal("save accepted a symlink destination")
		}
		if got, err := os.Readlink(path); err != nil || got != target {
			t.Fatalf("link changed: %q, %v", got, err)
		}
		assertPreferenceFixture(t, target, original)
	})
	t.Run("symlink parent", func(t *testing.T) {
		targetDir := t.TempDir()
		target := filepath.Join(targetDir, "preferences.json")
		const original = `{"language":"es","theme":"dark"}`
		writePreferenceFixture(t, target, original)
		dir := filepath.Join(t.TempDir(), "pulso")
		if err := os.Symlink(targetDir, dir); err != nil {
			t.Skipf("cannot create directory symlink: %v", err)
		}
		path := filepath.Join(dir, "preferences.json")
		if got, want := loadPreferencesAt(path), (Preferences{"en", "standard"}); got != want {
			t.Fatalf("loaded %+v, want %+v", got, want)
		}
		if err := savePreferencesAt(path, Preferences{"en", "light"}); err == nil {
			t.Fatal("save accepted a symlink parent")
		}
		assertPreferenceFixture(t, target, original)
	})
}

func TestPreferencesRejectInvalidWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	const original = `{"language":"es","theme":"dark"}`
	writePreferenceFixture(t, path, original)
	for _, p := range []Preferences{{"ES", "dark"}, {"es", "custom"}, {"es", strings.Repeat("x", 64<<10)}} {
		if err := savePreferencesAt(path, p); err == nil {
			t.Fatal("save accepted unsupported preferences")
		}
		assertPreferenceFixture(t, path, original)
	}
}

func TestPreferencesText(t *testing.T) {
	const english, spanish = "Search: %s {query}", "Buscar: %s {query}"
	for _, tc := range []struct{ language, want string }{
		{"es", spanish}, {"en", english}, {"", english}, {"ES", english}, {"%s\x1b[31m", english},
	} {
		if got := (Preferences{Language: tc.language}).Text(english, spanish); got != tc.want {
			t.Errorf("language %q returned %q, want literal %q", tc.language, got, tc.want)
		}
	}
	if got := (Preferences{Language: "es"}).Text(english, ""); got != english {
		t.Errorf("missing Spanish translation returned %q, want %q", got, english)
	}
}

func TestPreferencesPalette(t *testing.T) {
	standard := (Preferences{Theme: "standard"}).Palette()
	dark := (Preferences{Theme: "dark"}).Palette()
	light := (Preferences{Theme: "light"}).Palette()
	for _, p := range []Palette{standard, dark, light} {
		for _, c := range []lipgloss.TerminalColor{p.Accent, p.AccentText, p.Muted, p.Error} {
			if c == nil {
				t.Fatal("palette has an unset color")
			}
		}
		if p.Accent == p.AccentText {
			t.Fatal("accent and accent text use the same color")
		}
	}
	if standard == dark || standard == light || dark == light {
		t.Fatal("palettes must be distinct")
	}
	for _, theme := range []string{"", "#ff0000", "\x1b[31m", "DARK"} {
		if got := (Preferences{Theme: theme}).Palette(); got != standard {
			t.Errorf("theme %q did not fall back to standard", theme)
		}
	}
}

func writePreferenceFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func assertPreferenceFixture(t *testing.T, path, data string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != data {
		t.Fatalf("fixture changed: got %q, want %q", got, data)
	}
}
