package widgets

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"crude/tui/theme"
)

// ConfirmPromptSeverity drives the icon and accent color for the
// confirm prompt. Destructive prompts (e.g. "discard changes")
// default to Warning severity — a yellow icon without alarm.
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

// ConfirmPrompt builds the ANSI content lines for a small yes/no
// floating dialog using the unified popup design system. The output
// is the FULL popup (border, title, body) — pass it directly to
// widgets.NewPopupContent.
//
// Width is auto-computed from the longest content line. Use a minimum
// of 40 for comfortable reading; a maximum of 70 keeps the dialog
// from sprawling on wide terminals.
//
// Example (called from app.go showDirtyClosePrompt):
//
//	lines := widgets.ConfirmPrompt(th,
//	    "Unsaved changes",
//	    "Discard unsaved changes?",
//	    "'docconv.go' has unsaved changes.\nClosing will discard them.",
//	    widgets.ConfirmPromptWarning,  // warning yellow icon
//	    true,  // focusedYes
//	    56,    // width
//	)
func ConfirmPrompt(th *theme.Theme, title, question, detail string, severity ConfirmPromptSeverity, focusedYes bool, width int) []string {
	if width < 36 {
		width = 36
	}
	if width > 70 {
		width = 70
	}
	// Inner width = total - 6 (border + padding).
	innerW := width - 6

	mutedStyle := lipgloss.NewStyle().Foreground(th.Colors.Muted).Background(th.Colors.PopupBg)
	fgStyle := lipgloss.NewStyle().Foreground(th.Colors.Fg).Background(th.Colors.PopupBg)
	accentColor := severity.accent(th)
	accentStyle := lipgloss.NewStyle().Foreground(accentColor).Background(th.Colors.PopupBg).Bold(true)

	var lines []string

	// Question row: "<icon> <question>" — icon in accent, text in fg.
	q := padVisibleToWidth(
		accentStyle.Render(severity.icon()+" ")+fgStyle.Render(question),
		innerW)
	lines = append(lines, q)

	// Detail body (multi-line, each line muted).
	if detail != "" {
		lines = append(lines, PopupEmptyLine(th))
		for _, l := range strings.Split(detail, "\n") {
			if l == "" {
				lines = append(lines, PopupEmptyLine(th))
			} else {
				lines = append(lines, padVisibleToWidth(mutedStyle.Render(l), innerW))
			}
		}
		lines = append(lines, PopupEmptyLine(th))
	}

	// Spacer.
	lines = append(lines, PopupEmptyLine(th))

	// Button row: Yes | No (one focused).
	buttons := []PopupButtonSpec{
		{Label: "Yes", Focused: focusedYes},
		{Label: "No", Focused: !focusedYes},
	}
	lines = append(lines, padVisibleToWidth(PopupButtonRow(th, buttons), innerW))

	// Hint row.
	lines = append(lines, padVisibleToWidth(
		PopupHintLine(th, []HintSegment{
			{Key: "← →", Desc: "switch"},
			{Key: "enter", Desc: "confirm"},
			{Key: "y/n", Desc: "shortcut"},
			{Key: "esc", Desc: "cancel"},
		}),
		innerW))

	cfg := PopupChromeConfig{
		Title:       title,
		Width:       width,
		Focused:     true,
		TitleAccent: severity.accent(th),
	}
	return PopupChromeLines(th, cfg, lines)
}
