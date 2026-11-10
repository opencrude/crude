package viewmodel

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/clipboard"
	"crude/internal/testterminal"
	"crude/terminalvt"
	"crude/tui/screens/shellterm"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/workspace"
)

func TestTerminalMenuRenamePlacementAndHidePreserveThePTY(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 140, 44
	command, args := testterminal.Command(t, "idle")
	info, err := createAppTerminal(t, app, workspace.TerminalCreateOpts{Command: command, Args: args, CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	app.dockTerminal(info.ID)
	app.paintShellFrame()
	screen := app.termScreens[info.ID]
	if screen.Title() != "Terminal 1" {
		t.Fatal("default name is not stable", screen.Title())
	}
	body, ok := app.rootHost.ScreenRect(screen.ID())
	if !ok {
		t.Fatal("terminal body is not mounted")
	}
	pointer := tea.MouseClickMsg{X: body.X + 4, Y: body.Y + 1, Button: tea.MouseRight}
	app.dispatchMouseRightClick(pointer)
	menu := app.LastContextMenu
	if menu == nil {
		t.Fatal("right-click did not open terminal actions")
	}
	items := map[string]ui.ContextMenuItem{}
	for _, section := range menu.Sections {
		for _, item := range section.Items {
			items[item.Label] = item
		}
	}
	for _, label := range []string{"Copy", "Paste", "Select all", "Rename terminal", "New terminal", "Open as tab", "Open as window", "Kill terminal"} {
		if _, found := items[label]; !found {
			t.Fatal("missing terminal action", label)
		}
	}
	if _, shown := items["Take input control"]; shown {
		t.Fatal("controller was offered a useless control action")
	}
	fw := app.overlayHost.Window(ui.OverlayContextMenu)
	x, _ := fw.Position()
	if x != pointer.X {
		t.Fatal("terminal menu is not pointer-anchored", x, pointer.X)
	}
	app.closeContextMenu()
	results := make(chan tea.Msg, 16)
	restore := installDetachedMessageSinkForTest(app, func(msg tea.Msg) { results <- msg })
	defer restore()
	items["Rename terminal"].Action()
	if app.TextPrompt == nil || !app.overlayHost.IsOpen(ui.OverlayTextPrompt) {
		t.Fatal("rename did not open the native modal")
	}
	app.handleTextPromptInput(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	app.handleTextPromptInput(tea.PasteMsg{Content: "Model training"})
	app.handleTextPromptInput(tea.KeyPressMsg{Code: tea.KeyEnter})
	select {
	case msg := <-results:
		runTerminalCommand(t, app, func() tea.Msg { return msg })
	case <-time.After(5 * time.Second):
		t.Fatal("rename did not finish through its real command owner")
	}
	if screen.Title() != "Model training" {
		t.Fatal("rename did not reach presentation", screen.Title())
	}
	app.handleHostAction(&ui.Action{ID: termdock.ActionOpenAsTab, Payload: termdock.PanelTerminal})
	app.paintShellFrame()
	if app.termDock.Contains(info.ID) || app.mainTerminalOwnsKeys() != screen {
		t.Fatal("tab placement duplicated or lost terminal input")
	}
	tab := app.panelTab("terminal/" + info.ID)
	if tab == nil || !strings.Contains(bufferPlain(app.mainSurf.Buffer()), "Model training") {
		t.Fatal("terminal did not appear as a real main tab")
	}
	app.handleAgentTabClose(app.Win.IndexOf(tab.ID))
	app.paintShellFrame()
	if app.termDock.IsOpen() || app.termScreens[info.ID] != screen {
		t.Fatal("closing the tab destroyed or reopened the terminal")
	}
	if current, ok := app.terminalInfo(info.ID); !ok || !current.Alive || current.PID != info.PID {
		t.Fatal("placement changed the PTY process")
	}
	app.toggleWorkspacePanel(termdock.PanelTerminal)
	if app.termDock.Active() != screen {
		t.Fatal("footer did not restore the same terminal")
	}
	app.handleTerminalAction(ui.Action{ID: shellterm.ActionClose, Payload: info.ID})
	waitAppTerminalClosed(t, app, info.ID)
}

func TestTerminalMenuClipboardUsesTheOwnedNativePTY(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	command, args := testterminal.Command(t, "echo", "READY")
	info, err := createAppTerminal(t, app, workspace.TerminalCreateOpts{Command: command, Args: args, CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	dirty, cancel, err := termSvc(app).Subscribe(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	waitText := func(want string) {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			page, err := termSvc(app).Rows(info.ID, terminalvt.RowRequest{Start: -1, Count: 0})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(terminalProjectionText(page), want) {
				return
			}
			select {
			case <-dirty:
			case <-timer.C:
				t.Fatal("native child did not echo", want)
			}
		}
	}
	waitText("READY")
	app.dockTerminal(info.ID)
	app.paintShellFrame()
	runTerminalCommand(t, app, app.drainTerminalCommands())
	results := make(chan tea.Msg, 16)
	restoreMessages := installDetachedMessageSinkForTest(app, func(msg tea.Msg) { results <- msg })
	defer restoreMessages()
	restoreClipboard := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read: func(format clipboard.Format) []byte {
			if format == clipboard.FmtText {
				return []byte("PASTED")
			}
			return nil
		},
		writeText: func(string) error { return nil },
	})
	defer restoreClipboard()
	action := func(label string) ui.ContextMenuItem {
		t.Helper()
		for _, section := range app.terminalContextMenu(info.ID).Sections {
			for _, item := range section.Items {
				if item.Label == label {
					return item
				}
			}
		}
		t.Fatal("terminal action is absent", label)
		return ui.ContextMenuItem{}
	}
	paste := action("Paste")
	if paste.Disabled {
		t.Fatal("controller cannot paste")
	}
	paste.Action()
	select {
	case msg := <-results:
		runTerminalCommand(t, app, func() tea.Msg { return msg })
	case <-time.After(5 * time.Second):
		t.Fatal("menu Paste did not finish")
	}
	waitText("READYPASTED")
	runTerminalCommand(t, app, app.terminalRefresh(info.ID))
	screen := app.termScreens[info.ID]
	copied := make(chan string, 1)
	screen.SetCopy(func(text string) tea.Cmd { return func() tea.Msg { copied <- text; return nil } })
	action("Select all").Action()
	runTerminalCommand(t, app, app.drainTerminalCommands())
	copyItem := action("Copy")
	if copyItem.Disabled {
		t.Fatal("Select all did not enable Copy")
	}
	copyItem.Action()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case msg := <-results:
			runTerminalCommand(t, app, func() tea.Msg { return msg })
		case text := <-copied:
			if !strings.Contains(text, "READYPASTED") {
				t.Fatal("Copy returned another surface's text", text)
			}
			return
		case <-timer.C:
			t.Fatal("menu Copy did not finish")
		}
	}
}
