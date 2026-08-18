package ui

import (
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/theme"
)

// SemanticColor is a design-system color role. Feature code may only request
// these roles — never raw hex — when painting through ui controls.
type SemanticColor uint8

const (
	ColorFg SemanticColor = iota
	ColorBg
	ColorMuted
	ColorAccent
	ColorAccent2
	ColorSuccess
	ColorError
	ColorWarning
	ColorBorder
	ColorSelection
	ColorHover
	ColorSubPaneBg
	ColorPopupBg
	ColorPopupBorder
	ColorPopupBorderFocused
	ColorPopupTitle
	ColorPopupTitleFocused
	ColorPopupDivider
	ColorPopupSubtleBg
	ColorPopupButtonBg
	ColorPopupButtonFg
	ColorIncompleteBg
	// Diff colour roles mapped from theme.Colors.Diff so chat tool-body rows
	// resolve through TokenSet and follow theme preset switches.
	ColorDiffFile
	ColorDiffHunk
	ColorDiffContext
	ColorDiffAddition
	ColorDiffDeletion
)

// ControlState is the interactive state matrix used for token resolution.
type ControlState uint8

const (
	StateDefault ControlState = iota
	StateHovered
	StateFocused
	StatePressed
	StateSelected
	StateDisabled
	StateInvalid
)

// ResolveControlState applies precedence: disabled > focused > selected > pressed > hovered > default.
func ResolveControlState(disabled, focused, selected, pressed, hovered bool) ControlState {
	switch {
	case disabled:
		return StateDisabled
	case focused:
		return StateFocused
	case selected:
		return StateSelected
	case pressed:
		return StatePressed
	case hovered:
		return StateHovered
	default:
		return StateDefault
	}
}

// TokenSet is a versioned, resolved palette of cell colors derived from a Theme.
// Call Rebuild when the theme pointer changes or its contents are swapped.
// ColorVersion bumps on every rebuild; MetricVersion bumps only when layout
// metrics/spacing change so hosts can paint-only for color switches.
type TokenSet struct {
	Version       uint64
	ColorVersion  uint64
	MetricVersion uint64
	theme         *theme.Theme
	colors        map[SemanticColor]cell.Color
	lastMetrics   theme.Metrics
	lastSpacing   theme.Spacing
}

// NewTokenSet resolves colors from th.
func NewTokenSet(th *theme.Theme) *TokenSet {
	ts := &TokenSet{theme: th, colors: make(map[SemanticColor]cell.Color)}
	ts.Rebuild(th)
	return ts
}

