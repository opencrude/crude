//go:build !windows

package viewmodel

import (
	"io"
	"os"
)

func prepareTerminalSignalChild() error { return nil }
func terminalControlReader() io.Reader  { return os.Stdin }
