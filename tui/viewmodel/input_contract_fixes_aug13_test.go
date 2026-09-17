package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/editor"
	helpcatalog "crude/tui/screens/help"
	"crude/tui/screens/shelltree"
	"crude/tui/ui"
)

func newInputContractEditorAppAug13(t *testing.T, names ...string) (*App, []string) {
	t.Helper()
	dir := t.TempDir()
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	app.RootPath = dir
	app.launcherScreen = nil
	app.BufferMgr = editor.NewBufferManager()

	paths := make([]string, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := app.openEditorTab(path); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return app, paths
}

func contextMenuItemAug13(st *ui.ContextMenuState, label string) (ui.ContextMenuItem, bool) {
	if st == nil {
		return ui.ContextMenuItem{}, false
	}
	for _, section := range st.Sections {
		for _, item := range section.Items {
			if item.Label == label {
				return item, true
			}
		}
	}
	return ui.ContextMenuItem{}, false
}

func TestInputContractsAug13FocusChatTypesIntoInputWithEditorOpen(t *testing.T) {
	app, _ := setupChatRoutingApp(t, 3, []chatprojection.MessageRow{{
		ID: "m1", Lines: []string{"assistant reply"},
	}})
	path := filepath.Join(t.TempDir(), "main.go")
	ed := editor.NewPreview(app.Theme, path, "package main\n", 1)
	app.EditorState.Editor = ed
	app.EditorState.ActiveView = path
	beforeEditor := ed.Value()

	app.executePaletteCommand("view.focus.chat")
	if app.focusTarget() != FocusInput {
		t.Fatalf("focusTarget=%v want FocusInput", app.focusTarget())
	}
	app.Input.ClearInput()
	_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: 'x', Text: "x"})

	if got := app.Input.Value(); got != "x" {
		t.Fatalf("explicit Focus Chat typing reached input=%q want x", got)
	}
	if got := ed.Value(); got != beforeEditor {
		t.Fatalf("explicit Focus Chat mutated editor: got %q want %q", got, beforeEditor)
	}
}

func TestInputContractsAug13CtrlWUsesDirtySafeEditorClose(t *testing.T) {
	t.Run("clean tab closes", func(t *testing.T) {
		app, paths := newInputContractEditorAppAug13(t, "clean.go")
		_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
		if _, ok := app.EditorState.Tabs[paths[0]]; ok {
			t.Fatal("Ctrl+W left the clean editor tab open")
		}
		if app.DirtyPromptPath != "" {
			t.Fatalf("clean close opened dirty prompt for %q", app.DirtyPromptPath)
		}
	})

	t.Run("dirty tab prompts", func(t *testing.T) {
		app, paths := newInputContractEditorAppAug13(t, "dirty.go")
		app.EditorState.Editor.InsertRune('x')
		if !app.EditorState.Editor.Modified() {
			t.Fatal("precondition: editor should be dirty")
		}

		_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
		if _, ok := app.EditorState.Tabs[paths[0]]; !ok {
			t.Fatal("Ctrl+W destroyed a dirty tab before confirmation")
		}
		if app.DirtyPromptPath != paths[0] {
			t.Fatalf("dirty prompt path=%q want %q", app.DirtyPromptPath, paths[0])
		}
		if app.overlayHost.Window(ui.OverlayDirtyClose) == nil {
			t.Fatal("Ctrl+W did not open the dirty-close prompt")
		}
	})
}

func TestInputContractsAug13GlobalTabSwitchWorksFromEditor(t *testing.T) {
	app, paths := newInputContractEditorAppAug13(t, "first.go", "second.go")
	if app.focusTarget() != FocusEditor || app.EditorState.ActiveView != paths[1] {
		t.Fatalf("precondition: focus=%v active=%q", app.focusTarget(), app.EditorState.ActiveView)
	}

	_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: '[', Mod: tea.ModCtrl})
	if got := app.EditorState.ActiveView; got != paths[0] {
		t.Fatalf("Ctrl+[ active=%q want %q", got, paths[0])
	}
	_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	if got := app.EditorState.ActiveView; got != paths[1] {
		t.Fatalf("Ctrl+] active=%q want %q", got, paths[1])
	}
}

func TestInputContractsAug13PasteStaysWithFocusedRootControl(t *testing.T) {
	t.Run("launcher search", func(t *testing.T) {
		app, screen := setupLauncherRoutingApp(t, nil)
		app.Input.SetInput("chat-sentinel")
		_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: '/', Text: "/"})
		if !screen.FilterActive() {
			t.Fatal("precondition: launcher filter is not active")
		}

		_, _ = app.dispatchPasteMsg(tea.PasteMsg{Content: "workspace"})
		if got := screen.Query(); got != "workspace" {
			t.Fatalf("launcher query=%q want workspace", got)
		}
		if got := app.Input.Value(); got != "chat-sentinel" {
			t.Fatalf("launcher paste leaked into chat input: %q", got)
		}
	})

	t.Run("tree filter", func(t *testing.T) {
		app, model, _ := setupTreeRoutingApp(t)
		app.Input.SetInput("chat-sentinel")
		app.FocusStack.Clear()
		app.FocusStack.Push(FocusTree)
		_ = model.EnterFilterMode()
		app.paintShellFrame()
		app.rootHost.Focus(shelltree.FilterID)

		_, _ = app.dispatchPasteMsg(tea.PasteMsg{Content: "main"})
		if got := model.Filter(); got != "main" {
			t.Fatalf("tree filter=%q want main", got)
		}
		if got := app.Input.Value(); got != "chat-sentinel" {
			t.Fatalf("tree paste leaked into chat input: %q", got)
		}
	})
}

