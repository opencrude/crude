package chatprojection

import (
	"strings"

	"crude/tui/cell"
	"crude/tui/ui"
)

// ChatCopyCodeActionID copies the exact source of one fenced code block. The
// chip carries the source text, never the painted rows: Glamour pads and
// highlights code, so a selection copy of the block would keep that padding.
const ChatCopyCodeActionID = "chat.copy-code"

// ChatCopyCodePayload carries the exact block text and its message identity.
type ChatCopyCodePayload struct {
	MessageID string
	Language  string
	Code      string
}

// codeBlockChip binds one inserted marker line to the block it follows.
type codeBlockChip struct {
	marker   rune
	language string
	code     string
}

// decorateCodeBlocks inserts one private marker line after every complete
// fenced code block. Markdown renders the marker as its own paragraph; the
// renderer replaces that paragraph with a Copy code chip. The source text is
// captured before rendering, so it never contains layout padding.
func decorateCodeBlocks(content string) (string, []codeBlockChip) {
	if !strings.Contains(content, "```") && !strings.Contains(content, "~~~") {
		return content, nil
	}
	lines := strings.Split(content, "\n")
	var chips []codeBlockChip
	used := make(map[rune]bool)
	marker := rune(0xe000)
	nextMarker := func() rune {
		for marker <= 0xf8ff && (used[marker] || strings.ContainsRune(content, marker)) {
			marker++
		}
		if marker > 0xf8ff {
			return 0
		}
		used[marker] = true
		return marker
	}
	inBlock := false
	fence := ""
	fenceLen := 0
	language := ""
	start := 0
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !inBlock {
			f, n, lang, ok := openFence(line)
			if !ok {
				continue
			}
			inBlock, fence, fenceLen, language, start = true, f, n, lang, i+1
			continue
		}
		if !closeFence(line, fence, fenceLen) {
			continue
		}
		code := strings.Join(lines[start:i], "\n")
		if m := nextMarker(); m != 0 {
			// The marker is outside the fence, so the block text stays
			// byte-exact. Keep it in its own paragraph; Markdown otherwise
			// joins it to prose that follows the closing fence.
			insert := []string{string(m)}
			if i+1 >= len(lines) || strings.TrimSpace(lines[i+1]) != "" {
				insert = append(insert, "")
			}
			lines = append(lines[:i+1], append(insert, lines[i+1:]...)...)
			chips = append(chips, codeBlockChip{marker: m, language: language, code: code})
			i += len(insert)
		}
		inBlock = false
	}
	return strings.Join(lines, "\n"), chips
}

// openFence recognizes ``` or ~~~ with an optional language tag. Only a fence
// at the start of a trimmed line opens a block.
func openFence(line string) (fence string, length int, language string, ok bool) {
	for _, candidate := range []string{"```", "~~~"} {
		if !strings.HasPrefix(line, candidate) {
			continue
		}
		run := candidate[0]
		n := 0
		for n < len(line) && line[n] == run {
			n++
		}
		if n < 3 {
			return "", 0, "", false
		}
		rest := strings.TrimSpace(line[n:])
		if strings.ContainsRune(rest, rune(run)) {
			return "", 0, "", false
		}
		if fields := strings.Fields(rest); len(fields) > 0 {
			language = fields[0]
		}
		return string(run), n, language, true
	}
	return "", 0, "", false
}

func closeFence(line, fence string, length int) bool {
	if !strings.HasPrefix(line, fence) {
		return false
	}
	n := 0
	for n < len(line) && string(line[n]) == fence[:1] {
		n++
	}
	return n >= length && strings.TrimSpace(line[n:]) == ""
}

// injectCodeChips replaces each marker row with a clickable copy chip. When a
// marker did not survive rendering (for example inside an unclosed paragraph),
// the block is left without a chip rather than misplacing one.
func injectCodeChips(rows []PaintLine, messageID string, chips []codeBlockChip) []PaintLine {
	if len(chips) == 0 {
		return rows
	}
	byMarker := make(map[rune]codeBlockChip, len(chips))
	for _, chip := range chips {
		byMarker[chip.marker] = chip
	}
	out := make([]PaintLine, 0, len(rows))
	for _, row := range rows {
		text := row.PlainText()
		var chip codeBlockChip
		found := false
		for marker, candidate := range byMarker {
			if strings.ContainsRune(text, marker) {
				chip, found = candidate, true
				break
			}
		}
		if !found {
			out = append(out, row)
			continue
		}
		label := "⧉ Copy code"
		if chip.language != "" {
			label += " · " + chip.language
		}
		out = append(out, PaintLine{Spans: []RowSpan{{
			Text:   label,
			Role:   ui.ColorAccent2,
			Attr:   cell.AttrBold,
			Action: &ui.Action{ID: ChatCopyCodeActionID, Payload: ChatCopyCodePayload{MessageID: messageID, Language: chip.language, Code: chip.code}},
		}}})
	}
	return out
}
