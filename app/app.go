// Package app is the IDE shell and workspace lifecycle manager.
//
// Following the JetBrains model, app/ is the outermost layer that:
// - Owns the app config (crude.json / config.json)
// - Manages workspace lifecycle (create, list, resume, delete)
// - Handles CLI flags and dispatching
//
// app/ does NOT know about:
// - Session internals (that's workspace/ + session/)
// - LSP connections (that's workspace/)
// - Agent orchestration (that's session/)
// - TUI wiring or callbacks (that's tui/ ↔ workspace/)
// - Rendering or input handling (that's tui/)
//
// The app creates a workspace (the IDE instance) and hands it to the TUI.
// The workspace owns all business state; the TUI owns all rendering.
package app

import (
	"fmt"
	"os"
	"sync"
	"time"

	"crude/network"
	"crude/provider"
	"crude/sessiondb"
	"crude/tui"
	ttypes "crude/types"
	captypes "crude/types/capability"
	"crude/workspace"
)

// App is the IDE shell — the outermost layer of the three-layer architecture.
//
// It owns the app config and workspace lifecycle, but delegates all IDE
// business logic (sessions, LSP, agents, editor state) to the workspace.
type App struct {
	// fc is the app-level config; account credentials live only in Accounts.
	fc       FileConfig
	configMu sync.Mutex
	accounts *provider.AccountManager
	// persistedAccounts guards a refresh against an account replacement made
	// after that refresh began (including changes by another process).
	persistedAccounts map[string]provider.Account
	// Rotation predecessors identify credentials from a settings form opened
	// before a token refresh; they are not identities for an external reconnect.
	rotationPredecessors map[string][]provider.Account

	// home is the user's home directory (~).
	home string

	// projectDir is the project directory for this IDE instance.
	projectDir string

	// sessionsRoot is the base directory for session databases.
	// Layout: ~/.crude/sessions/ (legacy) — new sessions live under workspaces.
	sessionsRoot string

	// resumeID is an optional session ID from --resume.
	resumeID      string
	clientProfile string
	invocation    CLIFlags

	startupIssues []ttypes.OperationIssue

	// defaultsChanged is installed by the core host to republish committed
	// application defaults to every retained workspace.
	defaultsChanged func()
}

// Opts configures the App. Passed to New() during initialization.
type Opts struct {
	FC            FileConfig
	Home          string
	SessionsRoot  string
	ProjectDir    string // project directory (defaults to cwd)
	ResumeID      string // optional session to resume at startup
	ClientProfile string
	Invocation    CLIFlags
	StartupIssues []ttypes.OperationIssue
}

// New creates the IDE shell. Resolves paths and stores config.
// Does NOT create sessions or start the TUI — that happens in Run().
func New(opts Opts) (*App, error) {
	issues := append([]ttypes.OperationIssue(nil), opts.StartupIssues...)
	if err := network.ConfigureStartupDefault(FileConfigToAppSettings(opts.FC).Network); err != nil {
		issues = append(issues, networkStartupIssue(err))
	}
	for _, issue := range issues {
		if issue.Target == ttypes.RepairConfigFile {
			network.BlockStartupDefault(fmt.Errorf("saved configuration needs repair: %s", issue.Detail))
			break
		}
	}
	home := opts.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}

	return &App{
		fc:                   opts.FC,
		persistedAccounts:    cloneAccounts(opts.FC.Accounts),
		rotationPredecessors: make(map[string][]provider.Account),
		home:                 home,
		projectDir:           opts.ProjectDir,
		sessionsRoot:         opts.SessionsRoot,
		resumeID:             opts.ResumeID,
		clientProfile:        opts.ClientProfile,
		invocation:           opts.Invocation,
		startupIssues:        issues,
	}, nil
}

// Run starts a connected shell. The retained core, not this process, owns
// configuration, services and sessions; shell exit only releases attachments.
func (a *App) Run() error {
	return runConnectedShell(a.home, a.projectDir, a.resumeID, a.clientProfile, a.invocation)
}

// persistAccountRotation runs under the manager commit lock so account saves
// cannot interleave between durable rotation and the manager's installation.
func (a *App) persistAccountRotation(account provider.Account) error {
	a.configMu.Lock()
	defer a.configMu.Unlock()
	expected, ok := a.persistedAccounts[account.ID]
	if !ok || expected.ProviderID != account.ProviderID {
		return fmt.Errorf("account %q was removed or reconnected", account.ID)
	}
	if err := persistRotatedAccount(account, expected); err != nil {
		return err
	}
	a.rotationPredecessors[account.ID] = append(a.rotationPredecessors[account.ID], cloneAccount(expected))
	a.persistedAccounts[account.ID] = cloneAccount(account)
	a.fc.Accounts = cloneAccounts(a.persistedAccounts)
	return nil
}

