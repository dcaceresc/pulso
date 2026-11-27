//go:build !windows

package app

import (
	"errors"
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
