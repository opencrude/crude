package command

import "strings"

// Syntax is the command language a shell reads.
type Syntax string

const (
	SyntaxPOSIX      Syntax = "posix"
	SyntaxPowerShell Syntax = "powershell"
	SyntaxCmd        Syntax = "cmd"
)

// Shell is the interpreter that runs agent command lines. One resolved value
// drives execution, command review and the agent's prompt together.
type Shell struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Path   string `json:"path"`
	Syntax Syntax `json:"syntax"`
	// Unavailable explains why the chosen shell cannot run commands.
	Unavailable string `json:"unavailable,omitempty"`
}

// Usable reports whether commands can start in this shell.
func (s Shell) Usable() bool { return s.Path != "" && s.Unavailable == "" }

// SyntaxFor classifies a shell by its ID or executable name.
func SyntaxFor(nameOrPath string) Syntax {
	base := strings.ToLower(nameOrPath)
	if index := strings.LastIndexAny(base, `/\`); index >= 0 {
		base = base[index+1:]
	}
	switch strings.TrimSuffix(base, ".exe") {
	case "cmd":
		return SyntaxCmd
	case "powershell", "pwsh":
		return SyntaxPowerShell
	}
	return SyntaxPOSIX
}
