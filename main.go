package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

const usage = `Pulso — Spotify in the terminal

Usage:
  pulso doctor [--data-dir PATH]  Report local Soloist status
  pulso --help                   Show this help
`

const downloads = "https://developer.spotify.com/documentation/soloist/reference/downloads-and-updates"

type doctorDeps struct {
	find  func() (string, error)
	probe func(context.Context, string, []string, io.Writer, io.Writer) error
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, doctorDeps{
		find: findSoloist,
		probe: func(ctx context.Context, path string, args []string, stdout, stderr io.Writer) error {
			cmd := exec.CommandContext(ctx, path, args...)
			cmd.Stdout, cmd.Stderr = stdout, stderr
			return cmd.Run()
		},
	}))
}

func run(args []string, stdout, stderr io.Writer, deps doctorDeps) int {
	if len(args) == 0 || len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if args[0] != "doctor" || (len(args) != 1 && (len(args) != 3 || args[1] != "--data-dir" || args[2] == "")) {
		fmt.Fprintln(stderr, "Invalid arguments. Run pulso --help.")
		return 2
	}

	path, err := deps.find()
	if err != nil {
		fmt.Fprintf(stderr, "Could not locate Soloist: %v\n", err)
		return 1
	}
	if path == "" {
		fmt.Fprintf(stderr, "Soloist not found. Install it from %s\n", downloads)
		return 1
	}
	fmt.Fprintf(stdout, "Soloist: %s\n", path)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := deps.probe(ctx, path, append([]string{"ctl", "status"}, args[1:]...), stdout, stderr); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		fmt.Fprintf(stderr, "Soloist status check failed: %v\n", err)
		return 1
	}
	return 0
}

func findSoloist() (string, error) {
	if path, err := exec.LookPath("soloist"); err == nil {
		return path, nil
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	name := "soloist"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(configDir, "pulso", "bin", name)
	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		return "", nil
	case err != nil:
		return "", err
	case !info.Mode().IsRegular():
		return "", fmt.Errorf("%s is not a regular file", path)
	default:
		return path, nil
	}
}
