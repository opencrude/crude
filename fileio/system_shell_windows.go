//go:build windows

package fileio

import (
	"os"
	"path/filepath"

	"crude/types/command"
)

// SystemShell runs command lines that their caller quoted for cmd.exe, such as
// MCP package preparation. Agent commands use the agent command shell.
func SystemShell() command.Shell {
	path := os.Getenv("ComSpec")
	if info, err := os.Stat(path); path == "" || err != nil || info.IsDir() {
		path = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	return command.Shell{ID: "cmd", Label: "Command Prompt", Path: path, Syntax: command.SyntaxCmd}
}
