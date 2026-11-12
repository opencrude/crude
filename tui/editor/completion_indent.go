package editor

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"crude/lspcompletion"
	"crude/snippet"
	"crude/types"
)

// adjustCompletionInsertion derives the one primary insertion from the current
// cursor and file policy. The request's additional edits remain literal and in
// their original coordinates. Snippet syntax is reparsed only after verifying it
// still expands to the retained original text and exact initial placeholder plan.
func (e *Editor) adjustCompletionInsertion(original string, item types.CompletionItem) (string, *types.CompletionSnippet, error) {
	source := original
	if item.Snippet != nil {
		text, plan, err := snippet.Parse(item.SnippetSource)
		if err != nil || text != original || !reflect.DeepEqual(plan, item.Snippet) {
			return "", nil, fmt.Errorf("completion: snippet does not match its original text")
		}
		source = item.SnippetSource
	} else if item.SnippetSource != "" {
		return "", nil, fmt.Errorf("completion: unexpected snippet source")
	}
	if !strings.ContainsAny(source, "\r\n") {
		return original, item.Snippet, nil
	}
	raw, err := e.buf.lineView(e.cursor.Line)
	if err != nil {
		return "", nil, err
	}
	cursor := VisualColToByteOffset(raw, e.cursor.Col, e.TabWidth())
	end := 0
	for end < cursor && (raw[end] == ' ' || raw[end] == '\t') {
		end++
	}
	prefix := raw[:end]
	style := e.indentStyle()
	adjusted, err := reindentCompletion(source, prefix, e.lineEnding(), style)
	if err != nil {
		return "", nil, err
	}
	if item.Snippet == nil {
		return adjusted, nil, nil
	}
	starts := completionLeadingStops(original, item.Snippet, prefix, style)
	text, plan, err := snippet.ParseWithLeadingWhitespace(adjusted, func(id int, leading string) (string, error) {
		state, ok := starts[id]
		if !ok || state.blocked || !state.seen {
			return leading, nil
		}
		normalized := ""
		for i := 0; i <= len(state.otherColumns); i++ {
			start := state.firstColumn
			if i > 0 {
				start = state.otherColumns[i-1]
			}
			end := start
			for _, b := range leading {
				if b == '\t' {
					end += TabStopAdvance(end, style.tabSize)
				} else {
					end++
				}
			}
			count := completionPaddingSize(start, end, style)
			if count > lspcompletion.MaxStringBytes {
				return "", fmt.Errorf("completion: adjusted text exceeds its byte bound")
			}
			value := style.padding(start, end)
			if i > 0 && value != normalized {
				return leading, nil
			}
			normalized = value
		}
		return normalized, nil
	})
	if err != nil {
		return "", nil, fmt.Errorf("completion: could not adjust snippet: %w", err)
	}
	return text, plan, nil
}

// reindentCompletion normalizes the leading literal whitespace on each added
// line against the file's tab policy, after its existing prefix. It keeps the
// first line and all non-indentation text unchanged. For snippets it operates on
// their already-validated syntax before parsing, so placeholder boundaries,
// nested definitions, choices and linked occurrences are not guessed from text.
func reindentCompletion(source string, prefix []byte, eol string, style indentStyle) (string, error) {
	const limit = lspcompletion.MaxStringBytes
	if len(source) > limit || len(prefix) > limit {
		return "", fmt.Errorf("completion: adjusted text exceeds its byte bound")
	}
	if style.tabSize < 1 {
		style.tabSize = DefaultTabWidth
	}
	var out strings.Builder
	appendBounded := func(text string) bool {
		if len(text) > limit-out.Len() {
			return false
		}
		out.WriteString(text)
		return true
	}
	base := LineVisualWidth(prefix, style.tabSize)
	for at := 0; at < len(source); {
		n := strings.IndexAny(source[at:], "\r\n")
		if n < 0 {
			if !appendBounded(source[at:]) {
				return "", fmt.Errorf("completion: adjusted text exceeds its byte bound")
			}
			break
		}
		n += at
		if !appendBounded(source[at:n]) || !appendBounded(eol) || len(prefix) > limit-out.Len() {
			return "", fmt.Errorf("completion: adjusted text exceeds its byte bound")
		}
		out.Write(prefix)
		at = n + 1
		if source[n] == '\r' && at < len(source) && source[at] == '\n' {
			at++
		}
		col := base
		for at < len(source) && (source[at] == ' ' || source[at] == '\t') {
			if source[at] == '\t' {
				col += TabStopAdvance(col, style.tabSize)
			} else {
				col++
			}
			at++
		}
		// Count before building so repeated tabs or a huge existing prefix cannot
		// allocate beyond the accepted result bound.
		count := completionPaddingSize(base, col, style)
		if count > limit-out.Len() {
			return "", fmt.Errorf("completion: adjusted text exceeds its byte bound")
		}
		style.writePadding(&out, base, col)
	}
	return out.String(), nil
}

// A numeric stop has one shared value. Normalize its leading whitespace only
// if every occurrence is at a continuation line's indentation; otherwise an
// adjustment at just one copy would silently split its mirror from the others.
type completionStopIndent struct {
	firstColumn   int
	otherColumns  []int
	seen, blocked bool
}

func completionLeadingStops(text string, plan *types.CompletionSnippet, prefix []byte, style indentStyle) map[int]completionStopIndent {
	stops := make(map[int]completionStopIndent, len(plan.Occurrences))
	base := LineVisualWidth(prefix, style.tabSize)
	offset, col := 0, base
	continuation, leading := false, true
	for _, o := range plan.Occurrences {
		state := stops[o.Stop]
		start := o.StartByte
		if start < offset || start > len(text) {
			state.blocked = true
			stops[o.Stop] = state
			continue
		}
		// Parsed occurrences are in source preorder. Visit each byte at most
		// once, even when hundreds of stops share a long indentation prefix.
		for offset < start {
			b := text[offset]
			switch {
			case b == '\r' || b == '\n':
				continuation, leading, col = true, true, base
			case !leading:
			case b == ' ':
				col++
			case b == '\t':
				col += TabStopAdvance(col, style.tabSize)
			default:
				leading = false
			}
			offset++
		}
		if !continuation || !leading {
			state.blocked = true
		} else if !state.seen {
			state.firstColumn, state.seen = col, true
		} else if col != state.firstColumn && !slices.Contains(state.otherColumns, col) {
			state.otherColumns = append(state.otherColumns, col)
		}
		stops[o.Stop] = state
	}
	return stops
}
func completionPaddingSize(start, end int, style indentStyle) int {
	size := end - start
	tabSize := max(1, style.tabSize)
	if first := TabStopAdvance(start, tabSize); style.tab && size >= first {
		remaining := size - first
		size = 1 + remaining/tabSize + remaining%tabSize
	}
	return size
}
