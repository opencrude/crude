package widgets

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"crude/tui/rendering"
	"crude/tui/theme"
)

// =============================================================================
// Unified popup design system
// =============================================================================
//
// Every popup in the TUI (context menu, find/replace, save dialog, text
// prompt, file picker, diagnostic hover) goes through this design system.
// The chrome (rounded border + integrated title bar) is drawn by the
// widget itself rather than relying on FloatingWindow's generic title-bar
// decoration. This gives a single coherent look across the whole app.
//
// Layout reference:
//
//   ╭─ Title ────────────────────────────────╮
//   │  body content ...                       │
//   │  body content ...                       │
//   │  ─────────────────────────────────────  │
//   │  hint text                              │
//   ╰─────────────────────────────────────────╯
//
// Visual rules:
//   - Border: rounded (╭ ╮ ╰ ╯), 1 cell wide
//   - Padding: 2 cells horizontal, 0 vertical
//   - Title: integrated into top border, bold
//   - Background: theme.Colors.PopupBg (== Code.Bg)
//   - Focused: border becomes PopupBorderFocused, title PopupTitleFocused
//   - No close X button on most popups (Esc to close)
//   - Components: PopupInputField, PopupToggle, PopupButton, PopupDivider,
//     PopupHintLine, PopupSectionLabel, PopupEmptyLine
//
// All components produce content lines that are exactly `InnerWidth` chars
// wide. The chrome wraps them. If your component output is narrower, pad
// it with trailing spaces (use padVisibleToWidth) — the highlight/background
// needs to span the full row.

// PopupChromeConfig controls the look of a single popup.
type PopupChromeConfig struct {
	// Title appears in the top border. Empty string suppresses it
	// (the top border becomes pure dashes).
	Title string

	// Width is the TOTAL popup width, INCLUDING the border and the
	// internal padding. Must be >= 7 (1 corner + 1 dash + 1 space +
	// 1 char + 1 space + 1 corner).
	Width int

	// Focused controls whether the popup uses the focused colors
	// (PopupBorderFocused + PopupTitleFocused). Unfocused popups use
	// the dim colors.
	Focused bool

	// TitleIcon is an optional single-character icon shown before the
	// title text in the top border (e.g. "i" for info, "?" for help).
	// Rendered in Muted color so it doesn't dominate the title.
	TitleIcon string

	// TitleAccent overrides the title color when non-nil. Useful
	// for severity-colored titles in the diagnostic popup. When set,
	// the Focused color is ignored for the title (border still follows
	// Focused unless BorderAccent is also set).
	TitleAccent color.Color

	// BorderAccent overrides the border color (both focused and
	// unfocused) when non-nil. Used by the diagnostic popup to tint
	// the side borders with the severity color.
	BorderAccent color.Color
}

// InnerWidth returns the inner content area width: Width - 2 (border)
// - 4 (2 cells pad on each side).
func (c PopupChromeConfig) InnerWidth() int {
	return c.Width - 6
}

