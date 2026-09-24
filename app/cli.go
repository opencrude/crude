package app

import (
	"flag"
	"fmt"
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
	fc := LoadFileConfig()
	if fc.Providers == nil {
		fc.Providers = make(map[string]session.ProviderConfig)
	}
	if flags.APIKey != "" && flags.Account == "" {
		return FileConfig{}, fmt.Errorf("--key requires an explicit --account")
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

	// --resume uses the session database's account pin, even if that account is
	// no longer available in the current application config.
	providerID := flags.Provider
	if providerID == "" {
		if mainCat.AccountID != "" {
			if account, exists := fc.Accounts[mainCat.AccountID]; exists {
				providerID = account.ProviderID
			}
		}
		if providerID == "" {
			providerID = fc.ActiveProvider
		}
		if providerID == "" {
			providerID = "openrouter"
		}
	}
	configuredTemplate, configured := fc.Providers[providerID]
	if !configured && provider.GetPreset(providerID) == nil {
		return FileConfig{}, fmt.Errorf("provider template %q not found; choose a configured template with --provider", providerID)
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
				return FileConfig{}, fmt.Errorf("account %q not found; select or create it in Application Settings → Accounts (or use --account %s with --key)", accountID, accountID)
			}
			selectedAccount = provider.Account{ID: accountID, ProviderID: providerID, Label: accountID}
		} else if selectedAccount.ID != accountID {
			return FileConfig{}, fmt.Errorf("account %q is invalid; select an account in Application Settings → Accounts", accountID)
		} else if selectedAccount.ProviderID != providerID {
			return FileConfig{}, fmt.Errorf("account %q uses template %q, not selected template %q", accountID, selectedAccount.ProviderID, providerID)
		}
		if flags.APIKey != "" {
			selectedAccount.APIKey = flags.APIKey
			selectedAccount.OAuth = nil
			if fc.Accounts == nil {
				fc.Accounts = make(map[string]provider.Account)
			}
			fc.Accounts[accountID] = selectedAccount
		}
		template := provider.ResolveTemplate(providerID, configuredTemplate, selectedAccount.Overrides)
		if template.AuthType != provider.AuthNone && selectedAccount.APIKey == "" &&
			(selectedAccount.OAuth == nil || (selectedAccount.OAuth.AccessToken == "" && selectedAccount.OAuth.RefreshToken == "")) {
			return FileConfig{}, fmt.Errorf("account %q has no credentials for template %q; configure it in Application Settings → Accounts or pass --key with --account", accountID, providerID)
		}
	} else if flags.Resume == "" && (flags.Provider != "" || flags.Account != "" || flags.Model != "") {
		return FileConfig{}, fmt.Errorf("provider template %q needs an explicitly selected account; use --account <id> or configure one in Application Settings → Accounts", providerID)
	}

	// CLI selectors update only this invocation's main-agent session spec.
	if flags.Model != "" || flags.Account != "" || flags.Provider != "" {
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
		currentMain, ok := prof.Categories[session.CategoryDefault]
		if !ok {
			currentMain = prof.Categories["main"]
		}
		if flags.Model != "" {
			currentMain.Model = flags.Model
		}
		if flags.Account != "" {
			currentMain.AccountID = accountID
		}
		prof.Categories[session.CategoryDefault] = currentMain
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
