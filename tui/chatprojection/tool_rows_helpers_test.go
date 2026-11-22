package chatprojection

import (
	"crude/tui/theme"
	"crude/types/conversation"
)

// Existing focused row tests have no workspace; production always passes its
// renderer's prepared root to the same builders.
func buildToolTitleLine(msg conversation.ChatMessage, phase string, frame int) PaintLine {
	return buildProjectToolTitleLine(msg, phase, frame, nil)
}

func buildToolTitleLines(msg conversation.ChatMessage, phase string, frame, width int, disclosure DisclosureState) []PaintLine {
	return buildProjectToolTitleLines(msg, phase, frame, width, disclosure, nil)
}

func renderToolMessageLines(msg conversation.ChatMessage, th *theme.Theme, contentWidth, innerW, width, spinFrame, height int, phase string, disclosure DisclosureState) messageLines {
	return renderProjectToolMessageLines(msg, th, contentWidth, innerW, width, spinFrame, height, phase, disclosure, nil)
}
