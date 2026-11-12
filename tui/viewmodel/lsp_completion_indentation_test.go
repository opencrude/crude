package viewmodel

import (
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/indentation"
)

// The keystroke enters through the same connected completion path as ordinary
// typing. The provider returns standard raw protocol evidence, not an edit.
func TestConnectedAdjustedCompletionUsesFileIndentAndOneUndo(t *testing.T) {
	initial := "package p\r\n\r\n\tpr\r\nend\r\n"
	shells, eds, _, provider, path := newTypingCompletionFixture(t, initial)
	app, ed := shells[0].app, eds[0]
	if err := ed.SetIndentationChoices(indentation.Choices{Style: indentation.Tabs, Size: 4, TabWidth: 4}); err != nil {
		t.Fatal(err)
	}
	ed.SetCursorPosition(2, 6)
	provider.base.mu.Lock()
	provider.response = `[{"label":"print block","filterText":"printBlock","insertTextFormat":2,"insertTextMode":2,"textEdit":{"range":{"start":{"line":2,"character":1},"end":{"line":2,"character":4}},"newText":"if (${1:界}) {\n${2:    use($1);}\n}$0"},"additionalTextEdits":[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":0}},"newText":"import \"fmt\"\r\n"}]}]`
	provider.base.mu.Unlock()
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'i', Text: "i"})
	remoteJourneyUntil(t, shells, func() bool {
		runAppCommand(t, app, app.autoCompletionCmd(time.Now()))
		return app.completion != nil && app.completion.screen != nil
	})
	if app.completion.screen.SoleID() == "" || app.completion.screen.SelectedID() != "" {
		t.Fatal("adjusted suggestion was not visible and passive")
	}
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyTab})
	want := "package p\r\nimport \"fmt\"\r\n\r\n\tif (界) {\r\n\t\tuse(界);\r\n\t}\r\nend\r\n"
	remoteJourneyUntil(t, shells, func() bool { return ed.Value() == want && eds[1].Value() == want })
	if !ed.SnippetActive() || ed.SelectedText() != "界" || app.Input.Value() != "private draft" {
		t.Fatal("adjusted insertion lost the active field or draft")
	}
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'Z', Text: "Z"})
	mirrored := "package p\r\nimport \"fmt\"\r\n\r\n\tif (Z) {\r\n\t\tuse(Z);\r\n\t}\r\nend\r\n"
	remoteJourneyUntil(t, shells, func() bool { return ed.Value() == mirrored && eds[1].Value() == mirrored })
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool { return ed.Value() == want && eds[1].Value() == want })
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool { return ed.Value() == "package p\r\n\r\n\tpri\r\nend\r\n" && eds[1].Value() == ed.Value() })
	if data, err := os.ReadFile(path); err != nil || string(data) != initial {
		t.Fatal("completion changed the file on disk", err)
	}
}

// Do not let a late acceptance apply an adjusted insertion to a changed source.
// A key typed after the acceptance is neither lost nor reordered: it follows
// the insertion, as if acceptance were immediate.
func TestAdjustedCompletionCancelledOrChangedSourceCannotApplyLate(t *testing.T) {
	for _, change := range []string{"escape", "typing", "peer"} {
		t.Run(change, func(t *testing.T) {
			initial := "package p\n\n  pr\n"
			shells, eds, _, provider, path := newTypingCompletionFixture(t, initial)
			app, ed := shells[0].app, eds[0]
			ed.SetCursorPosition(2, 4)
			provider.base.mu.Lock()
			provider.response = `[{"label":"print block","filterText":"printBlock","insertTextMode":2,"insertText":"if {\n  body\n}"}]`
			provider.base.mu.Unlock()
			renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'i', Text: "i"})
			remoteJourneyUntil(t, shells, func() bool {
				runAppCommand(t, app, app.autoCompletionCmd(time.Now()))
				return app.completion != nil && app.completion.screen != nil
			})
			_, pending := app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			if pending == nil || app.completion == nil || app.completion.pending == nil {
				t.Fatal("acceptance did not remain pending")
			}
			want := "package p\n\n  pri\n"
			switch change {
			case "escape":
				renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyEsc})
			case "typing":
				renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'x', Text: "x"})
				if ed.Value() != want || !ed.HoldsDocumentInput() {
					t.Fatal("a key typed after the acceptance did not wait for the insertion", ed.Value())
				}
				want = "package p\n\n  if {\n    body\n  }x\n"
				runAppCommand(t, app, pending)
			case "peer":
				eds[1].SetCursorPosition(0, 0)
				renameConnectedKey(t, shells[1], tea.KeyPressMsg{Code: 'X', Text: "X"})
				want = "Xpackage p\n\n  pri\n"
			}
			remoteJourneyUntil(t, shells, func() bool { return ed.Value() == want && eds[1].Value() == want })
			runAppCommand(t, app, pending)
			remoteJourneyUntil(t, shells, func() bool { return ed.Value() == want && eds[1].Value() == want })
			if ed.Value() != want || app.Input.Value() != "private draft" {
				t.Fatal("stale completion changed source or draft", ed.Value())
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != initial {
				t.Fatal("stale completion wrote disk", err)
			}
		})
	}
}
