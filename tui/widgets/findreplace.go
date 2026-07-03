package widgets

import (
	"fmt"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	tea "charm.land/bubbletea/v2"

	"crude/tui/components"
	"crude/tui/editor"
	"crude/tui/rendering"
	"crude/tui/theme"
)

// FindReplaceState holds the live UI state of the find/replace popup
// that floats above the active editor tab. The popup re-reads this
// each time it renders, so changes from the keyboard (typing a new
// query, toggling an option) show up immediately on the next frame.
//
// As of the unification refactor, the underlying text input is a
// `components.TextInput` (not a hand-rolled string+cursor state). This
// gives the popup real selection, copy/paste, word motion, Home/End,
// Ctrl+Left/Right, and a rune-safe cursor — all features the old
// hand-rolled state either didn't have or had broken (the byte-offset
// cursor used to panic on multi-byte input).
type FindReplaceState struct {
	// ReplaceMode is true when the replace field is shown (Ctrl+H).
	// When false, only the find field is visible — keeps the popup
	// compact for users who never use replace.
	ReplaceMode bool

	// FocusedField is 0 for find, 1 for replace. Used to draw a
	// highlight around whichever field is currently active.
	FocusedField int

	// findInput and replaceInput are the underlying text inputs.
	// We expose their value via the Query/Replace helpers below
	// so the rest of the codebase doesn't need to know about
	// the internal TextInput instances.
	findInput    components.TextInput
	replaceInput components.TextInput

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

	width int
}

// Query returns the current find query. Backed by the embedded
// TextInput so the byte/rune cursor math is consistent.
func (st *FindReplaceState) Query() string {
	if st == nil {
		return ""
	}
	return st.findInput.Value()
}

// Replace returns the current replace text. Backed by the embedded
// TextInput (the replace field also has a real cursor now, unlike
// the old hand-rolled state where the cursor was always pinned to
// the end).
func (st *FindReplaceState) Replace() string {
	if st == nil {
		return ""
	}
	return st.replaceInput.Value()
}

// SetQuery replaces the find query and moves the cursor to the end.
// The OnChange callback (if set) is NOT called by this setter —
// callers that want to recompute matches should invoke OnChange
// themselves after the assignment.
func (st *FindReplaceState) SetQuery(q string) {
	if st == nil {
		return
	}
	st.findInput.SetValue(q)
}

// SetReplace replaces the replace text.
func (st *FindReplaceState) SetReplace(r string) {
	if st == nil {
		return
	}
	st.replaceInput.SetValue(r)
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

// FindInput returns a pointer to the find field's TextInput. Used by
// the viewmodel's key dispatcher to delegate text-editing keys
// (Backspace, Delete, Left/Right, Home/End, characters, etc.) to
// the same mature component that powers the chat input.
func (st *FindReplaceState) FindInput() *components.TextInput {
	if st == nil {
		return nil
	}
	return &st.findInput
}

// ReplaceInput returns a pointer to the replace field's TextInput.
func (st *FindReplaceState) ReplaceInput() *components.TextInput {
	if st == nil {
		return nil
	}
	return &st.replaceInput
}

// ActiveInput returns the currently focused field's TextInput.
// Returns the find input if no field is explicitly focused or if
// the replace field is hidden (ReplaceMode == false).
func (st *FindReplaceState) ActiveInput() *components.TextInput {
	if st == nil {
		return nil
	}
	if st.FocusedField == 1 && st.ReplaceMode {
		return &st.replaceInput
	}
	return &st.findInput
}

// FindReplaceLines renders the find/replace popup into a slice of
// pre-styled ANSI lines including the popup's own border, title, and
// padding (unified design — see PopupChromeLines).
//
// Layout (inner content):
//
//	Find:   <value>                        <cursor>
//	Replace: <value>                       <when ReplaceMode>
//	                                        <spacer>
//	3 matches  [ Aa ] [ \b ] [ .* ]         <toggles>
//	enter next · ⇧enter prev · esc close   <hints>
func FindReplaceLines(th *theme.Theme, st *FindReplaceState) []string {
	content := findReplaceContent(th, st)
	cfg := PopupChromeConfig{
		Title:     "Find",
		TitleIcon: "⌕",
		Width:     FindReplacePopupWidth(st),
		Focused:   true,
	}
	return PopupChromeLines(th, cfg, content)
}

// findReplaceContent builds the inner content lines (no border, no
// padding). Each line is exactly `cfg.InnerWidth()` chars wide.
func findReplaceContent(th *theme.Theme, st *FindReplaceState) []string {
	cfg := PopupChromeConfig{Width: FindReplacePopupWidth(st)}
	innerW := cfg.InnerWidth()

	var lines []string

	// Find input (always shown).
	findSelStart, findSelEnd := st.findInput.SelectionRange()
	lines = append(lines, padVisibleToWidth(
		PopupInputFieldWithSelection(th, "Find", st.Query(), "type to search",
			st.findInput.CursorFlat(), st.findInput.HasActiveSelection(),
			findSelStart, findSelEnd,
			st.FocusedField == 0),
		innerW))

	// Replace input (only if ReplaceMode).
	if st.ReplaceMode {
		repSelStart, repSelEnd := st.replaceInput.SelectionRange()
		lines = append(lines, padVisibleToWidth(
			PopupInputFieldWithSelection(th, "Replace", st.Replace(), "",
				st.replaceInput.CursorFlat(), st.replaceInput.HasActiveSelection(),
				repSelStart, repSelEnd,
				st.FocusedField == 1),
			innerW))
	}

	// Spacer.
	lines = append(lines, PopupEmptyLine(th))

	// Status row (match count) + toggle row.
	var status string
	switch {
	case st.Query() == "":
		status = mutedStyle(th).Render("type to search")
	case st.MatchCount == 0:
		status = lipgloss.NewStyle().Foreground(th.Colors.Warning).Background(th.Colors.PopupBg).Render("no results")
	default:
		// Bold the current/total numbers so the user can see them
		// at a glance.
		cur := fgStyle(th).Bold(true).Render(fmt.Sprintf("%d", st.CurrentIdx+1))
		tot := fgStyle(th).Bold(true).Render(fmt.Sprintf("%d", st.MatchCount))
		status = mutedStyle(th).Render("matches ") + cur + mutedStyle(th).Render(" / ") + tot
	}
	toggleRow := PopupToggleRow(th, "", []PopupToggleSpec{
		{Glyph: "Aa", Active: st.CaseSensitive, Hovered: st.HoveredToggle == 0},
		{Glyph: "\\b", Active: st.WholeWord, Hovered: st.HoveredToggle == 1},
		{Glyph: ".*", Active: st.Regex, Hovered: st.HoveredToggle == 2},
	})
	lines = append(lines, padVisibleToWidth(status+"   "+toggleRow, innerW))

	// Hint row.
	hints := []HintSegment{
		{Key: "enter", Desc: "next"},
		{Key: "⇧enter", Desc: "prev"},
	}
	if st.ReplaceMode {
		hints = append(hints, HintSegment{Key: "ctrl+alt+enter", Desc: "replace all"})
	}
	hints = append(hints, HintSegment{Key: "esc", Desc: "close"})
	if st.ReplaceMode && st.FocusedField == 1 {
		hints = append(hints, HintSegment{Key: "enter", Desc: "replace"})
	}
	lines = append(lines, padVisibleToWidth(PopupHintLine(th, hints), innerW))

	return lines
}

// fgStyle returns the popup fg text style.
func fgStyle(th *theme.Theme) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(th.Colors.Fg).Background(th.Colors.PopupBg)
}

