package cli

import (
	"os"
	"syscall"
	"unsafe"
)

func isTerminal(file *os.File) bool {
	// A character device such as /dev/null is not interactive; TCGETS distinguishes it from a TTY.
	var termios syscall.Termios
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		file.Fd(),
		uintptr(syscall.TCGETS),
		uintptr(unsafe.Pointer(&termios)),
	)
	return errno == 0
}
