package ui

import (
	"strings"
	"unicode/utf8"

	"crude/tui/cell"
	"crude/tui/theme"
)

// ConfirmPromptSeverity drives the icon, accent, and default button variant
// for confirm dialogs.
type ConfirmPromptSeverity int

const (
	ConfirmPromptInfo ConfirmPromptSeverity = iota
	ConfirmPromptWarning
	ConfirmPromptDanger
	ConfirmPromptQuestion
)

func (s ConfirmPromptSeverity) icon() string {
	switch s {
	case ConfirmPromptWarning:
		return "⚠"
	case ConfirmPromptDanger:
		return "✕"
	case ConfirmPromptQuestion:
		return "?"
	default:
		return "i"
	}
}

// actionVariant resolves a button's variant from its action semantics so the
// row reads like every ActionBar in the app: destructive actions are danger,
// the affirmative action is primary, neutral alternatives are secondary.
// Severity only disambiguates generic Yes (danger dialog → danger).
func actionVariant(actionID string, sev ConfirmPromptSeverity) ButtonVariant {
	switch actionID {
	case ConfirmActionSave:
		return VariantPrimary
	case ConfirmActionDiscard:
		return VariantDanger
	case ConfirmActionYes:
		if sev == ConfirmPromptDanger {
			return VariantDanger
		}
		return VariantPrimary
	case ConfirmActionNo, ConfirmActionCancel:
		return VariantSecondary
	default:
		return VariantSecondary
	}
}

// Confirm button action IDs (WindowFrame path).
const (
	ConfirmActionYes     = "confirm.yes"
	ConfirmActionNo      = "confirm.no"
	ConfirmActionSave    = "confirm.save"
	ConfirmActionDiscard = "confirm.discard"
	ConfirmActionCancel  = "confirm.cancel"
)

// ConfirmButtonSpec describes one action button in a confirm dialog. The
// variant is derived from ActionID semantics (destructive = danger,
// affirmative = primary, neutral = secondary).
type ConfirmButtonSpec struct {
	Label    string
	ActionID string
}

// ConfirmFrameSpec builds a WindowFrame-backed confirm dialog.
type ConfirmFrameSpec struct {
	Title        string
	Question     string
	Detail       string
	Severity     ConfirmPromptSeverity
	Buttons      []ConfirmButtonSpec
	FocusedIndex int
	Width        int
	// DetailMaxLines caps wrapped detail lines; overflow collapses to a
	// "…" tail. 0 means no cap.
	DetailMaxLines int
}

// ConfirmButtonID returns the component id for button index i.
func ConfirmButtonID(i int) ComponentID {
	return NewID(RootID, "confirm/btn-"+itoa(i))
}

// confirmDetailWrapWidth is the content width (spec.Width minus frame chrome
// and padding) available for detail text.
const confirmDetailWrapWidth = 8

