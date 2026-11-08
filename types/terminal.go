package types

import (
	"fmt"
	"maps"
	"slices"

	"crude/types/command"
)

// Terminal shell selections. Concrete shell IDs come from TerminalShell
// detection on the machine that runs the core, which may be remote.
const (
	TerminalShellAuto   = "auto"
	TerminalShellCustom = "custom"

	TerminalPlacementDocked   = "docked"
	TerminalPlacementFloating = "floating"

	DefaultTerminalScrollback = 10000
)

// TerminalSettings configure integrated terminals.
type TerminalSettings struct {
	// Shell is TerminalShellAuto, a detected TerminalShell ID, or
	// TerminalShellCustom.
	Shell string `json:"shell,omitempty"`
	// Command is the executable started by the custom shell.
	Command string `json:"command,omitempty"`
	// Args follow the shell's own startup arguments.
	Args []string `json:"args,omitempty"`
	// LoginShell starts shells that support it as login shells ("on"/"off").
	LoginShell  string            `json:"login_shell,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	// ScrollbackLines bounds each terminal's retained history.
	ScrollbackLines int `json:"scrollback_lines,omitempty"`
	// Placement is where a new terminal opens: docked or floating.
	Placement string `json:"placement,omitempty"`
}

// TerminalShell is one shell installed on the core's machine. Detection
// returns shells in Auto preference order, so Auto is the first entry.
type TerminalShell struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Path  string `json:"path"`
	// LoginArgs start this shell as a login shell; empty when unsupported.
	LoginArgs []string `json:"login_args,omitempty"`
	// Args always precede the user's arguments (for example `-i`).
	Args []string `json:"args,omitempty"`
}

// NormalizeTerminalSettings fills defaults and clamps bounds.
func NormalizeTerminalSettings(s TerminalSettings) TerminalSettings {
	s = CloneTerminalSettings(s)
	if s.Shell == "" {
		s.Shell = TerminalShellAuto
	}
	if s.LoginShell != "off" {
		s.LoginShell = "on"
	}
	if s.ScrollbackLines <= 0 {
		s.ScrollbackLines = DefaultTerminalScrollback
	}
	s.ScrollbackLines = min(DefaultTerminalScrollback, max(100, s.ScrollbackLines))
	if s.Placement != TerminalPlacementFloating {
		s.Placement = TerminalPlacementDocked
	}
	return s
}

func CloneTerminalSettings(s TerminalSettings) TerminalSettings {
	s.Args = slices.Clone(s.Args)
	s.Environment = maps.Clone(s.Environment)
	return s
}

// AgentShellAuto is the stored Auto choice for agent commands; empty means
// the same.
const AgentShellAuto = "auto"

// agentShellAutoOrder is Auto's preference for agent commands. Bash comes
// first because models write bash most reliably and it starts fastest (Git
// Bash about 60 ms against 300 ms or more for PowerShell). Without bash,
// PowerShell 7 comes before Windows PowerShell 5.1, which drops embedded
// double quotes in arguments to native programs. Command Prompt is last.
var agentShellAutoOrder = []string{"git-bash", "bash", "pwsh", "powershell", "cmd"}

// AgentShellAutoChoice is the shell that Auto resolves to, if any is detected.
func AgentShellAutoChoice(shells []TerminalShell) (TerminalShell, bool) {
	for _, id := range agentShellAutoOrder {
		if index := slices.IndexFunc(shells, func(shell TerminalShell) bool { return shell.ID == id }); index >= 0 {
			return shells[index], true
		}
	}
	for _, shell := range shells {
		if shell.ID != "wsl" {
			return shell, true
		}
	}
	return TerminalShell{}, false
}

// ResolveAgentShell selects the agent command shell from the shells detected
// on the core's machine. A chosen shell that is not installed stays chosen and
// reports why it cannot run; it is never silently replaced.
func ResolveAgentShell(choice string, shells []TerminalShell) command.Shell {
	from := func(shell TerminalShell) command.Shell {
		return command.Shell{ID: shell.ID, Label: shell.Label, Path: shell.Path, Syntax: command.SyntaxFor(shell.ID)}
	}
	if choice == "" || choice == AgentShellAuto {
		if shell, ok := AgentShellAutoChoice(shells); ok {
			return from(shell)
		}
		return command.Shell{ID: "sh", Label: "sh", Path: "sh", Syntax: command.SyntaxPOSIX}
	}
	if choice == "wsl" {
		return command.Shell{ID: choice, Label: "WSL", Syntax: command.SyntaxPOSIX,
			Unavailable: "Agent commands cannot run in WSL because WSL uses a separate file system. Choose another shell in Settings › Agents › Main Agent › Command shell."}
	}
	index := slices.IndexFunc(shells, func(shell TerminalShell) bool { return shell.ID == choice })
	if index < 0 {
		return command.Shell{ID: choice, Label: choice, Syntax: command.SyntaxFor(choice),
			Unavailable: fmt.Sprintf("Agent commands cannot run. The command shell %q is not installed on this computer. Install it, or choose another shell in Settings › Agents › Main Agent › Command shell.", choice)}
	}
	return from(shells[index])
}

// AgentShellChoices lists the detected shells agents can use.
func AgentShellChoices(shells []TerminalShell) []TerminalShell {
	return slices.DeleteFunc(slices.Clone(shells), func(shell TerminalShell) bool { return shell.ID == "wsl" })
}

// ResolveTerminalLaunch selects the executable and arguments for a new
// terminal. A selected shell that is not installed fails with guidance rather
// than silently starting a different one.
func ResolveTerminalLaunch(settings TerminalSettings, shells []TerminalShell) (string, []string, error) {
	settings = NormalizeTerminalSettings(settings)
	if settings.Shell == TerminalShellCustom {
		if settings.Command == "" {
			return "", nil, fmt.Errorf("the terminal shell is set to Custom without a command; set one in Settings › Terminal")
		}
		return settings.Command, slices.Clone(settings.Args), nil
	}
	var shell TerminalShell
	switch {
	case settings.Shell == TerminalShellAuto && len(shells) > 0:
		shell = shells[0]
	case settings.Shell == TerminalShellAuto:
		return "", nil, fmt.Errorf("no supported shell was found on this machine; choose Custom in Settings › Terminal")
	default:
		index := slices.IndexFunc(shells, func(candidate TerminalShell) bool { return candidate.ID == settings.Shell })
		if index < 0 {
			return "", nil, fmt.Errorf("terminal shell %q is not installed on this machine; choose another in Settings › Terminal", settings.Shell)
		}
		shell = shells[index]
	}
	var args []string
	if settings.LoginShell == "on" {
		args = append(args, shell.LoginArgs...)
	}
	args = append(append(args, shell.Args...), settings.Args...)
	return shell.Path, args, nil
}