// PopupChromeLines wraps content lines with the popup border, title,
// and padding. Content lines are padded to exactly `cfg.InnerWidth()`
// visible cells (use padVisibleToWidth or padRightASCII on each line
// before passing in, or pass plain lines — the wrapper pads them).
//
// Returns a slice of length `len(content) + 2`:
//   - line 0: top border (╭─ Title ─...─╮)
//   - lines 1..len(content): padded content rows
//   - last line: bottom border (╰─...─╯)
func PopupChromeLines(th *theme.Theme, cfg PopupChromeConfig, content []string) []string {
	if cfg.Width < 7 {
		cfg.Width = 7
	}
	innerW := cfg.InnerWidth()
	if innerW < 1 {
		innerW = 1
	}

	borderColor := th.Colors.PopupBorder
	titleColor := th.Colors.PopupTitle
	if cfg.Focused {
		borderColor = th.Colors.PopupBorderFocused
		titleColor = th.Colors.PopupTitleFocused
	}
	if cfg.TitleAccent != nil {
		titleColor = cfg.TitleAccent
	}
	if cfg.BorderAccent != nil {
		borderColor = cfg.BorderAccent
	}
	bgColor := th.Colors.PopupBg

	borderStyle := lipgloss.NewStyle().Foreground(borderColor).Background(bgColor)
	titleStyle := lipgloss.NewStyle().Foreground(titleColor).Background(bgColor).Bold(true)
	sideStyle := borderStyle
	bodyStyle := lipgloss.NewStyle().Background(bgColor)

	top := buildTopBorder(th, cfg, borderStyle, titleStyle, bodyStyle)
	bottom := sideStyle.Render("╰") + bodyStyle.Render(strings.Repeat("─", cfg.Width-2)) + sideStyle.Render("╯")

	out := make([]string, 0, len(content)+2)
	out = append(out, top)
	for _, line := range content {
		padded := padVisibleToWidth(line, innerW)
		out = append(out, sideStyle.Render("│")+bodyStyle.Render("  ")+padded+bodyStyle.Render("  ")+sideStyle.Render("│"))
	}
	out = append(out, bottom)
	return out
}

// buildTopBorder assembles the top border with the title embedded.
//
// Cases:
//   Title+icon: ╭─ i Title ─...─╮   k = Width - 6 - iconW - titleW
//   Icon only:  ╭─ i ─...─╮        k = Width - 6 - iconW
//   Title only: ╭─ Title ─...─╮     k = Width - 6 - titleW
//   No title:   ╭────...───╮        k = Width - 3
//
// k = trailing dashes. All are 1 corner + initial run + k dashes +
// 1 corner = Width.
//
// If content is too long, the title is truncated and k is set to 0.
func buildTopBorder(th *theme.Theme, cfg PopupChromeConfig, borderStyle, titleStyle, bgStyle lipgloss.Style) string {
	iconStyle := lipgloss.NewStyle().Foreground(th.Colors.Muted).Background(th.Colors.PopupBg)

	titleText := cfg.Title
	iconText := cfg.TitleIcon

	iconW := rendering.VisibleWidth(iconText)
	titleW := rendering.VisibleWidth(titleText)
	hasIcon := iconText != ""
	hasTitle := titleText != ""

	// Compute the width of the initial segment (between the corner
	// and the trailing dashes). This includes the starting dash(es),
	// the icon, the title, and the spaces between them.
	//
	// Layout when hasIcon||hasTitle:
	//   "─ " + icon + " " + title + " "
	//   PreDash  + iconW+1 + titleW + post
	//   = preDashW + iconW + 1 + titleW + postDashW
	//
	// Layout when no icon and no title:
	//   "─" only (single dash, no spaces)
	//   = 1
	preDashW := 1 // minimum: one dash
	iconSpace := 0
	postDashW := 0
	if hasIcon || hasTitle {
		preDashW = 2 // "─ " (space after initial dash)
	}
	if hasIcon {
		iconSpace = 1 // space after icon
	}
	if hasTitle {
		postDashW = 1 // space after title, before trailing dashes
	}
	initW := preDashW + iconW + iconSpace + titleW + postDashW

	trailingDashes := cfg.Width - 2 - initW // subtract ╭ and ╮

	if trailingDashes < 0 && hasTitle {
		// Title too long: truncate it.
		maxTitleArea := cfg.Width - 2 - (preDashW + iconW + iconSpace + postDashW)
		if maxTitleArea < 1 {
			maxTitleArea = 1
		}
		titleText = rendering.TruncateLine(cfg.Title, maxTitleArea)
		titleW = rendering.VisibleWidth(titleText)
		initW = preDashW + iconW + iconSpace + titleW + postDashW
		trailingDashes = cfg.Width - 2 - initW
		if trailingDashes < 0 {
			trailingDashes = 0
		}
	} else if trailingDashes < 0 {
		trailingDashes = 0
	}

	var b strings.Builder
	b.WriteString(borderStyle.Render("╭"))
	if hasIcon || hasTitle {
		b.WriteString(borderStyle.Render("─ "))
	} else {
		b.WriteString(borderStyle.Render("─"))
	}
	if hasIcon {
		b.WriteString(iconStyle.Render(iconText + " "))
	}
	if hasTitle {
		b.WriteString(titleStyle.Render(titleText))
		b.WriteString(bgStyle.Render(" "))
	}
	b.WriteString(bgStyle.Render(strings.Repeat("─", trailingDashes)))
	b.WriteString(borderStyle.Render("╮"))
	return b.String()
}

