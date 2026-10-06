package app

import (
	"flag"
	"fmt"
	"maps"
	"os"

	"crude/provider"
	"crude/session"
	"crude/tui/perf"
)

// CLIFlags holds parsed command-line flags.
type CLIFlags struct {
	APIKey        string
	Account       string
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
	apiKeyFlag := flag.String("key", "", "API key for the selected provider account")
	accountFlag := flag.String("account", "", "Explicit account ID for the selected provider")
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
		Account:       *accountFlag,
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

// ResolveConfig loads FileConfig and applies invocation-local CLI selectors.
func ResolveConfig(flags CLIFlags) (FileConfig, error) {
	return resolveConfigFrom(LoadFileConfig(), flags)
}

func resolveConfigFrom(fc FileConfig, flags CLIFlags) (FileConfig, error) {
	// Stage the exact invocation selection before validation. An unavailable
	// --account must not leave another saved account selected in the usable shell.
	fc.Accounts = cloneAccounts(fc.Accounts)
	if fc.Agents != nil {
		agents := *fc.Agents
		agents.Profiles = maps.Clone(agents.Profiles)
		for name, profile := range agents.Profiles {
			profile.Categories = maps.Clone(profile.Categories)
			agents.Profiles[name] = profile
		}
		fc.Agents = &agents
	}
	if flags.Model != "" || flags.Account != "" || flags.Provider != "" {
		if fc.Agents == nil {
			agents := session.DefaultAgentsConfig()
			fc.Agents = &agents
		}
		ap := fc.Agents.ActiveProfile
		if ap == "" {
			ap = "default"
			fc.Agents.ActiveProfile = ap
		}
		if fc.Agents.Profiles == nil {
			fc.Agents.Profiles = make(map[string]session.ProfileConfig)
		}
		prof := fc.Agents.Profiles[ap]
		if prof.Categories == nil {
			prof.Categories = make(map[string]session.CategoryConfig)
		}
		main := prof.Categories[session.CategoryDefault]
		if flags.Model != "" {
			main.Model = flags.Model
		}
		if flags.Account != "" {
			main.AccountID = flags.Account
		}
		prof.Categories[session.CategoryDefault] = main
		fc.Agents.Profiles[ap] = prof
	}
	if flags.APIKey != "" && flags.Account == "" {
		return fc, fmt.Errorf("--key requires an explicit --account")
	}

	profileName := "default"
	var mainCat session.CategoryConfig
	if fc.Agents != nil {
		if fc.Agents.ActiveProfile != "" {
			profileName = fc.Agents.ActiveProfile
		}
		profile := fc.Agents.Profiles[profileName]
		var exists bool
		mainCat, exists = profile.Categories[session.CategoryDefault]
		if !exists {
			mainCat = profile.Categories["main"]
		}
	}

	accountID := flags.Account
	if accountID == "" && flags.Resume == "" {
		accountID = mainCat.AccountID
	}
	var selectedAccount provider.Account
	if accountID != "" {
		var exists bool
		selectedAccount, exists = fc.Accounts[accountID]
		if !exists {
			if flags.Account == "" || flags.APIKey == "" {
				return fc, fmt.Errorf("account %q not found; select or create it in Application Settings → Accounts (or use --account %s with --key)", accountID, accountID)
			}
			seed, ok := provider.DefaultTemplates()[flags.Provider]
			if !ok {
				return fc, fmt.Errorf("a new CLI account requires --provider naming a known preset")
			}
			selectedAccount = provider.Account{ID: accountID, ProviderID: flags.Provider, Label: accountID, Connection: seed}
		} else if selectedAccount.ID != accountID {
			return fc, fmt.Errorf("account %q is invalid; select an account in Application Settings → Accounts", accountID)
		} else if flags.Provider != "" && selectedAccount.ProviderID != flags.Provider {
			return fc, fmt.Errorf("account %q uses connection %q, not requested %q", accountID, selectedAccount.ProviderID, flags.Provider)
		}
		if flags.APIKey != "" {
			selectedAccount.APIKey = flags.APIKey
			selectedAccount.OAuth = nil
			if fc.Accounts == nil {
				fc.Accounts = make(map[string]provider.Account)
			}
			fc.Accounts[accountID] = selectedAccount
		}
		template := selectedAccount.Connection
		if err := template.Validate(); err != nil {
			return fc, fmt.Errorf("account %q connection is invalid: %w", accountID, err)
		}
		if template.AuthType != provider.AuthNone && selectedAccount.APIKey == "" &&
			(selectedAccount.OAuth == nil || (selectedAccount.OAuth.AccessToken == "" && selectedAccount.OAuth.RefreshToken == "")) {
			return fc, fmt.Errorf("account %q has no credentials; configure it in Application Settings → Accounts or pass --key with --account", accountID)
		}
	} else if flags.Resume == "" && (flags.Provider != "" || flags.Account != "" || flags.Model != "") {
		return fc, fmt.Errorf("select an explicit account with --account or configure one in Application Settings → Accounts")
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

	prepared := PrepareStartup(flags)

	home, _ := os.UserHomeDir()
	projectDir, _ := os.Getwd()
	sessionsRoot, _ := session.SessionsRoot()

	application, err := New(Opts{
		FC:            prepared.Config,
		Home:          home,
		SessionsRoot:  sessionsRoot,
		ProjectDir:    projectDir,
		ResumeID:      prepared.ResumeID,
		StartupIssues: prepared.Issues,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	if err := application.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	return 0
}
