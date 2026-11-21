package workspace

import (
	"bytes"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"crude/agent/prompt"
	"crude/insight/predictioncontext"
	"crude/types"
)

// suggestionHunk is one change, in canonical byte offsets of the text that
// the request read.
type suggestionHunk struct {
	start, end int
	text       string
}

var errSuggestionOutput = errors.New("the model's answer was not a usable rewrite")

// parseSuggestionRewrite extracts the rewritten region. An answer without the
// end marker is incomplete (for example cut by the output limit) and is never
// read as a deletion of the rest of the region.
func parseSuggestionRewrite(raw, region string) (string, error) {
	end := strings.Index(raw, prompt.SuggestionRegionEnd)
	if end < 0 {
		return "", errSuggestionOutput
	}
	body := raw[:end]
	if start := strings.LastIndex(body, prompt.SuggestionRegionStart); start >= 0 {
		body = body[start+len(prompt.SuggestionRegionStart):]
	} else if fence := strings.LastIndex(body, "```"); fence >= 0 {
		body = body[fence+3:]
		if nl := strings.IndexByte(body, '\n'); nl >= 0 {
			body = body[nl:]
		}
	}
	body = strings.TrimPrefix(body, "\n")
	body = strings.ReplaceAll(body, prompt.SuggestionCursor, "")
	if strings.Contains(body, "<|") && prompt.ContainsSuggestionMarker(body) {
		return "", errSuggestionOutput
	}
	if strings.HasSuffix(region, "\n") && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	if !utf8.ValidString(body) || len(body) > 4*len(region)+4096 {
		return "", errSuggestionOutput
	}
	return body, nil
}

// suggestionHunks diffs region and rewrite by lines and refines each changed
// block to its minimal byte change. Hunks are in source order.
func suggestionHunks(c predictioncontext.Context, rewrite string) []suggestionHunk {
	if rewrite == c.Region {
		return nil
	}
	oldLines, newLines := splitKeepLines(c.Region), splitKeepLines(rewrite)
	if len(oldLines) > 200 || len(newLines) > 200 {
		return nil
	}
	// Longest common subsequence of lines; regions are a few lines long.
	lcs := make([][]int, len(oldLines)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(newLines)+1)
	}
	for i := len(oldLines) - 1; i >= 0; i-- {
		for j := len(newLines) - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	offsets := make([]int, len(oldLines)+1)
	for i, line := range oldLines {
		offsets[i+1] = offsets[i] + len(line)
	}
	var hunks []suggestionHunk
	i, j := 0, 0
	for i < len(oldLines) || j < len(newLines) {
		if i < len(oldLines) && j < len(newLines) && oldLines[i] == newLines[j] {
			i, j = i+1, j+1
			continue
		}
		a, b := i, j
		for i < len(oldLines) || j < len(newLines) {
			if i < len(oldLines) && j < len(newLines) && oldLines[i] == newLines[j] {
				break
			}
			if j < len(newLines) && (i == len(oldLines) || lcs[i][j+1] >= lcs[i+1][j]) {
				j++
			} else {
				i++
			}
		}
		// A block that changes each line in place is one change per line, so
		// a rename shows and applies use by use.
		if i-a == j-b && i-a > 1 {
			for k := 0; k < i-a; k++ {
				if oldLines[a+k] != newLines[b+k] {
					hunks = append(hunks, refineHunk(c, offsets[a+k], oldLines[a+k], newLines[b+k]))
				}
			}
			continue
		}
		oldText := c.Region[offsets[a]:offsets[i]]
		newText := strings.Join(newLines[b:j], "")
		hunks = append(hunks, refineHunk(c, offsets[a], oldText, newText))
	}
	return hunks
}

