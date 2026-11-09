package viewmodel

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/snippet"
	"crude/tui/editor"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/types"
	"github.com/charmbracelet/x/ansi"
)

func snippetJourney(t *testing.T, width int) (*App, *editor.Editor) {
	t.Helper()
	app, _, ed := lspJourneyEditor(t, "pr")
	app.Update(tea.WindowSizeMsg{Width: width, Height: 24})
	app.setFocusEditor()
	app.Input.SetInput("keep the private composer draft")
	ed.SetCursorPosition(0, 2)
	text, plan, err := snippet.Parse("print(${1:value}, ${2|red,blue,green|}, $1)$0")
	if err != nil {
		t.Fatal(err)
	}
	if err := ed.ApplyCompletion(types.CompletionItem{Label: "print", InsertText: text, Snippet: plan}); err != nil {
		t.Fatal(err)
	}
	app.adoptEditorSnippet(ed)
	app.View()
	if !ed.SnippetActive() || ed.SelectedText() != "value" || app.editorSnippet == nil {
		t.Fatalf("snippet did not adopt its first field: %q selected=%q state=%+v", ed.Value(), ed.SelectedText(), app.editorSnippet)
	}
	return app, ed
}

func TestEditorSnippetRenderedKeysChoicesAndFinalLanding(t *testing.T) {
	for _, width := range []int{160, 80} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			app, ed := snippetJourney(t, width)
			app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
			if ed.Value() != "print(x, red, x)" || !ed.SnippetActive() {
				t.Fatal("normal editor input did not update mirrors", ed.Value())
			}
			app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			if ed.SelectedText() != "red" {
				t.Fatal("Tab did not navigate to numeric stop two")
			}
			app.Update(tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
			if !app.overlayHost.IsOpen(ui.OverlaySnippetChoices) || !ed.SnippetActive() {
				t.Fatal("Ctrl+Space did not open existing stop choices")
			}
			frame := ansi.Strip(app.View().RenderContent())
			for _, want := range []string{"Snippet choices", "red", "blue", "green"} {
				if !strings.Contains(frame, want) {
					t.Fatalf("choice UI missing %q: %s", want, frame)
				}
			}
			app.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			runAppCommand(t, app, cmd)
			if ed.Value() != "print(x, blue, x)" || app.overlayHost.IsOpen(ui.OverlaySnippetChoices) || !ed.SnippetActive() {
				t.Fatalf("native list activation lost editor interaction: %q", ed.Value())
			}
			app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			if ed.SnippetActive() || ed.HasSelection() || ed.CursorCol() != 17 {
				t.Fatalf("final landing differs: active=%t col=%d", ed.SnippetActive(), ed.CursorCol())
			}
			if app.Input.Value() != "keep the private composer draft" {
				t.Fatal("snippet keys consumed composer input")
			}
		})
	}
}

