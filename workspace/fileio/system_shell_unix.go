//go:build unix

package fileio

import (
	"os/exec"

	"crude/types/command"
)

// SystemShell runs command lines that their caller quoted for a POSIX shell,
// such as MCP package preparation. Agent commands use the agent command shell.
func SystemShell() command.Shell {
	path := "/bin/sh"
	if bash, err := exec.LookPath("bash"); err == nil {
		path = bash
	}
	return command.Shell{ID: "sh", Label: "sh", Path: path, Syntax: command.SyntaxPOSIX}
}
