package input

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

func TestBlockingPromptKeepsDraftOnEnter(t *testing.T) {
	for _, thinking := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "active turn"}[thinking], func(t *testing.T) {
			model := NewModel(theme.DefaultDark())
			model.SetInput("keep this draft\nand its selection")
			model.UseReferenceIDs([]string{"unresolved-reference"})
			model.SetSelection(5, 9)
			before := model.input.EditState()
			revision, epoch, refs := model.DraftRevision(), model.DraftEpoch(), model.SnapshotReferenceIDs()
			updated, command, event := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, thinking, true)
			if command != nil || event != nil {
				t.Fatalf("pending prompt emitted a send/steer: command=%v event=%#v", command != nil, event)
			}
			if updated.input.EditState() != before || updated.DraftRevision() != revision || updated.DraftEpoch() != epoch || !slices.Equal(updated.SnapshotReferenceIDs(), refs) {
				t.Fatal("blocked Enter changed the exact composer draft")
			}
			updated, _, event = updated.Update(tea.KeyPressMsg{Code: 'x', Text: "x"}, thinking, true)
			if event != nil || updated.Value() != "keep x draft\nand its selection" {
				t.Fatal("pending prompt prevented ordinary draft editing")
			}
		})
	}
}

func TestBlockingPromptStillAllowsNewlineAndSlash(t *testing.T) {
	model := NewModel(theme.DefaultDark())
	model.SetInput("draft")
	model, _, event := model.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}, true, true)
	if event != nil || model.Value() != "draft\n" {
		t.Fatalf("pending prompt blocked a newline: event=%T text=%q", event, model.Value())
	}
	model.SetInput("/help")
	model, _, event = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, true, true)
	command, ok := event.(SlashCommandEvent)
	if !ok || command.Command != "/help" || model.Value() != "" {
		t.Fatalf("pending prompt blocked a shell command: event=%#v text=%q", event, model.Value())
	}
}