// padVisibleToWidth right-pads s to exactly `width` visible cells,
// returning the result (which may include ANSI codes — visible width
// is the true rendered width).
func padVisibleToWidth(s string, width int) string {
	w := rendering.VisibleWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

// PadRightANSI pads s to `width` visible cells using the given lipgloss
// style for the trailing spaces. The visible width is measured
// including any embedded ANSI codes; the trailing space fill is
// rendered in the given style (with the popup bg).
func PadRightANSI(s string, width int, th *theme.Theme, style lipgloss.Style) string {
	w := rendering.VisibleWidth(s)
	if w >= width {
		return style.Render(s)
	}
	return s + style.Render(strings.Repeat(" ", width-w))
}

// =============================================================================
// Components
// =============================================================================
//
// These produce INNER content lines — they do NOT add the border or
// padding. Use them inside PopupChromeLines, or any other widget that
// wants the unified component look.

// PopupInputField renders a single-line input as a single content line.
// Layout: "<label>: <value>" with the cursor at the given position.
// Empty value + cursor==0 shows a placeholder in italic muted.
//
// Focused controls whether the label is PopupTitleFocused (focused) or
// Muted (unfocused), and whether the cursor is visible.
func PopupInputField(th *theme.Theme, label, value, placeholder string, cursor int, focused bool) string {
	return PopupInputFieldWithSelection(th, label, value, placeholder, cursor, false, 0, 0, focused)
}

// PopupInputFieldWithSelection is the selection-aware variant of
// PopupInputField. It accepts:
//   - selection: whether the field has an active selection
//   - selStart, selEnd: the half-open selection range in the value
//     (selStart < selEnd). The selection is rendered with the
//     PopupSubtleBg highlight to match the chat input's selection
//     visual.
//
// The cursor and selection are RUNE positions, matching the
// TextInput.CursorFlat() and TextInput.SelectionRange() APIs.
func PopupInputFieldWithSelection(th *theme.Theme, label, value, placeholder string, cursor int, selection bool, selStart, selEnd int, focused bool) string {
	labelStyle := lipgloss.NewStyle().Foreground(th.Colors.Muted).Background(th.Colors.PopupBg)
	if focused {
		labelStyle = labelStyle.Foreground(th.Colors.PopupTitleFocused)
	}
	textStyle := lipgloss.NewStyle().Foreground(th.Colors.Fg).Background(th.Colors.PopupBg)
	placeholderStyle := lipgloss.NewStyle().
		Foreground(th.Colors.Muted).
		Background(th.Colors.PopupBg).
		Italic(true)
	selStyle := lipgloss.NewStyle().
		Foreground(th.Colors.Fg).
		Background(th.Colors.PopupSubtleBg).
		Bold(false)

	if cursor < 0 {
		cursor = 0
	}
	if cursor > runeLen(value) {
		cursor = runeLen(value)
	}

	labelPart := ""
	if label != "" {
		labelPart = labelStyle.Render(label + ":")
	}

	var valuePart string
	switch {
	case value == "" && placeholder != "":
		valuePart = placeholderStyle.Render(placeholder)
	case focused || selection:
		// Render with cursor + selection highlight.
		// Rune-safe: convert value to []rune for slicing.
		runes := []rune(value)
		if cursor > len(runes) {
			cursor = len(runes)
		}
		if selection && selStart >= 0 && selEnd <= len(runes) && selStart < selEnd {
			// Clamp cursor inside or outside the selection; the
			// rendering puts the cursor on whichever side it's on.
			pre := string(runes[:cursor])
			midStart := cursor
			midEnd := cursor
			post := ""
			if midStart > len(runes) {
				midStart = len(runes)
			}
			_ = midEnd
			post = string(runes[midStart:])
			// Pre-cursor chunk (from value start to cursor).
			// Mid chunk (cursor cell, single char or empty).
			// Post-cursor chunk (from cursor to value end).
			// Apply selection background to the part that overlaps
			// the selection range.
			preStart, preEnd := 0, min(cursor, len(runes))
			postStart, postEnd := cursor, len(runes)
			preSel := applySelBg(th, pre, preStart, preEnd, selStart, selEnd, textStyle, selStyle)
			postSel := applySelBg(th, post, postStart, postEnd, selStart, selEnd, textStyle, selStyle)
			caretRune := ""
			if cursor < len(runes) {
				caretRune = string(runes[cursor])
			} else {
				caretRune = "▏"
			}
			caretStyle := lipgloss.NewStyle().
				Foreground(th.Colors.PopupBg).
				Background(th.Colors.PopupTitleFocused)
			valuePart = preSel + caretStyle.Render(caretRune) + postSel
		} else {
			runes := []rune(value)
			if cursor > len(runes) {
				cursor = len(runes)
			}
			caret := lipgloss.NewStyle().
				Foreground(th.Colors.PopupBg).
				Background(th.Colors.PopupTitleFocused).
				Render("▏")
			valuePart = textStyle.Render(string(runes[:cursor])) + caret + textStyle.Render(string(runes[cursor:]))
		}
	default:
		valuePart = textStyle.Render(value)
	}

	if labelPart == "" {
		return valuePart
	}
	if valuePart == "" {
		return labelPart
	}
	return labelPart + " " + valuePart
}

// applySelBg splits a chunk of the value (between chunkStart and
// chunkEnd in rune positions) into "before selection" / "in selection"
// / "after selection" segments and applies the selection background
// to the in-selection portion.
func applySelBg(th *theme.Theme, chunk string, chunkStart, chunkEnd, selStart, selEnd int, base, sel lipgloss.Style) string {
	if chunk == "" {
		return ""
	}
	if selEnd <= chunkStart || selStart >= chunkEnd {
		// No overlap.
		return base.Render(chunk)
	}
	// Compute overlap on rune boundaries.
	runes := []rune(chunk)
	relSelStart := selStart - chunkStart
	relSelEnd := selEnd - chunkStart
	if relSelStart < 0 {
		relSelStart = 0
	}
	if relSelEnd > len(runes) {
		relSelEnd = len(runes)
	}
	before := string(runes[:relSelStart])
	in := string(runes[relSelStart:relSelEnd])
	after := string(runes[relSelEnd:])
	out := ""
	if before != "" {
		out += base.Render(before)
	}
	if in != "" {
		out += sel.Render(in)
	}
	if after != "" {
		out += base.Render(after)
	}
	return out
}

// runeLen returns the rune count of a string. Used for clamping
// cursor/selection positions to valid rune boundaries.
func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// PopupToggleSpec describes one toggle in PopupToggleRow.
type PopupToggleSpec struct {
	Glyph   string
	Active  bool
	Hovered bool // mouse hovers over this toggle
}

// PopupToggle renders a single toggle as a single styled string.
// Active: filled bg with Bg text — looks like a pill/button.
// Inactive: dim text, no bg — looks like a dimmed label.
// Hovered: SubtleBg highlight, regardless of Active state.
func PopupToggle(th *theme.Theme, glyph string, active, hovered bool) string {
	if active {
		style := lipgloss.NewStyle().
			Foreground(th.Colors.PopupButtonFg).
			Background(th.Colors.PopupButtonBg).
			Bold(true)
		return style.Render(" " + glyph + " ")
	}
	if hovered {
		style := lipgloss.NewStyle().
			Foreground(th.Colors.Fg).
			Background(th.Colors.PopupSubtleBg)
		return style.Render(" " + glyph + " ")
	}
	style := lipgloss.NewStyle().
		Foreground(th.Colors.Muted).
		Background(th.Colors.PopupBg)
	return style.Render(" " + glyph + " ")
}

// PopupToggleRow renders a row of toggles separated by single spaces,
// preceded by an optional label. Returns one content line.
func PopupToggleRow(th *theme.Theme, label string, toggles []PopupToggleSpec) string {
	mutedStyle := lipgloss.NewStyle().Foreground(th.Colors.Muted).Background(th.Colors.PopupBg)
	var parts []string
	if label != "" {
		parts = append(parts, mutedStyle.Render(label))
	}
	for _, t := range toggles {
		parts = append(parts, PopupToggle(th, t.Glyph, t.Active, t.Hovered))
	}
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteString(mutedStyle.Render(" "))
		}
		b.WriteString(p)
	}
	return b.String()
}

