package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/tui/screens/launcher"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func TestWorkspaceDeleteMenuPreviewCancelDeleteAndUndo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := t.TempDir()
	db, err := workspace.OpenWorkspaceDB(project)
	if err != nil {
		t.Fatal(err)
	}
	saved := db.Dir
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(project, "keep.txt"), []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	app := newFullTestApp(t)
	wsOf(app).RootPath = t.TempDir()
	app.width, app.height = 130, 36
	app.Tree = &TreeState{}
	app.launcherScreen.SetData(app.ws.RootPath(), []workspace.WorkspaceInfo{{ID: workspace.WorkspaceID(project), ProjectDir: project}})
	app.paintShellFrame()
	messages := make(chan tea.Msg, 4)
	restore := installDetachedMessageSinkForTest(app, func(msg tea.Msg) { messages <- msg })
	defer restore()
	list, _ := app.rootHost.ScreenRect(launcher.WorkspaceListID)
	menu := app.buildLauncherContextMenu(tea.Mouse{X: list.X + 1, Y: list.Y, Button: tea.MouseRight})
	if menu == nil {
		t.Fatal("resource context menu missing")
	}
	var open func()
	for _, section := range menu.Sections {
		for _, item := range section.Items {
			if item.Label == "Delete workspace…" {
				open = item.Action
			}
		}
	}
	if open == nil {
		t.Fatal("delete context action missing")
	}
	show := func() *workspaceDeletePrompt {
		t.Helper()
		open()
		select {
		case msg := <-messages:
			app.Update(msg)
		case <-time.After(5 * time.Second):
			t.Fatal("preview did not finish")
		}
		window := app.overlayHost.Window(ui.OverlayWorkspaceDelete)
		if window == nil {
			t.Fatal("delete frame missing")
		}
		frame := window.Content.(*ui.WindowFrameWidget)
		prompt := frame.Body().(*workspaceDeletePrompt)
		w, h := window.Size()
		frame.Host().Resize(w, h)
		frame.Host().Layout()
		buf := surface.NewCellBuffer(w, h)
		frame.Host().Paint(buf)
		text := bufferPlain(buf)
		if !strings.Contains(text, "Project files will not be changed") || !strings.Contains(text, "Delete workspace") {
			t.Fatal(text)
		}
		return prompt
	}
	first := show()
	_, cancelCmd := app.dispatchFloatingWindowKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cancelCmd == nil {
		t.Fatal("safe-default Enter did not cancel")
	}
	drainLauncherCommands(app, cancelCmd)
	if app.overlayHost.IsOpen(ui.OverlayWorkspaceDelete) {
		t.Fatal("safe default did not close confirmation")
	}
	if _, err = os.Stat(saved); err != nil {
		t.Fatal("cancel changed saved data")
	}
	prompt := show()
	if app.handleWorkspaceDeleteIntent(workspaceDeleteIntent{target: first, action: "delete"}) != nil {
		t.Fatal("stale dialog can delete")
	}
	_, _ = app.dispatchFloatingWindowKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	_, deleteCmd := app.dispatchFloatingWindowKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	drainLauncherCommands(app, deleteCmd)
	if prompt.deleted.RecoveryPath == "" || app.launcherScreen.TotalItems() != 2 {
		t.Fatal("delete did not settle with recovery and remove row")
	}
	if _, err = os.Stat(saved); !os.IsNotExist(err) {
		t.Fatal("saved data not removed from recents")
	}
	_, _ = app.dispatchFloatingWindowKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	_, undoCmd := app.dispatchFloatingWindowKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	drainLauncherCommands(app, undoCmd)
	if _, err = os.Stat(filepath.Join(saved, "workspace.db")); err != nil {
		t.Fatal("Undo lost saved data")
	}
	if text, err := os.ReadFile(filepath.Join(project, "keep.txt")); err != nil || string(text) != "untouched" {
		t.Fatal("project file was modified")
	}
}

