package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/dcaceresc/pulso/internal/config"
	"github.com/dcaceresc/pulso/internal/soloist"
	"github.com/dcaceresc/pulso/internal/spotify"
)

const (
	soloistExitTimeout = 5 * time.Second
	soloistExitPause   = 50 * time.Millisecond
	// uninstallWord es la confirmación escrita que exige el comando. Un Enter
	// vacío nunca borra nada.
	uninstallWord = "uninstall"
	// cancelledUninstall es el único cierre que comparten todas las negativas.
	cancelledUninstall = "Cancelled; nothing was deleted.\n"
)

var (
	errSoloistCacheDir = errors.New("Could not locate Soloist's default cache directory. Run pulso uninstall again.")
	errHomeDirectory   = errors.New("Could not locate your home directory.")
	errWorkingDir      = errors.New("Could not locate the current working directory.")
	errDaemonPID       = errors.New("Could not identify the running Soloist process. Close it and run pulso uninstall again.")
)

// uninstallTarget es una ruta que la desinstalación informa y, si procede, borra.
type uninstallTarget struct {
	path string
	kind string
}

// uninstallPlan reúne los objetivos resueltos y el directorio de datos que el
// inspector de procesos necesita para saber si Soloist está corriendo.
type uninstallPlan struct {
	dataDir string
	targets []uninstallTarget
}

// uninstallDeps inyecta todo lo que toca el sistema: resolución de rutas,
// inspección de procesos, terminación y borrado.
type uninstallDeps struct {
	plan        func() (uninstallPlan, error)
	executable  func() (string, error)
	cwd         func() (string, error)
	home        func() (string, error)
	running     func(context.Context, string) (int, bool, error)
	image       func(int) string
	terminate   func(int) error
	alive       func(int) bool
	remove      func(string) error
	exitTimeout time.Duration
	exitPause   time.Duration
}

func newUninstallDeps() uninstallDeps {
	return uninstallDeps{
		plan:        func() (uninstallPlan, error) { return uninstallPlanFor(runtime.GOOS, os.Getenv) },
		executable:  os.Executable,
		cwd:         os.Getwd,
		home:        os.UserHomeDir,
		running:     soloist.Running,
		image:       soloistProcessImage,
		terminate:   terminateSoloistProcess,
		alive:       soloistProcessAlive,
		remove:      os.RemoveAll,
		exitTimeout: soloistExitTimeout,
		exitPause:   soloistExitPause,
	}
}

// uninstallPlanFor compone los objetivos desde los ayudantes que ya usan el
// resto de Pulso; nunca escribe una ruta de usuario a mano.
func uninstallPlanFor(goos string, getenv func(string) string) (uninstallPlan, error) {
	binary, err := managedSoloistPath()
	if err != nil {
		return uninstallPlan{}, err
	}
	clientID, err := spotify.ClientIDPath()
	if err != nil {
		return uninstallPlan{}, err
	}
	session, err := spotify.SessionPath()
	if err != nil {
		return uninstallPlan{}, err
	}
	preferences, err := config.PreferencesPath()
	if err != nil {
		return uninstallPlan{}, err
	}
	dataDir, err := soloistDataDir(goos, getenv)
	if err != nil {
		return uninstallPlan{}, err
	}
	cacheDir, err := soloistCacheDir(goos, getenv)
	if err != nil {
		return uninstallPlan{}, err
	}
	return uninstallPlan{dataDir: dataDir, targets: []uninstallTarget{
		{path: binary, kind: "managed Soloist binary"},
		{path: clientID, kind: "Spotify Client ID"},
		{path: session, kind: "saved Spotify session"},
		{path: preferences, kind: "Pulso preferences"},
		{path: dataDir, kind: "Soloist data directory (pairing and cache)"},
		{path: cacheDir, kind: "Soloist cache directory"},
	}}, nil
}

