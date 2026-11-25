package core

import (
	"strings"

	"crude/internal/providersim"
)

func simMessageText(message providersim.Message) string {
	var texts []string
	for _, part := range message.Parts {
		if (part.Kind == providersim.PartText || part.Kind == providersim.PartToolResult) && part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func simConversationContains(dialog providersim.Conversation, text string) bool {
	for _, part := range dialog.System {
		if strings.Contains(part.Text, text) {
			return true
		}
	}
	for _, message := range dialog.Messages {
		if strings.Contains(simMessageText(message), text) {
			return true
		}
	}
	return false
}
