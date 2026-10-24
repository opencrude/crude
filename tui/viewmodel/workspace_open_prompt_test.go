package viewmodel

import (
	"strings"
	"testing"

	"crude/sessiondb"
	"crude/tui/screens/activitylist"
	filepickerscreen "crude/tui/screens/filepicker"
	"crude/tui/screens/launcher"
	"crude/tui/ui"
	"crude/workspace"

	tea "charm.land/bubbletea/v2"
)

func TestWorkspaceNavigationConsentAndLauncherJourney(t *testing.T) {
	rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root, target := t.TempDir(), t.TempDir()
	shell := newRemoteJourneyShell(t, rt, root)
	app, old := shell.app, shell.app.ws
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 35})
	app.launcherScreen = nil // live workflow removed home
	app.setActivity(activityWorkspaces, true)
	app.workspaceInfos = []workspace.WorkspaceInfo{{ProjectDir: target, ProjectName: "destination"}}
	app.paintShellFrame()
	r, _ := app.rootHost.ScreenRect(activitylist.ListID)
	// Navigation is one click into exact-destination consent, never a switch
	// merely because the resource was selected.
	run := func(msg tea.Msg) { _, cmd := app.Update(msg); runAppCommand(t, app, cmd) }
	run(tea.MouseClickMsg{X: r.X + 2, Y: r.Y + 1, Button: tea.MouseLeft})
	run(tea.MouseReleaseMsg{X: r.X + 2, Y: r.Y + 1, Button: tea.MouseLeft})
	if app.workspaceOpenPrompt == nil || app.workspaceOpenPrompt.root != target || app.workspaceAttachPending || app.ws != old {
		t.Fatal("single click did not request the exact destination safely")
	}
	if paint := app.View().RenderContent(); !strings.Contains(paint, "Switch workspace") || !strings.Contains(paint, "Open workspace") {
		t.Fatal("confirmation not painted")
	}
	// Pointer outside, global shortcuts, paste and wheel cannot bypass consent.
	run(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	run(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl | tea.ModAlt})
	run(tea.PasteMsg{Content: "must not enter composer"})
	if app.workspaceOpenPrompt == nil || app.Input.Value() != "" {
		t.Fatal("modal leaked input")
	}
	run(tea.KeyPressMsg{Code: tea.KeyEnter}) // safe default Cancel
	if app.workspaceOpenPrompt != nil || app.ws != old {
		t.Fatal("Enter did not keep workspace")
	}
	// Enter opens the selected resource after cancellation without a second
	// name/path hit implementation or an unreviewed switch.
	app.setFocusTree()
	app.paintShellFrame()
	app.rootHost.Focus(activitylist.ListID)
	run(tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.workspaceOpenPrompt == nil || app.workspaceOpenPrompt.root != target {
		t.Fatal("Enter did not request the selected workspace")
	}
	run(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.workspaceOpenPrompt != nil || app.ws != old {
		t.Fatal("Esc did not cancel")
	}
	app.launcherOpenFolder(target)
	// A real painted button click follows exactly the same decision owner.
	fw := app.overlayHost.Window(ui.OverlayWorkspaceSwitch)
	cw := fw.Content.(*ui.ComponentWidget)
	w, h := fw.Size()
	cw.Host().Resize(w, h)
	cw.Host().Layout()
	button, _ := cw.Host().ScreenRect(ui.ConfirmButtonID(0))
	x, y := fw.Position()
	run(tea.MouseClickMsg{X: x + button.X + 1, Y: y + button.Y, Button: tea.MouseLeft})
	if app.ws == old || app.ws.RootPath() != target {
		t.Fatal("approved navigation did not open destination")
	}
	app.paintShellFrame()
	if app.launcherScreen == nil || !app.launcherHostVisible() {
		t.Fatal("empty workspace did not mount launcher")
	}
	if _, ok := app.rootHost.ScreenRect(launcher.NewSessionID); !ok {
		t.Fatal("launcher actions not mounted")
	}
	if strings.Contains(bufferPlain(app.mainSurf.Buffer()), "Empty pane") {
		t.Fatal("workspace painted empty split state")
	}
}

func TestWorkspaceConsentFencesStaleAndCurrentTargets(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	app.launcherOpenFolder(app.ws.RootPath())
	if app.workspaceOpenPrompt != nil {
		t.Fatal("current workspace prompted")
	}
	old := app.ws
	app.launcherOpenFolder(t.TempDir())
	if app.workspaceOpenPrompt == nil {
		t.Fatal("missing prompt")
	}
	app.workspaceSwitchGen.Add(1)
	if cmd := app.resolveWorkspaceOpenPrompt(true); cmd != nil || app.ws != old || app.workspaceOpenPrompt != nil {
		t.Fatal("stale approval applied")
	}
}

func TestWorkspaceConsentRestoresFocusAfterRenderedKeys(t *testing.T) {
	rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	shell := newRemoteJourneyShell(t, rt, t.TempDir())
	app, old := shell.app, shell.app.ws
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	app.setActivity(activityWorkspaces, true)
	app.paintShellFrame()
	app.launcherOpenFolder(t.TempDir())
	app.View() // product repaint between every physical input
	fw := app.overlayHost.Window(ui.OverlayWorkspaceSwitch)
	cw := fw.Content.(*ui.ComponentWidget)
	if cw.Host().Focused() != ui.ConfirmButtonID(1) {
		t.Fatal("not Cancel focused")
	}
	app.frameGate.deferred, app.frameGate.due = true, false
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	runAppCommand(t, app, cmd)
	if app.shouldDeferFrame() {
		t.Fatal("confirmation key kept stale cached frame")
	}
	app.View()
	if cw.Host().Focused() != ui.ConfirmButtonID(0) {
		t.Fatal("paint reset deliberate confirmation focus")
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	app.View()
	if app.ws == old || !app.launcherHostVisible() {
		t.Fatal("painted keyboard journey did not switch to launcher")
	}
}

func TestWorkspaceConsentCancelInvalidatesDeferredFrame(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	app.launcherOpenFolder(t.TempDir())
	app.View()
	app.frameGate.deferred, app.frameGate.due = true, false
	app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.workspaceOpenPrompt != nil || app.shouldDeferFrame() {
		t.Fatal("cancel left confirmation painted by frame gate")
	}
}

func TestWorkspaceEntryPointsShareExactConsent(t *testing.T) {
	for _, entry := range []string{"home", "home-menu", "rail-name", "rail-path", "rail-menu", "folder-picker", "session-resume"} {
		t.Run(entry, func(t *testing.T) {
			app := newFullTestApp(t)
			wsOf(app).Logger = stubLogger{}
			app.width, app.height = 120, 35
			target := t.TempDir()
			app.workspaceInfos = []workspace.WorkspaceInfo{{ProjectDir: target, ProjectName: "target"}}
			app.launcherScreen.SetData(app.ws.RootPath(), app.workspaceInfos)
			app.paintShellFrame()
			switch entry {
			case "home":
				app.handleLauncherActivate(launcher.ActivateMsg{Screen: app.launcherScreen, Kind: launcher.ItemWorkspace, WorkspacePath: target})
			case "home-menu":
				r, _ := app.rootHost.ScreenRect(launcher.WorkspaceListID)
				menu := app.buildLauncherContextMenu(tea.Mouse{X: r.X + 2, Y: r.Y})
				if menu == nil {
					t.Fatal("home row has no context menu")
				}
				menu.Build()
				menu.Cursor = 0
				menu.Activate()
			case "rail-name", "rail-path", "rail-menu":
				app.setActivity(activityWorkspaces, true)
				app.paintShellFrame()
				r, _ := app.rootHost.ScreenRect(activitylist.ListID)
				if entry == "rail-menu" {
					menu := app.buildWorkspaceSidebarContextMenu(tea.Mouse{X: r.X + 2, Y: r.Y + 1})
					menu.Build()
					menu.Cursor = 0
					menu.Activate()
				} else {
					y := r.Y + 1
					if entry == "rail-path" {
						y++
					}
					app.Update(tea.MouseClickMsg{X: r.X + 2, Y: y, Button: tea.MouseLeft})
					app.Update(tea.MouseReleaseMsg{X: r.X + 2, Y: y, Button: tea.MouseLeft})
				}
			case "folder-picker":
				app.openFolderPicker()
				picker := app.FilePicker
				picker.Open(target, filepickerscreen.ModeFolder, nil)
				fw := app.overlayHost.Window(ui.OverlayFilePicker)
				frame := fw.Content.(*ui.WindowFrameWidget)
				w, h := fw.Size()
				frame.Host().Resize(w, h)
				frame.Host().Layout()
				frame.Host().Focus(filepickerscreen.ActionsID)
				result := frame.Host().Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
				runAppCommand(t, app, result.Cmd)
			case "session-resume":
				app.launcherResumeSession("exact-session", &sessiondb.SessionMeta{ProjectPath: target})
			}
			if p := app.workspaceOpenPrompt; p == nil || p.root != target || app.workspaceAttachPending {
				t.Fatalf("%s bypassed/retargeted consent: %+v", entry, p)
			}
			if entry == "session-resume" && app.workspaceOpenPrompt.resumeID != "exact-session" {
				t.Fatal("resume intent lost")
			}
		})
	}
}
