package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/history"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestSlashSelectionKeyboardPointerDraftParity(t *testing.T) {
	for _, path := range []string{"keyboard", "pointer"} {
		t.Run(path, func(t *testing.T) {
			model := NewModel(theme.DefaultDark())
			model.SetInput("/goal")
			model.UseReferenceIDs([]string{"old-unresolved-reference"})
			model.SetHistory([]history.Entry{{Text: "old recalled prompt"}})
			model.histIdx, model.histDraft, model.histPrefix = 0, "old unsent draft", "old"
			model.SetSelection(1, 3)
			model.LayoutShell(72, model.ShellHeight(72))
			beforeRevision, beforeEpoch := model.DraftRevision(), model.DraftEpoch()
			var event SlashCommandEvent
			if path == "keyboard" {
				updated, _, result := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false, false)
				model = updated
				var ok bool
				event, ok = result.(SlashCommandEvent)
				if !ok {
					t.Fatalf("keyboard event=%T, want SlashCommandEvent", result)
				}
			} else {
				bounds := model.slashList.Bounds()
				result := model.HandleShell(ui.EventContext{LocalX: bounds.X + 1, LocalY: bounds.Y}, ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
				if result.Action == nil || result.Action.ID != ActionSlashCommand {
					t.Fatalf("pointer action=%#v", result.Action)
				}
				var ok bool
				event, ok = result.Action.Payload.(SlashCommandEvent)
				if !ok {
					t.Fatalf("pointer event=%T, want SlashCommandEvent", result.Action.Payload)
				}
			}
			if event.Command != "/goal" || model.Value() != "" || model.SlashActive() || model.CursorFlat() != 0 || model.InputSelecting() || model.ScrollOffset() != 0 {
				t.Fatalf("selection did not activate /goal and reset the editor: event=%+v text=%q", event, model.Value())
			}
			if model.histIdx != -1 || model.histDraft != "" || model.histPrefix != "" || model.historyLoading || len(model.SnapshotReferenceIDs()) != 0 {
				t.Fatal("selection retained consumed history or reference state")
			}
			if model.DraftRevision() <= beforeRevision || model.DraftEpoch() <= beforeEpoch {
				t.Fatal("selection did not fence old draft and paste ownership")
			}
			for _, key := range []tea.KeyPressMsg{{Code: 'z', Mod: tea.ModCtrl}, {Code: tea.KeyDown}} {
				var result Event
				model, _, result = model.Update(key, false, false)
				if result != nil || model.Value() != "" {
					t.Fatalf("consumed draft resurfaced after %s: event=%T text=%q", key.String(), result, model.Value())
				}
			}
		})
	}
}

func TestSlashUnknownEnterStillDispatchesAndClears(t *testing.T) {
	model := NewModel(theme.DefaultDark())
	model.SetInput("/not-a-command")
	updated, _, event := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false, false)
	command, ok := event.(SlashCommandEvent)
	if !ok || command.Command != "/not-a-command" || updated.Value() != "" || updated.SlashActive() {
		t.Fatalf("unknown command behavior changed: event=%#v text=%q", event, updated.Value())
	}
}