// Rebuild re-resolves all semantic colors and bumps Version.
func (ts *TokenSet) Rebuild(th *theme.Theme) {
	ts.theme = th
	ts.Version++
	ts.ColorVersion++
	if th == nil {
		ts.colors = map[SemanticColor]cell.Color{}
		ts.lastMetrics = theme.Metrics{}
		ts.lastSpacing = theme.Spacing{}
		ts.MetricVersion++
		return
	}
	metricsChanged := th.Metrics != ts.lastMetrics || th.Spacing != ts.lastSpacing
	ts.lastMetrics = th.Metrics
	ts.lastSpacing = th.Spacing
	if metricsChanged || ts.MetricVersion == 0 {
		ts.MetricVersion++
	}
	c := th.Colors
	ts.colors = map[SemanticColor]cell.Color{
		ColorFg:                 rendering.ThemeColorToCell(c.Fg),
		ColorBg:                 rendering.ThemeColorToCell(c.Bg),
		ColorMuted:              rendering.ThemeColorToCell(c.Muted),
		ColorAccent:             rendering.ThemeColorToCell(c.Accent),
		ColorAccent2:            rendering.ThemeColorToCell(c.Accent2),
		ColorSuccess:            rendering.ThemeColorToCell(c.Success),
		ColorError:              rendering.ThemeColorToCell(c.Error),
		ColorWarning:            rendering.ThemeColorToCell(c.Warning),
		ColorBorder:             rendering.ThemeColorToCell(c.Border),
		ColorSelection:          rendering.ThemeColorToCell(c.Selection),
		ColorHover:              rendering.ThemeColorToCell(c.Hover),
		ColorSubPaneBg:          rendering.ThemeColorToCell(c.SubPaneBg),
		ColorPopupBg:            rendering.ThemeColorToCell(c.PopupBg),
		ColorPopupBorder:        rendering.ThemeColorToCell(c.PopupBorder),
		ColorPopupBorderFocused: rendering.ThemeColorToCell(c.PopupBorderFocused),
		ColorPopupTitle:         rendering.ThemeColorToCell(c.PopupTitle),
		ColorPopupTitleFocused:  rendering.ThemeColorToCell(c.PopupTitleFocused),
		ColorPopupDivider:       rendering.ThemeColorToCell(c.PopupDivider),
		ColorPopupSubtleBg:      rendering.ThemeColorToCell(c.PopupSubtleBg),
		ColorPopupButtonBg:      rendering.ThemeColorToCell(c.PopupButtonBg),
		ColorPopupButtonFg:      rendering.ThemeColorToCell(c.PopupButtonFg),
		ColorIncompleteBg:       rendering.ThemeColorToCell(c.IncompleteBg),
		ColorDiffFile:           rendering.ThemeColorToCell(c.Diff.File),
		ColorDiffHunk:           rendering.ThemeColorToCell(c.Diff.Hunk),
		ColorDiffContext:        rendering.ThemeColorToCell(c.Diff.Context),
		ColorDiffAddition:       rendering.ThemeColorToCell(c.Diff.Add),
		ColorDiffDeletion:       rendering.ThemeColorToCell(c.Diff.Delete),
	}
}

// Color returns a resolved cell color for the semantic role.
func (ts *TokenSet) Color(role SemanticColor) cell.Color {
	if ts == nil {
		return cell.NoColor
	}
	if c, ok := ts.colors[role]; ok {
		return c
	}
	return cell.NoColor
}

// Theme returns the underlying theme pointer.
func (ts *TokenSet) Theme() *theme.Theme {
	if ts == nil {
		return nil
	}
	return ts.theme
}

// SearchGlyph returns the themed monochrome magnifying-glass glyph with a
// trailing space ("⌕ ") for prefix placement. Prefer SearchGlyphEnd for
// search/filter fields so the glyph sits at the end of the input.
func SearchGlyph(ts *TokenSet) string {
	return DefaultGlyphsFromTokens(ts).Search + " "
}

// SearchGlyphEnd returns a leading-space magnifying glass for trailing
// placement (" ⌕"). The glyph is ordinary Unicode (U+2315), never emoji.
func SearchGlyphEnd(ts *TokenSet) string {
	return " " + strings.TrimSpace(DefaultGlyphsFromTokens(ts).Search)
}

// Spacing helpers from the active theme.
func (ts *TokenSet) PadLR() int {
	if ts == nil || ts.theme == nil {
		return 2
	}
	return ts.theme.Spacing.PadLR
}

func (ts *TokenSet) ButtonGap() int {
	if ts == nil || ts.theme == nil {
		return 2
	}
	return ts.theme.Spacing.ButtonGap
}

func (ts *TokenSet) FieldGap() int {
	if ts == nil || ts.theme == nil {
		return 1
	}
	return ts.theme.Spacing.FieldGap
}

func (ts *TokenSet) DialogPad() int {
	if ts == nil || ts.theme == nil {
		return 2
	}
	return ts.theme.Spacing.DialogPad
}

// FormIndent is the one-column inset of subsection labels under a section
// title (settings rail items under "AI"/"IDE", form fields under a separator).
func (ts *TokenSet) FormIndent() int {
	if ts == nil || ts.theme == nil {
		return 1
	}
	return ts.theme.Spacing.FormIndent
}

// FormTitlePad is the inherited top inset inside a form/settings body.
func (ts *TokenSet) FormTitlePad() int {
	if ts == nil || ts.theme == nil {
		return 1
	}
	return ts.theme.Spacing.FormTitlePad
}

