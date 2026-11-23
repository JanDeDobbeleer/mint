package mint

import (
	"os"
	"syscall"
	"unsafe"
)

func enableANSI(file *os.File) bool {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	var mode uint32
	ok, _, _ := kernel.NewProc("GetConsoleMode").Call(file.Fd(), uintptr(unsafe.Pointer(&mode)))
	if ok == 0 {
		return false
	}
	const virtualTerminalProcessing = 0x0004
	ok, _, _ = kernel.NewProc("SetConsoleMode").Call(file.Fd(), uintptr(mode|virtualTerminalProcessing))
	return ok != 0
}