// configureWorkspace installs the same callbacks used by Run, allowing save
// tests to exercise the real workspace entry point without starting the TUI.
func (a *App) configureWorkspace(ws *workspace.Workspace) {
	// The callback updates only its account: never write an old app snapshot.
	if a.accounts == nil {
		a.accounts = provider.NewAccountManager(a.fc.Accounts, a.persistAccountRotation)
	}
	sessCfg := FileConfigToSession(a.fc)
	sessCfg.AccountManager = a.accounts
	ws.SetBackendConfig(sessCfg, a.home)

	ws.OnAppSettingsSave = func(s ttypes.AppSettings) (ttypes.AppSettings, error) {
		if err := s.Network.Validate(); err != nil {
			return ttypes.AppSettings{}, fmt.Errorf("network settings were not saved: %w", err)
		}
		// Lock order is manager -> app config -> interprocess file lock, the
		// same order as refresh persistence. Installation is atomic with disk.
		var updated FileConfig
		err := a.accounts.Commit(func(live map[string]provider.Account) (map[string]provider.Account, error) {
			a.configMu.Lock()
			defer a.configMu.Unlock()
			// Certificate reads belong to the off-UI preferences command, and
			// only to an actual network edit, not an unrelated theme save.
			if network.Normalize(s.Network) != FileConfigToAppSettings(a.fc).Network || network.StartupPolicyError() != nil {
				if err := s.Network.ValidateFiles(); err != nil {
					return nil, fmt.Errorf("network settings were not saved: %w", err)
				}
			}
			var saveErr error
			updated, saveErr = updateFileConfig(func(current *FileConfig) error {
				// Account and provider-template writes have explicit owners. A
				// preferences save must never replay either from a stale settings
				// snapshot, including provider defaults.
				s.Accounts = nil

				ApplyAppSettingsToFileConfig(current, s)
				return nil
			})
			if saveErr != nil {
				return nil, saveErr
			}
			a.fc = updated
			a.persistedAccounts = cloneAccounts(updated.Accounts)
			a.rotationPredecessors = make(map[string][]provider.Account)
			return updated.Accounts, nil
		})
		if err != nil {
			return ttypes.AppSettings{}, err
		}
		if err := network.ConfigureDefault(FileConfigToAppSettings(updated).Network); err != nil {
			return ttypes.AppSettings{}, err
		}
		a.notifyDefaultsChanged()
		// Persistence returns a snapshot. The workspace/UI owner publishes it
		// only after its generation-bound command result arrives.
		return FileConfigToAppSettings(updated), nil
	}
	ws.OnAppAccountCommit = a.commitCoreAccount
	ws.OnAppPreferencesCommit = a.commitCorePreferences
	ws.OnAppAccountChange = func(change ttypes.AccountChange) error {
		updated, err := a.commitCoreAccount(change)
		if err != nil {
			return err
		}
		ws.SetAppDefaults(updated)
		return nil
	}
	ws.SetAppDefaults(FileConfigToAppSettings(a.fc))
	ws.OnAppCapabilityRegistration = func(change captypes.RegistrationChange) (captypes.Settings, error) {
		var updated FileConfig
		// Capture the effective acquisition policy before taking the config
		// lock: it is a workspace DB read and must not nest under configMu.
		effective := ws.LoadAppSettings().Capabilities
		err := a.accounts.Commit(func(_ map[string]provider.Account) (map[string]provider.Account, error) {
			a.configMu.Lock()
			defer a.configMu.Unlock()
			var saveErr error
			updated, saveErr = updateFileConfig(func(current *FileConfig) error {
				return mergeCapabilityRegistration(current, change, effective)
			})
			if saveErr != nil {
				return nil, saveErr
			}
			a.fc = updated
			a.persistedAccounts = cloneAccounts(updated.Accounts)
			return updated.Accounts, nil
		})
		if err != nil {
			return captypes.Settings{}, err
		}
		ws.SetAppDefaults(FileConfigToAppSettings(updated))
		a.notifyDefaultsChanged()
		if updated.Capabilities == nil {
			return captypes.Settings{}, nil
		}
		return captypes.Clone(*updated.Capabilities), nil
	}
}

// mergeCapabilityRegistration applies one reviewed application capability
// acquisition to the durable config through the shared registration owner.
// Only the selected identity is written; unrelated preferences and sibling
// registrations are preserved, and a stale preimage, source or acquisition
// policy fails closed.
func mergeCapabilityRegistration(current *FileConfig, change captypes.RegistrationChange, policy captypes.Settings) error {
	if current.Capabilities == nil {
		current.Capabilities = &captypes.Settings{}
	}
	merged, err := captypes.ApplyRegistration(captypes.Normalize(*current.Capabilities), change, policy)
	if err != nil {
		return err
	}
	current.Capabilities = &merged
	return nil
}

