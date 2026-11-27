//go:build !windows

package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// terminateSoloistProcess pide al daemon que termine con SIGTERM. Nunca envía
// SIGKILL: si no obedece, la desinstalación aborta sin borrar nada.
func terminateSoloistProcess(pid int) error {
	if pid <= 0 {
		return errors.New("invalid Soloist process identifier")
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}

// soloistProcessAlive informa si el PID sigue existiendo. Una comprobación sin
// señal no altera el proceso.
func soloistProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// soloistProcessImage devuelve el nombre base del ejecutable del PID. Lee el
// destino de /proc/<pid>/exe y recurre a /proc/<pid>/comm; una entrada ausente o
// ilegible significa "desconocido".
func soloistProcessImage(pid int) string {
	if pid <= 0 {
		return ""
	}
	if target, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err == nil {
		if base := filepath.Base(target); base != "" && base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
