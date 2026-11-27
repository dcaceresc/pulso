package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Sondas del verificador independiente sobre la identidad del proceso: solo un
// Soloist verificado puede detenerse, y las guardas no aceptan un descenso de
// un solo nivel fuera de la carpeta permitida.

func TestIsSoloistImageTable(t *testing.T) {
	for _, image := range []string{"soloist", "Soloist", "SOLOIST.EXE", "soloist.exe", "SOLOIST"} {
		if !isSoloistImage(image) {
			t.Fatalf("%q rechazado", image)
		}
	}
	for _, image := range []string{"", " ", "unknown", "soloist-helper", "bash", "soloist.exe.exe", "Soloist Extra", "soloists", ".exe"} {
		if isSoloistImage(image) {
			t.Fatalf("%q aceptado", image)
		}
	}
}

func TestUninstallRefusesUnknownImage(t *testing.T) {
	fixture := newUninstallCase(t)
	fixture.create(t)
	deps := fixture.deps()
	deps.image = func(int) string { return "" }
	deps.running = func(context.Context, string) (int, bool, error) { return 4242, true, nil }
	deps.terminate = func(int) error { t.Fatal("terminó un proceso sin imagen verificada"); return nil }
	deps.alive = func(int) bool { t.Fatal("comprobó la vida de un proceso sin imagen verificada"); return false }
	code, _, stderr := runUninstallFixture(t, deps, "uninstall\ny\n")
	if code != 1 || !strings.Contains(stderr, "unknown") || !strings.Contains(stderr, "Nothing was deleted.") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	assertPresent(t, fixture.paths...)
}

func TestSoloistProcessImageNamesOwnProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got := soloistProcessImage(os.Getpid())
	if got != filepath.Base(exe) || isSoloistImage(got) {
		t.Fatalf("imagen = %q; want %q", got, filepath.Base(exe))
	}
	if soloistProcessImage(0) != "" || soloistProcessImage(-1) != "" {
		t.Fatal("un pid no positivo debe quedar sin imagen")
	}
}

func TestGuardRejectsSingleParentTraversal(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	elsewhere := filepath.Join(root, "elsewhere")
	if err := guardUninstallTarget(filepath.Join(home, ".config", "pulso", ".."), home, elsewhere); err == nil {
		t.Fatal("aceptó un descenso que sale de la carpeta permitida")
	}
	if err := guardUninstallTarget(home, home+string(filepath.Separator), elsewhere); err == nil {
		t.Fatal("aceptó el directorio personal con barra final")
	}
}
