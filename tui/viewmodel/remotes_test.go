package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/remoteconn"
	"crude/shellport"
	remotesscreen "crude/tui/screens/remotes"
	"crude/tui/ui"
	"github.com/charmbracelet/x/ansi"
)

type fakeRemoteManager struct {
	snapshot        remoteconn.Snapshot
	snapshotErr     error
	err             error
	saves, attaches int
	attach          func(context.Context, string, string) (shellport.Workspace, error)
}

func (f *fakeRemoteManager) Snapshot(context.Context) (remoteconn.Snapshot, error) {
	return f.snapshot, f.snapshotErr
}
func (f *fakeRemoteManager) Save(_ context.Context, d shellport.RemoteConnectionDraft) (shellport.RemoteStatus, error) {
	f.saves++
	if f.err == nil {
		f.snapshot.Connections = append(f.snapshot.Connections, d.Connection)
	}
	return shellport.RemoteStatus{State: "connected", Carrier: "ssh", RTT: 24 * time.Millisecond}, f.err
}
func (f *fakeRemoteManager) Test(context.Context, remoteconn.Connection) (shellport.RemoteStatus, error) {
	return shellport.RemoteStatus{State: "connected", Carrier: "ssh"}, f.err
}
func (f *fakeRemoteManager) Remove(context.Context, string) error { return f.err }
func (f *fakeRemoteManager) Attach(ctx context.Context, id, root string) (shellport.Workspace, error) {
	f.attaches++
	if f.attach != nil {
		return f.attach(ctx, id, root)
	}
	return nil, f.err
}
func (f *fakeRemoteManager) RecordUse(context.Context, string, string) error { return nil }

func remoteClickItem(t *testing.T, app *App, id string) {
	t.Helper()
	for _, item := range app.remoteSidebarItems() {
		if item.ID == id {
			runAppCommand(t, app, app.handleSidebarActivate(item))
			return
		}
	}
	t.Fatalf("missing item %s", id)
}
func remoteAct(t *testing.T, app *App, p *remotesscreen.ConnectionEditor, action string) {
	t.Helper()
	runAppCommand(t, app, func() tea.Msg { return remotesscreen.ActionMsg{Target: p, Action: action} })
}

func TestRemotesAppRailAndFailedAddRetainDraftAtBothSizes(t *testing.T) {
	for _, size := range [][2]int{{160, 45}, {60, 20}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
			app := newRemoteJourneyShell(t, rt, t.TempDir()).app
			manager := &fakeRemoteManager{snapshot: remoteconn.Snapshot{Recent: map[string][]remoteconn.RecentWorkspace{}}, err: errors.New("host key not trusted")}
			app.SetRemoteManager(manager, remoteconn.Local)
			app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			runAppCommand(t, app, app.OpenRemotes())
			paint := ansi.Strip(app.View().RenderContent())
			if !strings.Contains(strings.ToLower(paint), "remotes") || !strings.Contains(paint, "Local") || !strings.Contains(paint, "Add connection") {
				t.Fatalf("missing rail: %s", paint)
			}
			remoteClickItem(t, app, "remote.add")
			p := app.remotes.editor
			if p == nil {
				t.Fatal("Add did not open form")
			}
			p.Model.SetValue("name", ui.StringValue("Keep this draft"))
			p.Model.SetValue("host", ui.StringValue("my-alias"))
			p.Model.SetValue("code", ui.StringValue("private-pair-code"))
			remoteAct(t, app, p, "save")
			if manager.saves != 1 || app.remotes.editor != p || p.Model.Field("host").Value.String() != "my-alias" || len(manager.snapshot.Connections) != 0 {
				t.Fatal("failed add lost draft or saved")
			}
			app.dirtyRemoteEditor()
			paint = ansi.Strip(app.View().RenderContent())
			if strings.Contains(paint, "private-pair-code") || !strings.Contains(paint, "not saved") {
				t.Fatalf("error missing or secret leaked: %s", paint)
			}
			remoteAct(t, app, p, "cancel")
			if app.remotes.editor != nil {
				t.Fatal("cancel did not close")
			}
			app.StopRemotes()
		})
	}
}
func TestRemotesOldEditorResultCannotCloseReplacement(t *testing.T) {
	app := newTestApp(t)
	manager := &fakeRemoteManager{snapshot: remoteconn.Snapshot{Recent: map[string][]remoteconn.RecentWorkspace{}}}
	app.SetRemoteManager(manager, "local")
	app.setActivity(activityRemotes, true)
	remoteClickItem(t, app, "remote.add")
	old := app.remotes.editor
	old.Model.SetValue("name", ui.StringValue("old"))
	old.Model.SetValue("host", ui.StringValue("alias"))
	_, cmd := app.HandleRemoteMessage(remotesscreen.ActionMsg{Target: old, Action: "save"})
	remoteAct(t, app, old, "cancel")
	remoteClickItem(t, app, "remote.add")
	replacement := app.remotes.editor
	runAppCommand(t, app, cmd)
	if app.remotes.editor != replacement || manager.saves != 0 {
		t.Fatal("cancelled queued save ran or old completion closed replacement")
	}
	app.StopRemotes()
}
func TestRemotesBackendFailureUsesExistingSwitchLifecycle(t *testing.T) {
	rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	shell := newRemoteJourneyShell(t, rt, t.TempDir())
	app := shell.app
	old := app.ws
	manager := &fakeRemoteManager{err: errors.New("destination unavailable")}
	app.SetRemoteManager(manager, "local")
	cmd := app.switchRemoteBackend("other", old.RootPath())
	if manager.attaches != 0 || app.ws != old || app.workspaceSwitchIntent == nil {
		t.Fatal("switch did I/O on Update or bypassed existing intent")
	}
	runAppCommand(t, app, cmd)
	if app.ws != old || app.remotes.current != "local" || app.workspaceSwitchIntent != nil || manager.attaches != 1 {
		t.Fatal("failed destination disturbed current workspace")
	}
	app.StopRemotes()
}
func TestRemotesBackendDirtyCancelNeverAttaches(t *testing.T) {
	rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	shell := newRemoteJourneyShell(t, rt, t.TempDir())
	app := shell.app
	old := app.ws
	path := switchTestFile(t, old.RootPath(), "dirty.txt", "original")
	ed := liveTestOpen(t, app, path)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.liveEditors[ed].topicReady })
	ed.InsertRune('X')
	manager := &fakeRemoteManager{}
	app.SetRemoteManager(manager, "local")
	runAppCommand(t, app, app.switchRemoteBackend("other", t.TempDir()))
	runAppCommand(t, app, app.drainLiveEditorCommands())
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.DirtyPromptPath != "" || app.workspaceSwitchIntent == nil })
	if app.DirtyPromptPath != path || manager.attaches != 0 {
		t.Fatal("dirty decision bypassed")
	}
	switchTestKey(t, app, tea.KeyEsc)
	if app.ws != old || ed.Value() != "Xoriginal" || !ed.IsOpen() || manager.attaches != 0 {
		t.Fatal("cancel lost dirty editor")
	}
	app.StopRemotes()
}