// soloistCacheDir replica la convención de soloistDataDir para la caché del
// daemon. Una plataforma sin directorio de caché conocido no propone ninguno.
func soloistCacheDir(goos string, getenv func(string) string) (string, error) {
	var dir string
	switch goos {
	case "windows":
		if base := getenv("LOCALAPPDATA"); filepath.IsAbs(base) {
			dir = filepath.Join(base, "soloist", "cache")
		}
	case "linux":
		switch {
		case getenv("XDG_CACHE_HOME") != "":
			dir = filepath.Join(getenv("XDG_CACHE_HOME"), "soloist")
		case getenv("HOME") != "":
			dir = filepath.Join(getenv("HOME"), ".cache", "soloist")
		}
	}
	if !filepath.IsAbs(dir) {
		return "", errSoloistCacheDir
	}
	return dir, nil
}

// runUninstall informa el plan, pide la confirmación escrita, cierra un daemon
// en ejecución solo con permiso explícito y borra objetivo por objetivo.
func runUninstall(stdin io.Reader, stdout, stderr io.Writer, deps uninstallDeps) int {
	plan, err := deps.plan()
	if err != nil {
		fmt.Fprintf(stderr, "Could not resolve the Pulso files to delete: %v\nNothing was deleted.\n", err)
		return 1
	}
	home, cwd, err := uninstallRoots(deps)
	if err != nil {
		fmt.Fprintf(stderr, "%v\nNothing was deleted.\n", err)
		return 1
	}
	// Las guardas se evalúan antes de cualquier borrado: un solo objetivo
	// inseguro cancela el plan completo.
	for _, target := range plan.targets {
		if err := guardUninstallTarget(target.path, home, cwd); err != nil {
			fmt.Fprintf(stderr, "Refusing to delete %s: %v\nNothing was deleted.\n", target.path, err)
			return 1
		}
	}

	fmt.Fprintln(stdout, "Pulso will delete:")
	for _, target := range plan.targets {
		fmt.Fprintf(stdout, "  %s  %s\n", target.path, target.kind)
	}

	reader := bufio.NewReader(stdin)
	fmt.Fprintf(stdout, "Type %s to continue: ", uninstallWord)
	if !readLine(reader, uninstallWord) {
		fmt.Fprint(stdout, cancelledUninstall)
		return 1
	}

	pid, running, err := deps.running(context.Background(), plan.dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "Could not check whether Soloist is running: %v\nNothing was deleted.\n", err)
		return 1
	}
	if running {
		fmt.Fprintln(stdout, "Soloist is running.")
		fmt.Fprint(stdout, "Close it and continue? [y/N] ")
		answer, _ := reader.ReadString('\n')
		switch strings.TrimSpace(answer) {
		case "y", "Y":
		default:
			fmt.Fprint(stdout, cancelledUninstall)
			return 1
		}
		if pid <= 0 {
			fmt.Fprintf(stderr, "%v\nNothing was deleted.\n", errDaemonPID)
			return 1
		}
		// Un marcador de PID puede nombrar cualquier proceso: solo se termina
		// cuando su imagen es Soloist, y sin comprobar su vida en caso contrario.
		if image := deps.image(pid); !isSoloistImage(image) {
			fmt.Fprintf(stderr, "Refusing to stop process %d: its image is %s, not Soloist.\nNothing was deleted.\n", pid, describeProcessImage(image))
			return 1
		}
		if err := deps.terminate(pid); err != nil {
			fmt.Fprintf(stderr, "Could not stop Soloist: %v\nNothing was deleted.\n", err)
			return 1
		}
		if !waitForSoloistExit(deps.alive, pid, deps.exitTimeout, deps.exitPause) {
			fmt.Fprintf(stderr, "Soloist did not exit.\nNothing was deleted.\n")
			return 1
		}
	}

	complete := true
	for _, target := range plan.targets {
		switch err := removeUninstallTarget(deps.remove, target.path); {
		case os.IsNotExist(err):
			fmt.Fprintf(stdout, "Absent  %s  %s\n", target.path, target.kind)
		case err != nil:
			fmt.Fprintf(stdout, "Failed  %s  %s: %v\n", target.path, target.kind, err)
			complete = false
		default:
			fmt.Fprintf(stdout, "Removed %s  %s\n", target.path, target.kind)
		}
	}
	printPulsoBinary(stdout, deps.executable)
	if !complete {
		return 1
	}
	return 0
}

