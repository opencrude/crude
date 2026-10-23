package chatprojection

import (
	"strings"

	"crude/tui/rendering"
	"crude/types"
)

func renderMarkdown(content string, mdRenderer *rendering.MarkdownRenderer) (string, bool) {
	if mdRenderer == nil {
		return "", false
	}
	content = presentProposedPlan(content)
	if content != "" && strings.TrimSpace(content) == content && strings.IndexFunc(content, func(character rune) bool {
		return character != ' ' && (character < 'a' || character > 'z') && (character < 'A' || character > 'Z')
	}) < 0 {
		return "", false
	}
	r, err := mdRenderer.Render(content)
	if err != nil {
		return "", false
	}
	r = strings.TrimPrefix(r, "\n")
	return strings.TrimRight(r, "\n"), true
}

func updateStreamingCache(msg *types.ChatMessage, mdRenderer *rendering.MarkdownRenderer, contentWidth int) {
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

// presentProposedPlan shows Plan mode's proposed_plan block as a titled
// section between rules: the tags are protocol for Crude, not prose. Live and
// hydrated rows both render through here, so they paint identically.
func presentProposedPlan(content string) string {
	if !strings.Contains(content, "proposed_plan>") {
		return content
	}
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		switch strings.TrimSpace(line) {
		case "<proposed_plan>":
			lines[index] = "\n---\n\n**Proposed plan**\n"
		case "</proposed_plan>":
			lines[index] = "\n---\n"
		}
	}
	return strings.Join(lines, "\n")
}
