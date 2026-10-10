package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"crude/coreprotocol"
	"crude/tui/filetree"
	"crude/tui/screens/termdock"
	"crude/types"
	captypes "crude/types/capability"
	"crude/workspace"
)

func TestRemoteShellTreeProjectsGitBeforeAndAfterDirectoryLoad(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runtime := remoteJourneyRuntime(t, "http://unused.invalid")
	root := newGitRepoFixture(t)
	path := filepath.Join(root, "tracked.txt")
	one := newRemoteJourneyShell(t, runtime, root)
	two := newRemoteJourneyShell(t, runtime, root)
	for _, shell := range []remoteJourneyShell{one, two} {
		// Git and diagnostics can finish before the first directory response.
		runAppCommand(t, shell.app, shell.app.refreshSourceControl())
		if shell.app.Tree.Model.Root.AggregateStatus != filetree.GitModified {
			t.Fatal("connected tree has no Git aggregate before directory load")
		}
		shell.app.Tree.Model.SetDiagnostics(path, 1, 0, 0)
		runAppCommand(t, shell.app, shell.app.beginClientFileTreeLoad())
		node := shell.app.Tree.Model.NodeByPath(path)
		if node == nil || node.GitStatus != filetree.GitModified || node.DiagErrors != 1 {
			t.Fatalf("late directory lost status projection: %+v", node)
		}
		if shell.app.Tree.Model.GitResolver() != nil || shell.app.Tree.Model.Watcher() != nil {
			t.Fatal("connected tree started a client-side Git resolver or watcher")
		}
	}
	if err := os.WriteFile(path, []byte("one\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, shell := range []remoteJourneyShell{one, two} {
		runAppCommand(t, shell.app, shell.app.startGitSnapshotRead())
		if node := shell.app.Tree.Model.NodeByPath(path); node.GitStatus != filetree.GitNone || shell.app.Tree.Model.Root.AggregateStatus != filetree.GitNone {
			t.Fatalf("clean Git snapshot retained stale markers: %+v", node)
		}
	}
}

func TestRemoteShellWorkingFileEventsReconcileGitWithoutIndexChange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runtime := remoteJourneyRuntime(t, "http://unused.invalid")
	root := newGitRepoFixture(t)
	shell := newRemoteJourneyShell(t, runtime, root)
	runAppCommand(t, shell.app, shell.app.refreshSourceControl())
	path := filepath.Join(root, "tracked.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runAppCommand(t, shell.app, shell.app.applyCoreFileChanges([]coreprotocol.FileChange{{Path: path}}))
	if shell.app.gitSnapshotEventAt.IsZero() {
		t.Fatal("ordinary file write did not arm Git reconciliation")
	}
	runAppCommand(t, shell.app, shell.app.startGitSnapshotRead())
	if node := shell.app.Tree.Model.NodeByPath(path); node == nil || node.GitStatus != filetree.GitNone {
		t.Fatalf("working-tree-only change retained old Git state: %+v", node)
	}
}

func TestRemoteShellColdLauncherLoadsCapabilitiesWithoutSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runtime := remoteJourneyRuntime(t, "http://unused.invalid")
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	runAppCommand(t, shell.app, shell.app.capabilitySnapshotCmd())
	if snapshot := shell.app.capabilitySnapshot; snapshot.Generation == 0 || snapshot.Error != "" {
		t.Fatalf("cold workspace home cannot read capabilities: %+v", snapshot)
	}
	paint := ansi.Strip(shell.app.View().Content)
	if strings.Contains(paint, "Couldn’t read") || strings.Contains(paint, "Checking…") {
		t.Fatalf("cold launcher did not settle its capability panels:\n%s", paint)
	}
}

func TestRemoteShellLSPDiagnosticsReachBothTreesBeforeBrowse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, path := createDiagnosticsProject(t)
	runtime := remoteJourneyRuntime(t, "http://unused.invalid")
	one := newRemoteJourneyShell(t, runtime, root)
	two := newRemoteJourneyShell(t, runtime, root)
	lsp := &immediateTreeDiagRuntime{filePath: path, diags: []workspace.LSPDiagnostic{{Severity: workspace.DiagError, Message: "Connected diagnostic evidence"}}}
	if err := runtime.Each(t.Context(), func(w *workspace.Workspace) { w.LSPMgr.SetRuntime(lsp) }); err != nil {
		t.Fatal(err)
	}
	command, err := one.app.openEditorTab(path)
	if err != nil {
		t.Fatal(err)
	}
	runAppCommand(t, one.app, command)
	remoteJourneyUntil(t, []remoteJourneyShell{one, two}, func() bool {
		return one.app.Tree.Model.Root.DiagErrors == 1 && two.app.Tree.Model.Root.DiagErrors == 1
	})
	for _, shell := range []remoteJourneyShell{one, two} {
		runAppCommand(t, shell.app, shell.app.beginClientFileTreeLoad())
		if node := shell.app.Tree.Model.NodeByPath(path); node == nil || node.DiagErrors != 1 {
			t.Fatalf("diagnostics did not attach to arriving directory rows: %+v", node)
		}
	}
	lsp.mu.Lock()
	client := lsp.client
	lsp.mu.Unlock()
	client.publishDiagnostics(nil)
	remoteJourneyUntil(t, []remoteJourneyShell{one, two}, func() bool {
		return one.app.Tree.Model.Root.DiagErrors == 0 && two.app.Tree.Model.Root.DiagErrors == 0
	})
}

