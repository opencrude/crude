package viewmodel

import (
	"cmp"
	"reflect"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"crude/textsearch"
	"crude/tui/editor"
	"crude/types"
)

// rankCompletions is a display projection of one acknowledged result set. It
// keeps exact original rows; it never rebases or rewrites an insertion.
//
//   - A row whose insertion only repeats the typed text is dropped: accepting
//     it would change nothing.
//   - Repeated rows with the same label, detail and complete insertion appear
//     once. Different import edits or snippet fields remain distinct choices.
//   - Rows that start with the typed text rank before other fuzzy matches,
//     exact case first. The server's own order breaks ties, so its relevance
//     (sortText) still decides among equally good matches.
//   - typed is the identifier before the caret when typing asked for the
//     suggestions. A row whose edit does not cover that word (empty prefix)
//     must still match it: after "strings.To", "const" would only append
//     nonsense, so it is not offered.
func rankCompletions(rows []types.LSPCompletionEntry, typed string) []types.LSPCompletionEntry {
	type ranked struct {
		row         types.LSPCompletionEntry
		tier, score int
	}
	out := make([]ranked, 0, len(rows))
	seen := make(map[[3]string][]types.CompletionItem, len(rows))
	for _, row := range rows {
		_, score, matches := textsearch.Fuzzy(row.FilterText, row.Prefix, false)
		if !matches {
			continue
		}
		if row.Prefix == "" && typed != "" {
			if _, _, continues := textsearch.Fuzzy(row.FilterText, typed, false); !continues {
				continue
			}
		}
		insertion := completionInsertText(row.Insertion)
		if row.Prefix != "" && insertion == row.Prefix && row.Insertion.Snippet == nil && len(row.Insertion.AdditionalTextEdits) == 0 {
			continue
		}
		key := [3]string{row.Insertion.Label, row.Documentation.Detail, insertion}
		if slices.ContainsFunc(seen[key], func(prior types.CompletionItem) bool {
			return reflect.DeepEqual(prior, row.Insertion)
		}) {
			continue
		}
		seen[key] = append(seen[key], row.Insertion)
		tier := 2
		switch {
		case row.Prefix == "":
			tier = 0
		case strings.HasPrefix(row.FilterText, row.Prefix):
			tier = 0
		case strings.HasPrefix(strings.ToLower(row.FilterText), strings.ToLower(row.Prefix)):
			tier = 1
		}
		out = append(out, ranked{row: row, tier: tier, score: score})
	}
	slices.SortStableFunc(out, func(a, b ranked) int {
		return cmp.Or(cmp.Compare(a.tier, b.tier), cmp.Compare(a.row.SortText, b.row.SortText),
			cmp.Compare(b.score, a.score), cmp.Compare(len(a.row.Insertion.Label), len(b.row.Insertion.Label)))
	})
	result := make([]types.LSPCompletionEntry, len(out))
	for i, r := range out {
		result[i] = r.row
	}
	return result
}

// completionInsertText is the text the primary edit inserts, by the same
// rule as lspcompletion.PrimaryEdit.
func completionInsertText(item types.CompletionItem) string {
	switch {
	case item.TextEdit != nil:
		return item.TextEdit.NewText
	case item.InsertText == "" && !item.HasInsertText && item.Snippet == nil:
		return item.Label
	default:
		return item.InsertText
	}
}

// completionGhostSuffix returns the text that accepting row adds after the
// typed text on the caret line, for display as faded text in front of the
// caret. literal reports that accepting is exactly inserting that text (no
// snippet fields, no other edits such as an import), so the editor may insert
// it itself; otherwise the inline text is a preview and Tab accepts the row
// through the normal path.
func completionGhostSuffix(row types.LSPCompletionEntry, line, col int) (suffix string, literal, ok bool) {
	item := row.Insertion
	if row.Prefix == "" {
		return "", false, false
	}
	if edit := item.TextEdit; edit != nil && (edit.Line != line || edit.EndLine != line || edit.EndCol != col) {
		return "", false, false
	}
	text := completionInsertText(item)
	if !strings.HasPrefix(text, row.Prefix) || len(text) == len(row.Prefix) {
		return "", false, false
	}
	suffix = text[len(row.Prefix):]
	literal = item.Snippet == nil && len(item.AdditionalTextEdits) == 0
	return suffix, literal, types.ValidEditorPredictionInsertion(suffix)
}