func TestRemotesOpenDialogAdoptsSamePathDifferentBackendAfterConsent(t *testing.T) {
	rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	shell := newRemoteJourneyShell(t, rt, t.TempDir())
	app := shell.app
	old := app.ws
	manager := &fakeRemoteManager{snapshot: remoteconn.Snapshot{Connections: []remoteconn.Connection{{ID: "other", Name: "Development", Kind: "ssh", Host: "alias"}}, Recent: map[string][]remoteconn.RecentWorkspace{"other": {{Path: old.RootPath(), Name: "project"}}}}}
	manager.attach = func(ctx context.Context, id, root string) (shellport.Workspace, error) {
		if app.ws != old {
			t.Fatal("old attachment released before destination attach")
		}
		return old.(shellport.Connection).AttachWorkspace(ctx, root)
	}
	app.SetRemoteManager(manager, "local")
	runAppCommand(t, app, app.OpenRemotes())
	remoteClickItem(t, app, "remote:other")
	remoteClickItem(t, app, "remote:other:open:"+old.RootPath())
	if manager.attaches != 0 || app.remotes.editor == nil {
		t.Fatal("opening request attached before consent")
	}
	frame := app.overlayHost.Window(remoteEditorOverlay).Content.(interface{ HandleEvent(tea.Msg) tea.Cmd })
	// Default focus is Cancel: an accidental Enter must not begin a switch.
	runAppCommand(t, app, frame.HandleEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if manager.attaches != 0 || app.ws != old || app.remotes.editor != nil {
		t.Fatal("Cancel default unsafe")
	}
	remoteClickItem(t, app, "remote:other:open:"+old.RootPath())
	frame = app.overlayHost.Window(remoteEditorOverlay).Content.(interface{ HandleEvent(tea.Msg) tea.Cmd })
	runAppCommand(t, app, frame.HandleEvent(tea.KeyPressMsg{Code: tea.KeyLeft}))
	runAppCommand(t, app, frame.HandleEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if manager.attaches != 1 || app.ws == old || app.remotes.current != "other" || app.workspaceSwitchIntent != nil {
		t.Fatal("confirmed backend not adopted")
	}
	if !strings.Contains(app.RemoteBackendLabel(), "Development") {
		t.Fatalf("backend identity not updated: %s", app.RemoteBackendLabel())
	}
	app.StopRemotes()
}

func TestRemotesRemoveDialogStartsOnCancel(t *testing.T) {
	app := newTestApp(t)
	manager := &fakeRemoteManager{snapshot: remoteconn.Snapshot{Connections: []remoteconn.Connection{{ID: "other", Name: "Development", Kind: "ssh", Host: "alias"}}}}
	app.SetRemoteManager(manager, "local")
	runAppCommand(t, app, app.OpenRemotes())
	remoteClickItem(t, app, "remote:other")
	remoteClickItem(t, app, "remote:other:remove:")
	if app.remotes.editor == nil {
		t.Fatal("remove did not open confirmation")
	}
	frame := app.overlayHost.Window(remoteEditorOverlay).Content.(interface{ HandleEvent(tea.Msg) tea.Cmd })
	runAppCommand(t, app, frame.HandleEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if app.remotes.editor != nil || len(manager.snapshot.Connections) != 1 {
		t.Fatal("remove confirmation did not safely cancel")
	}
	app.StopRemotes()
}
