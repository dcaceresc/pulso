package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dcaceresc/pulso/internal/config"
	"github.com/dcaceresc/pulso/internal/spotify"
)

type uninstallCase struct {
	root                string
	plan                uninstallPlan
	paths               []string
	config, data, cache string
}

func newUninstallCase(t *testing.T) uninstallCase {
	t.Helper()
	root := t.TempDir()
	configDir := filepath.Join(root, "config", "pulso")
	dataDir := filepath.Join(root, "data", "soloist")
	cacheDir := filepath.Join(root, "cache", "soloist")
	targets := uninstallTargetsFor(configDir, dataDir, cacheDir)
	paths := make([]string, 0, len(targets))
	for _, target := range targets {
		paths = append(paths, target.path)
	}
	return uninstallCase{
		root:   root,
		plan:   uninstallPlan{dataDir: dataDir, targets: targets},
		paths:  paths,
		config: configDir,
		data:   dataDir,
		cache:  cacheDir,
	}
}

func uninstallTargetsFor(configDir, dataDir, cacheDir string) []uninstallTarget {
	return []uninstallTarget{
		{path: filepath.Join(configDir, "bin", "soloist"), kind: "managed Soloist binary"},
		{path: filepath.Join(configDir, "client-id"), kind: "Spotify Client ID"},
		{path: filepath.Join(configDir, "spotify-session.json"), kind: "saved Spotify session"},
		{path: filepath.Join(configDir, "preferences.json"), kind: "Pulso preferences"},
		{path: dataDir, kind: "Soloist data directory (pairing and cache)"},
		{path: cacheDir, kind: "Soloist cache directory"},
	}
}

func (c uninstallCase) create(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(c.config, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range c.paths[:4] {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{c.data, c.cache} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state"), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func (c uninstallCase) binaryPath() string {
	return filepath.Join(c.root, "gopath", "bin", "pulso")
}

func (c uninstallCase) deps() uninstallDeps {
	return uninstallDeps{
		plan:        func() (uninstallPlan, error) { return c.plan, nil },
		executable:  func() (string, error) { return c.binaryPath(), nil },
		cwd:         func() (string, error) { return filepath.Join(c.root, "work", "repo"), nil },
		home:        func() (string, error) { return c.root, nil },
		running:     func(context.Context, string) (int, bool, error) { return 0, false, nil },
		terminate:   func(int) error { return nil },
		alive:       func(int) bool { return false },
		remove:      os.RemoveAll,
		exitTimeout: 10 * time.Millisecond,
		exitPause:   time.Millisecond,
	}
}

func runUninstallFixture(t *testing.T, deps uninstallDeps, input string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runUninstall(strings.NewReader(input), &stdout, &stderr, deps)
	return code, stdout.String(), stderr.String()
}

func assertPresent(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("%s should still exist: %v", path, err)
		}
	}
}

func assertGone(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("%s should be gone, stat error = %v", path, err)
		}
	}
}

func TestUninstallPrintsPlanAndRequiresTypedWord(t *testing.T) {
	for _, input := range []string{"\n", "", "delete\n", "UNINSTALL\n", "uninstall please\n"} {
		t.Run(strings.TrimSpace(input), func(t *testing.T) {
			fixture := newUninstallCase(t)
			fixture.create(t)
			code, stdout, stderr := runUninstallFixture(t, fixture.deps(), input)
			if code != 1 {
				t.Fatalf("exit = %d, want 1", code)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
			if !strings.Contains(stdout, "Pulso will delete:") || !strings.Contains(stdout, "Type uninstall to continue: ") {
				t.Fatalf("stdout = %q", stdout)
			}
			for _, target := range fixture.plan.targets {
				if !strings.Contains(stdout, target.path+"  "+target.kind) {
					t.Fatalf("stdout = %q, missing %q", stdout, target.path+"  "+target.kind)
				}
			}
			if !strings.Contains(stdout, "Cancelled; nothing was deleted.") {
				t.Fatalf("stdout = %q", stdout)
			}
			assertPresent(t, fixture.paths...)
		})
	}
}

func TestUninstallRemovesEveryTarget(t *testing.T) {
	fixture := newUninstallCase(t)
	fixture.create(t)
	code, stdout, stderr := runUninstallFixture(t, fixture.deps(), "uninstall\n")
	if code != 0 || stderr != "" {
		t.Fatalf("exit = %d, stderr = %q, stdout = %q", code, stderr, stdout)
	}
	assertGone(t, fixture.paths...)
	for _, target := range fixture.plan.targets {
		if !strings.Contains(stdout, "Removed "+target.path+"  "+target.kind) {
			t.Fatalf("stdout = %q, missing removal of %q", stdout, target.path)
		}
	}
	wantBinary := "Pulso binary: " + fixture.binaryPath()
	if !strings.Contains(stdout, wantBinary) || !strings.Contains(stdout, "go install") {
		t.Fatalf("stdout = %q, want the Pulso binary hint", stdout)
	}
}

func TestUninstallReportsAbsentTargetsAndIsIdempotent(t *testing.T) {
	fixture := newUninstallCase(t)
	deps := fixture.deps()
	for run := 1; run <= 2; run++ {
		code, stdout, stderr := runUninstallFixture(t, deps, "uninstall\n")
		if code != 0 || stderr != "" {
			t.Fatalf("run %d: exit = %d, stderr = %q, stdout = %q", run, code, stderr, stdout)
		}
		for _, target := range fixture.plan.targets {
			if !strings.Contains(stdout, "Absent  "+target.path+"  "+target.kind) {
				t.Fatalf("run %d: stdout = %q, missing absent %q", run, stdout, target.path)
			}
		}
		assertGone(t, fixture.paths...)
	}
}

func TestUninstallDeclinedDaemonQuestionDeletesNothing(t *testing.T) {
	for _, answer := range []string{"\n", "n\n", "no\n", "Y E S\n"} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			fixture := newUninstallCase(t)
			fixture.create(t)
			deps := fixture.deps()
			terminated := 0
			deps.running = func(context.Context, string) (int, bool, error) { return 4242, true, nil }
			deps.terminate = func(int) error { terminated++; return nil }
			code, stdout, stderr := runUninstallFixture(t, deps, "uninstall\n"+answer)
			if code != 1 || stderr != "" {
				t.Fatalf("exit = %d, stderr = %q, stdout = %q", code, stderr, stdout)
			}
			if !strings.Contains(stdout, "Soloist is running.") || !strings.Contains(stdout, "Close it and continue? [y/N] ") {
				t.Fatalf("stdout = %q", stdout)
			}
			if !strings.Contains(stdout, "Cancelled; nothing was deleted.") {
				t.Fatalf("stdout = %q", stdout)
			}
			if terminated != 0 {
				t.Fatalf("terminator called %d times", terminated)
			}
			assertPresent(t, fixture.paths...)
		})
	}
}

