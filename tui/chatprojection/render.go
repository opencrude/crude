package chatprojection

import (
	"strings"

	"crude/tui/rendering"
	"crude/tui/types"

	"charm.land/glamour/v2"
)

func renderMarkdown(content string, mdRenderer *glamour.TermRenderer) (string, bool) {
	if mdRenderer == nil {
		return "", false
	}
	r, err := mdRenderer.Render(content)
	if err != nil {
		return "", false
	}
	r = strings.TrimPrefix(r, "\n")
	return strings.TrimRight(r, "\n"), true
}

func updateStreamingCache(msg *types.ChatMessage, mdRenderer *glamour.TermRenderer, contentWidth int) {
	lastNL := strings.LastIndex(msg.Content, "\n")
	completedLen := 0
	if lastNL >= 0 {
		completedLen = lastNL + 1
	}
	if msg.StreamCompletedLen == completedLen {
		return
	}
	if completedLen == 0 {
		msg.StreamCompletedLines = nil
		msg.StreamCompletedLen = 0
		return
	}
	completed := msg.Content[:completedLen]
	rendered, isMarkdown := renderMarkdown(completed, mdRenderer)
	var lines []string
	if isMarkdown {
		lines = strings.Split(rendered, "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	} else {
		lines = rendering.WrapText(strings.TrimRight(completed, "\n"), contentWidth)
	}
	msg.StreamCompletedLines = lines
	msg.StreamCompletedLen = completedLen
}
