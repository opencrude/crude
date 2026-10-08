package viewmodel

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/tui/filetree"
	"crude/tui/screens/launcher"
	"crude/tui/ui"
	captypes "crude/types/capability"
	"crude/workspace"
)

func TestLauncherShellPreservesSharedPaintedEdges(t *testing.T) {
	for _, width := range []int{90, 160, 220} {
		for _, sidebar := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-sidebar-%v", width, sidebar), func(t *testing.T) {
				app := newFullTestApp(t)
				app.width, app.height = width, 40
				if sidebar {
					app.Tree = NewTreeState(filetree.NewFileTreeModel(t.TempDir()), 30)
				}
				app.Tree.Visible = sidebar
				app.sidebarScreen.SetModel(app.Tree.Model)
				app.launcherScreen.SetData("/projects/current", []workspace.WorkspaceInfo{{ProjectName: "alpha", ProjectDir: "/projects/alpha"}})
				app.launcherScreen.SetCapabilities(captypes.Snapshot{})
				app.paintShellFrame()
				rect := func(id ui.ComponentID) ui.Rect {
					t.Helper()
					r, ok := app.rootHost.ScreenRect(id)
					if !ok || r.Empty() {
						t.Fatalf("missing visible control %s: %+v", id, r)
					}
					return r
				}
				newSession, openWorkspace := rect(launcher.NewSessionID), rect(launcher.NewWorkspaceID)
				search := rect(launcher.SearchID)
				contentWidth := app.computeLayout().ContentW
				var skills, mcp ui.Rect
				if contentWidth >= 72 {
					skills, mcp = rect(launcher.SkillsID), rect(launcher.MCPID)
					if newSession.X != openWorkspace.X || openWorkspace.X != skills.X || skills.X != mcp.X || openWorkspace.W != skills.W || skills.W != mcp.W || search.X <= skills.X+skills.W || newSession.X+newSession.W != search.X+search.W {
						t.Fatalf("shell broke wide launcher geometry: new=%+v workspace=%+v skills=%+v mcp=%+v search=%+v", newSession, openWorkspace, skills, mcp, search)
					}
				} else if newSession.X != openWorkspace.X || newSession.W != openWorkspace.W || search.X != newSession.X || search.W != newSession.W {
					t.Fatalf("shell broke cramped full-width geometry: new=%+v workspace=%+v search=%+v", newSession, openWorkspace, search)
				}
				buf := app.mainSurf.Buffer()
				if buf.Get(newSession.X, newSession.Y+1).Char != '│' || buf.Get(openWorkspace.X, openWorkspace.Y+1).Char != '│' || buf.Get(newSession.X+newSession.W-1, newSession.Y+1).Char != '│' {
					t.Fatal("shell paint did not match published edge geometry")
				}
				if contentWidth >= 72 && (buf.Get(skills.X, skills.Y).Char != 'S' || buf.Get(mcp.X, mcp.Y).Char != 'M') {
					t.Fatal("wide launcher capability headings did not match published geometry")
				}
			})
		}
	}
}

func TestLauncherLoadingOwnsAnimationAndRejectsStaleCompletion(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 30
	app.Tree = &TreeState{}
	app.openLauncher()
	app.paintShellFrame()
	screen, gen := app.launcherScreen, app.launcherLoadGen.Load()
	if !screen.Loading() || !app.wantsFastTick() {
		t.Fatal("initial load missing animated lifecycle")
	}
	if text := bufferPlain(app.mainSurf.Buffer()); !strings.Contains(text, "Loading recent workspaces") || strings.Contains(text, "No recent workspaces") {
		t.Fatal(text)
	}
	app.Update(launcherWorkspacesMsg{Screen: screen, Gen: gen - 1, Err: errors.New("old failure")})
	if !screen.Loading() {
		t.Fatal("stale completion stopped animation")
	}
	app.Update(launcherWorkspacesMsg{Screen: screen, Gen: gen, Cwd: app.RootPath, Err: errors.New("listing unavailable")})
	app.paintShellFrame()
	if screen.Loading() || app.wantsFastTick() {
		t.Fatal("settled load kept fast ticks")
	}
	if text := bufferPlain(app.mainSurf.Buffer()); !strings.Contains(text, "Retry") {
		t.Fatal(text)
	}
	retry, _ := app.rootHost.ScreenRect(launcher.RetryID)
	result := app.rootHost.Handle(tea.MouseClickMsg{X: retry.X + 1, Y: retry.Y, Button: tea.MouseLeft})
	if result.Cmd == nil {
		t.Fatal("retry has no command")
	}
	app.Update(result.Cmd())
	if !screen.Loading() || app.launcherLoadGen.Load() <= gen {
		t.Fatal("retry did not start a fresh generation")
	}
}

func TestLauncherCapabilityResultsDoNotCrossOwnerOrWorkspace(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 30
	app.Tree = &TreeState{}
	snapshot := captypes.Snapshot{Generation: 1, Entries: []captypes.Entry{
		{ID: "team:review", Name: "Review skill", Kind: captypes.KindSkill, State: "available", Mode: "automatic"},
		{ID: "git", Name: "GitHub", Kind: captypes.KindMCP, State: "connected"},
	}, Bindings: []captypes.AgentBinding{{AgentID: "missing-agent", ID: "team:review", Kind: captypes.KindSkill}}}
	app.launcherCapabilitySequence = 4
	msg := launcherCapabilitiesMsg{screen: app.launcherScreen, workspace: app.workspaceSwitchGen.Load(), sequence: 4, snapshot: snapshot}
	app.applyLauncherCapabilities(msg)
	app.paintShellFrame()
	text := bufferPlain(app.mainSurf.Buffer())
	if !strings.Contains(text, "Review skill") || !strings.Contains(text, "GitHub") || strings.Contains(text, "loaded") {
		t.Fatal("capabilities were missing or historical binding claimed to be live: " + text)
	}
	app.launcherScreen = launcher.New(app.Theme)
	app.applyLauncherCapabilities(msg)
	app.paintShellFrame()
	if strings.Contains(bufferPlain(app.mainSurf.Buffer()), "Review skill") {
		t.Fatal("old owner result attached to replacement launcher")
	}
	msg.screen = app.launcherScreen
	msg.workspace++
	app.applyLauncherCapabilities(msg)
	app.paintShellFrame()
	if strings.Contains(bufferPlain(app.mainSurf.Buffer()), "GitHub") {
		t.Fatal("foreign workspace result applied")
	}
	command := app.launcherCapabilitiesCmd(time.Now())
	if command == nil {
		t.Fatal("runtime observation was not scheduled")
	}
	app.cancelCapabilityOperations()
	result := command().(launcherCapabilitiesMsg)
	if result.err == nil {
		t.Fatal("runtime observation ignored teardown cancellation")
	}
}

func TestLauncherRefreshKeepsFocusOutsideLauncher(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 30
	app.openLauncher()
	app.paintShellFrame()
	app.FocusStack.Push(FocusTree)
	app.syncRootFocus()
	focus := app.rootHost.Focused()
	app.Update(launcherWorkspacesMsg{Screen: app.launcherScreen, Cwd: app.RootPath, Gen: app.launcherLoadGen.Load(), Infos: []workspace.WorkspaceInfo{{ID: "other", ProjectDir: "/projects/other"}}})
	if app.rootHost.Focused() != focus {
		t.Fatal("workspace completion stole sidebar focus")
	}
}
