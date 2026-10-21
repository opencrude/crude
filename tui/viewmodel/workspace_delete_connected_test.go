package viewmodel

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/shellport/remote"
	"crude/tui/screens/activitylist"
	"crude/tui/screens/launcher"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func TestConnectedWorkspaceDeleteRetiresUnusedActorAndUndo(t *testing.T) {
	r, err := core.New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "", "tester", types.Settings{}, noopLogger{}), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	project := t.TempDir()
	old := newRemoteJourneyShell(t, r, project)
	current := newRemoteJourneyShell(t, r, t.TempDir())
	// The real shell switch detaches only its old attachment; the core stays up.
	old.app.StopAsyncIO()
	if err := old.app.CloseWorkspaceAttachment(); err != nil {
		t.Fatal(err)
	}
	root, err := workspace.WorkspaceRoot()
	if err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(root, workspace.WorkspaceID(project))
	if err := os.WriteFile(filepath.Join(saved, "saved-evidence"), []byte("retained conversation evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "keep.txt"), []byte("project unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	app := current.app
	app.launcherScreen = nil // The sidebar remains usable during a live workflow.
	app.workspaceInfos = []workspace.WorkspaceInfo{{ID: workspace.WorkspaceID(project), ProjectDir: project, ProjectName: "unused"}}
	app.setActivity(activityWorkspaces, true)
	app.paintShellFrame()
	list, ok := app.rootHost.ScreenRect(activitylist.ListID)
	if !ok {
		t.Fatal("workspace sidebar is not mounted")
	}
	app.Update(tea.MouseClickMsg{X: list.X + 2, Y: list.Y + 1, Button: tea.MouseRight})
	if app.LastContextMenu == nil || !strings.Contains(app.View().Content, "Delete workspace") {
		t.Fatal("right-click did not paint the shared resource Delete action")
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // Open -> Copy path
	app.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // Copy path -> Delete
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	window := app.overlayHost.Window(ui.OverlayWorkspaceDelete)
	if window == nil {
		t.Fatal("sidebar context menu did not open the deletion review")
	}
	p := window.Content.(*ui.WindowFrameWidget).Body().(*workspaceDeletePrompt)
	remoteJourneyUntil(t, []remoteJourneyShell{current}, func() bool { return p.actions.ActionEnabled("delete") })
	runAppCommand(t, app, app.handleWorkspaceDeleteIntent(workspaceDeleteIntent{target: p, action: "delete"}))
	if p.deleted.RecoveryPath == "" {
		t.Fatalf("connected deletion failed: %s: %s", p.title.Text(), p.detail.Value())
	}
	status, err := r.Status(context.Background())
	if err != nil || len(status.Workspaces) != 1 || status.Workspaces[0].Root != app.ws.RootPath() {
		t.Fatalf("delete left stale actor or disturbed current workspace: %+v, %v", status, err)
	}
	if _, err := os.Stat(saved); !os.IsNotExist(err) {
		t.Fatalf("deleted workspace remains at source: %v", err)
	}
	for _, item := range app.workspaceSidebarItems() {
		if item.ID == sidebarItemWorkspacePrefix+project {
			t.Fatal("committed deletion left a stale sidebar resource")
		}
	}
	runAppCommand(t, app, app.handleWorkspaceDeleteIntent(workspaceDeleteIntent{target: p, action: "undo"}))
	data, err := os.ReadFile(filepath.Join(saved, "saved-evidence"))
	if err != nil || string(data) != "retained conversation evidence" {
		t.Fatalf("Undo lost saved work: %q, %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(project, "keep.txt"))
	if err != nil || string(data) != "project unchanged" {
		t.Fatalf("project was modified: %q, %v", data, err)
	}
	if !strings.Contains(p.title.Text(), "restored") {
		t.Fatal("Undo did not show restored status")
	}
	remoteJourneyUntil(t, []remoteJourneyShell{current}, func() bool {
		for _, info := range app.workspaceInfos {
			if info.ID == workspace.WorkspaceID(project) {
				return true
			}
		}
		return false
	})
	app.overlayHost.Close(ui.OverlayWorkspaceDelete)
	if !strings.Contains(app.View().Content, filepath.Base(project)) {
		t.Fatal("Undo did not return the resource to the rendered sidebar")
	}
}

type deletionResponseDropper struct {
	net.Conn
	buffer  []byte
	dropped chan struct{}
}

func (c *deletionResponseDropper) Write(data []byte) (int, error) {
	c.buffer = append(c.buffer, data...)
	for len(c.buffer) >= 4 {
		size := int(binary.BigEndian.Uint32(c.buffer[:4]))
		if len(c.buffer) < size+4 {
			break
		}
		packet := c.buffer[:size+4]
		var frame corewire.Frame
		if err := json.Unmarshal(packet[4:], &frame); err != nil {
			return 0, err
		}
		if frame.Kind == corewire.KindResponse && frame.Method == coreprotocol.WorkspaceDelete {
			close(c.dropped)
		} else {
			if _, err := c.Conn.Write(packet); err != nil {
				return 0, err
			}
		}
		c.buffer = c.buffer[size+4:]
	}
	return len(data), nil
}

func TestConnectedWorkspaceDeleteLostAcknowledgementChecksOriginalReceipt(t *testing.T) {
	r, err := core.New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "", "tester", types.Settings{}, noopLogger{}), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	project := t.TempDir()
	old := newRemoteJourneyShell(t, r, project)
	old.app.StopAsyncIO()
	if err := old.app.CloseWorkspaceAttachment(); err != nil {
		t.Fatal(err)
	}
	dropped := make(chan struct{})
	dial := func(ctx context.Context, id string) (*coreclient.Client, error) {
		server, client := net.Pipe()
		go func() {
			_ = core.ServeConnection(t.Context(), r, &deletionResponseDropper{Conn: server, dropped: dropped})
		}()
		return coreclient.Dial(ctx, client, id)
	}
	port, err := remote.Attach(t.Context(), remote.Options{Root: t.TempDir(), Dial: dial})
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(port, nil)
	fifo := make(chan any, 1024)
	app.eventBus.SetSink(remoteJourneySink{fifo})
	port.StartReplica()
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	t.Cleanup(func() { app.StopAsyncIO(); _ = app.CloseWorkspaceAttachment() })
	shell := remoteJourneyShell{app: app, fifo: fifo}
	app.openWorkspaceDelete(launcher.WorkspaceEntry{ID: workspace.WorkspaceID(project), Path: project})
	p := app.overlayHost.Window(ui.OverlayWorkspaceDelete).Content.(*ui.WindowFrameWidget).Body().(*workspaceDeletePrompt)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return p.actions.ActionEnabled("delete") })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan workspaceDeleteResultMsg, 1)
	go func() {
		deleted, err := port.DeleteSavedWorkspace(ctx, p.plan)
		result <- workspaceDeleteResultMsg{target: p, deleted: deleted, err: err}
	}()
	select {
	case <-dropped:
	case <-time.After(5 * time.Second):
		t.Fatal("real deletion acknowledgement was not reached")
	}
	cancel() // The effect committed; only delivery of its response was lost.
	msg := <-result
	var uncertain *coreclient.UncertainError
	if !errors.As(msg.err, &uncertain) || uncertain.RequestID == "" {
		t.Fatalf("lost acknowledgement has no original identity: %v", msg.err)
	}
	app.Update(msg)
	if !p.actions.ActionEnabled("check") || p.actions.ActionEnabled("delete") || p.actions.ActionEnabled("review") {
		t.Fatal("unknown result has no safe observation or permits replay")
	}
	runAppCommand(t, app, app.handleWorkspaceDeleteIntent(workspaceDeleteIntent{target: p, action: "check"}))
	if p.deleted.RecoveryPath == "" || !p.actions.ActionEnabled("undo") || p.title.Text() != "Workspace deleted" {
		t.Fatalf("receipt observation did not recover the original result: %s: %s", p.title.Text(), p.detail.Value())
	}
	root, err := workspace.WorkspaceRoot()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(root), "deleted-workspaces"))
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), p.plan.ID+"-") {
			count++
		}
	}
	if err != nil || count != 1 {
		t.Fatalf("receipt observation repeated this deletion: %d entries, %v", count, err)
	}
	runAppCommand(t, app, app.handleWorkspaceDeleteIntent(workspaceDeleteIntent{target: p, action: "undo"}))
	if p.title.Text() != "Workspace restored" {
		t.Fatalf("recovered deletion lost Undo: %s: %s", p.title.Text(), p.detail.Value())
	}
}

