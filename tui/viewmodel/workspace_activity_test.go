package viewmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/core/port"
	"crude/tui/screens/activitylist"
	"crude/tui/screens/launcher"
	"crude/tui/ui"
	"crude/workspace"
)

type workspaceActivityTestPort struct {
	shellport.Workspace
	root, namespace string
	read            map[string]workspace.WorkspaceActivity
	readErr         error
	reads           int
}

func (*workspaceActivityTestPort) Logger() workspace.Logger            { return stubLogger{} }
func (p *workspaceActivityTestPort) RootPath() string                  { return p.root }
func (p *workspaceActivityTestPort) WorkspaceID() string               { return "test-attachment" }
func (p *workspaceActivityTestPort) WorkspaceCatalogNamespace() string { return p.namespace }
func (p *workspaceActivityTestPort) Close() error                      { return nil }
func (p *workspaceActivityTestPort) AttachWorkspace(context.Context, string) (shellport.Workspace, error) {
	return nil, errors.New("unexpected attachment")
}
func (p *workspaceActivityTestPort) ReadWorkspaceActivity(context.Context) (map[string]workspace.WorkspaceActivity, error) {
	p.reads++
	if p.readErr != nil {
		return nil, p.readErr
	}
	return p.read, nil
}

func observedActivity(agents, terminals, files int) workspace.WorkspaceActivity {
	return workspace.WorkspaceActivity{
		Agents:    workspace.ActivityCount{Value: agents, Observed: true},
		Terminals: workspace.ActivityCount{Value: terminals, Observed: true},
		Unsaved:   workspace.ActivityCount{Value: files, Observed: true},
	}
}

func TestWorkspaceActivityPaintsSharedViewsAndDoesNotMoveBrowseSelection(t *testing.T) {
	app := newFullTestApp(t)
	const root = "/srv/user/deep/project"
	const peer = "/srv/user/deep/Project"
	port := &workspaceActivityTestPort{Workspace: app.ws, root: root, namespace: "ssh:dev",
		read: map[string]workspace.WorkspaceActivity{
			"mine": observedActivity(1, 0, 0), "peer": observedActivity(2, 1, 3),
		}}
	app.ws = port
	app.Update(tea.WindowSizeMsg{Width: 144, Height: 38})
	app.workspaceInfos = []workspace.WorkspaceInfo{
		{ID: "peer", ProjectName: "Project", ProjectDir: peer, DisplayPath: "~/deep/Project"},
		{ID: "mine", ProjectName: "project", ProjectDir: root, DisplayPath: "~/deep/project"},
		{ID: "cold", ProjectName: "cold", ProjectDir: "/srv/user/deep/cold", DisplayPath: "~/deep/cold"},
	}
	app.launcherScreen = launcher.New(app.Theme)
	app.launcherScreen.SetData(root, app.workspaceInfos)
	app.setActivity(activityWorkspaces, true)
	app.Tree.Width = 32
	app.setFocusTree()
	app.paintShellFrame()
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	browse := app.activityList.ViewState()

	now := time.Now()
	read := app.workspaceActivityCmd(now)
	if read == nil {
		t.Fatal("visible Workspaces did not schedule a retained read")
	}
	if app.workspaceActivityCmd(now) != nil || port.reads != 0 {
		t.Fatal("a command performed I/O on Update or admitted a duplicate")
	}
	app.Update(read())
	app.paintShellFrame()
	if after := app.activityList.ViewState(); after != browse {
		t.Fatalf("activity moved the list cursor or viewport: %+v -> %+v", browse, after)
	}
	side, _ := app.rootHost.ScreenRect(activitylist.ListID)
	home, _ := app.rootHost.ScreenRect(launcher.WorkspaceListID)
	line := func(r ui.Rect, row int) string {
		buf := app.mainSurf.Buffer()
		var b strings.Builder
		for x := r.X; x < r.X+r.W; x++ {
			b.WriteRune(buf.Get(x, r.Y+row).Char)
		}
		return strings.TrimRight(b.String(), " \x00")
	}
	if got := line(side, 1); !strings.HasSuffix(got, "◈2 >_1 ✎3") {
		t.Fatalf("sidebar did not right-align exact positive counts: %q", got)
	}
	if home.Empty() || !strings.HasSuffix(line(home, 0), "◈2 >_1 ✎3") {
		t.Fatalf("Home did not paint the same activity: %+v %q", home, line(home, 0))
	}
	if got := line(side, 2); !strings.Contains(got, "~/deep/Project") || strings.Contains(got, "/srv/user") {
		t.Fatalf("sidebar used the unabridged foreign path: %q", got)
	}
	items := app.workspaceSidebarItems()
	if items[1].Active || !items[2].Active || items[1].ID != sidebarItemWorkspacePrefix+peer {
		t.Fatalf("local path rules or browse state changed attached identity: %+v", items)
	}
	if strings.Contains(line(side, 5), "0") || strings.Contains(line(side, 5), "◈") {
		t.Fatalf("unobserved cold workspace claimed zero or activity: %q", line(side, 5))
	}
	app.Update(tea.MouseMotionMsg{X: side.X + 3, Y: side.Y + 2})
	if tip := app.tooltip.pending; tip == nil || !strings.Contains(tip.text, peer) ||
		!strings.Contains(tip.text, "Open terminals: 1") || !strings.Contains(tip.text, "Unsaved documents: 3") {
		t.Fatalf("hover omitted full path or count meanings: %+v", tip)
	}

	// A new observation can remove counts without reopening a list. The header
	// menu updates in place; its original cursor and actions stay installed.
	app.openWorkspacePathSwitcher()
	menu := app.LastContextMenu
	fw := app.overlayHost.Window(ui.OverlayContextMenu)
	list := ui.ContextMenuList(fw.Content.(*ui.ComponentWidget))
	selected := list.SelectedIndex()
	if entry, _ := list.Selected(); entry.Label != "Project" || entry.Trailing[2] != "✎3" {
		t.Fatalf("header did not share the retained snapshot: %+v", entry)
	}
	port.read = map[string]workspace.WorkspaceActivity{"mine": observedActivity(0, 0, 0), "peer": observedActivity(0, 0, 0)}
	read = app.workspaceActivityCmd(now.Add(time.Second))
	if read == nil {
		t.Fatal("open header switcher stopped refreshing its activity")
	}
	app.Update(read())
	if app.LastContextMenu != menu || list.SelectedIndex() != selected || list.Items()[selected].Trailing != (ui.RowBadges{}) {
		t.Fatal("settled activity changed header selection or left stale badges")
	}
	app.overlayHost.Close(ui.OverlayContextMenu)
	app.paintShellFrame()
	if strings.Contains(line(side, 1), "✎3") || strings.Contains(line(home, 0), ">_1") {
		t.Fatalf("finished work stayed visible after the updated read: %q / %q", line(side, 1), line(home, 0))
	}
}

