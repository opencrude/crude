package platform

import "syscall"

func InitConsole() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	setConsoleOutputCP := kernel32.NewProc("SetConsoleOutputCP")
	setConsoleCP := kernel32.NewProc("SetConsoleCP")
	setConsoleOutputCP.Call(65001)
	setConsoleCP.Call(65001)
}
