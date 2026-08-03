package shellinput

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/input"
	"crude/tui/theme"
)

// TestShellInputKeyboardContractTable documents slash-mode and submit keyboard
// behavior for the chat input shell surface (via bound input.Model).
func TestShellInputKeyboardContractTable(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		key             tea.KeyPressMsg
		wantSlashActive bool
		wantEvent       string // "", "slash", "submit"
		wantCommand     string
		wantInput       string
	}{
		{
			name:            "slash_prefix_opens_popup",
			input:           "/s",
			key:             tea.KeyPressMsg{Code: tea.KeyUp}, // no-op in slash list nav; stays active
			wantSlashActive: true,
		},
		{
			name:            "esc_dismisses_slash_popup",
			input:           "/s",
			key:             tea.KeyPressMsg{Code: tea.KeyEsc},
			wantSlashActive: false,
		},
		{
			name:            "enter_selects_slash_command",
			input:           "/help",
			key:             tea.KeyPressMsg{Code: tea.KeyEnter},
			wantSlashActive: false,
			wantEvent:       "slash",
			wantCommand:     "/help",
		},
		{
			name:            "tab_completes_slash_command",
			input:           "/set",
			key:             tea.KeyPressMsg{Code: tea.KeyTab},
			wantSlashActive: false,
			wantInput:       "/settings ",
		},
		{
			name:      "enter_submits_plain_text",
			input:     "hello",
			key:       tea.KeyPressMsg{Code: tea.KeyEnter},
			wantEvent: "submit",
		},
		{
			name:  "enter_empty_is_noop",
			input: "   ",
			key:   tea.KeyPressMsg{Code: tea.KeyEnter},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			th := theme.DefaultDark()
			m := input.NewModel(th)
			m.SetWidth(48)
			if tc.input != "" {
				m.SetInput(tc.input)
			}

			updated, _, ev := m.Update(tc.key, false, false)

			if updated.SlashActive() != tc.wantSlashActive {
				t.Fatalf("SlashActive=%v want %v", updated.SlashActive(), tc.wantSlashActive)
			}
			if tc.wantInput != "" && updated.Value() != tc.wantInput {
				t.Fatalf("input=%q want %q", updated.Value(), tc.wantInput)
			}

			switch tc.wantEvent {
			case "slash":
				se, ok := ev.(input.SlashCommandEvent)
				if !ok {
					t.Fatalf("event=%T want SlashCommandEvent", ev)
				}
				if se.Command != tc.wantCommand {
					t.Fatalf("command=%q want %q", se.Command, tc.wantCommand)
				}
			case "submit":
				if _, ok := ev.(input.SubmitEvent); !ok {
					t.Fatalf("event=%T want SubmitEvent", ev)
				}
			case "":
				if ev != nil {
					t.Fatalf("unexpected event %T", ev)
				}
			}
		})
	}
}