// PopupButtonSpec describes one button in PopupButtonRow.
type PopupButtonSpec struct {
	Label   string
	Focused bool
	Hovered bool // mouse hovers over this button
}

// PopupButton renders a single button. Focused buttons are filled
// (PopupButtonBg bg, PopupButtonFg text, bold). Hovered buttons get
// the SubtleBg highlight. Unfocused buttons are subtle (Muted text, no bg).
func PopupButton(th *theme.Theme, label string, focused, hovered bool) string {
	if focused {
		style := lipgloss.NewStyle().
			Foreground(th.Colors.PopupButtonFg).
			Background(th.Colors.PopupButtonBg).
			Bold(true)
		return style.Render(" " + label + " ")
	}
	if hovered {
		style := lipgloss.NewStyle().
			Foreground(th.Colors.Fg).
			Background(th.Colors.PopupSubtleBg).
			Bold(true)
		return style.Render(" " + label + " ")
	}
	style := lipgloss.NewStyle().
		Foreground(th.Colors.Muted).
		Background(th.Colors.PopupBg)
	return style.Render(" " + label + " ")
}

// PopupButtonRow renders a row of buttons joined by single spaces.
// At most one button should be focused or hovered.
func PopupButtonRow(th *theme.Theme, buttons []PopupButtonSpec) string {
	mutedStyle := lipgloss.NewStyle().Foreground(th.Colors.Muted).Background(th.Colors.PopupBg)
	var parts []string
	for _, b := range buttons {
		parts = append(parts, PopupButton(th, b.Label, b.Focused, b.Hovered))
	}
	var sb strings.Builder
	for i, p := range parts {
		if i > 0 {
			sb.WriteString(mutedStyle.Render(" "))
		}
		sb.WriteString(p)
	}
	return sb.String()
}

