package widgets

import (
	"image/color"
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
)

// ConfirmPromptSeverity drives the icon and accent color for confirm dialogs.
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

func (s ConfirmPromptSeverity) accent(th *theme.Theme) color.Color {
	switch s {
	case ConfirmPromptWarning:
		return th.Colors.Warning
	case ConfirmPromptDanger:
		return th.Colors.Error
	case ConfirmPromptQuestion:
		return th.Colors.Accent2
	default:
		return th.Colors.Accent2
	}
}

// Confirm button action IDs (WindowFrame path).
const (
	ConfirmActionYes    = "confirm.yes"
	ConfirmActionNo     = "confirm.no"
	ConfirmActionSave   = "confirm.save"
	ConfirmActionDiscard = "confirm.discard"
	ConfirmActionCancel = "confirm.cancel"
)

// ConfirmButtonSpec describes one action button in a confirm dialog.
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
}

// ConfirmButtonID returns the component id for button index i.
func ConfirmButtonID(i int) ui.ComponentID {
	return ui.NewID(ui.RootID, "confirm/btn-"+itoa(i))
}

// BuildConfirmFrame builds a unified WindowFrame dialog with question, detail,
// horizontal Button row, and hint. Replaces ConfirmPrompt / ConfirmPrompt3Button
// for dirty-close and tree-delete product windows.
func BuildConfirmFrame(th *theme.Theme, spec ConfirmFrameSpec) *ui.ComponentWidget {
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

	tokens := ui.NewTokenSet(th)
	popupBG := rendering.ThemeColorToCell(th.Colors.PopupBg)
	mutedFG := rendering.ThemeColorToCell(th.Colors.Muted)
	fg := rendering.ThemeColorToCell(th.Colors.Fg)
	accentFG := rendering.ThemeColorToCell(spec.Severity.accent(th))

	// Body rows: icon+question, optional detail lines, spacer, buttons, hint
	var bodyChildren []ui.Component

	qText := spec.Severity.icon() + " " + spec.Question
	qLabel := ui.NewLabel(ui.NewID(ui.RootID, "confirm/question"), qText, th)
	qLabel.SetStyle(fg, popupBG, cell.AttrBold)
	// Color the icon portion by using accent for whole line when short.
	_ = accentFG
	bodyChildren = append(bodyChildren, qLabel)

	if strings.TrimSpace(spec.Detail) != "" {
		bodyChildren = append(bodyChildren, spacerLabel(th, popupBG, "confirm/sp1"))
		for i, line := range strings.Split(spec.Detail, "\n") {
			lab := ui.NewLabel(ui.NewID(ui.RootID, "confirm/detail-"+itoa(i)), line, th)
			lab.SetStyle(mutedFG, popupBG, 0)
			bodyChildren = append(bodyChildren, lab)
		}
	}
	bodyChildren = append(bodyChildren, spacerLabel(th, popupBG, "confirm/sp2"))

	// Buttons
	var btns []ui.Component
	for i, b := range spec.Buttons {
		btn := ui.NewButton(ConfirmButtonID(i), b.Label, b.ActionID)
		btn.SetTokens(tokens)
		btns = append(btns, btn)
	}
	btnRow := ui.NewStack(ui.NewID(ui.RootID, "confirm/buttons"), ui.Horizontal, 2, btns...)
	bodyChildren = append(bodyChildren, btnRow)

	hintText := "← → switch · enter confirm · esc cancel"
	hint := ui.NewLabel(ui.NewID(ui.RootID, "confirm/hint"), hintText, th)
	hint.SetStyle(mutedFG, popupBG, cell.AttrItalic)
	bodyChildren = append(bodyChildren, hint)

	body := ui.NewStack(ui.NewID(ui.RootID, "confirm/body"), ui.Vertical, 0, bodyChildren...)

	// Content height: measure-ish estimate for floating window size.
	// question + detail lines + spacers + button + hint
	detailLines := 0
	if strings.TrimSpace(spec.Detail) != "" {
		detailLines = len(strings.Split(spec.Detail, "\n")) + 1 // + spacer before
	}
	contentH := 1 + detailLines + 1 + 1 + 1 // q + detail block + spacer + buttons + hint
	// Frame adds 2 border rows
	h := contentH + 2
	if h < 6 {
		h = 6
	}

	frame := ui.NewWindowFrame(ui.NewID(ui.RootID, "confirm"), ui.FrameDialog, tokens)
	frame.SetTitle(spec.Title)
	frame.SetIcon(spec.Severity.icon())
	frame.SetClosable(false)
	frame.SetContent(body)

	cw := ui.NewComponentWidget(th, frame, spec.Width, h)
	cw.Focus(ConfirmButtonID(spec.FocusedIndex))
	return cw
}

// BuildDirtyCloseFrame is the 3-button unsaved-changes dialog.
func BuildDirtyCloseFrame(th *theme.Theme, filename string, focusedIndex int) *ui.ComponentWidget {
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
func BuildTreeDeleteFrame(th *theme.Theme, question, detail string, focusedIndex int) *ui.ComponentWidget {
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
func ConfirmButtonIndexFromID(id ui.ComponentID) int {
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

func spacerLabel(th *theme.Theme, bg cell.Color, id string) *ui.Label {
	lab := ui.NewLabel(ui.NewID(ui.RootID, id), " ", th)
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