func TestInputContractsAug13EditorMenuHasNoNoopFormatSelection(t *testing.T) {
	app, _ := newInputContractEditorAppAug13(t, "selection.go")
	app.EditorState.Editor.SetSelection(0, 0, 0, 7)
	if !app.EditorState.Editor.HasSelection() {
		t.Fatal("precondition: editor selection is not active")
	}

	menu := app.buildEditorContextMenu(tea.Mouse{})
	for _, section := range menu.Sections {
		for _, item := range section.Items {
			if strings.EqualFold(item.Label, "Format selection") {
				t.Fatal("editor menu still exposes the clickable no-op Format selection row")
			}
		}
	}
}

func TestInputContractsAug13CtrlCCopiesActiveChatSelection(t *testing.T) {
	app := newFullTestApp(t)
	screen, host, localX := setupCapturedChatSelection(t, app, 2, []chatprojection.MessageRow{{
		ID: "m1", Lines: []string{"hello world"},
	}})
	host.Handle(tea.MouseMotionMsg{
		X:      app.chatRect.X + localX + 5,
		Y:      app.chatRect.Y,
		Button: tea.MouseLeft,
	})
	if !screen.HasActiveSelection() || screen.SelectedText() == "" {
		t.Fatal("precondition: chat selection is empty")
	}

	_, cmd := app.dispatchKeyPress(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl+C on an active chat-message selection did not emit a copy command")
	}
	if host.HasPointerCapture() || screen.HasActiveSelection() {
		t.Fatal("Ctrl+C should finish capture and clear the copied chat selection")
	}
}

func TestInputContractsAug13ShortcutMetadataAndHelpStayConsistent(t *testing.T) {
	reg := ui.DefaultRegistry()

	if cancel := reg.Get("input.cancel"); cancel == nil || cancel.Label != "Cancel" {
		t.Fatalf("input.cancel metadata=%#v want Label Cancel", cancel)
	}
	ctrlO := tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}
	if match := reg.MatchGlobal(ctrlO); match == nil || match.ID != "file.attach" {
		t.Fatalf("Ctrl+O global match=%#v want file.attach", match)
	}
	ctrlW := tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl}
	if match := reg.MatchGlobal(ctrlW); match == nil || match.ID != "file.close" {
		t.Fatalf("Ctrl+W global match=%#v want file.close", match)
	}
	for key, want := range map[tea.KeyPressMsg]ui.ActionID{
		{Code: '[', Mod: tea.ModCtrl}: "tabs.previous",
		{Code: ']', Mod: tea.ModCtrl}: "tabs.next",
	} {
		if match := reg.MatchGlobal(key); match == nil || match.ID != want {
			t.Fatalf("%s global match=%#v want %s", key.String(), match, want)
		}
	}

	app, _ := newInputContractEditorAppAug13(t, "menu.go")
	app.paintShellFrame()
	closeLocalX := app.tabScreen.TabsControl().CloseLocalX(0)
	if closeLocalX < 0 {
		t.Fatal("could not resolve tab geometry")
	}
	menu := app.buildTabBarContextMenu(tea.Mouse{X: app.tabBarRailCol() + closeLocalX, Y: 3})
	closeItem, ok := contextMenuItemAug13(menu, "Close tab")
	if !ok {
		t.Fatal("tab context menu has no Close tab action")
	}
	for _, id := range []ui.ActionID{"file.close", "editor.close"} {
		want := ui.FormatShortcut(reg.HintFor(id))
		if want == "" || !strings.Contains(closeItem.Detail, want) {
			t.Fatalf("Close tab detail=%q missing registry hint %q (%s)", closeItem.Detail, want, id)
		}
	}

	topics := helpcatalog.UsageTopics()
	var chatBody, editorBody string
	for _, topic := range topics {
		switch topic.ID {
		case "chat":
			chatBody = topic.Body
		case "editor":
			editorBody = topic.Body
		}
	}
	if !strings.Contains(chatBody, "Ctrl+O") || !strings.Contains(chatBody, "Ctrl+C") {
		t.Fatalf("chat help does not describe attach/copy contracts: %q", chatBody)
	}
	for _, chord := range []string{"Ctrl+W", "Esc", "Ctrl+[", "Ctrl+]"} {
		if !strings.Contains(editorBody, chord) {
			t.Fatalf("editor help missing %q: %q", chord, editorBody)
		}
	}
}

func TestInputContractsAug13CtrlOOpensAttachFileGlobally(t *testing.T) {
	app, _ := newInputContractEditorAppAug13(t, "attach.go")
	if app.focusTarget() != FocusEditor {
		t.Fatalf("precondition: focus=%v want editor", app.focusTarget())
	}

	_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if app.FilePicker == nil || app.OverlayState.Current() != ModeFilePicker {
		t.Fatalf("Ctrl+O did not open Attach File: picker=%v mode=%v", app.FilePicker != nil, app.OverlayState.Current())
	}
}
