package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/shellport"
	"crude/snippet"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
	"github.com/charmbracelet/x/ansi"
)

// This fixture supplies a parsed completion result, not a real language engine.
// It exercises the actual two-shell/core document owner, topic, sparse edit,
// author-history, save and reattach flow. The gopls visual journey separately
// covers native provider conversion and the generated connected result codec.
func TestConnectedSnippetSparseMirrorsPrivateNavigationAuthorUndoAndResume(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "snippet.txt")
	initial := "A pr Z"
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	apps := []*App{one.app, two.app}
	a, b := liveTestOpen(t, one.app, path), liveTestOpen(t, two.app, path)
	one.app.setFocusEditor()
	two.app.setFocusEditor()
	one.app.Input.SetInput("private one")
	two.app.Input.SetInput("private two")
	sync := func(want string) {
		t.Helper()
		for _, app := range apps {
			runAppCommand(t, app, app.drainLiveEditorCommands())
		}
		liveTestPump(t, apps, nil)
		for _, app := range apps {
			ed := app.EditorState.Editor
			if ed.Value() != want || ed.DocumentState().Conflict != nil ||
				ed.DocumentState().PendingEdits != 0 || ed.DocumentState().InFlight ||
				ed.DocumentState().UndoPending || ed.DocumentState().HistoryQueued {
				t.Fatalf("shared text/history did not settle: want=%q got=%q state=%+v", want, ed.Value(), ed.DocumentState())
			}
			if frame := ansi.Strip(app.View().RenderContent()); !strings.Contains(frame, want) {
				t.Fatalf("canonical bytes missing from rendered editor: %q\n%s", want, frame)
			}
		}
		if b.SnippetActive() {
			t.Fatal("peer shell acquired another view's private Tab interaction")
		}
	}
	key := func(code rune, text string, mod tea.KeyMod) {
		t.Helper()
		_, cmd := one.app.Update(tea.KeyPressMsg{Code: code, Text: text, Mod: mod})
		runAppCommand(t, one.app, cmd)
	}

	a.SetCursorPosition(0, 4)
	text, plan, err := snippet.Parse("pair(${1:word}, ${2|red,blue|}, $1)$0")
	if err != nil {
		t.Fatal(err)
	}
	line, col := a.LSPCursorPosition()
	one.app.handleAppMessages(lspCompletionResultMsg{owner: a, revision: a.Revision(),
		generation: one.app.workspaceSwitchGen.Load(), filePath: path, line: line, col: col,
		items: []types.CompletionItem{{Label: "pair", InsertText: text, Snippet: plan}}})
	one.app.View()
	key(tea.KeyTab, "", 0)
	sync("A pair(word, red, word) Z")
	if !a.SnippetActive() || a.SelectedText() != "word" {
		t.Fatal("own receipt/publication retired the newly accepted field")
	}

	b.SetCursorPosition(0, 0)
	_, cmd := two.app.Update(tea.KeyPressMsg{Code: '!', Text: "!"})
	runAppCommand(t, two.app, cmd)
	sync("!A pair(word, red, word) Z")
	if !a.SnippetActive() || a.SelectedText() != "word" {
		t.Fatal("disjoint peer prefix lost private source anchors")
	}
	key('x', "x", 0)
	sync("!A pair(x, red, x) Z")
	key(tea.KeyTab, "", 0)
	key(' ', "", tea.ModCtrl)
	one.app.View()
	key(tea.KeyDown, "", 0)
	key(tea.KeyEnter, "", 0)
	sync("!A pair(x, blue, x) Z")
	if !a.SnippetActive() {
		t.Fatal("native choice or its own acknowledgment retired navigation")
	}
	one.app.requestEditorSaveWithFormat(a, workspace.DocumentSaveManual, false, false)
	sync("!A pair(x, blue, x) Z")
	if a.Modified() || b.Modified() || !a.SnippetActive() {
		t.Fatal("savepoint changed text, dirty state or private interaction")
	}
	for _, want := range []string{"!A pair(x, red, x) Z", "!A pair(word, red, word) Z", "!A pr Z"} {
		key('z', "", tea.ModCtrl)
		sync(want)
		if a.SnippetActive() {
			t.Fatal("Undo must retire private interaction, never peer work")
		}
	}
	for _, want := range []string{"!A pair(word, red, word) Z", "!A pair(x, red, x) Z", "!A pair(x, blue, x) Z"} {
		key('z', "", tea.ModCtrl|tea.ModShift)
		sync(want)
		if a.SnippetActive() {
			t.Fatal("Redo revived a stale interaction instead of only text")
		}
	}
	if one.app.Input.Value() != "private one" || two.app.Input.Value() != "private two" {
		t.Fatal("editor interactions consumed a private composer draft")
	}
	disk, err := os.ReadFile(path)
	if err != nil || string(disk) != "!A pair(x, blue, x) Z" {
		t.Fatalf("canonical save differs: %q %v", disk, err)
	}
	three := newRemoteJourneyShell(t, runtime, root)
	reopened := liveTestOpen(t, three.app, path)
	liveTestPump(t, append(apps, three.app), nil)
	if reopened.Value() != string(disk) || reopened.SnippetActive() || reopened.Modified() {
		t.Fatal("new attachment did not resume exact saved text without private snippets")
	}
}

