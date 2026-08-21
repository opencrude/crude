package chatprojection

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/ui"
)

// ansiBridgeState holds the reverse colour->role map built from a TokenSet so
// bridged ANSI content can map known theme colours back to SemanticColor roles
// (which follow theme preset switches) while carrying explicit overrides for
// colours that have no role (Chroma syntax colours).
type ansiBridgeState struct {
	// fgRoles maps a resolved cell.Color to the SemanticColor it came from.
	fgRoles map[cell.Color]ui.SemanticColor
	// bgRoles maps a resolved cell.Color to the SemanticColor it came from.
	bgRoles map[cell.Color]ui.SemanticColor
}

func newAnsiBridgeState(tokens *ui.TokenSet) ansiBridgeState {
	st := ansiBridgeState{
		fgRoles: make(map[cell.Color]ui.SemanticColor, 32),
		bgRoles: make(map[cell.Color]ui.SemanticColor, 32),
	}
	if tokens == nil {
		return st
	}
	for _, role := range semanticFgRoles {
		c := tokens.Color(role)
		if !c.IsDefault() {
			st.fgRoles[c] = role
		}
	}
	for _, role := range semanticBgRoles {
		c := tokens.Color(role)
		if !c.IsDefault() {
			st.bgRoles[c] = role
		}
	}
	return st
}

var semanticFgRoles = []ui.SemanticColor{
	ui.ColorFg, ui.ColorMuted, ui.ColorAccent, ui.ColorAccent2,
	ui.ColorSuccess, ui.ColorError, ui.ColorWarning, ui.ColorBorder,
	ui.ColorPopupTitle, ui.ColorPopupTitleFocused,
	ui.ColorDiffFile, ui.ColorDiffHunk, ui.ColorDiffContext,
	ui.ColorDiffAddition, ui.ColorDiffDeletion,
}

var semanticBgRoles = []ui.SemanticColor{
	ui.ColorSubPaneBg, ui.ColorPopupBg, ui.ColorPopupSubtleBg,
	ui.ColorSelection, ui.ColorHover, ui.ColorIncompleteBg,
	ui.ColorPopupButtonBg,
}

// BridgeAnsiLine converts one ANSI-styled line into a PaintLine. SGR runs are
// parsed by rendering.ParseAnsiRuns (the canonical SGR parser — no duplicate
// parser here). Known theme colours are mapped back to their SemanticColor
// role so they follow theme switches; colours with no matching role (e.g.
// Chroma 24-bit syntax colours) are carried as explicit FGColor/BGColor
// overrides on the RowSpan. Empty input yields a blank native spacer line
// (NOT an ANSI-fallback line) so the blank-spacer-vs-fallback ambiguity stays
// resolved. Adjacent runs with identical style are coalesced to keep span
// counts small.
func BridgeAnsiLine(line string, tokens *ui.TokenSet) PaintLine {
	runs := rendering.ParseAnsiRuns(line)
	if len(runs) == 0 {
		return buildPlainBodyLine("")
	}
	st := newAnsiBridgeState(tokens)
	var spans []RowSpan
	for _, r := range runs {
		if r.Text == "" {
			continue
		}
		span := ansiRunToSpan(r, st)
		if len(spans) > 0 && coalesceInto(&spans[len(spans)-1], span) {
			continue
		}
		spans = append(spans, span)
	}
	if len(spans) == 0 {
		return buildPlainBodyLine("")
	}
	return PaintLine{Spans: spans}
}

func ansiRunToSpan(r rendering.AnsiRun, st ansiBridgeState) RowSpan {
	span := RowSpan{Text: r.Text, Attr: r.Attr}
	if !r.FG.IsDefault() {
		if role, ok := st.fgRoles[r.FG]; ok {
			span.Role = role
		} else {
			span.FGSet = true
			span.FGColor = r.FG
			span.Role = ui.ColorFg
		}
	} else {
		span.Role = ui.ColorFg
	}
	if !r.BG.IsDefault() {
		if role, ok := st.bgRoles[r.BG]; ok {
			span.BgRole = role
			span.BgSet = true
		} else {
			span.BGSet = true
			span.BGColor = r.BG
		}
	}
	return span
}

// coalesceInto appends span.Text into dst when dst and span have identical
// styling (same role, same override flags/colours, same attrs, same bg).
// Returns true if coalesced.
func coalesceInto(dst *RowSpan, span RowSpan) bool {
	if dst.Role != span.Role {
		return false
	}
	if dst.Attr != span.Attr {
		return false
	}
	if dst.FGSet != span.FGSet || (dst.FGSet && !dst.FGColor.Equal(span.FGColor)) {
		return false
	}
	if dst.BGSet != span.BGSet || (dst.BGSet && !dst.BGColor.Equal(span.BGColor)) {
		return false
	}
	if dst.BgSet != span.BgSet || (dst.BgSet && dst.BgRole != span.BgRole) {
		return false
	}
	dst.Text += span.Text
	return true
}