// BuildConfirmFrame builds a unified WindowFrame dialog with question, detail,
// horizontal Button row, and hint. Buttons use the standard TokenSet +
// ButtonVariant pipeline shared with ActionBar, and the row sizes to its
// labels (no full-width accent slab).
func BuildConfirmFrame(th *theme.Theme, spec ConfirmFrameSpec) *ComponentWidget {
	if th == nil {
		th = theme.DefaultDark()
	}
	if spec.Width < 36 {
		spec.Width = 36
	}
	if spec.Width > 78 {
		spec.Width = 78
	}
	if len(spec.Buttons) == 0 {
		spec.Buttons = []ConfirmButtonSpec{
			{Label: "Yes", ActionID: ConfirmActionYes},
			{Label: "No", ActionID: ConfirmActionNo},
		}
	}
	if spec.FocusedIndex < 0 || spec.FocusedIndex >= len(spec.Buttons) {
		spec.FocusedIndex = len(spec.Buttons) - 1
	}

	tokens := NewTokenSet(th)
	popupBG := tokens.Color(ColorPopupBg)
	mutedFG := tokens.Color(ColorMuted)
	fg := tokens.Color(ColorFg)
	accent := tokens.Color(ColorPopupTitle)
	switch spec.Severity {
	case ConfirmPromptDanger:
		accent = tokens.Color(ColorError)
	case ConfirmPromptWarning:
		accent = tokens.Color(ColorWarning)
	}

	detailLines := wrapDetailLines(spec.Detail, spec.Width-confirmDetailWrapWidth, spec.DetailMaxLines)

	// Body rows: question, optional wrapped detail, buttons, hint.
	var bodyChildren []Component

	qLabel := NewLabel(NewID(RootID, "confirm/question"), spec.Question, th)
	qLabel.SetStyle(fg, popupBG, cell.AttrBold)
	bodyChildren = append(bodyChildren, qLabel)

	if len(detailLines) > 0 {
		bodyChildren = append(bodyChildren, spacerLabel(th, popupBG, "confirm/sp1"))
		for i, line := range detailLines {
			lab := NewLabel(NewID(RootID, "confirm/detail-"+itoa(i)), line, th)
			lab.SetStyle(mutedFG, popupBG, 0)
			bodyChildren = append(bodyChildren, lab)
		}
	}
	bodyChildren = append(bodyChildren, spacerLabel(th, popupBG, "confirm/sp2"))

	// Buttons size to their labels; variants come from action semantics
	// (destructive = danger, affirmative = primary, neutral = secondary).
	var btns []Component
	for i, b := range spec.Buttons {
		btn := NewButton(ConfirmButtonID(i), b.Label, b.ActionID)
		btn.SetTokens(tokens)
		btn.SetVariant(actionVariant(b.ActionID, spec.Severity))
		btns = append(btns, btn)
	}
	btnRow := NewStack(NewID(RootID, "confirm/buttons"), Horizontal, 2, btns...)
	bodyChildren = append(bodyChildren, btnRow)

	// Standard hint: navigation is uniform across every dialog in the app.
	hint := NewLabel(NewID(RootID, "confirm/hint"), "←→ switch · enter confirm · esc cancel", th)
	hint.SetStyle(mutedFG, popupBG, cell.AttrItalic)
	bodyChildren = append(bodyChildren, hint)

	body := NewStack(NewID(RootID, "confirm/body"), Vertical, 0, bodyChildren...)

	// Content height: question + (spacer + detail) + spacer + buttons + hint.
	contentH := 1 + 1 + 1 + 1 // q + spacer + buttons + hint
	if len(detailLines) > 0 {
		contentH += 1 + len(detailLines) // spacer before detail + detail rows
	}
	h := contentH + 2 // frame chrome: title row + borders
	if h < 6 {
		h = 6
	}

	frame := NewWindowFrame(NewID(RootID, "confirm"), FrameDialog, tokens)
	frame.SetTitle(spec.Title)
	frame.SetIcon(spec.Severity.icon())
	frame.SetBorderAccent(accent)
	frame.SetClosable(false)
	frame.SetContent(body)

	cw := NewComponentWidget(th, frame, spec.Width, h)
	cw.Focus(ConfirmButtonID(spec.FocusedIndex))
	return cw
}

