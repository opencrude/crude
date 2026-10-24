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
	"context"
	"crude/buildinfo"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// App is the IDE shell: the outermost layer of the three-layer architecture.
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

	// sessionsRoot is the CLI session listing root; interactive sessions live
	// under their workspace folder.
	sessionsRoot string

	// resumeID is an optional session ID from --resume.
	resumeID      string
	clientProfile string
	invocation    CLIFlags

	startupIssues []ttypes.OperationIssue

	// defaultsChanged is installed by the core host to republish committed
	// application defaults to every retained workspace.
	defaultsChanged func()
	remote          *coreRemote // listener owner, installed by the retained core host
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
// Does NOT create sessions or start the TUI; Run() does that.
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
		home, _ = buildinfo.Home()
	}

	application := &App{
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
	}
	application.accounts = provider.NewAccountManager(application.fc.Accounts, application.persistAccountRotation)
	return application, nil
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
		before := a.committedDefaults()
		s.Accounts = nil
		return a.commitCorePreferences(ttypes.SettingsEdits(before, s))
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

type cliSessionStore struct {
	workspaceDB string
	sessionsDir string
}

var errNoSavedWorkspace = errors.New("no saved workspace exists for this path")

func resolveCLISessionStore(home, projectDir string) (cliSessionStore, error) {
	if strings.TrimSpace(projectDir) == "" {
		return cliSessionStore{}, fmt.Errorf("workspace path is required")
	}
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return cliSessionStore{}, fmt.Errorf("resolve workspace path: %w", err)
	}
	dir := filepath.Join(DataDir(home), "workspaces", workspace.WorkspaceID(abs))
	return cliSessionStore{workspaceDB: filepath.Join(dir, "workspace.db"), sessionsDir: filepath.Join(dir, "sessions")}, nil
}

