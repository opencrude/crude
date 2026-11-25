package workspace

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

func simConversationTexts(dialog providersim.Conversation) []string {
	texts := make([]string, 0, len(dialog.System)+len(dialog.Messages))
	for _, part := range dialog.System {
		texts = append(texts, part.Text)
	}
	for _, message := range dialog.Messages {
		texts = append(texts, simMessageText(message))
	}
	return texts
}