// uninstallRoots resuelve el hogar y el directorio actual, que las guardas
// comparan contra cada objetivo.
func uninstallRoots(deps uninstallDeps) (string, string, error) {
	home, err := deps.home()
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", errHomeDirectory, err)
	}
	cwd, err := deps.cwd()
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", errWorkingDir, err)
	}
	return filepath.Clean(home), filepath.Clean(cwd), nil
}

// readLine acepta solo la palabra escrita completa. Cualquier otra respuesta,
// incluido un Enter vacío, no confirma nada.
func readLine(reader *bufio.Reader, want string) bool {
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line) == want
}

// isSoloistImage informa si el nombre de imagen corresponde a Soloist, sin
// distinguir mayúsculas ni la extensión .exe. Un nombre desconocido nunca lo
// es, así que la terminación no procede.
func isSoloistImage(image string) bool {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(image)), ".exe") == "soloist"
}

// describeProcessImage nombra la imagen para el mensaje de aborto.
func describeProcessImage(image string) string {
	if strings.TrimSpace(image) == "" {
		return "unknown"
	}
	return strconv.Quote(image)
}

// guardUninstallTarget rechaza rutas que nunca se deben borrar: vacías, la raíz
// del sistema, el hogar, el directorio actual o cualquier ruta que no pertenezca
// a la carpeta `pulso` o `soloist` que el programa ya conoce.
func guardUninstallTarget(path, home, cwd string) error {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return errors.New("the path is empty or not absolute")
	}
	clean := filepath.Clean(path)
	if home != "" {
		home = filepath.Clean(home)
	}
	if cwd != "" {
		cwd = filepath.Clean(cwd)
	}
	if clean == string(filepath.Separator) || clean == filepath.VolumeName(clean)+string(filepath.Separator) {
		return errors.New("the path is the filesystem root")
	}
	if home != "" && strings.EqualFold(clean, home) {
		return errors.New("the path is your home directory")
	}
	if cwd != "" && containsPath(clean, cwd) {
		return errors.New("the path contains the current working directory")
	}
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(clean)), ".exe")
	parent := strings.ToLower(filepath.Base(filepath.Dir(clean)))
	// Un objetivo válido es la carpeta pulso/soloist, una de sus entradas
	// directas o el binario gestionado dentro de `pulso/bin`.
	if base != "pulso" && base != "soloist" && parent != "pulso" && parent != "soloist" {
		return fmt.Errorf("the path %q is not named pulso or soloist", filepath.Base(clean))
	}
	return nil
}

// containsPath informa si root es cwd o un ancestro suyo.
func containsPath(root, cwd string) bool {
	root, cwd = strings.ToLower(root), strings.ToLower(cwd)
	if root == cwd {
		return true
	}
	return strings.HasPrefix(cwd, root+string(filepath.Separator))
}

// removeUninstallTarget borra la ruta sin seguir enlaces simbólicos: un enlace
// se elimina como enlace y su destino queda intacto.
func removeUninstallTarget(remove func(string) error, path string) error {
	if _, err := os.Lstat(path); err != nil {
		return err
	}
	return remove(path)
}

// waitForSoloistExit espera a que el proceso desaparezca. Nunca fuerza la
// salida: si sigue vivo al agotarse la espera, el llamador aborta.
func waitForSoloistExit(alive func(int) bool, pid int, timeout, pause time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !alive(pid) {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(pause)
	}
}

// printPulsoBinary nunca borra el binario de Pulso: solo informa dónde está.
func printPulsoBinary(stdout io.Writer, executable func() (string, error)) {
	path, err := executable()
	if err != nil || strings.TrimSpace(path) == "" {
		fmt.Fprintln(stdout, "Pulso binary: unknown (delete it yourself; go install owns it).")
		return
	}
	fmt.Fprintf(stdout, "Pulso binary: %s (delete it yourself; go install owns it).\n", path)
}
