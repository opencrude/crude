package widgets

import (
	"fmt"

	"charm.land/lipgloss/v2"

	"crude/tui/editor"
	"crude/tui/rendering"
	"crude/tui/theme"
)

// FindReplaceState holds the live UI state of the find/replace popup
// that floats above the active editor tab. The popup re-reads this
// each time it renders, so changes from the keyboard (typing a new
// query, toggling an option) show up immediately on the next frame.
type FindReplaceState struct {
	// ReplaceMode is true when the replace field is shown (Ctrl+H).
	// When false, only the find field is visible — keeps the popup
	// compact for users who never use replace.
	ReplaceMode bool

	// FocusedField is 0 for find, 1 for replace. Used to draw a
	// highlight around whichever field is currently active.
	FocusedField int

	// Query and Replace mirror the editor's search state. We keep
	// separate copies here so the popup's text doesn't get clobbered
	// by editor-driven updates. The popup is the source of truth
	// while it's open.
	Query   string
	Replace string
	Cursor  int // byte offset within Query

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

	// OnNext / OnPrev fire on F3 / Shift+F3.
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
		Title:   "Find",
		TitleIcon: "⌕",
		Width:   FindReplacePopupWidth(st),
		Focused: true,
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
	lines = append(lines, padVisibleToWidth(
		PopupInputField(th, "Find", st.Query, "type to search", st.Cursor, st.FocusedField == 0),
		innerW))

	// Replace input (only if ReplaceMode).
	if st.ReplaceMode {
		lines = append(lines, padVisibleToWidth(
			PopupInputField(th, "Replace", st.Replace, "", len(st.Replace), st.FocusedField == 1),
			innerW))
	}

	// Spacer.
	lines = append(lines, PopupEmptyLine(th))

	// Status row (match count) + toggle row.
	var status string
	switch {
	case st.Query == "":
		status = mutedStyle(th).Render("type to search")
	case st.MatchCount == 0:
		status = lipgloss.NewStyle().Foreground(th.Colors.Warning).Background(th.Colors.PopupBg).Render("no results")
	default:
		status = fmt.Sprintf("%s %s%d / %d",
			mutedStyle(th).Render("matches"),
			fgStyle(th).Bold(true).Render(""),
			st.CurrentIdx+1, st.MatchCount)
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
	// Toggles row: " Aa  \b  .* " ≈ 11 chars. Add a few for spacing.
	togglesW := 11 + 6 // "Aa \b .* " + status prefix
	w := len(st.Query) + 12 // "Find: " + value + padding
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

// stripANSIForWidth is a helper used by callers that need the visible
// width of a styled line. Re-exported here to avoid an import cycle.
func stripANSIForWidth(s string) int {
	inEscape := false
	n := 0
	for _, r := range s {
		if r == 0x1b {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		n++
	}
	return n
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
	st.Query = ss.Query
	st.Replace = ss.Replace
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
	e.SetReplace(st.Replace)
	e.SetQuery(st.Query)
	// Re-read after the editor computed matches.
	ss := e.Search()
	if ss != nil {
		st.MatchCount = len(ss.Matches)
		st.CurrentIdx = ss.CurrentIdx
	}
}

// visibleWidthOf is a thin wrapper around rendering.VisibleWidth that
// also handles the case where the input contains ANSI codes.
func visibleWidthOf(s string) int {
	return rendering.VisibleWidth(s)
}

// VisibleWidth is a package-level re-export of rendering.VisibleWidth
// for callers that need a quick way to measure ANSI-stripped strings.
func VisibleWidth(s string) int { return rendering.VisibleWidth(s) }