func splitKeepLines(text string) []string {
	lines := strings.SplitAfter(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// refineHunk trims the common prefix and suffix. When the change can be a
// pure insertion at the caret, it is anchored there, so typing through it and
// Tab behave as ghost text.
func refineHunk(c predictioncontext.Context, base int, oldText, newText string) suggestionHunk {
	trim := func(prefixCap, suffixCap int) (int, int) {
		p := 0
		for p < len(oldText) && p < len(newText) && p < prefixCap && oldText[p] == newText[p] {
			p++
		}
		for p > 0 && (p < len(oldText) && !utf8.RuneStart(oldText[p]) || p < len(newText) && !utf8.RuneStart(newText[p])) {
			p--
		}
		s := 0
		for s < len(oldText)-p && s < len(newText)-p && s < suffixCap && oldText[len(oldText)-1-s] == newText[len(newText)-1-s] {
			s++
		}
		for s > 0 && (!utf8.RuneStart(oldText[len(oldText)-s]) || !utf8.RuneStart(newText[len(newText)-s])) {
			s--
		}
		return p, s
	}
	p, s := trim(len(oldText), len(oldText))
	minimal := suggestionHunk{start: c.RegionStart + base + p, end: c.RegionStart + base + len(oldText) - s, text: newText[p : len(newText)-s]}
	if caret := c.Caret - base; caret >= 0 && caret <= len(oldText) {
		p, s = trim(caret, len(oldText)-caret)
		if p == caret && len(oldText)-s == caret {
			return suggestionHunk{start: c.RegionStart + c.Caret, end: c.RegionStart + c.Caret, text: newText[p : len(newText)-s]}
		}
	}
	return minimal
}

// classifySuggestion names how the shell shows one hunk.
func classifySuggestion(text []byte, caret int, h suggestionHunk) types.EditorPredictionKind {
	if h.start == caret && h.end == caret {
		return types.PredictionInsert
	}
	caretLine := bytes.Count(text[:caret], []byte{'\n'})
	first := bytes.Count(text[:h.start], []byte{'\n'})
	last := first + bytes.Count(text[h.start:h.end], []byte{'\n'})
	if first <= caretLine+1 && last >= caretLine-1 {
		return types.PredictionEdit
	}
	return types.PredictionJump
}

// checkSuggestion drops hunks that the user would not want to see: invalid
// text, a repeat of the code after the caret, or a reversal of the user's
// own latest edit in this file.
func checkSuggestion(text []byte, caret int, h suggestionHunk, own []types.RecentEdit) bool {
	if h.start < 0 || h.end < h.start || h.end > len(text) || !types.ValidEditorPredictionText(h.text) {
		return false
	}
	removed := string(text[h.start:h.end])
	if removed == h.text {
		return false
	}
	if h.start == h.end {
		// An insertion that repeats the text right after or right before it
		// (the model kept a line and also wrote it again) is not a change.
		trimmed := strings.TrimSpace(h.text)
		after := strings.TrimLeft(string(text[h.start:min(len(text), h.start+len(h.text)+256)]), " \t\n")
		before := strings.TrimRight(string(text[max(0, h.start-len(h.text)-256):h.start]), " \t\n")
		// Short text such as "}" may close a block right after another one.
		if trimmed == "" || strings.HasPrefix(after, trimmed) || len(trimmed) >= 8 && strings.HasSuffix(before, trimmed) {
			return false
		}
	}
	if len(own) > 0 {
		last := own[len(own)-1]
		if last.Inserted != "" && removed == last.Inserted && h.text == last.Removed {
			return false
		}
	}
	return true
}

// suggestionAgentWindow is how long lines that an agent or the disk changed
// stay out of suggestions: Errand does not suggest where an agent is editing.
const suggestionAgentWindow = 10 * time.Second

// agentBusy reports whether h touches lines of rel that another author
// changed within the window. Lines are 1-based in the text after each edit.
func agentBusy(text []byte, h suggestionHunk, rel string, edits []types.RecentEdit, now time.Time) bool {
	first := 1 + bytes.Count(text[:h.start], []byte{'\n'})
	last := first + bytes.Count(text[h.start:h.end], []byte{'\n'})
	for _, e := range edits {
		if e.Author == types.RecentEditYou || e.Path != rel || now.Sub(time.Unix(e.Unix, 0)) > suggestionAgentWindow {
			continue
		}
		end := e.Line + strings.Count(e.Inserted, "\n")
		if first <= end+1 && last >= e.Line-1 {
			return true
		}
	}
	return false
}

// shiftHunks moves later hunks across an applied one.
func shiftHunks(applied suggestionHunk, rest []suggestionHunk) []suggestionHunk {
	delta := len(applied.text) - (applied.end - applied.start)
	out := make([]suggestionHunk, 0, len(rest))
	for _, h := range rest {
		switch {
		case h.start >= applied.end:
			h.start, h.end = h.start+delta, h.end+delta
		case h.end <= applied.start:
		default:
			continue // overlapping hunks cannot be chained
		}
		out = append(out, h)
	}
	return out
}

// applyHunk returns text with one hunk applied.
func applyHunk(text []byte, h suggestionHunk) []byte {
	out := make([]byte, 0, len(text)+len(h.text)-(h.end-h.start))
	out = append(out, text[:h.start]...)
	out = append(out, h.text...)
	return append(out, text[h.end:]...)
}

// orderHunks puts the hunk at or after the caret first, then the later ones,
// then the earlier ones: the order in which Tab, Tab walks them.
func orderHunks(hunks []suggestionHunk, caret int) []suggestionHunk {
	var after, before []suggestionHunk
	for _, h := range hunks {
		if h.end >= caret {
			after = append(after, h)
		} else {
			before = append(before, h)
		}
	}
	return append(after, before...)
}