func TestWorkspaceActivityFencesSwitchesAndDoesNotReadWhileHidden(t *testing.T) {
	app := newFullTestApp(t)
	const samePath = "/srv/user/repo"
	const stableID = "shared-id"
	local := &workspaceActivityTestPort{Workspace: app.ws, root: samePath, namespace: "",
		read: map[string]workspace.WorkspaceActivity{stableID: observedActivity(7, 4, 2)}}
	foreign := &workspaceActivityTestPort{Workspace: app.ws, root: samePath, namespace: "ssh:distinct-host",
		read: map[string]workspace.WorkspaceActivity{stableID: observedActivity(1, 0, 0)}}
	app.ws = local
	app.bindWorkspaceCatalogOwner()
	app.workspaceInfos = []workspace.WorkspaceInfo{{ID: stableID, ProjectDir: samePath, DisplayPath: "~/local/repo"}}
	app.launcherScreen = nil
	app.Tree.Visible = false
	now := time.Now()
	if app.workspaceActivityCmd(now) != nil || local.reads != 0 {
		t.Fatal("hidden workspaces incurred a background read")
	}
	app.setActivity(activityWorkspaces, true)
	initial := app.workspaceActivityCmd(now)
	if initial == nil {
		t.Fatal("showing Workspaces did not start observing")
	}
	old := initial()
	app.beginWorkspaceSwitch()
	app.ws = foreign
	app.resetWorkspaceNavigation()
	if len(app.workspaceInfos) != 0 {
		t.Fatal("a backend switch let the previous host's catalog remain actionable")
	}
	app.Update(launcherWorkspacesMsg{Gen: app.launcherLoadGen.Load(), Infos: []workspace.WorkspaceInfo{
		{ID: stableID, ProjectDir: samePath, DisplayPath: "~/remote/repo"},
	}})
	read := app.workspaceActivityCmd(now)
	if read == nil {
		t.Fatal("the new attachment inherited the old owner's read slot")
	}
	app.Update(old)
	if seen := app.workspaceSidebarItems()[1]; seen.Trailing != (ui.RowBadges{}) || seen.Subtitle != "~/remote/repo" {
		t.Fatalf("old owner changed the same ID on another host: %+v", seen)
	}
	app.Update(read())
	seen := app.workspaceSidebarItems()[1]
	if seen.Trailing[0] != "◈1" || seen.Trailing[1] != "" || seen.Trailing[2] != "" || !seen.Active {
		t.Fatalf("new owner did not establish its own current row and counts: %+v", seen)
	}
}

// A read that times out while the core is busy is not an observation of zero
// activity. It used to install empty counts, so every badge blinked off until
// the next completed read.
func TestWorkspaceActivityKeepsBadgesWhenAReadFails(t *testing.T) {
	app := newFullTestApp(t)
	const root = "/srv/user/repo"
	port := &workspaceActivityTestPort{Workspace: app.ws, root: root,
		read: map[string]workspace.WorkspaceActivity{"repo": observedActivity(2, 1, 0)}}
	app.ws = port
	app.bindWorkspaceCatalogOwner()
	app.workspaceInfos = []workspace.WorkspaceInfo{{ID: "repo", ProjectDir: root, DisplayPath: "~/repo"}}
	app.setActivity(activityWorkspaces, true)
	now := time.Now()
	app.Update(app.workspaceActivityCmd(now)())
	if seen := app.workspaceSidebarItems()[1]; seen.Trailing[0] != "◈2" || seen.Trailing[1] != ">_1" {
		t.Fatalf("first read did not paint the badges: %+v", seen)
	}
	port.readErr = context.DeadlineExceeded
	read := app.workspaceActivityCmd(now.Add(time.Second))
	if read == nil {
		t.Fatal("the next poll was not scheduled")
	}
	app.Update(read())
	if seen := app.workspaceSidebarItems()[1]; seen.Trailing[0] != "◈2" || seen.Trailing[1] != ">_1" {
		t.Fatalf("a failed read erased the badges: %+v", seen)
	}
	if app.workspaceActivity.cancel != nil {
		t.Fatal("a failed read left its slot busy, so the next poll cannot start")
	}
	port.readErr, port.read = nil, map[string]workspace.WorkspaceActivity{"repo": observedActivity(0, 0, 0)}
	app.Update(app.workspaceActivityCmd(now.Add(2 * time.Second))())
	if seen := app.workspaceSidebarItems()[1]; seen.Trailing != (ui.RowBadges{}) {
		t.Fatalf("the next completed read did not replace the kept badges: %+v", seen)
	}
}