// Glyphs returns the active glyph token table.
func (ts *TokenSet) Glyphs() theme.Glyphs {
	if ts == nil || ts.theme == nil {
		return theme.DefaultGlyphs()
	}
	return ts.theme.Glyphs
}

// Typography returns the active typography token table.
func (ts *TokenSet) Typography() theme.Typography {
	if ts == nil || ts.theme == nil {
		return theme.DefaultTypography()
	}
	return ts.theme.Type
}

// Metrics returns the active layout metric token table.
func (ts *TokenSet) Metrics() theme.Metrics {
	if ts == nil || ts.theme == nil {
		return theme.DefaultMetrics()
	}
	return ts.theme.Metrics
}

// MetricsChangedSince reports whether layout metrics changed since version.
func (ts *TokenSet) MetricsChangedSince(version uint64) bool {
	if ts == nil {
		return false
	}
	return ts.MetricVersion != version
}

// ButtonColors returns fg/bg for a button in the given interactive state.
func (ts *TokenSet) ButtonColors(st ControlState) (fg, bg cell.Color) {
	fg = ts.Color(ColorPopupButtonFg)
	bg = ts.Color(ColorPopupButtonBg)
	switch st {
	case StateDisabled:
		fg = ts.Color(ColorMuted)
		bg = ts.Color(ColorSubPaneBg)
	case StateFocused, StatePressed:
		fg = ts.Color(ColorBg)
		bg = ts.Color(ColorAccent)
	case StateHovered:
		bg = ts.Color(ColorHover)
		fg = ts.Color(ColorFg)
	case StateSelected:
		bg = ts.Color(ColorSelection)
		fg = ts.Color(ColorFg)
	}
	return fg, bg
}

// InputColors returns fg/bg/border for a text field in the given state.
func (ts *TokenSet) InputColors(st ControlState) (fg, bg, border cell.Color) {
	fg = ts.Color(ColorFg)
	bg = ts.Color(ColorPopupBg)
	border = ts.Color(ColorPopupBorder)
	switch st {
	case StateDisabled:
		fg = ts.Color(ColorMuted)
		border = ts.Color(ColorBorder)
	case StateFocused:
		border = ts.Color(ColorPopupBorderFocused)
	case StateInvalid:
		border = ts.Color(ColorError)
	case StateHovered:
		bg = ts.Color(ColorPopupSubtleBg)
	}
	return fg, bg, border
}

// ListRowColors returns fg/bg for a list row.
// Precedence is enforced by the caller via ResolveControlState; colors here
// must keep focused (keyboard) visually stronger and distinct from selected
// (current item in an unfocused region) and hovered.
func (ts *TokenSet) ListRowColors(st ControlState) (fg, bg cell.Color) {
	fg = ts.Color(ColorFg)
	// Lists are opaque controls. Leaving the default background unset replaces
	// already-painted popup cells with inheriting cells, allowing content below
	// a floating window to bleed through behind each label.
	bg = ts.Color(ColorPopupBg)
	switch st {
	case StateDisabled:
		fg = ts.Color(ColorMuted)
	case StateFocused:
		// Keyboard focus: accent fill + inverse fg (same language as focused buttons).
		fg = ts.Color(ColorBg)
		bg = ts.Color(ColorAccent)
	case StateSelected:
		fg = ts.Color(ColorFg)
		bg = ts.Color(ColorSelection)
	case StateHovered:
		bg = ts.Color(ColorHover)
	}
	return fg, bg
}

// MenuInk is the shared text language for lists, menus, and compact docks:
// primary labels/values use ColorFg, meta/inherited/accelerators use
// ColorMuted, and focused/selected rows keep meta readable on the row fill.
func (ts *TokenSet) MenuInk(st ControlState, fg cell.Color) (primary, muted cell.Color) {
	if ts == nil {
		return fg, fg
	}
	primary = fg
	muted = ts.Color(ColorMuted)
	if st == StateFocused || st == StateSelected {
		muted = fg
	}
	if st == StateDisabled {
		primary = muted
	}
	return primary, muted
}