// wrapDetailLines wraps detail text to width, preserving explicit newlines;
// empty/blank detail yields nil. Path-like tokens without spaces break after
// '/' separators; overflow beyond maxLines (when > 0) collapses into a
// trailing "…" line.
func wrapDetailLines(detail string, width int, maxLines int) []string {
	if strings.TrimSpace(detail) == "" {
		return nil
	}
	if width < 12 {
		width = 12
	}
	var out []string
	for _, para := range strings.Split(detail, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := ""
		for _, w := range words {
			for _, chunk := range splitLongToken(w, width) {
				switch {
				case line == "":
					line = chunk
				case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(chunk) <= width:
					line += " " + chunk
				default:
					out = append(out, line)
					line = chunk
				}
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	if maxLines > 0 && len(out) > maxLines {
		out = append(out[:maxLines-1], "…")
	}
	return out
}

// splitLongToken breaks a token wider than width into chunks: at '/'
// separators once half full (paths), else at the hard width limit.
func splitLongToken(tok string, width int) []string {
	if utf8.RuneCountInString(tok) <= width {
		return []string{tok}
	}
	var parts []string
	cur := ""
	for _, r := range tok {
		if utf8.RuneCountInString(cur)+1 > width {
			parts = append(parts, cur)
			cur = ""
		}
		cur += string(r)
		if r == '/' && utf8.RuneCountInString(cur) >= width/2 {
			parts = append(parts, cur)
			cur = ""
		}
	}
	if cur != "" {
		parts = append(parts, cur)
	}
	return parts
}

// BuildDirtyCloseFrame is the 3-button unsaved-changes dialog.
func BuildDirtyCloseFrame(th *theme.Theme, filename string, focusedIndex int) *ComponentWidget {
	detail := "'" + filename + "' has unsaved changes."
	return BuildConfirmFrame(th, ConfirmFrameSpec{
		Title:    "Unsaved changes",
		Question: "What do you want to do with the unsaved changes?",
		Detail:   detail,
		Severity: ConfirmPromptWarning,
		Buttons: []ConfirmButtonSpec{
			{Label: "Save", ActionID: ConfirmActionSave},
			{Label: "Don't Save", ActionID: ConfirmActionDiscard},
			{Label: "Cancel", ActionID: ConfirmActionCancel},
		},
		FocusedIndex: focusedIndex,
		Width:        62,
	})
}

// BuildTreeDeleteFrame is the Yes/No delete confirmation.
func BuildTreeDeleteFrame(th *theme.Theme, question, detail string, focusedIndex int) *ComponentWidget {
	return BuildConfirmFrame(th, ConfirmFrameSpec{
		Title:    "Delete",
		Question: question,
		Detail:   detail,
		Severity: ConfirmPromptDanger,
		Buttons: []ConfirmButtonSpec{
			{Label: "Yes", ActionID: ConfirmActionYes},
			{Label: "No", ActionID: ConfirmActionNo},
		},
		FocusedIndex: focusedIndex,
		Width:        60,
	})
}

// ConfirmButtonIndexFromID maps a component id to button index, or -1.
func ConfirmButtonIndexFromID(id ComponentID) int {
	s := string(id)
	const prefix = "confirm/btn-"
	if !strings.HasPrefix(s, prefix) {
		// may be nested under parent path
		if i := strings.LastIndex(s, prefix); i >= 0 {
			s = s[i:]
		} else {
			return -1
		}
	}
	n := strings.TrimPrefix(s, prefix)
	if n == "" {
		return -1
	}
	v := 0
	for _, r := range n {
		if r < '0' || r > '9' {
			return -1
		}
		v = v*10 + int(r-'0')
	}
	return v
}

// ConfirmActionIndex maps action IDs to button indices for dirty (3) and delete (2).
func ConfirmActionIndex(actionID string) int {
	switch actionID {
	case ConfirmActionSave, ConfirmActionYes:
		return 0
	case ConfirmActionDiscard, ConfirmActionNo:
		return 1
	case ConfirmActionCancel:
		return 2
	default:
		return -1
	}
}

// MoveConfirmFocus shifts button focus by delta within the dialog's host,
// wrapping at the ends, without rebuilding the component tree. Returns the
// new focused index, or -1 when the dialog is not a confirm frame.
// The repaint is bridged through onVisualChange so the WM compositor picks
// the new focus fill (Host.MarkPaint alone never reaches the stale surface).
func MoveConfirmFocus(cw *ComponentWidget, delta int, count int) int {
	next := -1
	changed := confirmVisualChange(cw, func() bool {
		if cw == nil || cw.Host() == nil || count <= 0 {
			return false
		}
		cur := ConfirmButtonIndexFromID(cw.Host().Focused())
		if cur < 0 {
			cur = 0
		}
		next = ((cur+delta)%count + count) % count
		return cw.Host().Focus(ConfirmButtonID(next))
	})
	if !changed {
		return -1
	}
	return next
}

// SetConfirmFocus focuses a specific confirm button (mouse hover/click path)
// without a tree rebuild. Returns false when the index is invalid.
func SetConfirmFocus(cw *ComponentWidget, index int) bool {
	changed := confirmVisualChange(cw, func() bool {
		if cw == nil || cw.Host() == nil || index < 0 {
			return false
		}
		return cw.Host().Focus(ConfirmButtonID(index))
	})
	return changed
}

// SetConfirmHover sets the hovered confirm button (mouse path), leaving
// keyboard focus untouched: hover paints the subtle ColorHover fill while
// the focused button keeps the accent fill — the two states stay distinct
// per the control-state language. index < 0 clears hover. Returns true when
// the hover target changed.
func SetConfirmHover(cw *ComponentWidget, index int) bool {
	if cw == nil || cw.Host() == nil {
		return false
	}
	id := RootID
	if index >= 0 {
		id = ConfirmButtonID(index)
	}
	changed := cw.Host().Hovered() != id
	cw.Host().SetHovered(id)
	if changed {
		cw.MarkContentDirty()
	}
	return changed
}

// confirmVisualChange runs fn and marks the owning FloatingWindow's content
// dirty afterwards. The WM composites floating windows from cached surfaces
// and only re-renders content when contentDirty is set; Host-level paint
// flags do not cross that boundary, so visual changes from keyboard/hover
// must be bridged here.
func confirmVisualChange(cw *ComponentWidget, fn func() bool) bool {
	changed := fn()
	if changed && cw != nil {
		cw.MarkContentDirty()
	}
	return changed
}

func spacerLabel(th *theme.Theme, bg cell.Color, id string) *Label {
	lab := NewLabel(NewID(RootID, id), " ", th)
	lab.SetStyle(cell.NoColor, bg, 0)
	return lab
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
