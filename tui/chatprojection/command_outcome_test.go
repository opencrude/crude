package chatprojection

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/provider"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/command"
)

func TestCommandOutcomesStayCompactStaticAndHydrateIdentically(t *testing.T) {
	for _, status := range []string{"completed", "failed", "killed"} {
		for _, width := range []int{60, 160} {
			t.Run(fmt.Sprintf("%s/%d", status, width), func(t *testing.T) {
				info := &command.Outcome{Handle: "settings-repro-build", Command: "go build ./...", PID: 41,
					Status: status, Duration: 4 * time.Second, ExitKnown: status != "killed", OutputBytes: 14}
				switch status {
				case "failed":
					info.ExitCode, info.Error = 7, "compiler failed"
				case "killed":
					info.Error = "timed out"
				}
				message := types.ChatMessage{InternalID: "outcome", Role: types.RoleUser, Origin: provider.OriginPeer,
					Content: "runtime instructions for read_async_output; do not rerun the command", Collaboration: &provider.CollaborationAttribution{
						EventID: "event", SenderName: "Background command settings-repro-build", Kind: "command_outcome", Command: info,
					}}
				newRenderer := func() *Renderer {
					r := NewRenderer(theme.DefaultDark())
					r.SetSize(width, 30)
					r.SetChatAreaBounds(0, 20)
					return r
				}
				live := newRenderer()
				live.AppendMessage(message)
				raw, _ := json.Marshal(message)
				var detached types.ChatMessage
				if err := json.Unmarshal(raw, &detached); err != nil {
					t.Fatal(err)
				}
				hydrated := newRenderer()
				hydrated.SyncMessages([]types.ChatMessage{detached}, 1)
				for _, expanded := range []bool{false, true} {
					if expanded {
						live.ToggleDisclosure(message.InternalID)
						hydrated.ToggleDisclosure(message.InternalID)
					}
					rows, _, _ := live.ExportMessageRows(width)
					resumed, _, _ := hydrated.ExportMessageRows(width)
					if !reflect.DeepEqual(rows, resumed) || len(rows) != 1 || rows[0].ToggleLine != 1 {
						t.Fatal("live/hydrated outcome or disclosure differs")
					}
					text := strings.Join(rows[0].Lines, "\n")
					if !expanded && (len(rows[0].Native) != 2 || strings.Contains(text, "read_async_output") || strings.Contains(text, " → ")) {
						t.Fatalf("runtime evidence still paints as an oversized peer card: %s", text)
					}
					if expanded && (!strings.Contains(text, "go build ./...") || !strings.Contains(text, "14 B") || !strings.Contains(text, "PID: 41")) {
						t.Fatalf("disclosure lost exact command evidence: %s", text)
					}
					for _, row := range rows[0].Native {
						if row.PaneBackground {
							t.Fatal("outcome retained collaboration-band background")
						}
					}
					wantTitle := ui.ColorToolTitle
					if status != "completed" {
						wantTitle = ui.ColorError
					}
					if rows[0].Native[1].Spans[2].Role != wantTitle || rows[0].Native[1].Spans[3].Role != ui.ColorToolOperand {
						t.Fatal("outcome drifted from ordinary tool semantic ink")
					}
					live.SetSpinFrame(400)
					later, _, _ := live.ExportMessageRows(width)
					if !reflect.DeepEqual(rows, later) {
						t.Fatal("settled command animates or changes geometry on clock ticks")
					}
				}
			})
		}
	}
}

func TestCommandOutcomeOutputSurvivesHydrationWithoutAnAgentRead(t *testing.T) {
	msg := types.ChatMessage{InternalID: "settled", Role: types.RoleUser, Collaboration: &provider.CollaborationAttribution{Kind: "command_outcome", Command: &command.Outcome{
		Handle: "tests", Command: "go test ./...", Status: "failed", ExitCode: 7, ExitKnown: true, OutputBytes: 40,
		Output: &command.Result{OutputDescriptor: command.OutputDescriptor{Handle: "tests", Status: command.Failed, TotalBytes: 40}, Output: "\x1b[31mFAIL\x1b[0m\r\n    retained stderr\n"},
	}}}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var restored types.ChatMessage
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	for _, message := range []types.ChatMessage{msg, restored} {
		r := newActivityRenderer()
		r.SyncMessages([]types.ChatMessage{message}, 1)
		compact, _, _ := r.ExportMessageRows(100)
		if strings.Contains(strings.Join(compact[0].Lines, "\n"), "retained stderr") || len(compact[0].Native) != 2 {
			t.Fatal("compact background outcome contains an output teaser")
		}
		r.ToggleDisclosure("settled")
		rows, _, _ := r.ExportMessageRows(100)
		text := strings.Join(rows[0].Lines, "\n")
		if !strings.Contains(text, "FAIL") || !strings.Contains(text, "    retained stderr") || strings.Contains(text, "[31m") {
			t.Fatalf("completion evidence is missing or unreadable: %s", text)
		}
	}
	clone := types.CloneConversationMessage(types.ConversationMessage{Collaboration: msg.Collaboration})
	clone.Collaboration.Command.Output.OutputDescriptor.Handle = "changed"
	if msg.Collaboration.Command.Output.OutputDescriptor.Handle != "tests" {
		t.Fatal("completion evidence aliases the source")
	}
}