func TestCapabilityRefreshSupersessionDoesNotPublishCancellation(t *testing.T) {
	app := newDiagnosticsTreeApp(t, t.TempDir())
	old := app.capabilitySnapshotCmd()
	current := app.capabilitySnapshotCmd()
	runAppCommand(t, app, current)
	if app.capabilitySnapshot.Generation == 0 || app.capabilitySnapshot.Error != "" {
		t.Fatalf("current refresh failed: %+v", app.capabilitySnapshot)
	}
	runAppCommand(t, app, old)
	if app.capabilitySnapshot.Error != "" {
		t.Fatalf("superseded refresh replaced success with cancellation: %s", app.capabilitySnapshot.Error)
	}
}

func TestCapabilityRefreshCancelCannotInvalidateNewerPoll(t *testing.T) {
	app := newDiagnosticsTreeApp(t, t.TempDir())
	old := app.capabilitySnapshotCmd()
	current := app.capabilitySnapshotCmd()
	runAppCommand(t, app, current)
	poll := app.launcherCapabilitiesCmd(time.Now().Add(3 * time.Second))
	runAppCommand(t, app, old)
	runAppCommand(t, app, poll)
	if app.capabilitySnapshot.Error != "" || app.launcherCapabilityCancel != nil {
		t.Fatal("stale read cancelled or stranded the newer passive observation")
	}
}

func TestRemoteShellTerminalCreateReceiptBeforeInventoryChangesPanel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	runtime := remoteJourneyRuntime(t, "http://unused.invalid")
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	shell.app.toggleWorkspacePanel(termdock.PanelNotices)
	command := shell.app.openNewTerminal()
	created, ok := command().(terminalCreatedMsg)
	if !ok || created.err != nil {
		t.Fatalf("terminal creation: %+v", created)
	}
	if _, installed := shell.app.ws.TermMgr().Get(created.info.ID); installed {
		t.Fatal("test failed to hold the inventory replica behind the create receipt")
	}
	_, next := shell.app.Update(created)
	if !shell.app.termDock.TerminalActive() || shell.app.termDock.ActiveID() != created.info.ID {
		t.Fatal("create receipt left Notices painted while terminal inventory was pending")
	}
	runTerminalCommand(t, shell.app, next)
	if !shell.app.terminalDockOwnsKeys() {
		t.Fatal("receipt-mounted terminal did not receive keyboard focus")
	}
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		_, exists := shell.app.ws.TermMgr().Get(created.info.ID)
		return exists
	})
	shell.app.toggleWorkspacePanel(termdock.PanelNotices)
	shell.app.toggleWorkspacePanel(termdock.PanelTerminal)
	if !shell.app.termDock.TerminalActive() || strings.Contains(ansi.Strip(shell.app.View().Content), "Notifications") {
		t.Fatal("Notices -> Terminal did not replace the rendered panel")
	}
}

func TestRemoteShellColdNoticesAndPeerDelivery(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	seed := workspace.NewWorkspace(root, "", "tester", types.Settings{}, nil)
	seed.Status.Notify(types.NotificationRecord{ID: "persisted-notice", Summary: "Persisted workspace notice", Detail: "Exact persisted evidence.", Source: "fixture"})
	seed.Close()
	runtime := remoteJourneyRuntime(t, "http://unused.invalid")
	one := newRemoteJourneyShell(t, runtime, root)
	two := newRemoteJourneyShell(t, runtime, root)
	for _, shell := range []remoteJourneyShell{one, two} {
		shell.app.toggleWorkspacePanel(termdock.PanelNotices)
		if !strings.Contains(ansi.Strip(shell.app.View().Content), "Persisted workspace notice") {
			t.Fatal("cold core omitted saved Notices from the connected panel")
		}
	}
	one.app.ws.Status().Notify(types.NotificationRecord{ID: "peer-notice", Summary: "Peer workspace notice", Source: "fixture"})
	remoteJourneyUntil(t, []remoteJourneyShell{one, two}, func() bool {
		return strings.Contains(ansi.Strip(two.app.View().Content), "Peer workspace notice")
	})
}

func TestCapabilityObservationFailureKeepsUsableLauncherResources(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 160, 40
	app.compositor.Resize(160, 40)
	snapshot := capabilityVisibilitySnapshot(1, "local:kept", "Kept skill", captypes.ScopeWorkspace)
	snapshot.Error = "Temporary observation failure"
	app.applyObservedCapabilitySnapshot(snapshot, nil, true)
	if paint := ansi.Strip(app.View().Content); !strings.Contains(paint, "Kept skill") {
		t.Fatalf("observation failure removed the usable last listing:\n%s", paint)
	}
}