func TestWorkspaceDeleteFeedbackAndNarrowResize(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 36
	messages := make(chan tea.Msg, 4)
	restore := installDetachedMessageSinkForTest(app, func(msg tea.Msg) { messages <- msg })
	defer restore()
	// A missing fixture settles with a real preparation failure before we
	// exercise the explicit busy/uncertain result projections in the same frame.
	app.openWorkspaceDelete(launcher.WorkspaceEntry{ID: "missing", Path: t.TempDir()})
	select {
	case msg := <-messages:
		app.Update(msg)
	case <-time.After(5 * time.Second):
		t.Fatal("preview did not settle")
	}
	frame := app.overlayHost.Window(ui.OverlayWorkspaceDelete).Content.(*ui.WindowFrameWidget)
	p := frame.Body().(*workspaceDeletePrompt)
	app.applyWorkspaceDeletePreview(workspaceDeletePreviewMsg{target: p, plan: workspace.WorkspaceDeletion{Issue: &types.OperationIssue{
		Title: "Workspace is in use", Summary: "This workspace is open in 2 views.", Detail: "Deleting it would disconnect those views.", Guidance: "Switch those views to another workspace or close them.",
	}}})
	frame.Host().Resize(78, 16)
	frame.Host().Layout()
	buf := surface.NewCellBuffer(78, 16)
	frame.Host().Paint(buf)
	text := bufferPlain(buf)
	for _, want := range []string{"Workspace is in use", "open in 2 views", "Switch those views", "Review again", "Cancel"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in rendered refusal:\n%s", want, text)
		}
	}
	if p.actions.ActionEnabled("delete") || app.handleWorkspaceDeleteIntent(workspaceDeleteIntent{target: p, action: "delete"}) != nil {
		t.Fatal("blocked preview permits deletion")
	}
	_, cancel := app.dispatchFloatingWindowKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	drainLauncherCommands(app, cancel)
	if app.overlayHost.IsOpen(ui.OverlayWorkspaceDelete) {
		t.Fatal("blocked dialog did not default to Cancel")
	}

	// Layout may disable a clipped destructive action, but widening must restore
	// its real state rather than leave it permanently disabled.
	p = newWorkspaceDeletePrompt(app.Theme, nil, app.workspaceSwitchGen.Load())
	p.buttons(ui.ActionBarButton{Label: "Delete workspace", Action: "delete", Enabled: true}, ui.ActionBarButton{Label: "Cancel", Action: "cancel", Enabled: true})
	p.Layout(ui.LayoutContext{}, ui.Rect{W: 20, H: 8})
	if p.actions.ActionEnabled("delete") {
		t.Fatal("clipped destructive action is reachable")
	}
	p.Layout(ui.LayoutContext{}, ui.Rect{W: 74, H: 12})
	if !p.actions.ActionEnabled("delete") {
		t.Fatal("widening did not restore safe deletion action")
	}
}

func TestWorkspaceDeleteUnknownResultNeverOffersReplay(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 36
	app.openWorkspaceDelete(launcher.WorkspaceEntry{ID: "missing", Path: t.TempDir()})
	p := app.overlayHost.Window(ui.OverlayWorkspaceDelete).Content.(*ui.WindowFrameWidget).Body().(*workspaceDeletePrompt)
	p.busy = true
	app.applyWorkspaceDeleteResult(workspaceDeleteResultMsg{target: p, err: &coreclient.UncertainError{RequestID: "original-delete", Cause: &coreprotocol.Fault{Code: "outcome_pending", Message: "acknowledgement lost"}}})
	if p.actions.ActionEnabled("delete") || p.actions.ActionEnabled("review") || p.receiptID != "original-delete" || !strings.Contains(p.detail.Value(), "Do not repeat") {
		t.Fatalf("unknown result permits replay or loses identity: %s", p.detail.Value())
	}
	if strings.Contains(p.title.Text(), "not deleted") {
		t.Fatal("unconfirmed effect was reported as unchanged")
	}
}

func drainLauncherCommands(app *App, command tea.Cmd) {
	if command == nil {
		return
	}
	msg := command()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			drainLauncherCommands(app, child)
		}
		return
	}
	_, next := app.Update(msg)
	drainLauncherCommands(app, next)
}
