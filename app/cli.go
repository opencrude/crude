package app

import (
	"flag"
	"fmt"
	"os"

	"crude/session"
	"crude/tui/perf"
)

// CLIFlags holds parsed command-line flags.
type CLIFlags struct {
	APIKey        string
	Model         string
	Provider      string
	SystemPrompt  string
	Resume        string
	ListSessions  bool
	Limit         int
	Offset        int
	DeleteSession string
}

// ParseFlags parses os.Args flags into CLIFlags.
func ParseFlags() CLIFlags {
	apiKeyFlag := flag.String("key", "", "API key (or set CRUDE_API_KEY)")
	modelFlag := flag.String("model", "", "Model to use")
	providerFlag := flag.String("provider", "", "Provider ID (openrouter, openai, anthropic, etc.)")
	sysPromptFlag := flag.String("system", "", "Override system prompt")
	resumeFlag := flag.String("resume", "", "Resume session (session ID, 'last', or empty for picker)")
	listSessionsFlag := flag.Bool("list-sessions", false, "List sessions and exit")
	limitFlag := flag.Int("limit", 50, "Number of sessions to list (used with --list-sessions)")
	offsetFlag := flag.Int("offset", 0, "Offset for session listing (used with --list-sessions)")
	deleteSessionFlag := flag.String("delete-session", "", "Delete a session by ID")
	flag.Parse()

	_ = sysPromptFlag // reserved for future use

	return CLIFlags{
		APIKey:        *apiKeyFlag,
		Model:         *modelFlag,
		Provider:      *providerFlag,
		SystemPrompt:  *sysPromptFlag,
		Resume:        *resumeFlag,
		ListSessions:  *listSessionsFlag,
		Limit:         *limitFlag,
		Offset:        *offsetFlag,
		DeleteSession: *deleteSessionFlag,
	}
}

// ResolveConfig loads FileConfig and applies CLI flag / env overrides.
func ResolveConfig(flags CLIFlags) (FileConfig, error) {
	fc := LoadFileConfig()
	if fc.Providers == nil {
		fc.Providers = make(map[string]session.ProviderConfig)
	}

	// Resolve provider ID from flag → config → default.
	providerID := fc.ActiveProvider
	if providerID == "" {
		providerID = "openrouter"
		fc.ActiveProvider = providerID
	}
	if flags.Provider != "" {
		providerID = flags.Provider
		fc.ActiveProvider = providerID
	}

	// Resolve API key from flag → env vars → config.
	pc := fc.Providers[providerID]
	if pc.APIKey == "" {
		if flags.APIKey != "" {
			pc.APIKey = flags.APIKey
		} else {
			pc.APIKey = os.Getenv("CRUDE_API_KEY")
			if pc.APIKey == "" {
				pc.APIKey = os.Getenv("WAFER_API_KEY")
			}
			if pc.APIKey == "" {
				pc.APIKey = os.Getenv("OPENROUTER_API_KEY")
			}
		}
	}
	fc.Providers[providerID] = pc

	// Apply --model flag to the active profile's default category.
	if flags.Model != "" {
		if fc.Agents == nil {
			agents := session.DefaultAgentsConfig()
			fc.Agents = &agents
		}
		ap := fc.Agents.ActiveProfile
		if ap == "" {
			ap = "default"
		}
		if fc.Agents.Profiles == nil {
			fc.Agents.Profiles = make(map[string]session.ProfileConfig)
		}
		prof := fc.Agents.Profiles[ap]
		if prof.Categories == nil {
			prof.Categories = make(map[string]session.CategoryConfig)
		}
		mainCat := prof.Categories[session.CategoryDefault]
		mainCat.Model = flags.Model
		mainCat.Provider = providerID
		prof.Categories[session.CategoryDefault] = mainCat
		fc.Agents.Profiles[ap] = prof
	}

	return fc, nil
}

// HandleCLICommands runs CLI-only operations (list/delete sessions).
// Returns true if a CLI-only command was handled (caller should exit).
func HandleCLICommands(flags CLIFlags) bool {
	sessionsRoot, _ := session.SessionsRoot()

	if flags.ListSessions {
		ListSessions(sessionsRoot, flags.Limit, flags.Offset)
		return true
	}
	if flags.DeleteSession != "" {
		DeleteSession(sessionsRoot, flags.DeleteSession)
		return true
	}
	return false
}

// ResolveResume resolves the --resume flag. Empty means launcher picker.
func ResolveResume(flags CLIFlags) (string, error) {
	if flags.Resume == "" {
		return "", nil
	}
	sessionsRoot, _ := session.SessionsRoot()
	return ResolveResumeID(sessionsRoot, flags.Resume)
}

// Main is the app-layer entry point: parse flags, resolve config, run IDE.
func Main() int {
	flags := ParseFlags()

	// Live perf sentinel (tui/perf). main.go discards slog to protect the
	// TUI surface, so the sentinel writes directly to this file when set.
	if err := perf.ConfigureSentinel(os.Getenv("CRUDE_PERF_LOG")); err != nil {
		fmt.Fprintf(os.Stderr, "crude-perf: %v\n", err)
	}

	if HandleCLICommands(flags) {
		return 0
	}

	resumeID, err := ResolveResume(flags)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}

	fc, err := ResolveConfig(flags)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	home, _ := os.UserHomeDir()
	projectDir, _ := os.Getwd()
	sessionsRoot, _ := session.SessionsRoot()

	application, err := New(Opts{
		FC:           fc,
		Home:         home,
		SessionsRoot: sessionsRoot,
		ProjectDir:   projectDir,
		ResumeID:     resumeID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	application.Run()
	return 0
}
