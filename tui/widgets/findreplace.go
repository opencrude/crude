package widgets

import (
	"crude/tui/editor"
	"crude/tui/rendering"
	"crude/tui/ui"
)

// FindReplaceState holds the live UI state of the find/replace popup
// that floats above the active editor tab. The popup re-reads this
// each time it renders, so changes from the keyboard (typing a new
// query, toggling an option) show up immediately on the next frame.
//
// Editing is backed by ui.TextEdit (shared rune-safe engine). Paint
// uses WindowFrame + ui.TextField via BuildFindReplaceFrame with
// value/cursor sync from these editors.
type FindReplaceState struct {
	// ReplaceMode is true when the replace field is shown (Ctrl+H).
	// When false, only the find field is visible — keeps the popup
	// compact for users who never use replace.
	ReplaceMode bool

	// FocusedField is 0 for find, 1 for replace. Used to draw a
	// highlight around whichever field is currently active.
	FocusedField int

	// findEdit and replaceEdit are the underlying text engines.
	findEdit    *ui.TextEdit
	replaceEdit *ui.TextEdit

	// Toggles
	CaseSensitive bool
	WholeWord     bool
	Regex         bool

	// Match info
	MatchCount int
	CurrentIdx int // -1 if no matches

	// HoveredToggle is the index of the toggle pill currently under the
	// mouse cursor (-1 for none, 0=Aa, 1=\b, 2=.*). Drives the hover
	// visual in the rendered output. Updated by mouse motion.
	HoveredToggle int

	// OnChange fires when the user types in the find field, toggles
	// an option, or navigates. The caller (App) forwards these to
	// the active editor and triggers a re-render of the buffer.
	OnChange func()

	// OnNext / OnPrev fire on F3 / Shift+F3 (or Enter / Shift+Enter).
	OnNext func()
	OnPrev func()

	// OnReplace fires when the user presses Enter while focused on
	// the replace field, replacing the current match.
	OnReplace func()
	// OnReplaceAll fires on Ctrl+Alt+Enter from anywhere in the popup.
	OnReplaceAll func()
	// OnClose fires on Esc.
	OnClose func()
}

func (st *FindReplaceState) ensureFindEdit() *ui.TextEdit {
	if st == nil {
		return nil
	}
	if st.findEdit == nil {
		st.findEdit = ui.NewTextEdit()
	}
	return st.findEdit
}

func (st *FindReplaceState) ensureReplaceEdit() *ui.TextEdit {
	if st == nil {
		return nil
	}
	if st.replaceEdit == nil {
		st.replaceEdit = ui.NewTextEdit()
	}
	return st.replaceEdit
}

// Query returns the current find query.
func (st *FindReplaceState) Query() string {
	if st == nil {
		return ""
	}
	return st.ensureFindEdit().Value()
}

// Replace returns the current replace text.
func (st *FindReplaceState) Replace() string {
	if st == nil {
		return ""
	}
	return st.ensureReplaceEdit().Value()
}

// SetQuery replaces the find query and moves the cursor to the end.
// The OnChange callback (if set) is NOT called by this setter —
// callers that want to recompute matches should invoke OnChange
// themselves after the assignment.
func (st *FindReplaceState) SetQuery(q string) {
	if st == nil {
		return
	}
	st.ensureFindEdit().SetValue(q)
}

// SetReplace replaces the replace text.
func (st *FindReplaceState) SetReplace(r string) {
	if st == nil {
		return
	}
	st.ensureReplaceEdit().SetValue(r)
}

// SetOptions updates the case/wholeword/regex flags. Recomputing
// matches is the caller's responsibility.
func (st *FindReplaceState) SetOptions(caseSensitive, wholeWord, regex bool) {
	if st == nil {
		return
	}
	st.CaseSensitive = caseSensitive
	st.WholeWord = wholeWord
	st.Regex = regex
}

// FindEdit returns the find field's TextEdit. Used by the viewmodel's
// key dispatcher to delegate text-editing keys.
func (st *FindReplaceState) FindEdit() *ui.TextEdit {
	return st.ensureFindEdit()
}

// ReplaceEdit returns the replace field's TextEdit.
func (st *FindReplaceState) ReplaceEdit() *ui.TextEdit {
	return st.ensureReplaceEdit()
}

// ActiveEdit returns the currently focused field's TextEdit.
// Returns the find edit if no field is explicitly focused or if
// the replace field is hidden (ReplaceMode == false).
func (st *FindReplaceState) ActiveEdit() *ui.TextEdit {
	if st == nil {
		return nil
	}
	if st.FocusedField == 1 && st.ReplaceMode {
		return st.ensureReplaceEdit()
	}
	return st.ensureFindEdit()
}

// FindReplacePopupWidth returns the suggested total width for the
// floating window. The window manager uses this so the input fields
// don't truncate.
func FindReplacePopupWidth(st *FindReplaceState) int {
	// Inner width = max(query+label, toggles) + padding.
	// Toggles row: "Aa \b .* " ≈ 11 chars. Add a few for spacing.
	togglesW := 11 + 6 // "Aa \b .* " + status prefix
	qw := rendering.VisibleWidth(st.Query())
	w := qw + 12 // "Find: " + value + padding
	if w < togglesW {
		w = togglesW
	}
	if w < 38 {
		w = 38
	}
	// Add 6 for chrome (2 border + 4 padding).
	w += 6
	if w > 90 {
		w = 90
	}
	return w
}

// FindReplacePopupHeight returns the suggested height for the popup.
// Counts: 2 borders + N content lines.
func FindReplacePopupHeight(st *FindReplaceState) int {
	// Content rows: 1 (find) + 1 (replace, if any) + 1 (spacer) + 1 (toggles) + 1 (hint).
	contentH := 5
	if st.ReplaceMode {
		contentH = 6
	}
	return contentH + 2 // +2 for chrome
}

// FindReplaceFromEditor copies the current search state from the
// editor into the popup state. The caller should mutate the popup
// state and then call FindReplaceToEditor to push it back.
func FindReplaceFromEditor(st *FindReplaceState, e *editor.Editor) {
	if e == nil {
		return
	}
	ss := e.Search()
	if ss == nil {
		return
	}
	st.SetQuery(ss.Query)
	st.SetReplace(ss.Replace)
	st.CaseSensitive = ss.Options.CaseSensitive
	st.WholeWord = ss.Options.WholeWord
	st.Regex = ss.Options.Regex
	st.MatchCount = len(ss.Matches)
	st.CurrentIdx = ss.CurrentIdx
	if ss.CurrentIdx < 0 && len(ss.Matches) > 0 {
		st.CurrentIdx = 0
	}
}

// FindReplaceToEditor pushes popup state back to the editor. The
// caller (App) handles triggering a buffer re-render.
func FindReplaceToEditor(st *FindReplaceState, e *editor.Editor) {
	if e == nil {
		return
	}
	e.SetOptions(editor.SearchOptions{
		CaseSensitive: st.CaseSensitive,
		WholeWord:     st.WholeWord,
		Regex:         st.Regex,
	})
	e.SetReplace(st.Replace())
	e.SetQuery(st.Query())
	// Re-read after the editor computed matches.
	ss := e.Search()
	if ss != nil {
		st.MatchCount = len(ss.Matches)
		st.CurrentIdx = ss.CurrentIdx
	}
}

// VisibleWidth is a package-level re-export of rendering.VisibleWidth
// for callers that need a quick way to measure ANSI-stripped strings.
func VisibleWidth(s string) int { return rendering.VisibleWidth(s) }
