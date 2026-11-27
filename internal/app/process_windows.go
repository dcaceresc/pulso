//go:build windows

package app

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// stillActive es el código que Windows informa para un proceso vivo.
const stillActive = 259

var (
	kernel32DLL                    = syscall.NewLazyDLL("kernel32.dll")
	procQueryFullProcessImageNameW = kernel32DLL.NewProc("QueryFullProcessImageNameW")
)

// queryFullProcessImageName obtiene la ruta del ejecutable del handle abierto.
func queryFullProcessImageName(handle syscall.Handle, buffer *uint16, size *uint32) error {
	r1, _, callErr := procQueryFullProcessImageNameW.Call(uintptr(handle), 0, uintptr(unsafe.Pointer(buffer)), uintptr(unsafe.Pointer(size)))
	if r1 != 0 {
		return nil
	}
	if code, ok := callErr.(syscall.Errno); ok && code != 0 {
		return callErr
	}
	return syscall.EINVAL
}

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

// soloistProcessImage devuelve el nombre base del ejecutable del PID mediante
// QueryFullProcessImageNameW sobre el handle que ya se abre. Cualquier fallo
// significa "desconocido".
func soloistProcessImage(pid int) string {
	if pid <= 0 {
		return ""
	}
	handle, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(handle)
	buffer := make([]uint16, 1024)
	size := uint32(len(buffer))
	if err := queryFullProcessImageName(handle, &buffer[0], &size); err != nil {
		return ""
	}
	return filepath.Base(syscall.UTF16ToString(buffer[:size]))
}