func TestUninstallStopsRunningDaemonThenDeletes(t *testing.T) {
	fixture := newUninstallCase(t)
	fixture.create(t)
	deps := fixture.deps()
	stopped, terminated := false, 0
	deps.running = func(context.Context, string) (int, bool, error) { return 4242, true, nil }
	deps.terminate = func(pid int) error {
		if pid != 4242 {
			t.Fatalf("terminated pid %d, want 4242", pid)
		}
		terminated++
		stopped = true
		return nil
	}
	deps.alive = func(pid int) bool {
		if pid != 4242 {
			t.Fatalf("checked pid %d, want 4242", pid)
		}
		return !stopped
	}
	code, stdout, stderr := runUninstallFixture(t, deps, "uninstall\ny\n")
	if code != 0 || stderr != "" {
		t.Fatalf("exit = %d, stderr = %q, stdout = %q", code, stderr, stdout)
	}
	if terminated != 1 {
		t.Fatalf("terminator called %d times", terminated)
	}
	assertGone(t, fixture.paths...)
}

func TestUninstallAbortsWhenDaemonIgnoresTermination(t *testing.T) {
	fixture := newUninstallCase(t)
	fixture.create(t)
	deps := fixture.deps()
	terminated := 0
	deps.running = func(context.Context, string) (int, bool, error) { return 4242, true, nil }
	deps.terminate = func(int) error { terminated++; return nil }
	deps.alive = func(int) bool { return true }
	deps.exitTimeout = 2 * time.Millisecond
	code, stdout, stderr := runUninstallFixture(t, deps, "uninstall\ny\n")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if terminated != 1 {
		t.Fatalf("terminator called %d times", terminated)
	}
	if !strings.Contains(stderr, "did not exit") || !strings.Contains(stderr, "Nothing was deleted.") {
		t.Fatalf("stderr = %q, stdout = %q", stderr, stdout)
	}
	assertPresent(t, fixture.paths...)
}

func TestUninstallRefusesUnsafeTargets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   func(c uninstallCase) string
		reason string
	}{
		{name: "empty path", path: func(uninstallCase) string { return "" }, reason: "empty or not absolute"},
		{name: "filesystem root", path: func(uninstallCase) string { return string(filepath.Separator) }, reason: "filesystem root"},
		{name: "home directory", path: func(c uninstallCase) string { return c.root }, reason: "home directory"},
		{name: "foreign directory", path: func(c uninstallCase) string { return filepath.Join(c.root, "Descargas") }, reason: "not named pulso or soloist"},
		{name: "working directory", path: func(c uninstallCase) string { return filepath.Join(c.root, "work") }, reason: "current working directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newUninstallCase(t)
			fixture.create(t)
			valid := fixture.paths[1]
			deps := fixture.deps()
			deps.plan = func() (uninstallPlan, error) {
				return uninstallPlan{dataDir: fixture.data, targets: []uninstallTarget{
					{path: valid, kind: "Spotify Client ID"},
					{path: tc.path(fixture), kind: "unsafe target"},
				}}, nil
			}
			code, _, stderr := runUninstallFixture(t, deps, "uninstall\n")
			if code != 1 {
				t.Fatalf("exit = %d, want 1", code)
			}
			if !strings.Contains(stderr, "Refusing to delete") || !strings.Contains(stderr, tc.reason) {
				t.Fatalf("stderr = %q, want reason %q", stderr, tc.reason)
			}
			assertPresent(t, valid)
		})
	}
}

