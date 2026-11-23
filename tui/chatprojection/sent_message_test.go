package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/theme"
	"crude/types/conversation"
)

// A message an agent sends to other agents reads like a message it receives:
// its text is open by default, under the same rail. Only the delivery details
// wait for expansion, and expansion never repeats the message.
func TestSentAgentMessagesShowTheirTextByDefault(t *testing.T) {
	for _, tc := range []struct {
		tool, content, body string
	}{
		{"agent_send", `{"event":{"id":"event-41","sequence":7,"kind":"work_request","origin":"agent","sender_id":"root","sender_name":"Main","recipients":[{"id":"child-1","name":"Nova"}],"body":"Check the gopls restart path.\n\nReport what fails."},"deliveries":[{"id":"delivery-1","recipient_id":"child-1","state":"pending","version":1}]}`, "Check the gopls restart path."},
		{"agent_respond", `{"event":{"id":"event-42","sequence":8,"kind":"request_disposition","origin":"agent","sender_id":"child-1","sender_name":"Nova","recipients":[{"id":"root","name":"Main"}],"body":"Accepted. I start with the restart path."},"deliveries":[{"id":"delivery-2","recipient_id":"root","state":"delivered","version":2}]}`, "Accepted. I start with the restart path."},
		{"publish_report", `{"artifact":{"id":"report-1","revision":2,"author_id":"child-1","kind":"final","summary":"Restart review","body":"The restart path drops the old client. Tests pass."},"publish":{"event":{"id":"event-43","kind":"report_published"},"deliveries":[{"id":"delivery-3","recipient_id":"root","state":"pending","version":1}]}}`, "The restart path drops the old client."},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			r := NewRenderer(theme.DefaultDark())
			r.SetSize(100, 40)
			r.SetChatAreaBounds(0, 30)
			msg := conversation.ChatMessage{
				InternalID: "sent", Role: conversation.RoleTool, ToolName: tc.tool,
				ToolStatus: conversation.ToolStatusSuccess, ToolInput: `{"body":"input copy"}`, Content: tc.content,
			}
			r.SyncMessages([]conversation.ChatMessage{msg}, 1, 0)
			rows, _, _ := r.ExportMessageRows(96)
			closed := strings.Join(rows[0].Lines, "\n")
			if !strings.Contains(closed, tc.body) || !strings.Contains(closed, collaborationRail+tc.body[:10]) {
				t.Fatalf("sent message text is not open by default under the rail:\n%s", closed)
			}
			if strings.Contains(closed, "Delivery") || strings.Contains(closed, "event-4") {
				t.Fatalf("delivery details show before expansion:\n%s", closed)
			}

			r.ToggleDisclosure("sent")
			rows, _, _ = r.ExportMessageRows(96)
			open := strings.Join(rows[0].Lines, "\n")
			if !strings.Contains(open, "Delivery") && !strings.Contains(open, "recipients") {
				t.Fatalf("expansion lost the delivery details:\n%s", open)
			}
			if got := strings.Count(open, tc.body); got != 1 {
				t.Fatalf("expanded row shows the message %d times:\n%s", got, open)
			}
		})
	}
}

// A failed send has no delivered text; it keeps the ordinary closed tool row.
func TestFailedAgentSendKeepsItsClosedRow(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(100, 40)
	r.SetChatAreaBounds(0, 30)
	r.SyncMessages([]conversation.ChatMessage{{
		InternalID: "failed", Role: conversation.RoleTool, ToolName: "agent_send",
		ToolStatus: conversation.ToolStatusError, ToolInput: `{"body":"Never delivered"}`,
		Content: `{"message":"recipient not found"}`,
	}}, 1, 0)
	rows, _, _ := r.ExportMessageRows(96)
	if text := strings.Join(rows[0].Lines, "\n"); strings.Contains(text, "Never delivered") {
		t.Fatalf("an undelivered message reads as sent:\n%s", text)
	}
}
