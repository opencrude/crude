//go:build unix && !linux && !darwin

package workspace

import "errors"

// processName is not read on this platform; the message names "a program".
func processName(int) string { return "" }

func backgroundProgram(int) (string, bool, error) {
	return "", false, errors.New("terminal child-process state is unavailable")
}
