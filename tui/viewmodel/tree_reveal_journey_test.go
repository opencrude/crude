package viewmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/tui/screens/shelltree"
	"crude/tui/ui"
	"crude/types/control"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestTreeContextMenuDoesNotOfferRevealInTree(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newEditorPaintApp(t, root)
	app.setActivity(activityFiles, true)
	x, y := editorTreePoint(t, app, "file.txt")
	app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight})
	if app.LastContextMenu == nil {
		t.Fatal("tree context menu did not open")
	}
	if _, found := contextMenuItem(app.LastContextMenu, "Reveal in tree"); found {
		t.Fatal("tree context menu offers to reveal its already visible row")
	}
	if item := menuItem(t, app.LastContextMenu, treeRevealLabel()); item.Disabled {
		t.Fatal("desktop file-manager action must remain available")
	}
}

func TestConnectedTabRevealShowsClickedFileInTree(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "nested", "deep", "needle.txt")
			other := filepath.Join(root, "other.txt")
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{target, other} {
				if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for i := range 40 {
				if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("a%02d.txt", i)), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			runtime := remoteJourneyRuntime(t, "")
			shell := newRemoteJourneyShell(t, runtime, root)
			app := shell.app
			app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			runAppCommand(t, app, app.openPaneFiles([]string{target}, app.Win.ActiveGroup()))
			targetTab := app.Win.TabByTitle(app.editorTabTitle(target))
			if targetTab == nil {
				t.Fatal("target tab did not open")
			}
			runAppCommand(t, app, app.openPaneFiles([]string{other}, app.Win.ActiveGroup()))
			active := app.Win.ActiveTab().ID
			runAppCommand(t, app, app.beginClientFileTreeLoad())
			app.setActivity(activityFiles, true)
			runAppCommand(t, app, app.beginTreePathSearch("other"))
			app.setActivity(activityAgents, false)
			app.Tree.Visible = false
			paneOpenTabMenu(t, app, targetTab.ID)
			paneChooseMenu(t, app, "Reveal in tree", size[0] == 80)
			if !app.Tree.Visible || app.activityView != activityFiles {
				t.Fatal("tab reveal did not show the file explorer")
			}
			remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
				node := app.Tree.Model.SelectedNode()
				return node != nil && node.Path == target
			})
			paint := ansi.Strip(app.View().RenderContent())
			if !strings.Contains(paint, "needle.txt") {
				t.Fatalf("selected target is not painted in the tree:\n%s", paint)
			}
			requireTreeRowHighlight(t, app, target)
			if app.Win.ActiveTab().ID != active {
				t.Fatal("revealing an inactive tab changed the active tab")
			}
		})
	}
}

func requireTreeRowHighlight(t *testing.T, app *App, path string) {
	t.Helper()
	_ = app.View()
	if app.focusTarget() != FocusTree || app.rootHost.Focused() != shelltree.TreeID {
		t.Fatalf("revealed tree does not own focus: target=%v host=%s", app.focusTarget(), app.rootHost.Focused())
	}
	rect, ok := app.rootHost.ScreenRect(app.sidebarScreen.ID())
	if !ok {
		t.Fatal("file tree is not mounted")
	}
	for y := 0; y < rect.H; y++ {
		if app.sidebarScreen.PathAtLocalY(y) != path {
			continue
		}
		for x := rect.X; x < rect.X+rect.W; x++ {
			if app.mainSurf.Buffer().Get(x, rect.Y+y).BG == ui.NewTokenSet(app.Theme).TreeCursorBg() {
				return
			}
		}
		t.Fatal("revealed row was painted without its selection highlight")
	}
	t.Fatal("selected file is outside the painted tree viewport")
}

func TestConnectedPaletteRevealShowsHiddenFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, ".config", "nested", "settings.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, ""), root)
	app := shell.app
	runAppCommand(t, app, app.openPaneFiles([]string{target}, app.Win.ActiveGroup()))
	app.Tree.Model.SetShowHidden(false)
	app.setActivity(activityAgents, true)
	runAppCommand(t, app, app.executePaletteCommand("file.revealInTree"))
	if !app.Tree.Visible || app.activityView != activityFiles || !app.Tree.Model.ShowHidden() {
		t.Fatal("palette reveal did not show the explorer and hidden path")
	}
	if node := app.Tree.Model.SelectedNode(); node == nil || node.Path != target {
		t.Fatalf("palette selected %v, want %s", node, target)
	}
	requireTreeRowHighlight(t, app, target)
}

func TestTreeRevealNewerIntentAndClosedTabRejectLateResult(t *testing.T) {
	root := t.TempDir()
	paths := []string{filepath.Join(root, "first", "a.txt"), filepath.Join(root, "second", "b.txt")}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, ""), root)
	app := shell.app
	for _, path := range paths {
		runAppCommand(t, app, app.openPaneFiles([]string{path}, app.Win.ActiveGroup()))
	}
	first := app.Win.TabByTitle(app.editorTabTitle(paths[0]))
	second := app.Win.TabByTitle(app.editorTabTitle(paths[1]))
	oldCmd := app.beginUserTreeReveal(paths[0], app.paneOwner(first.ID, first.Group))
	oldResult := oldCmd()
	newCmd := app.beginUserTreeReveal(paths[1], app.paneOwner(second.ID, second.Group))
	app.Update(oldResult)
	runAppCommand(t, app, newCmd)
	if node := app.Tree.Model.SelectedNode(); node == nil || node.Path != paths[1] {
		t.Fatalf("late old reveal overrode the newer selection: %v", node)
	}

	oldCmd = app.beginUserTreeReveal(paths[0], app.paneOwner(first.ID, first.Group))
	oldResult = oldCmd()
	app.Win.CloseTab(app.Win.IndexOf(first.ID))
	app.Update(oldResult)
	if node := app.Tree.Model.SelectedNode(); node == nil || node.Path != paths[1] {
		t.Fatalf("closed tab's result changed the tree selection: %v", node)
	}
}

type blockingRevealFiles struct {
	fakeClientFiles
	started  chan struct{}
	canceled chan struct{}
}

func (p *blockingRevealFiles) BrowseDirectory(ctx context.Context, _ string) ([]shellport.ClientDirectoryEntry, error) {
	close(p.started)
	<-ctx.Done()
	close(p.canceled)
	return nil, ctx.Err()
}

func TestTreeInteractionCancelsRevealRead(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "target.txt")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	app := newEditorPaintApp(t, root)
	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	port := &blockingRevealFiles{fakeClientFiles: fakeClientFiles{Workspace: app.ws, root: root},
		started: make(chan struct{}), canceled: make(chan struct{})}
	app.ws = port
	model := newClientFileTree(app)
	app.Tree.Model = model
	app.sidebarScreen.SetModel(model)
	tab := app.Win.TabByTitle(app.editorTabTitle(path))
	cmd := app.beginUserTreeReveal(path, app.paneOwner(tab.ID, tab.Group))
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case <-port.started:
	case <-time.After(3 * time.Second):
		t.Fatal("directory read did not start")
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	select {
	case <-port.canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("tree navigation did not cancel the pending directory read")
	}
	app.Update(<-result)
	if app.treeReveal != nil {
		t.Fatal("cancelled reveal remained active")
	}
}

