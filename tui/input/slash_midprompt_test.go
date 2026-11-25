package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/agent/sessiondb"
	"crude/tui/theme"
)

func typeInto(t *testing.T, model Model, text string) Model {
	t.Helper()
	for _, r := range text {
		var event Event
		model, _, event = model.Update(tea.KeyPressMsg{Code: r, Text: string(r)}, false, false)
		if event != nil {
			t.Fatalf("typing %q emitted %T", text, event)
		}
	}
	return model
}

// The user writes a prompt, remembers a setting, types a command where the
// caret is and picks it. Only the command name leaves the draft.
func TestSlashCommandInsidePromptRunsAndKeepsThePrompt(t *testing.T) {
	for _, accept := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, {Code: tea.KeyTab}} {
		t.Run(accept.String(), func(t *testing.T) {
			model := NewModel(theme.DefaultDark())
			model = typeInto(t, model, "fix the failing test ")
			model = typeInto(t, model, "/comp")
			if !model.SlashActive() || len(model.SlashFiltered()) == 0 || model.SlashFiltered()[0].Name != "compact" {
				t.Fatalf("command list did not open inside the prompt: active=%v %+v", model.SlashActive(), model.SlashFiltered())
			}
			model.LayoutShell(72, model.ShellHeight(72))
			updated, _, event := model.Update(accept, false, false)
			slash, ok := event.(SlashCommandEvent)
			if !ok || slash.Command != "/compact" || len(slash.ReferenceIDs) != 0 {
				t.Fatalf("event=%#v", event)
			}
			if updated.Value() != "fix the failing test" || updated.CursorFlat() != len("fix the failing test") || updated.SlashActive() {
				t.Fatalf("draft=%q cursor=%d", updated.Value(), updated.CursorFlat())
			}
		})
	}
}

func TestSlashCommandBetweenWordsRemovesOnlyItsName(t *testing.T) {
	model := NewModel(theme.DefaultDark())
	model.SetInput("alpha  beta")
	model.input.SetCursorFlat(len("alpha "))
	model = typeInto(t, model, "/sett")
	model.LayoutShell(72, model.ShellHeight(72))
	updated, _, event := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false, false)
	if slash, ok := event.(SlashCommandEvent); !ok || slash.Command != "/settings" {
		t.Fatalf("event=%#v", event)
	}
	if updated.Value() != "alpha beta" || updated.CursorFlat() != len("alpha ") {
		t.Fatalf("draft=%q cursor=%d", updated.Value(), updated.CursorFlat())
	}
}

// Attachments in the prompt stay attached and are not handed to the command.
func TestSlashCommandInsidePromptKeepsAttachments(t *testing.T) {
	model := NewModel(theme.DefaultDark())
	ref := sessiondb.Reference{ID: "ref-1", Label: "[Pasted Text1]", Kind: sessiondb.KindPastedText}
	model.SetReferenceStore(&pasteReferenceStore{references: []sessiondb.Reference{ref}})
	model.SetInput("review " + ref.Label + " ")
	model.UseReferenceIDs([]string{ref.ID})
	model.input.SetCursorFlat(len([]rune(model.Value())))
	model = typeInto(t, model, "/sess")
	model.LayoutShell(72, model.ShellHeight(72))
	updated, _, event := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false, false)
	if slash, ok := event.(SlashCommandEvent); !ok || slash.Command != "/sessions" || len(slash.ReferenceIDs) != 0 {
		t.Fatalf("event=%#v", event)
	}
	if updated.Value() != "review "+ref.Label || len(updated.ReferencedIDs()) != 1 || updated.ReferencedIDs()[0] != ref.ID {
		t.Fatalf("draft=%q refs=%v", updated.Value(), updated.ReferencedIDs())
	}
}

// Paths and other slash prose are never commands.
func TestSlashProseIsSentAsAPrompt(t *testing.T) {
	model := NewModel(theme.DefaultDark())
	model = typeInto(t, model, "look at /usr/bin")
	if model.SlashActive() {
		t.Fatal("a path opened the command list")
	}
	if _, _, event := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false, false); event == nil {
		t.Fatal("prompt was not sent")
	} else if submit, ok := event.(SubmitEvent); !ok || submit.Text != "look at /usr/bin" {
		t.Fatalf("event=%#v", event)
	}
	model = NewModel(theme.DefaultDark())
	model.SetInput("/etc/hosts is wrong, fix it")
	updated, _, event := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false, false)
	if submit, ok := event.(SubmitEvent); !ok || submit.Text != "/etc/hosts is wrong, fix it" {
		t.Fatalf("a prompt that starts with a path was not sent: %#v", event)
	}
	_ = updated
}

// A mistyped command alone is explained and kept for correction, never
// cleared or sent to the model.
func TestUnknownCommandKeepsTheDraft(t *testing.T) {
	model := NewModel(theme.DefaultDark())
	model = typeInto(t, model, "/compcat")
	updated, _, event := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false, false)
	if unknown, ok := event.(UnknownCommandEvent); !ok || unknown.Name != "/compcat" {
		t.Fatalf("event=%#v", event)
	}
	if updated.Value() != "/compcat" {
		t.Fatalf("draft=%q", updated.Value())
	}
}

// A command that takes no arguments, typed in front of a prompt, runs alone:
// the prompt after it stays. Commands with arguments still take the line.
func TestLeadingCommandWithoutArgumentsKeepsTheRestOfThePrompt(t *testing.T) {
	model := NewModel(theme.DefaultDark())
	model.SetInput("/compact then refactor the parser")
	updated, _, event := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false, false)
	if slash, ok := event.(SlashCommandEvent); !ok || slash.Command != "/compact" {
		t.Fatalf("event=%#v", event)
	}
	if updated.Value() != "then refactor the parser" {
		t.Fatalf("draft=%q", updated.Value())
	}
	model = NewModel(theme.DefaultDark())
	model.SetInput("/plan refactor the parser")
	updated, _, event = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false, false)
	if slash, ok := event.(SlashCommandEvent); !ok || slash.Command != "/plan refactor the parser" || updated.Value() != "" {
		t.Fatalf("event=%#v draft=%q", event, updated.Value())
	}
}

// Moving the caret away from the command name or breaking the line closes the
// list; the newline chord never runs a command.
func TestSlashListFollowsTheCaret(t *testing.T) {
	model := NewModel(theme.DefaultDark())
	model = typeInto(t, model, "note /sett")
	if !model.SlashActive() {
		t.Fatal("list did not open")
	}
	model, _, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyLeft}, false, false)
	if model.SlashActive() {
		t.Fatal("list stayed open with the caret inside the name")
	}
	model, _, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyRight}, false, false)
	if !model.SlashActive() {
		t.Fatal("list did not reopen at the end of the name")
	}
	model, _, event := model.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}, false, false)
	if event != nil || model.Value() != "note /sett\n" {
		t.Fatalf("newline chord: event=%#v draft=%q", event, model.Value())
	}
}