// mutedStyle returns the muted style used throughout the popup.
func mutedStyle(th *theme.Theme) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(th.Colors.Muted).Background(th.Colors.PopupBg)
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

// HandleKey routes a keypress to the focused input in the popup.
// The find/replace popup's keybinds are split into two layers:
//
//   1. The viewmodel's handleFindReplaceKey intercepts popup-level
//      keys (Esc closes, Tab switches field, Alt+C/W/R toggles,
//      Ctrl+Alt+Enter replaces all, Shift+Enter goes to prev match).
//   2. Everything else is forwarded to the focused input's
//      TextInput.Update, which handles all the text-editing keys:
//      character insertion, Backspace, Delete, Left/Right, Home/End,
//      Ctrl+Left/Right (word motion), Ctrl+A/E/U/K/W (line ops).
//
// This is the same pattern the chat input and the composer use, so
// all the popup's text fields now have the same feature set as the
// chat input: real selection, word motion, Home/End, no byte-cursor
// bugs.
//
// Returns the updated state, a tea.Cmd (if any), and a bool indicating
// whether the input was actually changed (the caller can use this
// to skip a redundant OnChange call).
func HandleKey(st *FindReplaceState, msg tea.KeyMsg) (*FindReplaceState, tea.Cmd, bool) {
	if st == nil {
		return st, nil, false
	}
	// Choose the input to route to.
	var input *components.TextInput
	if st.FocusedField == 1 && st.ReplaceMode {
		input = &st.replaceInput
	} else {
		input = &st.findInput
	}
	oldVal := input.Value()
	updated, cmd := input.Update(msg, false)
	if updated.Value() != oldVal {
		if st.FocusedField == 1 && st.ReplaceMode {
			st.replaceInput = updated
		} else {
			st.findInput = updated
		}
		return st, cmd, true
	}
	if st.FocusedField == 1 && st.ReplaceMode {
		st.replaceInput = updated
	} else {
		st.findInput = updated
	}
	return st, cmd, false
}

// runeCount returns the rune length of a string. Used internally
// for width calculations; replaced by utf8.RuneCountInString.
func runeCount(s string) int { return utf8.RuneCountInString(s) }

// visibleWidthOf is a thin wrapper around rendering.VisibleWidth.
func visibleWidthOf(s string) int {
	return rendering.VisibleWidth(s)
}

// VisibleWidth is a package-level re-export of rendering.VisibleWidth
// for callers that need a quick way to measure ANSI-stripped strings.
func VisibleWidth(s string) int { return rendering.VisibleWidth(s) }
