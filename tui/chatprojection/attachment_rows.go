package chatprojection

import (
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/ui"
	"crude/types"
	"crude/types/attachment"
	"unicode/utf8"
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

type attachmentCopyOffset struct {
	runes  int
	bytes  int
	source *rendering.CopyText
}

func restoreAttachmentSpans(line PaintLine, msgID string, markers map[rune]attachment.Target, offsets map[rune]attachmentCopyOffset) PaintLine {
	if len(markers) == 0 {
		return line
	}
	out := line
	out.Spans = nil
	for _, span := range line.Spans {
		byteStart := 0
		var pending strings.Builder
		flush := func() {
			if pending.Len() > 0 {
				fragment := span.Slice(byteStart, byteStart+pending.Len())
				out.Spans = append(out.Spans, fragment)
				pending.Reset()
			}
		}
		for pos, r := range span.Text {
			target, ok := markers[r]
			if !ok {
				if pending.Len() == 0 {
					byteStart = pos
				}
				pending.WriteRune(r)
				continue
			}
			flush()
			label := []rune(target.Label)
			offset := offsets[r]
			at := offset.runes
			if at >= len(label) {
				continue
			}
			if offset.source == nil {
				offset.source = &rendering.CopyText{Value: target.Label, Block: -int(r)}
			}
			offset.runes++
			from := offset.bytes
			offset.bytes += utf8.RuneLen(label[at])
			offsets[r] = offset
			fragment := span
			fragment.Text = string(label[at])
			fragment.Copy = rendering.CopyRange{Text: offset.source, Start: from, End: offset.bytes}
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