// PopupDivider renders a single divider char (the chrome wrapper
// stretches it to inner width via background fill). Use it as a
// full-width divider line.
func PopupDivider(th *theme.Theme) string {
	style := lipgloss.NewStyle().Foreground(th.Colors.PopupDivider).Background(th.Colors.PopupBg)
	return style.Render("─")
}

// PopupSectionLabel renders a small italic label that sits above a
// group of items. Returns one content line. Intended for context menus.
func PopupSectionLabel(th *theme.Theme, text string) string {
	style := lipgloss.NewStyle().
		Foreground(th.Colors.Muted).
		Background(th.Colors.PopupBg).
		Italic(true)
	return style.Render(" " + text)
}

// HintSegment describes one part of a hint line.
type HintSegment struct {
	Key  string
	Desc string
}

// PopupHintLine renders a row of hint segments. Each segment is
// "<key> <description>". Keys are bolded, descriptions are muted.
// Segments are joined by " · " (middle dot, the standard separator).
func PopupHintLine(th *theme.Theme, segments []HintSegment) string {
	keyStyle := lipgloss.NewStyle().Foreground(th.Colors.Fg).Background(th.Colors.PopupBg).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(th.Colors.Muted).Background(th.Colors.PopupBg)

	var parts []string
	for _, s := range segments {
		parts = append(parts, keyStyle.Render(s.Key)+descStyle.Render(" "+s.Desc))
	}
	return strings.Join(parts, descStyle.Render(" · "))
}

