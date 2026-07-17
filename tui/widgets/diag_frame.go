package widgets

import (
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
)

// DiagFrameIDs are component IDs inside a diagnostic frame.
const (
	DiagFrameID = "diag-popup"
	DiagContent = "diag-popup/content"
)

// DiagSeverity is the severity level for a diagnostic popup.
type DiagSeverity uint8

const (
	DiagSevError   DiagSeverity = 1
	DiagSevWarning DiagSeverity = 2
	DiagSevInfo    DiagSeverity = 3
	DiagSevHint    DiagSeverity = 4
)

// DiagLine is one line of diagnostic content with its own style.
type DiagLine struct {
	Text string
	Kind DiagLineKind
}

// DiagLineKind controls the visual style of a diagnostic line.
type DiagLineKind uint8

const (
	DiagLineTitle   DiagLineKind = iota // accent bold (file:line:col)
	DiagLineSource                      // fg (source code line)
	DiagLineDivider                     // muted (─ separator)
	DiagLineBody                        // fg (message body)
)

// DiagFrameSpec configures a WindowFrame-backed diagnostic popup.
type DiagFrameSpec struct {
	Title    string
	Icon     string
	Severity DiagSeverity
	Lines    []DiagLine // structured content with per-line styles
	Width    int
	Height   int
}

// BuildDiagnosticFrame builds a WindowFrame popover for diagnostic display.
// The content lines are rendered as styled Label components inside a vertical
// Stack. The border and title are tinted with the severity color, preserving
// the full visual fidelity of the old PopupChromeLines + BorderAccent path.
func BuildDiagnosticFrame(th *theme.Theme, spec DiagFrameSpec) *ui.ComponentWidget {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)

	w := spec.Width
	if w < 44 {
		w = 44
	}
	if w > 80 {
		w = 80
	}
	innerW := w - 6 // 2 border + 4 padding (2 each side)
	if innerW < 10 {
		innerW = 10
	}

	// Resolve severity accent color
	var borderAccent, titleAccent cell.Color
	switch spec.Severity {
	case DiagSevError:
		borderAccent = tokens.Color(ui.ColorError)
		titleAccent = tokens.Color(ui.ColorError)
	case DiagSevWarning:
		borderAccent = tokens.Color(ui.ColorWarning)
		titleAccent = tokens.Color(ui.ColorWarning)
	case DiagSevInfo, DiagSevHint:
		borderAccent = tokens.Color(ui.ColorMuted)
		titleAccent = tokens.Color(ui.ColorMuted)
	}

	// Build content as styled Labels in a Stack
	var children []ui.Component
	for _, line := range spec.Lines {
		// Word-wrap long body lines
		wrapped := []string{line.Text}
		if line.Kind == DiagLineBody && rendering.VisibleWidth(line.Text) > innerW {
			wrapped = rendering.WordWrap(line.Text, innerW)
		} else if rendering.VisibleWidth(line.Text) > innerW {
			// Truncate non-body lines
			wrapped = []string{rendering.TruncateLine(line.Text, innerW)}
		}
		for _, wl := range wrapped {
			lbl := ui.NewLabel(ui.NewID(ui.RootID, DiagContent), wl, th)
			applyDiagLineStyle(lbl, line.Kind, tokens)
			children = append(children, lbl)
		}
	}
	if len(children) == 0 {
		children = append(children, ui.NewLabel(ui.NewID(ui.RootID, DiagContent), "", th))
	}

	stack := ui.NewStack(ui.NewID(ui.RootID, DiagContent), ui.Vertical, 0, children...)

	frame := ui.NewWindowFrame(ui.NewID(ui.RootID, DiagFrameID), ui.FramePopover, tokens)
	frame.SetTitle(spec.Title)
	frame.SetIcon(spec.Icon)
	frame.SetClosable(false)
	if !borderAccent.IsDefault() {
		frame.SetBorderAccent(borderAccent)
	}
	if !titleAccent.IsDefault() {
		frame.SetTitleAccent(titleAccent)
	}
	frame.SetContent(stack)

	h := spec.Height
	if h < 4 {
		h = 4
	}

	cw := ui.NewComponentWidget(th, frame, w, h)
	return cw
}

// applyDiagLineStyle sets the Label style based on the line kind.
func applyDiagLineStyle(lbl *ui.Label, kind DiagLineKind, tokens *ui.TokenSet) {
	bg := tokens.Color(ui.ColorPopupBg)
	switch kind {
	case DiagLineTitle:
		lbl.SetStyle(tokens.Color(ui.ColorAccent), bg, cell.AttrBold)
	case DiagLineSource:
		lbl.SetStyle(tokens.Color(ui.ColorFg), bg, 0)
	case DiagLineDivider:
		lbl.SetStyle(tokens.Color(ui.ColorMuted), bg, 0)
	case DiagLineBody:
		lbl.SetStyle(tokens.Color(ui.ColorFg), bg, 0)
	}
}

// DiagLineDividerText returns a divider string of the given width.
func DiagLineDividerText(width int) string {
	if width < 1 {
		width = 1
	}
	return strings.Repeat("─", width)
}