func providerPresets() []tui.ProviderPreset {
	out := make([]tui.ProviderPreset, 0, len(provider.PresetList()))
	for _, p := range provider.PresetList() {
		out = append(out, tui.ProviderPreset{
			ID: p.ID, Name: p.Name, HelpText: p.HelpText,
			BaseURL: p.BaseURL, StandardID: p.StandardID,
		})
	}
	return out
}

// initialSettings builds the initial settings snapshot from the app config.
func (a *App) initialSettings() ttypes.Settings { return a.initialSettingsFrom(a.fc) }

// initialSettingsFrom builds a workspace's initial settings from one config
// snapshot, so the core can build workspaces from its latest committed config.
func (a *App) initialSettingsFrom(fc FileConfig) ttypes.Settings {
	settings := ttypes.Settings{
		TurnLogsEnabled: boolStr(fc.IsTurnLogsEnabled()),
	}
	main := FileConfigToSession(fc).GetActiveDefaultCategory()
	settings.AccountID, settings.Model, settings.ThinkingEffort = main.AccountID, main.Model, main.ThinkingEffort
	if account, ok := fc.Accounts[main.AccountID]; ok {
		settings.ProviderID = account.ProviderID
		settings.APIKey, settings.ProviderAPIKey = account.APIKey, account.APIKey
		template := account.Connection
		settings.ProviderURL, settings.APIStandard = template.ProviderURL, template.APIStandard
	}
	return settings
}

func boolStr(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// CLI Commands
//
// These functions are called directly from main.go for CLI-only operations
// (list, delete) that don't start the TUI.

// ListSessions prints all sessions to stdout. Used by `crude --list-sessions`.
func ListSessions(sessionsRoot string, limit, offset int) {
	metas, total, err := sessiondb.ListSessionsPaginated(sessionsRoot, sessiondb.ListSessionsOpts{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing sessions: %v\n", err)
		return
	}
	if total == 0 {
		fmt.Println("No sessions found.")
		return
	}
	fmt.Printf("Showing %d-%d of %d sessions\n\n", offset+1, offset+len(metas), total)
	fmt.Printf("%-30s %-20s %-8s %-8s %-20s %-16s %s\n", "ID", "Project", "Turns", "Msgs", "Model", "Created", "First Prompt")
	sep := make([]byte, 140)
	for i := range sep {
		sep[i] = '-'
	}
	fmt.Println(string(sep))
	for _, m := range metas {
		firstPrompt := ""
		if m.FirstPrompt.Valid {
			firstPrompt = m.FirstPrompt.String
		}
		created := ""
		if m.CreatedAt > 0 {
			created = time.Unix(0, m.CreatedAt).Format("2006-01-02 15:04")
		}
		fmt.Printf("%-30s %-20s %-8d %-8d %-20s %-16s %.40s\n", m.ID, m.ProjectName, m.TurnCount, m.MessageCount, m.Model, created, firstPrompt)
	}
}

// DeleteSession deletes a session by ID. Used by `crude --delete-session`.
func DeleteSession(sessionsRoot, sessionID string) {
	if err := sessiondb.DeleteSession(sessionsRoot, sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "Error deleting session: %v\n", err)
		return
	}
	fmt.Printf("Session %s deleted.\n", sessionID)
}

// ResolveResumeID resolves the --resume flag to a session ID.
// Supports "last", "true", or an explicit session ID.
func ResolveResumeID(sessionsRoot, resumeFlag string) (string, error) {
	if resumeFlag == "" {
		return "", nil
	}
	if resumeFlag == "last" || resumeFlag == "true" {
		metas, err := sessiondb.ListSessions(sessionsRoot)
		if err != nil || len(metas) == 0 {
			return "", fmt.Errorf("no sessions found to resume")
		}
		return metas[0].ID, nil
	}
	return resumeFlag, nil
}

// appLogger is a minimal logger that discards everything.
// The session package creates its own logger for turn logs.
// The workspace logger is only for workspace-level debug events.
type appLogger struct{}

func (l *appLogger) Info(tag, msg string, pairs ...any)  {}
func (l *appLogger) Warn(tag, msg string, pairs ...any)  {}
func (l *appLogger) Error(tag, msg string, pairs ...any) {}
func (l *appLogger) Debug(tag, msg string, pairs ...any) {}

// Ensure appLogger satisfies workspace.Logger.
var _ workspace.Logger = (*appLogger)(nil)