func TestUninstallRemovesSymlinkWithoutFollowingIt(t *testing.T) {
	fixture := newUninstallCase(t)
	fixture.create(t)
	outside := filepath.Join(fixture.root, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(fixture.root, "data", "soloist")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	deps := fixture.deps()
	deps.plan = func() (uninstallPlan, error) {
		return uninstallPlan{dataDir: link, targets: []uninstallTarget{{path: link, kind: "Soloist data directory"}}}, nil
	}
	code, stdout, stderr := runUninstallFixture(t, deps, "uninstall\n")
	if code != 0 || stderr != "" {
		t.Fatalf("exit = %d, stderr = %q, stdout = %q", code, stderr, stdout)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("symlink still present: %v", err)
	}
	if _, err := os.Lstat(keep); err != nil {
		t.Fatalf("symlink target changed: %v", err)
	}
}

func TestUninstallKeepsGoingAfterAFailedDeletion(t *testing.T) {
	fixture := newUninstallCase(t)
	fixture.create(t)
	deps := fixture.deps()
	failing := fixture.paths[1]
	boom := errors.New("fixture deletion failure")
	deps.remove = func(path string) error {
		if path == failing {
			return boom
		}
		return os.RemoveAll(path)
	}
	code, stdout, stderr := runUninstallFixture(t, deps, "uninstall\n")
	if code != 1 || stderr != "" {
		t.Fatalf("exit = %d, stderr = %q, stdout = %q", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "Failed  "+failing+"  Spotify Client ID: "+boom.Error()) {
		t.Fatalf("stdout = %q", stdout)
	}
	assertPresent(t, failing)
	for _, path := range fixture.paths[2:] {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("%s was not processed after the failure: %v", path, err)
		}
	}
	if !strings.Contains(stdout, "Removed "+fixture.paths[2]) {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestUninstallPlanUsesExistingHelpers(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	t.Setenv("XDG_CONFIG_HOME", configDir) // Linux
	t.Setenv("APPDATA", configDir)         // Windows
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "local"))
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("STATE_DIRECTORY", "")

	plan, err := uninstallPlanFor(runtime.GOOS, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := managedSoloistPath()
	if err != nil {
		t.Fatal(err)
	}
	clientID, err := spotify.ClientIDPath()
	if err != nil {
		t.Fatal(err)
	}
	session, err := spotify.SessionPath()
	if err != nil {
		t.Fatal(err)
	}
	preferences, err := config.PreferencesPath()
	if err != nil {
		t.Fatal(err)
	}
	dataDir, err := soloistDataDir(runtime.GOOS, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	cacheDir, err := soloistCacheDir(runtime.GOOS, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	want := []uninstallTarget{
		{path: binary, kind: "managed Soloist binary"},
		{path: clientID, kind: "Spotify Client ID"},
		{path: session, kind: "saved Spotify session"},
		{path: preferences, kind: "Pulso preferences"},
		{path: dataDir, kind: "Soloist data directory (pairing and cache)"},
		{path: cacheDir, kind: "Soloist cache directory"},
	}
	if plan.dataDir != dataDir || !reflect.DeepEqual(plan.targets, want) {
		t.Fatalf("plan = %+v, want data dir %q and targets %+v", plan, dataDir, want)
	}
	for _, target := range plan.targets {
		if !filepath.IsAbs(target.path) {
			t.Fatalf("%q is not absolute", target.path)
		}
		if err := guardUninstallTarget(target.path, filepath.Join(root, "home"), root); err != nil {
			t.Fatalf("the real plan violates the guards at %q: %v", target.path, err)
		}
	}
}

func TestSoloistCacheDir(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{
		"XDG_CACHE_HOME": filepath.Join(root, "xdg-cache"),
		"HOME":           filepath.Join(root, "home"),
		"LOCALAPPDATA":   filepath.Join(root, "local"),
	}
	getenv := func(name string) string { return env[name] }
	for _, tc := range []struct{ platform, unset, want string }{
		{"windows", "", filepath.Join(env["LOCALAPPDATA"], "soloist", "cache")},
		{"linux", "", filepath.Join(env["XDG_CACHE_HOME"], "soloist")},
		{"linux", "XDG_CACHE_HOME", filepath.Join(env["HOME"], ".cache", "soloist")},
	} {
		delete(env, tc.unset)
		if got, err := soloistCacheDir(tc.platform, getenv); err != nil || got != tc.want {
			t.Fatalf("%s cache directory = %q, %v; want %q", tc.platform, got, err, tc.want)
		}
	}
	delete(env, "HOME")
	if got, err := soloistCacheDir("linux", getenv); got != "" || err == nil {
		t.Fatalf("missing default cache directory = %q, %v", got, err)
	}
}
