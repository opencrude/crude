package turnloop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	captypes "crude/types/capability"
	"crude/types/conversation"
)

// formQuestionLane uses only the human question path. Approval, YOLO and
// programmatic question answers are not human consent to share information.
func formQuestionLane(blockID, tool string, emit func(AgentEvent), responses <-chan conversation.BlockResponse) func(context.Context, captypes.Form) captypes.FormReply {
	sequence := 0 // The call owner presents forms serially, including later rounds.
	return func(ctx context.Context, form captypes.Form) captypes.FormReply {
		cancel := captypes.FormReply{Action: "cancel"}
		if ctx.Err() != nil || responses == nil || blockID == "" {
			return cancel
		}
		values := make(map[string]string)
		items := make([]normalizedQuestion, 0, len(form.Fields)+1)
		recipient := fmt.Sprintf("MCP server %q, tool %q", form.ConnectionID, form.Tool)
		for index, f := range form.Fields {
			question := fmt.Sprintf("%s asks for %q (%s). Do not enter secrets or payment details.", recipient, f.Name, f.Type)
			if index == 0 {
				question += fmt.Sprintf("\nServer message: %q", form.Message)
			}
			if f.Title != "" || f.Description != "" {
				question += fmt.Sprintf("\nField: %q. %q", f.Title, f.Description)
			}
			if !f.Required {
				question += "\nYou can leave this blank."
			}
			opts := f.Choices
			if f.JSONChoiceInput() {
				choices, _ := json.Marshal(f.Choices)
				if f.Type == "array" {
					question += "\nEnter a JSON list of the values to send. Choices: "
				} else {
					question += "\nEnter the exact value as a quoted JSON string. Choices: "
				}
				question += string(choices)
				opts = nil
			}
			items = append(items, normalizedQuestion{Header: "Server question", Question: question, Options: opts, Multiple: opts != nil && f.Type == "array"})
		}
		share := fmt.Sprintf("Send your answers to %s? Select Decline to send no data.", recipient)
		if len(form.Fields) == 0 {
			share += fmt.Sprintf("\nServer message: %q", form.Message)
		}
		items = append(items, normalizedQuestion{Header: "Share answers", Question: share, Options: []string{"Decline", "Send answers"}})
		for base := 0; base < len(items); base += 4 {
			page := items[base:min(base+4, len(items))]
			body, err := normalizeQuestionRequest(map[string]any{"questions": page})
			if err != nil || ctx.Err() != nil {
				return cancel
			}
			sequence++
			id := fmt.Sprintf("%s:form:%d", blockID, sequence)
			emit(AgentEvent{Type: "blocking", Data: map[string]any{"block_id": id, "operation": "questions", "tool": tool, "questions_json": string(body)}})
			resp, ok := waitForBlockResponse(ctx, responses, id)
			emit(AgentEvent{Type: "blocking_complete", Data: map[string]string{"block_id": id}})
			if !ok || ctx.Err() != nil || resp.AttributionKind != "" || resp.Outcome != conversation.BlockAnswered {
				return cancel
			}
			for i := range page {
				answer := resp.HumanAnswers[i]
				if base+i == len(form.Fields) {
					switch answer {
					case "Decline":
						return captypes.FormReply{Action: "decline"}
					case "Send answers":
						return captypes.FormReply{Action: "accept", Values: values}
					default:
						return cancel
					}
				}
				if strings.TrimSpace(answer) != "" {
					values[form.Fields[base+i].Name] = answer
				}
			}
		}
		return cancel
	}
}