func TestConnectedWorkspaceDeletePeerBlockerAndExplicitReview(t *testing.T) {
	r, err := core.New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "", "tester", types.Settings{}, noopLogger{}), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	project := t.TempDir()
	peer := newRemoteJourneyShell(t, r, project)
	current := newRemoteJourneyShell(t, r, t.TempDir())
	app := current.app
	app.openWorkspaceDelete(launcher.WorkspaceEntry{ID: workspace.WorkspaceID(project), Path: project})
	p := app.overlayHost.Window(ui.OverlayWorkspaceDelete).Content.(*ui.WindowFrameWidget).Body().(*workspaceDeletePrompt)
	remoteJourneyUntil(t, []remoteJourneyShell{peer, current}, func() bool { return p.actions.ActionEnabled("review") })
	if p.actions.ActionEnabled("delete") || !strings.Contains(p.detail.Value(), "open in 1 view(s)") || !strings.Contains(p.detail.Value(), "Switch those views") {
		t.Fatalf("peer view blocker is missing or unsafe: %s", p.detail.Value())
	}
	peer.app.StopAsyncIO()
	if err := peer.app.CloseWorkspaceAttachment(); err != nil {
		t.Fatal(err)
	}
	runAppCommand(t, app, app.handleWorkspaceDeleteIntent(workspaceDeleteIntent{target: p, action: "review"}))
	remoteJourneyUntil(t, []remoteJourneyShell{current}, func() bool { return p.actions.ActionEnabled("delete") })
	if p.deleted.RecoveryPath != "" || p.plan.Token == "" {
		t.Fatal("Review again either performed deletion or failed to prepare a fresh review")
	}
	_, cancel := app.dispatchFloatingWindowKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cancel)
	if app.overlayHost.IsOpen(ui.OverlayWorkspaceDelete) {
		t.Fatal("fresh destructive review did not default to Cancel")
	}
}