func TestEditorSnippetNativeChoicePointerAndStaleInvocation(t *testing.T) {
	app, ed := snippetJourney(t, 100)
	app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if !app.openSnippetChoices() {
		t.Fatal("choices missing")
	}
	first := app.editorSnippet.choice
	app.View()
	frame := first.window.Content.(*ui.WindowFrameWidget)
	rect, ok := frame.Host().ScreenRect(first.list.ID())
	if !ok {
		t.Fatal("native choice list not mounted")
	}
	x, y := first.window.Position()
	_, cmd := app.Update(tea.MouseClickMsg{X: x + rect.X + 2, Y: y + rect.Y + 1, Button: tea.MouseLeft})
	runAppCommand(t, app, cmd)
	if ed.Value() != "print(value, blue, value)" {
		t.Fatalf("native choice pointer selected wrong value: %q", ed.Value())
	}
	if !app.openSnippetChoices() {
		t.Fatal("choice interaction lost after applying first alternative")
	}
	current := app.editorSnippet.choice
	app.Update(editorSnippetChoiceMsg{choice: first, value: "green"})
	if ed.Value() != "print(value, blue, value)" || app.editorSnippet.choice != current {
		t.Fatal("an old callback edited or dismissed a later invocation")
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if !ed.SnippetActive() || app.overlayHost.IsOpen(ui.OverlaySnippetChoices) {
		t.Fatal("Escape must leave only the choice picker, preserving accepted bytes")
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if ed.SnippetActive() || !ed.IsOpen() || ed.Value() != "print(value, blue, value)" {
		t.Fatal("Escape on snippet interaction closed the editor or changed text")
	}
}

func TestEditorSnippetTrueFocusBoundariesAndAutomaticCompletion(t *testing.T) {
	for _, mode := range []string{"tree", "popup", "dock", "blur", "workspace", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			app, ed := snippetJourney(t, 100)
			before := ed.Value()
			switch mode {
			case "tree":
				app.setFocusTree()
			case "popup":
				app.openFindReplace(false)
			case "dock":
				screen := app.ensureTermScreen("snippet-focus", "Snippet focus PTY")
				screen.SetControlled(true)
				app.termDock.Mount("snippet-focus", screen)
				app.termDock.ShowPanel(termdock.PanelTerminal)
				app.paintShellFrame()
				r, ok := app.rootHost.ScreenRect(screen.ID())
				if !ok || r.Empty() {
					t.Fatal("terminal input owner is not actually rendered")
				}
				app.Update(tea.MouseClickMsg{X: r.X + 2, Y: r.Y, Button: tea.MouseLeft})
				app.Update(tea.MouseReleaseMsg{X: r.X + 2, Y: r.Y, Button: tea.MouseLeft})
				if !app.terminalDockOwnsKeys() {
					t.Fatal("rendered terminal did not gain input ownership")
				}
			case "blur":
				app.Update(tea.BlurMsg{})
			case "workspace":
				app.workspaceSwitchGen.Add(1)
			case "replacement":
				ed.Close()
			}
			app.syncEditorSnippet()
			if ed.SnippetActive() || app.editorSnippet != nil {
				t.Fatal("true owner departure retained snippet Tab ownership")
			}
			if mode != "replacement" && ed.Value() != before {
				t.Fatal("retiring private navigation changed accepted text")
			}
		})
	}
	app, ed := snippetJourney(t, 100)
	app.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if cmd := app.autoCompletionCmd(time.Now()); cmd != nil || app.completionIntent != nil || !ed.SnippetActive() {
		t.Fatal("automatic completions can steal active snippet Tab")
	}
}

func TestEditorSnippetLateAutomaticResultCannotStealIdenticalSourceInteraction(t *testing.T) {
	app, port := completionTestApp(t, 100, 32, "word")
	ed := app.EditorState.Editor
	ed.SetCursorPosition(0, 4)
	automatic := app.requestCompletion(true)
	if automatic == nil || app.completion == nil || !app.completion.automatic {
		t.Fatal("old automatic invocation was not really admitted")
	}
	old := app.completion
	late := automatic()
	text, plan, err := snippet.Parse("${1:word}$0")
	if err != nil {
		t.Fatal(err)
	}
	port.set.Entries = []types.LSPCompletionEntry{{ID: "original-word", Insertion: types.CompletionItem{Label: "word", InsertText: text, Snippet: plan}}}
	completionTestKey(t, app, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyTab})
	if !ed.SnippetActive() || ed.SelectedText() != "word" || ed.Revision() != old.revision || ed.Value() != "word" {
		t.Fatal("fixture did not retain identical source with a new private interaction")
	}
	app.Update(late)
	if app.completion != nil || !ed.SnippetActive() || ed.SelectedText() != "word" || ed.Value() != "word" {
		t.Fatal("late automatic result stole the identical-source snippet's Tab")
	}
	// A fresh explicit invocation remains permitted at the same source. An active
	// field with choices has its own Ctrl+Space priority in the preceding tests.
	completionTestKey(t, app, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
	if app.completion == nil || app.completion.screen == nil || app.completion.automatic {
		t.Fatal("manual request was suppressed by a snippet without choices")
	}
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyEsc})
	// This fresh manual chooser is a true input-owner departure, unlike the
	// retired automatic reply above. Dismissing it must not revive old fields.
	if ed.SnippetActive() || ed.Value() != "word" {
		t.Fatal("manual chooser departure revived snippet fields or changed accepted bytes")
	}
}