// obviousCompletion is the best row that continues the typed text exactly
// (same case), when choosing it cannot be wrong for the others: it is the only
// such row, or the name of every other such row starts with its name (print
// before println, ToUpper before ToUpperSpecial). Names that differ (ToUpper,
// ToLower), or equal names with different original edits, are a real choice, so
// none is shown inline. Names are compared, not inserted text, because a server
// may add call syntax (ToUpper()).
func obviousCompletion(rows []types.LSPCompletionEntry) (types.LSPCompletionEntry, bool) {
	var best types.LSPCompletionEntry
	var names []string
	for _, row := range rows {
		if row.Prefix == "" || !strings.HasPrefix(completionInsertText(row.Insertion), row.Prefix) {
			continue
		}
		if len(names) == 0 {
			best = row
		}
		names = append(names, completionName(row))
	}
	if len(names) == 0 {
		return types.LSPCompletionEntry{}, false
	}
	top := completionName(best)
	for i, name := range names {
		if !strings.HasPrefix(name, top) || i > 0 && name == top {
			return types.LSPCompletionEntry{}, false
		}
	}
	return best, true
}

// completionName is the text a row is matched by.
func completionName(row types.LSPCompletionEntry) string {
	if row.FilterText != "" {
		return row.FilterText
	}
	return row.Insertion.Label
}

// offerCompletionGhost shows the obvious suggestion as faded text at the caret.
func (m *App) offerCompletionGhost(s *completionState, rows []types.LSPCompletionEntry) bool {
	ed := s.editor
	if !s.automatic || m.ws.LSPMgr().Settings().CompletionAcceptance() == "enter" {
		return false
	}
	row, obvious := obviousCompletion(rows)
	if !obvious {
		return false
	}
	suffix, literal, ok := completionGhostSuffix(row, s.line, s.col)
	if !ok {
		return false
	}
	start, end, exact := ed.SourceByteRange()
	if !exact || start != end || ed.SetGhostTextFor(completionGhostOwner, ed.Revision(), start, suffix) != nil {
		return false
	}
	s.inlineID, s.inlineLiteral = row.ID, literal
	return true
}

// keepCompletionWhileTyping keeps visible suggestions on screen while the
// user continues the same word. The list becomes a read-only view of the
// previous result until the next result replaces it; it is never accepted
// from. Inline text the user types through stays and still accepts with Tab:
// the editor checked that it continues exactly the typed text.
func (m *App) keepCompletionWhileTyping(ed *editor.Editor) bool {
	s := m.completion
	if s == nil || s.editor != ed || s.screen == nil && ed.GhostOwner() != completionGhostOwner {
		return false
	}
	raw := ed.LineText(ed.CursorLine())
	off := editor.VisualColToByteOffset(raw, ed.CursorCol(), editor.DefaultTabWidth)
	if !completionWordBefore(raw[:max(0, min(off, len(raw)))]) {
		return false
	}
	if !s.inlineLiteral {
		// A preview of a snippet or import row cannot be accepted as typed
		// text; the next result offers it again.
		dismissCompletionGhost(ed)
	}
	if !s.stale {
		s.stale = true
		if s.screen != nil && s.screen.SelectedID() != "" {
			s.screen.ClearSelection()
			m.markCompletionDirty(s)
		}
		m.retireCompletion(s)
	}
	m.cancelCompletionIntent()
	return true
}

// completionWordBefore: the caret continues an identifier, so the next
// result narrows the same list instead of starting a different one.
func completionWordBefore(text string) bool {
	return typedWordBefore(text) != ""
}

// typedWordBefore is the identifier that ends at the end of text.
func typedWordBefore(text string) string {
	start := len(text)
	for start > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:start])
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) {
			break
		}
		start -= size
	}
	return text[start:]
}

// editorTypedWord is the identifier before the caret of ed.
func editorTypedWord(ed *editor.Editor) string {
	raw := ed.LineText(ed.CursorLine())
	off := editor.VisualColToByteOffset(raw, ed.CursorCol(), editor.DefaultTabWidth)
	return typedWordBefore(raw[:max(0, min(off, len(raw)))])
}
