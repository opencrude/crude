package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// KeyChord paints a shortcut key (bold fg) followed by a muted label.
type KeyChord struct {
	Base
	key    string
	label  string
	tokens *TokenSet
}

// NewKeyChord creates a key+label chord.
func NewKeyChord(id ComponentID, key, label string, tokens *TokenSet) *KeyChord {
	return &KeyChord{Base: NewBase(id), key: key, label: label, tokens: tokens}
}

// KeyChordFromAction resolves display key/label from the action registry.
func KeyChordFromAction(id ComponentID, actionID ActionID, reg *ActionRegistry, tokens *TokenSet) *KeyChord {
	key, label := "", ""
	if reg != nil {
		if a := reg.Get(actionID); a != nil {
			key = FormatShortcut(reg.HintFor(actionID))
			label = strings.ToLower(a.Label)
		}
	}
	return NewKeyChord(id, key, label, tokens)
}

// SetTokens updates theme tokens.
func (k *KeyChord) SetTokens(tokens *TokenSet) { k.tokens = tokens }

// SetContent updates key and label text.
func (k *KeyChord) SetContent(key, label string) {
	k.key = key
	k.label = label
}

// PlainText returns "Key label" (or just key / label if one side empty).
func (k *KeyChord) PlainText() string {
	switch {
	case k.key != "" && k.label != "":
		return k.key + " " + k.label
	case k.key != "":
		return k.key
	default:
		return k.label
	}
}

func (k *KeyChord) Measure(_ LayoutContext, cs Constraints) Size {
	w := rendering.VisibleWidth(k.PlainText())
	return cs.ClampSize(Size{W: w, H: 1})
}

func (k *KeyChord) Layout(_ LayoutContext, r Rect) { k.SetBounds(r) }

func (k *KeyChord) Paint(buf *surface.CellBuffer, ox, oy int, _ PaintContext) {
	b := k.Bounds()
	if b.Empty() || buf == nil {
		return
	}
	x, y := ox+b.X, oy+b.Y
	fg := cell.NoColor
	muted := cell.NoColor
	if k.tokens != nil {
		fg = k.tokens.Color(ColorFg)
		muted = k.tokens.Color(ColorMuted)
	}
	remain := b.W
	if k.key != "" && remain > 0 {
		text := rendering.TruncateLine(k.key, remain)
		buf.WriteString(x, y, text, fg, cell.NoColor, cell.AttrBold)
		tw := rendering.VisibleWidth(text)
		x += tw
		remain -= tw
	}
	if k.label != "" && remain > 1 {
		buf.WriteString(x, y, " ", muted, cell.NoColor, 0)
		x++
		remain--
		text := rendering.TruncateLine(k.label, remain)
		buf.WriteString(x, y, text, muted, cell.NoColor, 0)
	}
}

func (k *KeyChord) Handle(_ EventContext, _ tea.Msg) EventResult { return EventResult{} }

func (k *KeyChord) Semantics() Semantics {
	return Semantics{Role: RoleLabel, Label: k.PlainText(), Value: k.PlainText()}
}
