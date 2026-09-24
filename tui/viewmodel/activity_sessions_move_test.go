package viewmodel

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/tui/screens/statusoverview"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/workspace"
)

func seedWorkspaceSession(t *testing.T, app *App, id, title string) {
	t.Helper()
	path, err := workspace.SessionDBPath(app.RootPath, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sessiondb.OpenAndMigrate(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := sessiondb.Create(db, sessiondb.SessionMeta{
		ID: id, ProjectPath: app.RootPath, ProjectName: "Test project", CreatedAt: time.Now().UnixNano(),
		Provider: "openai", AccountID: "test", FirstPrompt: sql.NullString{String: title, Valid: true}, Model: "test-model", TurnCount: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.WorkspaceDB.RegisterSession(id, app.RootPath, "Test project", "test-model", "test-provider"); err != nil {
		t.Fatal(err)
	}
	if err := app.WorkspaceDB.UpdateSessionMeta(id, 3, 6, title, "last prompt", 0); err != nil {
		t.Fatal(err)
	}
}

func TestPreviousSessionsResumeDirectlyInsideWorkspace(t *testing.T) {
	for _, keyboard := range []bool{false, true} {
		t.Run(mapBool(keyboard, "keyboard", "mouse"), func(t *testing.T) {
			app := newTransitionApp(t)
			t.Cleanup(app.Close)
			app.width, app.height = 160, 36
			seedWorkspaceSession(t, app, "saved-session", "Fix compiler warnings")
			app.toggleWorkspacePanel(termdock.PanelWorkspace)
			app.paintShellFrame()
			if strings.Contains(rowText(app, app.height-1), "Previous Sessions") {
				t.Fatal("Previous Sessions must not have a footer button")
			}
			// Click the actual painted session title, not a launcher or coordinates
			// obtained from the layout helpers under test.
			x, y := -1, -1
			for row := 0; row < app.height-2; row++ {
				if col := strings.Index(rowText(app, row), "Fix compiler warnings"); col >= 0 {
					x, y = col, row
					break
				}
			}
			if x < 0 {
				t.Fatal("Workspace does not paint the saved session")
			}
			var result ui.EventResult
			if keyboard {
				app.rootHost.Focus(statusoverview.ListID)
				app.rootHost.Handle(tea.KeyPressMsg{Code: tea.KeyEnd})
				result = app.rootHost.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			} else {
				result = app.rootHost.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			}
			if !result.Handled || result.Cmd == nil {
				t.Fatalf("session row did not activate: %+v", result)
			}
			msg, ok := result.Cmd().(statusoverview.NavigateMsg)
			if !ok || msg.Action != "session:resume:saved-session" {
				t.Fatalf("unexpected navigation: %#v", msg)
			}
			_, _ = app.Update(msg)
			if app.overlayHost.Window(ui.OverlaySessionPicker) != nil || app.sessionPicker != nil {
				t.Fatal("resuming inline must not open the session picker")
			}
			if app.ActiveSession == nil || app.ActiveSession.SessionID != "saved-session" || app.termDock.IsOpen() {
				t.Fatal("inline resume did not activate the saved session and return to chat")
			}
			if app.AgentMgr.Get("agent-1") == nil {
				t.Fatal("resuming a saved session discarded the existing agent")
			}
		})
	}
}

func TestWorkspaceSessionsSearchRefreshAndEmptyState(t *testing.T) {
	app := newTransitionApp(t)
	t.Cleanup(app.Close)
	app.width, app.height = 160, 36
	app.toggleWorkspacePanel(termdock.PanelWorkspace)
	app.paintShellFrame()
	foundEmpty := false
	for y := 0; y < app.height; y++ {
		foundEmpty = foundEmpty || strings.Contains(rowText(app, y), "No previous sessions yet")
	}
	if !foundEmpty {
		t.Fatal("missing inline empty state")
	}
	seedWorkspaceSession(t, app, "first", "Fix compiler warnings")
	seedWorkspaceSession(t, app, "second", "Improve documentation")
	app.workspaceSessions.readAt = time.Time{}
	app.workspacePanelLastSecond = 0
	app.syncWorkspacePanels()
	app.paintShellFrame()
	app.rootHost.Focus(statusoverview.SearchID)
	for _, r := range "compiler" {
		app.rootHost.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	app.paintShellFrame()
	var painted strings.Builder
	for y := 0; y < app.height; y++ {
		painted.WriteString(rowText(app, y))
	}
	if !strings.Contains(painted.String(), "Fix compiler warnings") || strings.Contains(painted.String(), "Improve documentation") {
		t.Fatal("Workspace search does not filter session rows")
	}
	panel := app.termDock.Panel(termdock.PanelWorkspace).(*statusoverview.Surface)
	selected, ok := panel.Selected()
	if !ok || selected.ID != "session:first" {
		t.Fatalf("filtered selection=%+v", selected)
	}
	app.workspaceSessions.readAt = time.Time{}
	app.workspacePanelLastSecond = 0
	app.syncWorkspacePanels()
	if selected, ok = panel.Selected(); !ok || selected.ID != "session:first" {
		t.Fatal("refresh lost the session search/selection")
	}
	app.RootPath = t.TempDir()
	if cmd := app.resumeWorkspaceSession("first"); cmd != nil || app.ActiveSession != nil && app.ActiveSession.SessionID == "first" {
		t.Fatal("stale session action crossed workspace boundaries")
	}
}
