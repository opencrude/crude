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
	"time"

	"crude/provider"
	"crude/session"
	"crude/sessiondb"
	"crude/tui"
	"crude/workspace"
)

// App is the IDE shell — the outermost layer of the three-layer architecture.
//
// It owns the app config and workspace lifecycle, but delegates all IDE
// business logic (sessions, LSP, agents, editor state) to the workspace.
type App struct {
	// fc is the app-level config (crude.json). Contains provider keys,
	// theme preferences, editor settings, and other app-wide configuration.
	fc FileConfig

	// home is the user's home directory (~).
	home string

	// projectDir is the project directory for this IDE instance.
	projectDir string

	// sessionsRoot is the base directory for session databases.
	// Layout: ~/.crude/sessions/ (legacy) — new sessions live under workspaces.
	sessionsRoot string

	// resumeID is an optional session ID from --resume.
	resumeID string
}

// Opts configures the App. Passed to New() during initialization.
type Opts struct {
	FC           FileConfig
	Home         string
	SessionsRoot string
	ProjectDir   string // project directory (defaults to cwd)
	ResumeID     string // optional session to resume at startup
}

// New creates the IDE shell. Resolves paths and stores config.
// Does NOT create sessions or start the TUI — that happens in Run().
func New(opts Opts) (*App, error) {
	home := opts.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}

	return &App{
		fc:           opts.FC,
		home:         home,
		projectDir:   opts.ProjectDir,
		sessionsRoot: opts.SessionsRoot,
		resumeID:     opts.ResumeID,
	}, nil
}

// Run starts the IDE. This is the main entry point that:
//  1. Creates a workspace (the IDE instance)
//  2. Optionally resumes a session from --resume
//  3. Starts the TUI with the workspace (launcher = workspace selector)
//  4. Enters the main event loop
//
// When the TUI exits, Run() returns.
func (a *App) Run() {
	lg := &appLogger{}

	// 1. Create the workspace shell (no session until launcher/resume picks one).
	ws := workspace.NewWorkspace(
		a.projectDir,
		a.fc.ActiveProvider, // model name (will be overridden by session)
		session.ResolveUsername(),
		a.initialSettings(),
		lg,
	)

	// 2. Convert app config to session config.
	sessCfg := FileConfigToSession(a.fc)
	ws.SetBackendConfig(sessCfg, a.home)

	// Persist provider credentials back to config.json when settings save.
	ws.OnConfigSave = func(cfg *session.Config) error {
		a.fc = SessionToFileConfig(cfg)
		// Preserve visual app-level prefs already on FileConfig.
		return SaveFileConfig(a.fc)
	}

	// 3. Optional --resume: bootstrap that session now (launcher still available
	//  for multi-session after the TUI starts).
	if a.resumeID != "" {
		if _, err := ws.Bootstrap(sessCfg, a.home, a.resumeID); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to resume session: %v\n", err)
			os.Exit(1)
		}
	}

	// 4. Start the TUI with provider presets for app settings.
	history := LoadHistory()
	presets := providerPresets()
	t, err := tui.Start(ws, presets, history, lg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start TUI: %v\n", err)
		os.Exit(1)
	}

	// 5. Store the EventSender on the workspace for orchestrator creation.
	if g := tui.GetGlobal(); g != nil {
		ws.SetEventSender(g)
	}

	// 6. If --resume, start orchestrator + hydrate into TUI.
	if a.resumeID != "" && ws.ActiveSession != nil {
		if g := tui.GetGlobal(); g != nil {
			orch, orchErr := ws.StartOrchestrator(g)
			if orchErr == nil && orch != nil {
				t.AddOrchestrator(orch.ID, orch.InputCh, orch.BtwCh, orch.CancelCh, orch.BlockResolveCh)
			}
		}
		ws.Hydrate()
		if msgs := ws.ActiveSession.HydratedMessages(); len(msgs) > 0 {
			t.HydrateMessages(msgs)
		}
		if refs := ws.ActiveSession.HydratedRefs(); len(refs) > 0 {
			t.SetReferences(refs)
		}
	}

	// 7. Wait for exit.
	t.Wait()
	ws.CloseAllSessions()
	ws.Close()
}

func providerPresets() []tui.ProviderPreset {
	out := make([]tui.ProviderPreset, 0, len(provider.Presets))
	for _, p := range provider.Presets {
		out = append(out, tui.ProviderPreset{
			ID: p.ID, Name: p.Name, HelpText: p.HelpText, BaseURL: p.BaseURL,
		})
	}
	return out
}

// initialSettings builds the initial settings snapshot from the app config.
func (a *App) initialSettings() tui.Settings {
	settings := tui.Settings{
		ProviderID:      a.fc.ActiveProvider,
		TurnLogsEnabled: boolStr(a.fc.IsTurnLogsEnabled()),
	}
	if pc, ok := a.fc.Providers[a.fc.ActiveProvider]; ok {
		settings.APIKey = pc.APIKey
		settings.ProviderAPIKey = pc.APIKey
		settings.ProviderURL = pc.ProviderURL
		settings.Model = pc.Model
		settings.ThinkingEffort = pc.ThinkingEffort
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
