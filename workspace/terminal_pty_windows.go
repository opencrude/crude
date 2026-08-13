//go:build windows

package workspace

import "fmt"

func startTerminalIO(command string, args []string, cwd string, cols, rows int) (terminalIO, error) {
	return nil, fmt.Errorf("in-app terminals are not supported on Windows yet")
}
