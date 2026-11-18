package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/agent/sessiondb"
	"crude/tui/ui"
	"crude/tui/window"
)

// askCrudeApp opens files in a real local shell at the given size.
func askCrudeApp(t *testing.T, width, height int, files map[string]string) *App {
	t.Helper()
	app, _ := newRealShellApp(t)
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(app.ws.RootPath(), name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	paneJourneySize(app, width, height)
	return app
}

// pressAskCrude sends the Ask Crude key through the real key path and runs
// the resulting commands to completion.
func pressAskCrude(t *testing.T, app *App) {
	t.Helper()
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	_ = app.View()
}

// askChat returns the agent tab that the Ask placed, failing when there is
// not exactly one agent tab.
func askChat(t *testing.T, app *App) *window.Tab {
	t.Helper()
	var found *window.Tab
	for _, tab := range app.Win.Tabs() {
		if app.isAgentTabTitle(tab.Title) {
			if found != nil {
				t.Fatal("Ask Crude opened more than one chat")
			}
			tab := tab
			found = &tab
		}
	}
	if found == nil {
		t.Fatal("Ask Crude opened no chat")
	}
	return found
}

func askComposerRef(t *testing.T, app *App) sessiondb.Reference {
	t.Helper()
	ids := app.Input.ReferencedIDs()
	if len(ids) != 1 {
		t.Fatalf("composer references = %v, want one; draft %q", ids, app.Input.Value())
	}
	ref, ok := app.RefStore().GetReference(ids[0])
	if !ok {
		t.Fatal("composer chip has no reference in the chat's session")
	}
	return ref
}

func TestAskCrudeSplitsBesideTheCodeAndAttachesTheSelection(t *testing.T) {
	app := askCrudeApp(t, 160, 40, map[string]string{"store.go": "package store\n\nfunc A() {}\nfunc B() {}\n"})
	openEditorTabs(t, app, "store.go")
	editorGroup := app.Win.ActiveGroup()
	editorTab := app.Win.ActiveTab().ID
	// A selection that ends at the start of line 4 covers lines 1-3 only.
	app.EditorState.Editor.SetSelection(0, 3, 3, 0)

	pressAskCrude(t, app)

	chat := askChat(t, app)
	if chat.Group == editorGroup || app.Win.Neighbor(editorGroup, ui.Horizontal, false) != chat.Group {
		t.Fatalf("chat group %d is not a new pane right of the editor (%d)", chat.Group, editorGroup)
	}
	if selected := app.Win.GroupActiveTab(editorGroup); selected == nil || selected.ID != editorTab {
		t.Fatal("the editor pane no longer shows the code that was asked about")
	}
	if app.Win.ActiveGroup() != chat.Group || app.focusTarget() != FocusInput {
		t.Fatal("the chat composer did not get the caret")
	}
	ref := askComposerRef(t, app)
	if ref.Kind != sessiondb.KindCodeSelection || ref.DisplayName() != "[store.go:1-3]" {
		t.Fatalf("chip = %s %q", ref.Kind, ref.DisplayName())
	}
	if ref.Content != "package store\n\nfunc A() {}" || !strings.HasSuffix(ref.FilePath, "store.go") {
		t.Fatalf("captured %q from %q", ref.Content, ref.FilePath)
	}
	if !strings.HasPrefix(app.Input.Value(), "[store.go:1-3] ") {
		t.Fatalf("draft %q does not start with the chip and a space", app.Input.Value())
	}
}

func TestAskCrudeUsesTheExistingSidePaneInsteadOfSplittingAgain(t *testing.T) {
	app := askCrudeApp(t, 160, 40, map[string]string{"left.go": "package left\n", "right.go": "package right\n"})
	openEditorTabs(t, app, "left.go")
	left := app.Win.ActiveGroup()
	right := app.splitMainGroup(ui.Horizontal, false, 0)
	runAppCommand(t, app, app.openPaneFiles([]string{filepath.Join(app.ws.RootPath(), "right.go")}, right))
	app.focusMainGroup(left)
	groups := len(app.Win.AllGroupIDs())

	pressAskCrude(t, app)

	chat := askChat(t, app)
	if chat.Group != right || len(app.Win.AllGroupIDs()) != groups {
		t.Fatalf("chat landed in group %d with %d panes; want the right pane %d and no new split", chat.Group, len(app.Win.AllGroupIDs()), right)
	}
	if app.Win.TabByTitle(app.editorTabTitle(filepath.Join(app.ws.RootPath(), "right.go"))) == nil {
		t.Fatal("the side pane lost its file tab")
	}
}

func TestAskCrudeReusesAVisibleChatAndKeepsItsDraft(t *testing.T) {
	app := askCrudeApp(t, 160, 40, map[string]string{"code.go": "package code\n"})
	openEditorTabs(t, app, "code.go")
	pressAskCrude(t, app)
	first := askChat(t, app)
	app.Input.SetInput(app.Input.Value() + "why")
	editorGroup := app.Win.GroupIDs()[0]
	app.focusMainGroup(editorGroup)
	app.setFocusEditor()

	pressAskCrude(t, app)

	if second := askChat(t, app); second.ID != first.ID {
		t.Fatal("a second Ask opened another chat while one was visible")
	}
	if got := app.Input.Value(); got != "[code.go:1] why [code.go:1] " || len(app.Input.ReferencedIDs()) != 1 {
		t.Fatalf("draft = %q", got)
	}
}

func TestAskCrudeOnANarrowTerminalKeepsOnePane(t *testing.T) {
	app := askCrudeApp(t, 80, 24, map[string]string{"small.go": "package small\n"})
	openEditorTabs(t, app, "small.go")
	editorGroup := app.Win.ActiveGroup()

	pressAskCrude(t, app)

	chat := askChat(t, app)
	if chat.Group != editorGroup || len(app.Win.AllGroupIDs()) != 1 {
		t.Fatalf("narrow Ask split the pane: chat group %d, panes %d", chat.Group, len(app.Win.AllGroupIDs()))
	}
	if askComposerRef(t, app).DisplayName() != "[small.go:1]" {
		t.Fatal("narrow Ask lost the chip")
	}
}

func TestAskCrudeSavesUnsavedCodeFirst(t *testing.T) {
	app := askCrudeApp(t, 160, 40, map[string]string{"dirty.go": "package dirty\n"})
	paths := openEditorTabs(t, app, "dirty.go")
	ed := app.EditorState.Editor
	ed.SetCursorPosition(0, len("package dirty"))
	_, _ = app.Update(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if !ed.Modified() {
		t.Fatal("setup: typing did not change the editor")
	}

	pressAskCrude(t, app)

	data, err := os.ReadFile(paths[0])
	if err != nil || string(data) != "package dirtyX\n" {
		t.Fatalf("disk = %q, %v; Ask Crude must save first", data, err)
	}
	if ref := askComposerRef(t, app); ref.Content != "package dirtyX" {
		t.Fatalf("chip carries %q, not the text the user saw", ref.Content)
	}
}

func TestAskCrudeExplainSendsThePreparedPromptToANewChat(t *testing.T) {
	app := askCrudeApp(t, 160, 40, map[string]string{"explain.go": "package explain\n"})
	openEditorTabs(t, app, "explain.go")

	for _, cmd := range app.askCrude(askExplain, false, "") {
		runAppCommand(t, app, cmd)
	}
	_ = app.View()

	chat := askChat(t, app)
	agentID := app.agentIDFromTabTitle(chat.Title)
	messages := app.ws.AgentMgr().Get(agentID).State.Messages.Messages()
	if len(messages) == 0 || !strings.Contains(messages[0].Content, "Explain [explain.go:1].") {
		t.Fatalf("first message = %+v", messages)
	}
	if app.Input.Value() != "" {
		t.Fatalf("a sent prompt left a draft: %q", app.Input.Value())
	}
}

func TestAskCrudeWithoutSelectionAttachesTheWholeBlock(t *testing.T) {
	app := askCrudeApp(t, 160, 40, map[string]string{"block.go": "package block\n\nfunc F() {\n\tif true {\n\t\treturn\n\t}\n}\n"})
	openEditorTabs(t, app, "block.go")
	app.EditorState.Editor.SetCursorPosition(3, 2) // inside the if, in F

	pressAskCrude(t, app)

	if ref := askComposerRef(t, app); ref.DisplayName() != "[block.go:4-6]" || ref.Content != "\tif true {\n\t\treturn\n\t}" {
		t.Fatalf("chip %q with %q; want the whole if block with its closing line", ref.DisplayName(), ref.Content)
	}
}

func TestAskCrudeCtrlKAfterKeyboardNegotiation(t *testing.T) {
	before := ui.CurrentKeyboardProfile()
	t.Cleanup(func() { ui.SetKeyboardProfile(before) })
	ui.SetKeyboardProfile(ui.KeyboardProfile{})

	app := askCrudeApp(t, 160, 40, map[string]string{"keys.go": "package keys\n"})
	_, _ = app.Update(tea.KeyboardEnhancementsMsg{Flags: 1})
	openEditorTabs(t, app, "keys.go")
	menu := app.buildEditorContextMenu(tea.Mouse{})
	found := false
	for _, section := range menu.Sections {
		for _, item := range section.Items {
			if item.Label == "Ask Crude" {
				found = true
				if item.Detail != "Ctrl+K" {
					t.Errorf("menu shortcut = %q, want Ctrl+K after keyboard negotiation", item.Detail)
				}
			}
		}
	}
	if !found {
		t.Fatal("editor menu has no Ask Crude action")
	}

	// Kitty-protocol terminals (Ghostty here) report Num Lock on modified
	// keys: this is the exact key the user's Ctrl+K produced. Use the real
	// Update path, not the registry alone, and keep the source unchanged.
	ed := app.EditorState.Editor
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl | tea.ModNumLock})
	runAppCommand(t, app, cmd)
	_ = app.View()
	chat := askChat(t, app)
	if app.Win.ActiveGroup() != chat.Group || app.focusTarget() != FocusInput {
		t.Fatal("Ctrl+K did not focus the chat composer")
	}
	if ref := askComposerRef(t, app); ref.Content != "package keys" {
		t.Fatalf("Ctrl+K attached %q, want the editor's code", ref.Content)
	}
	if ed.LineText(0) != "package keys" || ed.Modified() {
		t.Fatal("Ctrl+K changed the code")
	}
}
