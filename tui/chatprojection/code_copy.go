package chatprojection

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/ui"
)

const ChatCopyCodeActionID = "chat.copy-code"

type ChatCopyCodePayload struct{ MessageID, Language, Code string }

// The code node that produced the paint also owns its copy action. There is no
// fence scanner, text marker, or inference from a painted code block.
func codeCopyLine(code *rendering.CopyText, messageID string) PaintLine {
	language := rendering.SanitizeForTerminal(code.Language)
	label := "⧉ Copy code"
	if language != "" {
		label += " · " + language
	}
	return PaintLine{SourceAware: true, Spans: []RowSpan{{Text: label, Role: ui.ColorAccent2, Attr: cell.AttrBold,
		Action: &ui.Action{ID: ChatCopyCodeActionID, Payload: ChatCopyCodePayload{MessageID: messageID, Language: language, Code: code.Value}},
	}}}
}
