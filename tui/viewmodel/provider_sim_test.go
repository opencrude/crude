package viewmodel

import (
	"encoding/json"
	"strings"
	"testing"

	"crude/internal/providersim"
)

// The Chat wire emits the assistant role before its first text delta.
const chatFirstTextEvents = 2

func simulatedChat(t *testing.T, turns ...providersim.Turn) *providersim.Server {
	t.Helper()
	sim := providersim.New(t)
	sim.Script(turns...)
	return sim
}

// The ordinary shell fixtures do not report token usage unless they script it.
func simulatedAnswer(text string) providersim.Turn {
	return providersim.Text(text).WithoutUsage()
}

func simulatedCalls(calls ...providersim.ToolCall) providersim.Turn {
	return providersim.CallTools(calls...).WithoutUsage()
}

func simulatedTool(id, name string, arguments any) providersim.Turn {
	return simulatedCalls(simulatedCall(id, name, arguments))
}

func simulatedCall(id, name string, arguments any) providersim.ToolCall {
	args, err := json.Marshal(arguments)
	if err != nil {
		panic(err)
	}
	return providersim.ToolCall{ID: id, Name: name, Arguments: string(args)}
}

func simulatedToolResult(message providersim.Message) providersim.Part {
	for _, part := range message.Parts {
		if part.Kind == providersim.PartToolResult {
			return part
		}
	}
	return providersim.Part{}
}

func simulatedText(parts []providersim.Part) string {
	return simulatedPartText(parts, "\n")
}

func simulatedPartText(parts []providersim.Part, separator string) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Text != "" && (p.Kind == providersim.PartText || p.Kind == providersim.PartToolResult) {
			if b.Len() != 0 {
				b.WriteString(separator)
			}
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func simulatedUserText(request providersim.Request, separator string) string {
	return simulatedUserParts(request, separator, "\n")
}

func simulatedUserParts(request providersim.Request, separator, partSeparator string) string {
	var users []string
	for _, m := range request.Messages {
		if m.Role == "user" {
			users = append(users, simulatedPartText(m.Parts, partSeparator))
		}
	}
	return strings.Join(users, separator)
}
