//go:build windows

package viewmodel

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

func prepareTerminalSignalChild() error {
	h := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return err
	}
	if err := windows.SetConsoleMode(h, mode|windows.ENABLE_PROCESSED_INPUT); err != nil {
		return err
	}
	// go test's launching console can ignore Ctrl+C. Native console children
	// inherit that bit independently of their installed signal handlers.
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler")
	r, _, err := proc.Call(0, 0)
	if r == 0 {
		return err
	}
	return nil
}

type nativeControlReader struct{}

func (nativeControlReader) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	data := make([]uint16, len(b))
	var n uint32
	err := windows.ReadConsole(windows.Handle(os.Stdin.Fd()), &data[0], uint32(len(data)), &n, nil)
	for i := 0; i < int(n); i++ {
		b[i] = byte(data[i])
	}
	return int(n), err
}

// os.File.Read treats Ctrl+Z as EOF even in raw Windows consoles. Use the
// native API to observe the bytes rather than testing Go's DOS EOF policy.
func terminalControlReader() io.Reader { return nativeControlReader{} }
