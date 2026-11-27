//go:build windows

package app

import (
	"errors"
	"os"
	"syscall"
)

// stillActive es el código que Windows informa para un proceso vivo.
const stillActive = 259

// terminateSoloistProcess usa TerminateProcess, la terminación que ofrece la
// plataforma; Windows no tiene SIGTERM.
func terminateSoloistProcess(pid int) error {
	if pid <= 0 {
		return errors.New("invalid Soloist process identifier")
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}

// soloistProcessAlive consulta el proceso sin terminarlo y sin exigir permisos
// de escritura.
func soloistProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	var code uint32
	if err := syscall.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == stillActive
}
