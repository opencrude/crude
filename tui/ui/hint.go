package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// HintSegmentKind selects how a status/help segment is resolved.
type HintSegmentKind uint8

const (
	// HintKindAction resolves key+label from ActionRegistry.
	HintKindAction HintSegmentKind = iota
	// HintKindChord uses an explicit key + label pair.
	HintKindChord
	// HintKindText is descriptive muted text with no shortcut.
	HintKindText
)

// HintSegment is one piece of contextual help.
type HintSegment struct {
	Kind     HintSegmentKind
	ActionID ActionID
	Key      string
	Label    string
}

// ActionSeg builds a registry-backed segment.
func ActionSeg(id ActionID) HintSegment {
	return HintSegment{Kind: HintKindAction, ActionID: id}
}

// ChordSeg builds an explicit key+label segment.
func ChordSeg(key, label string) HintSegment {
	return HintSegment{Kind: HintKindChord, Key: key, Label: label}
}

// TextSeg builds a muted text-only segment.
func TextSeg(text string) HintSegment {
	return HintSegment{Kind: HintKindText, Label: text}
}

const defaultHintSep = "  ·  "

// Hint paints a row of shortcut chords and text segments.
type Hint struct {
	Base
	segments  []HintSegment
	prefix    string
	registry  *ActionRegistry
	tokens    *TokenSet
	separator string
}

// NewHint creates an empty hint row.
func NewHint(id ComponentID, reg *ActionRegistry, tokens *TokenSet) *Hint {
	return &Hint{
		Base:      NewBase(id),
		registry:  reg,
		tokens:    tokens,
		separator: defaultHintSep,
	}
}

// SetTokens updates theme tokens.
func (h *Hint) SetTokens(tokens *TokenSet) { h.tokens = tokens }

// SetRegistry updates the action registry used for HintKindAction.
func (h *Hint) SetRegistry(reg *ActionRegistry) { h.registry = reg }

// SetSegments replaces the hint segments.
func (h *Hint) SetSegments(segs []HintSegment) { h.segments = segs }

// SetPrefix sets optional leading static text (e.g. editor file/line info).
func (h *Hint) SetPrefix(s string) { h.prefix = s }

// Segments returns the current segments.
func (h *Hint) Segments() []HintSegment { return h.segments }

// Prefix returns the leading static text.
func (h *Hint) Prefix() string { return h.prefix }

// ResolveSegment returns display key and label for one segment.
func (h *Hint) ResolveSegment(seg HintSegment) (key, label string) {
	switch seg.Kind {
	case HintKindAction:
		if h.registry == nil {
			return "", ""
		}
		a := h.registry.Get(seg.ActionID)
		if a == nil {
			return "", ""
		}
		key = FormatShortcut(h.registry.HintFor(seg.ActionID))
		label = strings.ToLower(a.Label)
		return key, label
	case HintKindChord:
		return seg.Key, seg.Label
	default:
		return "", seg.Label
	}
}

// PlainText flattens prefix + segments for tests and clipboard.
func (h *Hint) PlainText() string {
	var parts []string
	if h.prefix != "" {
		parts = append(parts, h.prefix)
	}
	for _, seg := range h.segments {
		key, label := h.ResolveSegment(seg)
		switch {
		case key != "" && label != "":
			parts = append(parts, key+" "+label)
		case key != "":
			parts = append(parts, key)
		case label != "":
			parts = append(parts, label)
		}
	}
	sep := h.separator
	if sep == "" {
		sep = defaultHintSep
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// VisibleWidth returns the display width of PlainText().
func (h *Hint) VisibleWidth() int {
	return rendering.VisibleWidth(h.PlainText())
}

func (h *Hint) Measure(_ LayoutContext, cs Constraints) Size {
	w := h.VisibleWidth()
	return cs.ClampSize(Size{W: w, H: 1})
}

func (h *Hint) Layout(_ LayoutContext, r Rect) { h.SetBounds(r) }

func (h *Hint) Paint(buf *surface.CellBuffer, ox, oy int, _ PaintContext) {
	b := h.Bounds()
	if b.Empty() || buf == nil {
		return
	}
	h.PaintInto(buf, ox+b.X, oy+b.Y, b.W)
}

// PaintInto paints the hint starting at (x,y) within maxW columns.
func (h *Hint) PaintInto(buf *surface.CellBuffer, x, y, maxW int) {
	if buf == nil || maxW <= 0 {
		return
	}
	fg := cell.NoColor
	muted := cell.NoColor
	if h.tokens != nil {
		fg = h.tokens.Color(ColorFg)
		muted = h.tokens.Color(ColorMuted)
	}
	sep := h.separator
	if sep == "" {
		sep = defaultHintSep
	}
	remain := maxW
	write := func(text string, color cell.Color, attr cell.Attrs) {
		if text == "" || remain <= 0 {
			return
		}
		text = rendering.TruncateLine(text, remain)
		buf.WriteString(x, y, text, color, cell.NoColor, attr)
		tw := rendering.VisibleWidth(text)
		x += tw
		remain -= tw
	}

	first := true
	emitSep := func() {
		if !first && remain > 0 {
			write(sep, muted, 0)
		}
		first = false
	}

	if h.prefix != "" {
		emitSep()
		write(h.prefix, muted, 0)
	}
	for _, seg := range h.segments {
		key, label := h.ResolveSegment(seg)
		if key == "" && label == "" {
			continue
		}
		// Skip segments that cannot fit (keep prefix; drop trailing chords).
		need := 0
		if key != "" && label != "" {
			need = rendering.VisibleWidth(key) + 1 + rendering.VisibleWidth(label)
		} else if key != "" {
			need = rendering.VisibleWidth(key)
		} else {
			need = rendering.VisibleWidth(label)
		}
		if !first {
			need += rendering.VisibleWidth(sep)
		}
		if need > remain {
			break
		}
		emitSep()
		if key != "" {
			write(key, fg, cell.AttrBold)
			if label != "" {
				write(" ", muted, 0)
				write(label, muted, 0)
			}
		} else {
			write(label, muted, 0)
		}
	}
}

func (h *Hint) Handle(_ EventContext, _ tea.Msg) EventResult { return EventResult{} }

func (h *Hint) Semantics() Semantics {
	return Semantics{Role: RoleLabel, Label: "Hint", Value: h.PlainText()}
}