func TestConnectedSnippetChoiceNativeOverflowAndExactSemanticOwner(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "choices.txt")
	if err := os.WriteFile(path, []byte("pr"), 0600); err != nil {
		t.Fatal(err)
	}
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	a, b := liveTestOpen(t, one.app, path), liveTestOpen(t, two.app, path)
	one.app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	one.app.setFocusEditor()
	a.SetCursorPosition(0, 2)
	alternatives := []string{""}
	for i := 1; i <= 40; i++ {
		alternatives = append(alternatives, fmt.Sprintf("option-%02d", i))
	}
	text, plan, err := snippet.Parse("${1|" + strings.Join(alternatives, ",") + "|}:$1$0")
	if err != nil {
		t.Fatal(err)
	}
	line, col := a.LSPCursorPosition()
	one.app.Update(lspCompletionResultMsg{owner: a, revision: a.Revision(),
		generation: one.app.workspaceSwitchGen.Load(), filePath: path, line: line, col: col,
		items: []types.CompletionItem{{Label: "choices", InsertText: text, Snippet: plan}}})
	one.app.View()
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	runAppCommand(t, one.app, cmd)
	liveTestPump(t, []*App{one.app, two.app}, nil)
	_, cmd = one.app.Update(tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
	runAppCommand(t, one.app, cmd)
	p := one.app.editorSnippet.choice
	frame := p.window.Content.(*ui.WindowFrameWidget)
	paint := ansi.Strip(one.app.View().RenderContent())
	if !strings.Contains(paint, "(empty)") || !p.scrollbar.Needed() {
		t.Fatal("empty alternative or actual overflow scrollbar is absent")
	}
	target := one.app.ws.(shellport.ShellControl).ControlIdentity()
	page, err := one.app.inspectShellControls(target, control.PageRequest{Limit: 128})
	if err != nil {
		t.Fatal(err)
	}
	oldID := ""
	for _, node := range page.Nodes {
		if strings.HasSuffix(node.ID, "::snippet-choice/list") {
			oldID = node.ID
		}
	}
	if oldID == "" {
		t.Fatal("native list is not available through its current semantic owner")
	}
	rect, ok := frame.Host().ScreenRect(p.list.ID())
	if !ok || rect.Empty() {
		t.Fatal("long native choice list is not actually mounted")
	}
	x, y := p.window.Position()
	for range 3 {
		_, cmd = one.app.Update(tea.MouseWheelMsg{X: x + rect.X + 2, Y: y + rect.Y + 2, Button: tea.MouseWheelDown})
		runAppCommand(t, one.app, cmd)
	}
	paint = ansi.Strip(one.app.View().RenderContent())
	if p.list.Scroll().Offset() == 0 || !strings.Contains(paint, "option-09") || strings.Contains(paint, "(empty)") {
		t.Fatal("wheel did not scroll the actual choice viewport")
	}
	shells := []remoteJourneyShell{one, two}
	result := journeySettingsControl(t, runtime, shells, one, "snippet-choice/list", ui.ControlOperation{Verb: "activate", ItemID: "32"})
	if result.State != control.Admitted || len(result.ResourceIDs) != 1 || result.ResourceIDs[0] != a.DocumentState().DocumentID {
		t.Fatalf("optimistic choice falsely claimed a canonical/disk commit: %+v", result)
	}
	liveTestPump(t, []*App{one.app, two.app}, nil)
	if a.Value() != "option-32:option-32" || b.Value() != a.Value() || !a.SnippetActive() || b.SnippetActive() ||
		one.app.overlayHost.IsOpen(ui.OverlaySnippetChoices) {
		t.Fatal("semantic choice did not use the same mirror/canonical/private owners")
	}
	if !one.app.openSnippetChoices() {
		t.Fatal("reopening current alternatives failed")
	}
	current := one.app.editorSnippet.choice
	stale := requestShellForTest(t, runtime, shells, one, "shell.describe_control",
		shellControlIntent{ComponentID: oldID, Operation: ui.ControlOperation{Verb: "activate", ItemID: "40"}}, nil)
	if stale.State == control.Applied || one.app.editorSnippet.choice != current || a.Value() != "option-32:option-32" {
		t.Fatal("retired control lifetime activated or dismissed a newer invocation")
	}
	foreign := requestShellForTest(t, runtime, shells, two, "shell.describe_control",
		shellControlIntent{ComponentID: oldID, Operation: ui.ControlOperation{Verb: "activate", ItemID: "40"}}, nil)
	if foreign.State == control.Applied || b.Value() != "option-32:option-32" {
		t.Fatal("another shell acquired a private snippet choice owner")
	}
}
