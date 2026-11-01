package viewmodel

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"github.com/charmbracelet/x/ansi"
)

func ghostShellJourney(t *testing.T, width int) (*App, *editor.Editor) {
	t.Helper()
	app, _, ed := lspJourneyEditor(t, "base")
	app.Update(tea.WindowSizeMsg{Width: width, Height: 24})
	app.setFocusEditor()
	app.Input.SetInput("keep the private prediction draft")
	ed.SetCursorPosition(0, 4)
	app.paintShellFrame()
	if err := ed.SetGhostText(ed.Revision(), 4, "candidate"); err != nil {
		t.Fatal(err)
	}
	app.Update(tea.WindowSizeMsg{Width: width, Height: 24})
	if frame := ansi.Strip(app.View().RenderContent()); !strings.Contains(frame, "basecandidate") {
		t.Fatal("mounted shell did not paint its exact display-only candidate")
	}
	return app, ed
}

func TestEditorGhostShellEscapeDoesNotCloseItsSourceTab(t *testing.T) {
	for _, width := range []int{160, 80} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			app, ed := ghostShellJourney(t, width)
			app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			if !ed.IsOpen() || app.EditorState.Editor != ed || ed.Value() != "base" {
				t.Fatal("native shell Escape closed the source tab instead of dismissing prediction")
			}
			if _, _, present := ed.GhostText(); present {
				t.Fatal("native shell Escape retained a dismissed prediction")
			}
			if frame := ansi.Strip(app.View().RenderContent()); strings.Contains(frame, "basecandidate") {
				t.Fatal("dismissed prediction is still painted")
			}
			app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			if ed.Value() != "base    " || app.Input.Value() != "keep the private prediction draft" {
				t.Fatal("dismissal changed ordinary Tab indentation or consumed the private draft")
			}
		})
	}
}

func TestEditorGhostShellOwnerDepartureCannotReviveTabAcceptance(t *testing.T) {
	for _, departure := range []string{"tree", "popup", "blur", "workspace"} {
		t.Run(departure, func(t *testing.T) {
			app, ed := ghostShellJourney(t, 100)
			switch departure {
			case "tree":
				app.setFocusTree()
			case "popup":
				app.openFindReplace(false)
			case "blur":
				app.Update(tea.BlurMsg{})
			case "workspace":
				app.workspaceSwitchGen.Add(1)
			}
			app.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
			if _, _, present := ed.GhostText(); present {
				t.Fatal("native shell owner departure retained prediction acceptance authority")
			}
			if departure == "popup" {
				app.closeFindReplace()
			}
			app.setFocusEditor()
			app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			if ed.Value() != "base    " || app.Input.Value() != "keep the private prediction draft" {
				t.Fatal("returning focus revived a retired prediction or consumed source/draft")
			}
		})
	}
}

func TestEditorGhostShellNativeWordAndTypedPrefixRemainSourceEdits(t *testing.T) {
	app, ed := ghostShellJourney(t, 100)
	app.Update(tea.KeyPressMsg{Code: 'c', Text: "can"})
	if ed.Value() != "basecan" {
		t.Fatal("native shell typing did not retain its exact source edit")
	}
	if anchor, rest, present := ed.GhostText(); !present || anchor != 7 || rest != "didate" {
		t.Fatalf("native typed-through remainder = %d, %q, %t", anchor, rest, present)
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl})
	if ed.Value() != "basecandidate" {
		t.Fatal("native shell Ctrl+Right did not accept one literal candidate word")
	}
	if _, _, present := ed.GhostText(); present {
		t.Fatal("fully accepted candidate remained available for another Tab")
	}
	if app.Input.Value() != "keep the private prediction draft" {
		t.Fatal("candidate acceptance consumed private composer input")
	}
}

func TestEditorGhostAutomaticCompletionCannotStealCandidateKeys(t *testing.T) {
	for _, arrival := range []string{"new", "late"} {
		t.Run(arrival, func(t *testing.T) {
			app, _ := completionTestApp(t, 100, 32, "word")
			ed := app.EditorState.Editor
			ed.SetCursorPosition(0, 4)
			var late tea.Msg
			if arrival == "late" {
				command := app.requestCompletion(true)
				if command == nil {
					t.Fatal("fixture did not admit the original automatic request")
				}
				late = command()
			}
			if err := ed.SetGhostText(ed.Revision(), 4, "candidate"); err != nil {
				t.Fatal(err)
			}
			app.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
			if arrival == "new" {
				if app.requestCompletion(true) != nil {
					t.Fatal("automatic completion queried over a retained literal candidate")
				}
			} else {
				app.Update(late)
				if app.completion != nil {
					t.Fatal("late automatic completion stole the candidate's native keys")
				}
			}
			if _, rest, present := ed.GhostText(); !present || rest != "candidate" || ed.Value() != "word" {
				t.Fatal("automatic completion changed literal source or retired the current candidate")
			}
		})
	}
}
