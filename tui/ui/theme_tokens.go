package ui

import (
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
type TokenSet struct {
	Version uint64
	theme   *theme.Theme
	colors  map[SemanticColor]cell.Color
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
	if th == nil {
		ts.colors = map[SemanticColor]cell.Color{}
		return
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
func (ts *TokenSet) ListRowColors(st ControlState) (fg, bg cell.Color) {
	fg = ts.Color(ColorFg)
	bg = cell.NoColor
	switch st {
	case StateDisabled:
		fg = ts.Color(ColorMuted)
	case StateFocused, StateSelected:
		fg = ts.Color(ColorFg)
		bg = ts.Color(ColorSelection)
	case StateHovered:
		bg = ts.Color(ColorHover)
	}
	return fg, bg
}