// PopupEmptyLine returns a single blank line with the popup background.
// Useful for vertical breathing room inside a popup.
func PopupEmptyLine(th *theme.Theme) string {
	style := lipgloss.NewStyle().Background(th.Colors.PopupBg)
	return style.Render(" ")
}

// =============================================================================
// Sizing helpers
// =============================================================================

// PopupContentWidth returns the recommended total popup width for
// the given inner content width. Adds 6 for the chrome (2 border +
// 4 padding). Use this to compute FloatingWindow dimensions from the
// inner width you want.
func PopupContentWidth(innerW int) int {
	return innerW + 6
}

// PopupContentHeight returns the total popup height for the given
// number of content rows. Adds 2 for top and bottom borders.
func PopupContentHeight(contentRows int) int {
	return contentRows + 2
}

// PopupFromLines is a convenience: wraps the given pre-built lines
// (assumed already padded to inner width) in the standard chrome.
// Width is auto-computed from the first line's visible width.
func PopupFromLines(th *theme.Theme, title string, focused bool, lines []string) []string {
	if len(lines) == 0 {
		return PopupChromeLines(th, PopupChromeConfig{Title: title, Width: 24, Focused: focused}, lines)
	}
	innerW := rendering.VisibleWidth(lines[0])
	cfg := PopupChromeConfig{Title: title, Width: innerW + 6, Focused: focused}
	return PopupChromeLines(th, cfg, lines)
}

// PopupChromeLinesReveal is like PopupChromeLines but renders only
// the first `visibleRows` content rows. Hidden rows are filled
// with empty bg lines so the popup maintains its full height
// during the animation. totalRows is the full content row count
// (used to size the bottom border's distance from the top).
func PopupChromeLinesReveal(th *theme.Theme, cfg PopupChromeConfig, visibleContent []string, totalRows int) []string {
	if cfg.Width < 7 {
		cfg.Width = 7
	}
	innerW := cfg.InnerWidth()
	if innerW < 1 {
		innerW = 1
	}

	borderColor := th.Colors.PopupBorder
	titleColor := th.Colors.PopupTitle
	if cfg.Focused {
		borderColor = th.Colors.PopupBorderFocused
		titleColor = th.Colors.PopupTitleFocused
	}
	if cfg.TitleAccent != nil {
		titleColor = cfg.TitleAccent
	}
	if cfg.BorderAccent != nil {
		borderColor = cfg.BorderAccent
	}
	bgColor := th.Colors.PopupBg

	borderStyle := lipgloss.NewStyle().Foreground(borderColor).Background(bgColor)
	titleStyle := lipgloss.NewStyle().Foreground(titleColor).Background(bgColor).Bold(true)
	sideStyle := borderStyle
	bodyStyle := lipgloss.NewStyle().Background(bgColor)

	top := buildTopBorder(th, cfg, borderStyle, titleStyle, bodyStyle)
	bottom := sideStyle.Render("╰") + bodyStyle.Render(strings.Repeat("─", cfg.Width-2)) + sideStyle.Render("╯")

	// Total lines: 1 (top) + totalRows (content) + 1 (bottom).
	out := make([]string, 0, totalRows+2)
	out = append(out, top)

	// Render visible content rows.
	for _, line := range visibleContent {
		padded := padVisibleToWidth(line, innerW)
		out = append(out, sideStyle.Render("│")+bodyStyle.Render("  ")+padded+bodyStyle.Render("  ")+sideStyle.Render("│"))
	}

	// Fill remaining rows (hidden by the animation) with empty
	// bg-only lines.
	hidden := totalRows - len(visibleContent)
	for i := 0; i < hidden; i++ {
		emptyPadded := bodyStyle.Render(strings.Repeat(" ", innerW))
		out = append(out, sideStyle.Render("│")+bodyStyle.Render("  ")+emptyPadded+bodyStyle.Render("  ")+sideStyle.Render("│"))
	}

	out = append(out, bottom)
	return out
}