func openCLIWorkspaceDB(store cliSessionStore) (*sql.DB, error) {
	if _, err := os.Stat(store.workspaceDB); err != nil {
		if os.IsNotExist(err) {
			return nil, errNoSavedWorkspace
		}
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(store.workspaceDB) + "?mode=ro&_busy_timeout=1000"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func listCLISessions(home, projectDir string) ([]sessiondb.SessionMeta, error) {
	store, err := resolveCLISessionStore(home, projectDir)
	if err != nil {
		return nil, err
	}
	db, err := openCLIWorkspaceDB(store)
	if err != nil {
		if errors.Is(err, errNoSavedWorkspace) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`
		SELECT session_id, project_dir, project_name, model, provider,
		       created_at, closed_at, message_count, turn_count, first_prompt, last_prompt
		FROM workspace_sessions
		WHERE db_missing = 0
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("read current workspace session registry: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var metas []sessiondb.SessionMeta
	for rows.Next() {
		var m sessiondb.SessionMeta
		if err := rows.Scan(&m.ID, &m.ProjectPath, &m.ProjectName, &m.Model, &m.Provider,
			&m.CreatedAt, &m.ClosedAt, &m.MessageCount, &m.TurnCount, &m.FirstPrompt, &m.LastPrompt); err != nil {
			return nil, err
		}
		if !validCLISessionID(m.ID) {
			return nil, fmt.Errorf("invalid session identity in workspace registry")
		}
		if info, err := os.Lstat(filepath.Join(store.sessionsDir, m.ID+".db")); err == nil && info.Mode().IsRegular() {
			metas = append(metas, m)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return metas, rows.Err()
}

// ListSessions prints sessions from the current workspace store. Used by
// `crude --list-sessions`.
func ListSessions(home, projectDir string, limit, offset int) error {
	if offset < 0 {
		return fmt.Errorf("offset must not be negative")
	}
	metas, err := listCLISessions(home, projectDir)
	if err != nil {
		return err
	}
	total := len(metas)
	if total == 0 {
		fmt.Println("No sessions found.")
		return nil
	}
	if limit <= 0 {
		limit = 50
	}
	start := min(offset, total)
	end := start + min(limit, total-start)
	metas = metas[start:end]
	if len(metas) == 0 {
		fmt.Printf("No sessions at offset %d (%d total).\n", offset, total)
		return nil
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
	return nil
}

func validCLISessionID(id string) bool {
	return id != "" && filepath.Base(id) == id && id != "." && id != ".." && !strings.ContainsAny(id, "/\\\x00:")
}

// DeleteSession deletes one closed session from the exact current workspace.
func DeleteSession(home, projectDir, sessionID string) (resultErr error) {
	if !validCLISessionID(sessionID) {
		return fmt.Errorf("invalid session ID %q", sessionID)
	}
	// Own the same exclusive endpoint as the core for the entire maintenance
	// operation. A failed status probe is not proof that no live writer exists.
	guard, err := listenCore(context.Background(), DataDir(home))
	if err != nil {
		return fmt.Errorf("session deletion requires an offline core: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, guard.Close()) }()
	store, err := resolveCLISessionStore(home, projectDir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(store.workspaceDB); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no saved workspace exists for this path")
		}
		return err
	}
	dsn := "file:" + filepath.ToSlash(store.workspaceDB) + "?_busy_timeout=1000"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var closedAt int64
	if err := tx.QueryRow(`SELECT closed_at FROM workspace_sessions WHERE session_id = ? AND db_missing = 0`, sessionID).Scan(&closedAt); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("session %q is not registered in this workspace", sessionID)
		}
		return err
	}
	if closedAt == 0 {
		return fmt.Errorf("session %q is still active", sessionID)
	}
	dbPath := filepath.Join(store.sessionsDir, sessionID+".db")
	info, err := os.Lstat(dbPath)
	if err != nil {
		return fmt.Errorf("session %q storage is unavailable: %w", sessionID, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("session %q storage is not a regular file", sessionID)
	}
	quarantine, err := os.MkdirTemp(filepath.Dir(store.sessionsDir), ".session-delete-")
	if err != nil {
		return err
	}
	// Keep the exact registry preimage and every SQLite sidecar for recovery.
	var registry map[string]any
	rows, err := tx.Query(`SELECT * FROM workspace_sessions WHERE session_id=?`, sessionID)
	if err != nil {
		return err
	}
	columns, err := rows.Columns()
	if err != nil {
		_ = rows.Close()
		return err
	}
	if rows.Next() {
		values, pointers := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			_ = rows.Close()
			return err
		}
		registry = make(map[string]any, len(columns))
		for i, key := range columns {
			registry[key] = values[i]
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	manifest, err := json.MarshalIndent(map[string]any{"session_id": sessionID, "workspace": projectDir, "registry": registry, "original_directory": store.sessionsDir}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(quarantine, "recovery.json"), manifest, 0600); err != nil {
		return err
	}
	var moved []string
	committed := false
	defer func() {
		if !committed {
			for i := len(moved) - 1; i >= 0; i-- {
				name := moved[i]
				if err := os.Rename(filepath.Join(quarantine, name), filepath.Join(store.sessionsDir, name)); err != nil {
					resultErr = errors.Join(resultErr, fmt.Errorf("restore %s failed; preserved at %s: %w", name, quarantine, err))
				}
			}
		}
	}()
	for _, name := range []string{sessionID + ".db", sessionID + ".db-wal", sessionID + ".db-shm", sessionID} {
		source := filepath.Join(store.sessionsDir, name)
		info, err := os.Lstat(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink session artifact %s", name)
		}
		if err := os.Rename(source, filepath.Join(quarantine, name)); err != nil {
			return fmt.Errorf("stage session artifact %s: %w", name, err)
		}
		moved = append(moved, name)
	}
	result, err := tx.Exec(`DELETE FROM workspace_sessions WHERE session_id = ?`, sessionID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return fmt.Errorf("session %q registry changed during deletion", sessionID)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	fmt.Printf("Session recovery copy: %s\n", quarantine)
	return nil
}

// ResolveResumeID resolves the --resume flag to a session ID.
// Supports "last", "true", or an explicit session ID.
func ResolveResumeID(home, projectDir, resumeFlag string) (string, error) {
	if resumeFlag == "" {
		return "", nil
	}
	metas, err := listCLISessions(home, projectDir)
	if err != nil {
		return "", err
	}
	if resumeFlag == "last" || resumeFlag == "true" {
		if len(metas) == 0 {
			return "", fmt.Errorf("no sessions found to resume")
		}
		return metas[0].ID, nil
	}
	for _, meta := range metas {
		if meta.ID == resumeFlag {
			return resumeFlag, nil
		}
	}
	return "", fmt.Errorf("session %q is not available in this workspace", resumeFlag)
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