type displacedRevealFiles struct {
	fakeClientFiles
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (p *displacedRevealFiles) BrowseDirectory(ctx context.Context, _ string) ([]shellport.ClientDirectoryEntry, error) {
	if p.calls.Add(1) == 2 {
		close(p.started)
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return p.entries, nil
}

func TestCancelledRevealKeepsDisplacedTreeRefresh(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	app := newEditorPaintApp(t, root)
	if _, err := app.openEditorTab(target); err != nil {
		t.Fatal(err)
	}
	port := &displacedRevealFiles{
		fakeClientFiles: fakeClientFiles{Workspace: app.ws, root: root, entries: []shellport.ClientDirectoryEntry{
			{Name: "other.txt", Path: filepath.Join(root, "other.txt")},
			{Name: "target.txt", Path: target},
		}},
		started: make(chan struct{}), release: make(chan struct{}),
	}
	app.ws = port
	model := newClientFileTree(app)
	app.Tree.Model = model
	app.sidebarScreen.SetModel(model)
	old := model.RequestRemoteDirectory(root)()
	tab := app.Win.TabByTitle(app.editorTabTitle(target))
	cmd := app.beginUserTreeReveal(target, app.paneOwner(tab.ID, tab.Group))
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case <-port.started:
	case <-time.After(3 * time.Second):
		t.Fatal("replacement refresh did not start")
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	app.Update(old)
	if len(model.VisibleRows()) != 0 {
		t.Fatal("superseded original refresh was installed")
	}
	close(port.release)
	app.Update(<-result)
	if model.Root.Loading || len(model.VisibleRows()) != 2 {
		t.Fatalf("cancelled reveal stranded the explorer refresh: loading=%v rows=%v", model.Root.Loading, model.VisibleRows())
	}
	if node := model.SelectedNode(); node != nil && node.Path == target {
		t.Fatal("cancelled reveal selected the target")
	}
}

func TestRevealReportsMissingAndOutsideWorkspaceFiles(t *testing.T) {
	for _, outside := range []bool{false, true} {
		t.Run(fmt.Sprint(outside), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "file.txt")
			if outside {
				path = filepath.Join(t.TempDir(), "outside.txt")
			}
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			app := newEditorPaintApp(t, root)
			if _, err := app.openEditorTab(path); err != nil {
				t.Fatal(err)
			}
			if !outside {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			runAppCommand(t, app, app.executePaletteCommand("file.revealInTree"))
			found := false
			for _, notice := range app.ws.Status().Notifications(false) {
				if strings.Contains(notice.Summary, "Cannot show this file in the tree") {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing actionable error for outside=%v", outside)
			}
		})
	}
}

func TestTreeInputSupersedesPendingControlReveal(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "nested", "target.txt")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	first, second := filepath.Join(root, "other-a.txt"), filepath.Join(root, "other-b.txt")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, ""), root)
	app := shell.app
	app.setActivity(activityFiles, true)
	app.setFocusTree()
	runAppCommand(t, app, app.beginClientFileTreeLoad())
	if !app.Tree.Model.SelectVisible(first) {
		t.Fatal("first file is not visible")
	}
	_ = app.View()
	port := app.ws.(shellport.ShellControl)
	input, err := json.Marshal(map[string]any{"paths": []string{target}})
	if err != nil {
		t.Fatal(err)
	}
	replies := make(chan control.Result, 1)
	request := shellport.ControlRequestMsg{
		Owner: port,
		Reply: func(_ context.Context, result control.Result) error { replies <- result; return nil },
		Envelope: coreprotocol.ControlEnvelope{Request: control.Request{
			ID: "pending-tree-reveal", CommandID: "tree.reveal", Target: port.ControlIdentity(), Input: input,
		}},
	}
	cmd := app.beginTreeControl(request)
	if cmd == nil {
		t.Fatal("control reveal did not start")
	}
	result := cmd()
	app.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if node := app.Tree.Model.SelectedNode(); node == nil || node.Path != second {
		t.Fatalf("tree input did not select the next row: %v", node)
	}
	_, reply := app.Update(result)
	runAppCommand(t, app, reply)
	select {
	case response := <-replies:
		if response.State != control.Failed || response.Fault == nil || response.Fault.Code != control.Stale {
			t.Fatalf("older control reveal was not rejected: %+v", response)
		}
	default:
		t.Fatal("stale control reveal did not receive a result")
	}
	if node := app.Tree.Model.SelectedNode(); node == nil || node.Path != second {
		t.Fatalf("older control reveal changed the user's selection: %v", node)
	}
	if app.Tree.Model.Root.Loading {
		t.Fatal("older control reveal left the tree stuck loading")
	}
	if node := app.Tree.Model.NodeByPath(filepath.Dir(target)); node == nil || node.IsExpanded {
		t.Fatalf("older control reveal expanded the requested folder: %v", node)
	}
}
