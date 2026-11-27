package viewmodel

import (
	"crude/types/setting"
	"crude/workspace"
	"crude/workspace/localhistory"
	"github.com/charmbracelet/x/ansi"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	historyscreen "crude/tui/screens/localhistory"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/wm"
)

func TestLocalHistoryWindowUsesCanonicalFrameInteractions(t *testing.T) {
	app := &App{Theme: theme.DefaultDark(), width: 120, height: 40}
	fw := app.newLocalHistoryWindow(historyscreen.New(app.Theme))
	if !fw.Decorated() || !fw.Movable() || !fw.Closable() || !fw.Resizable() {
		t.Fatalf("window interactions decorated=%v movable=%v closable=%v resizable=%v", fw.Decorated(), fw.Movable(), fw.Closable(), fw.Resizable())
	}
	fw.SetPosition(10, 5)
	w, h := fw.Size()
	if edge := fw.HitTestEdge(10+w-1, 5+h-1); edge != wm.EdgeBottomRight {
		t.Fatalf("bottom-right edge=%v", edge)
	}
	if !fw.IsOnCloseButton(10+w-2, 5) {
		t.Fatal("close button has no WM hit region")
	}
}

func TestLocalHistoryWindowCloseAndResizeRouteThroughWindowManager(t *testing.T) {
	th := theme.DefaultDark()
	app := &App{Theme: th, width: 120, height: 40, windowManager: wm.NewWindowManager()}
	app.overlayHost = ui.NewOverlayHost(app.windowManager)
	fw := app.newLocalHistoryWindow(historyscreen.New(th))
	fw.SetPosition(10, 5)
	app.overlayHost.Open(ui.OverlayLocalHistory, fw, ui.ModalDialogPolicy(), nil)
	w, h := fw.Size()
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: 10 + w/2, Y: 5 + h - 1})
	if fw.HoverEdge() != wm.EdgeBottom {
		t.Fatalf("border hover=%v want bottom", fw.HoverEdge())
	}
	app.windowManager.HandleMouse(tea.MouseClickMsg{X: 10 + w - 1, Y: 5 + h - 1, Button: tea.MouseLeft})
	app.windowManager.HandleMouseMotion(tea.MouseMotionMsg{X: 10 + w + 3, Y: 5 + h + 2, Button: tea.MouseLeft})
	app.windowManager.HandleMouseRelease(tea.MouseReleaseMsg{X: 10 + w + 3, Y: 5 + h + 2, Button: tea.MouseLeft})
	grownW, grownH := fw.Size()
	if grownW <= w || grownH <= h {
		t.Fatalf("resize=%dx%d want larger than %dx%d", grownW, grownH, w, h)
	}
	x, y := fw.Position()
	app.dispatchMouseClick(tea.MouseClickMsg{X: x + grownW - 2, Y: y, Button: tea.MouseLeft})
	if app.windowManager.Window(string(ui.OverlayLocalHistory)) != nil {
		t.Fatal("close icon did not remove Local History overlay")
	}
}

// Preview is an agent-wide read. A later empty message and the current disk
// contents cannot hide an earlier edit or turn Enter into a restore.
func TestConnectedAgentEditPreviewReadsEarlierReceipt(t *testing.T) {
	var store *localhistory.Store
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(ws *workspace.Workspace) {
		ws.SetAppDefaults(setting.App{LocalHistory: setting.LocalHistory{Enabled: "on"}})
		store = ws.LocalHistory
	})
	root := t.TempDir()
	shell := newRemoteJourneyShell(t, runtime, root)
	app := shell.app
	id := remoteJourneyCreate(t, shell)
	session := app.ActiveSession.SessionID
	manifest, err := store.Snapshot(t.Context(), localhistory.SnapshotOptions{Kind: "before_message"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.BeginMessageScope(session, "older", manifest.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	path := "notes.txt"
	op := localhistory.AgentOperation{ID: "confirmed-file-edit", SessionID: session, MessageID: "older", AgentID: id, Source: "edit_file", Paths: []localhistory.OperationPath{{Path: path, Before: &localhistory.OperationState{Type: localhistory.EntryFile, Mode: 0644, Data: []byte("old line\n")}, After: &localhistory.OperationState{Type: localhistory.EntryFile, Mode: 0644, Data: []byte("recorded line\n")}}}}
	if err = store.RecordAgentOperation(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	if err = store.CompleteAgentOperation(op.ID, true); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, path), []byte("later external edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	app.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	runAppCommand(t, app, app.openAgentReceipts(session, id))
	view := func() string { return ansi.Strip(app.View().RenderContent()) }
	if s := view(); !strings.Contains(s, path) || strings.Contains(s, "Restore preview") {
		t.Fatalf("not an agent edit list: %s", s)
	}
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	if s := view(); !strings.Contains(s, "+recorded line") || !strings.Contains(s, "-old line") || strings.Contains(s, "+later external edit") {
		t.Fatalf("not the historical diff: %s", s)
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	runAppCommand(t, app, cmd)
	if !strings.Contains(view(), "Agent file changes") {
		t.Fatal("Back did not return to the agent edits")
	}
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil || string(data) != "later external edit\n" {
		t.Fatal("preview changed the working file", err)
	}
}
