package shelltabs

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/tui/ui"
)

// TestShellTabsInteractionContractTable documents keyboard tab-switch and
// mouse activate/close behavior for the shell tab bar.
func TestShellTabsInteractionContractTable(t *testing.T) {
	th := theme.DefaultDark()
	baseItems := []TabProjection{
		{ID: "1", Label: "Editor", Icon: "📄", Closable: true},
		{ID: "2", Label: "Chat", Icon: "💬", Closable: true},
	}

	tests := []struct {
		name        string
		activeID    string
		msg         tea.Msg
		resolveX    func(*Screen) int
		wantAction  string
		wantPayload string
		wantActive  string
	}{
		{
			name:        "right_arrow_activates_next_tab",
			activeID:    "1",
			msg:         tea.KeyPressMsg{Code: tea.KeyRight},
			wantAction:  "tabs.activate",
			wantPayload: "2",
			wantActive:  "2",
		},
		{
			name:        "left_arrow_wraps_to_last_tab",
			activeID:    "1",
			msg:         tea.KeyPressMsg{Code: tea.KeyLeft},
			wantAction:  "tabs.activate",
			wantPayload: "2",
			wantActive:  "2",
		},
		{
			name:        "enter_confirms_tab_bar_focus",
			activeID:    "1",
			msg:         tea.KeyPressMsg{Code: tea.KeyEnter},
			wantAction:  "tabs.enter",
			wantPayload: "1",
			wantActive:  "1",
		},
		{
			name:        "click_body_activates_tab",
			activeID:    "1",
			resolveX:    func(s *Screen) int { return tabBodyX(s, 1) },
			wantAction:  "tabs.activate",
			wantPayload: "2",
			wantActive:  "2",
		},
		{
			name:        "click_close_emits_close",
			activeID:    "1",
			resolveX:    func(s *Screen) int { return tabCloseX(s, 0) },
			wantAction:  "tabs.close",
			wantPayload: "1",
			wantActive:  "1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			screen := New(th)
			screen.SetProjection(baseItems, tc.activeID, -1, true)

			host := ui.NewHost(th, screen)
			host.RegisterTree(screen)
			host.Resize(48, 2)
			host.Layout()
			host.Focus(TabsID)

			tabs := screen.TabsControl()

			var res ui.EventResult
			switch msg := tc.msg.(type) {
			case tea.KeyPressMsg:
				res = host.Handle(msg)
			default:
				clickX := tc.resolveX(screen)
				if clickX < 0 {
					t.Fatal("could not resolve click X")
				}
				res = host.Handle(tea.MouseClickMsg{X: clickX, Y: 0, Button: tea.MouseLeft})
			}

			if tc.wantAction == "" {
				if res.Action != nil {
					t.Fatalf("unexpected action %+v", res.Action)
				}
				return
			}
			if res.Action == nil {
				t.Fatal("expected action, got nil")
			}
			if res.Action.ID != tc.wantAction {
				t.Fatalf("action=%q want %q", res.Action.ID, tc.wantAction)
			}
			if payload, ok := res.Action.Payload.(string); ok && payload != tc.wantPayload {
				t.Fatalf("payload=%q want %q", payload, tc.wantPayload)
			}
			if tc.wantActive != "" && tabs.ActiveID() != tc.wantActive {
				t.Fatalf("active=%q want %q", tabs.ActiveID(), tc.wantActive)
			}
		})
	}
}

func tabBodyX(s *Screen, tabIndex int) int {
	for x := 0; x < 48; x++ {
		idx, closeHit := s.HitTest(x)
		if idx == tabIndex && !closeHit {
			return x
		}
	}
	return -1
}

func tabCloseX(s *Screen, tabIndex int) int {
	for x := 0; x < 48; x++ {
		idx, closeHit := s.HitTest(x)
		if idx == tabIndex && closeHit {
			return x
		}
	}
	return -1
}
