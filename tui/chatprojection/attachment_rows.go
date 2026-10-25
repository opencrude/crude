package chatprojection

import (
	"strings"

	"crude/tui/cell"
	"crude/tui/ui"
	"crude/types"
	"crude/types/attachment"
)

// Protect source spans before Markdown changes source positions. Private-use
// markers are chosen outside the authored text, never parsed from labels in the
// rendered output. One marker per source rune retains wrapping geometry.
func attachmentSource(msg types.ChatMessage) (string, map[rune]attachment.Target) {
	if len(msg.Attachments) == 0 && len(msg.ReferenceLabels) == 0 {
		return msg.Content, nil
	}
	markers := make(map[rune]attachment.Target)
	source := []rune(msg.Content)
	end := 0
	marker := rune(0xe000)
	targets := msg.Attachments
	if len(targets) == 0 {
		var selected []attachment.Target
		for _, label := range msg.ReferenceLabels {
			selected = append(selected, attachment.Target{Label: label})
		}
		targets = attachment.Locate(msg.Content, selected)
	}
	for _, target := range targets {
		if !target.Valid(msg.Content) || target.Start < end {
			continue
		}
		for marker <= 0xf8ff && strings.ContainsRune(msg.Content, marker) {
			marker++
		}
		if marker > 0xf8ff {
			break
		}
		markers[marker] = target
		for i := target.Start; i < target.End; i++ {
			source[i] = marker
		}
		end = target.End
		marker++
	}
	return string(source), markers
}

func restoreAttachmentSpans(line PaintLine, msgID string, markers map[rune]attachment.Target, offsets map[rune]int) PaintLine {
	if len(markers) == 0 {
		return line
	}
	out := line
	out.Spans = nil
	for _, span := range line.Spans {
		var pending strings.Builder
		flush := func() {
			if pending.Len() > 0 {
				fragment := span
				fragment.Text = pending.String()
				out.Spans = append(out.Spans, fragment)
				pending.Reset()
			}
		}
		for _, r := range span.Text {
			target, ok := markers[r]
			if !ok {
				pending.WriteRune(r)
				continue
			}
			flush()
			label := []rune(target.Label)
			at := offsets[r]
			if at >= len(label) {
				continue
			}
			offsets[r]++
			fragment := span
			fragment.Text = string(label[at])
			fragment.Role, fragment.BgRole, fragment.BgSet = ui.ColorAccent2, ui.ColorSelection, true
			fragment.FGSet, fragment.BGSet = false, false
			fragment.Attr |= cell.AttrBold
			fragment.Action = &ui.Action{ID: ChatAttachmentOpenActionID, Payload: ChatAttachmentOpenPayload{MessageID: msgID, Target: target}}
			out.Spans = append(out.Spans, fragment)
		}
		flush()
	}
	return out
}
