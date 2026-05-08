package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type yoloForm struct {
	active  bool
	focused int
}

func newYoloForm() yoloForm {
	return yoloForm{focused: 0}
}

func (f *yoloForm) Reset() {
	f.active = true
	f.focused = 0
}

func (f *yoloForm) Dismiss() {
	f.active = false
}

func (f yoloForm) Update(msg tea.Msg) (yoloForm, bool, bool) {
	if !f.active {
		return f, false, false
	}

	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return f, false, false
	}

	switch keyMsg.Type {
	case tea.KeyLeft:
		if f.focused > 0 {
			f.focused--
		}
	case tea.KeyRight, tea.KeyTab:
		if f.focused < 1 {
			f.focused++
		}
	case tea.KeyShiftTab:
		if f.focused > 0 {
			f.focused--
		}
	case tea.KeyEnter:
		f.active = false
		return f, f.focused == 0, true
	case tea.KeyEsc:
		f.active = false
		return f, false, true
	default:
		s := keyMsg.String()
		switch s {
		case "y", "Y":
			f.focused = 0
			f.active = false
			return f, true, true
		case "n", "N":
			f.focused = 1
			f.active = false
			return f, false, true
		}
	}

	return f, false, false
}

func (f yoloForm) View() string {
	if !f.active {
		return ""
	}

	dim := lipgloss.NewStyle().Foreground(colorMuted)
	boxStyle := lipgloss.NewStyle().
		Foreground(colorMainText).
		Background(lipgloss.Color("#1a1a2e")).
		Padding(0, 1)
	dangerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")).Bold(true)

	var b strings.Builder

	b.WriteString(dangerStyle.Render("☠  YOLO MODE"))
	b.WriteString("\n")
	b.WriteString(dim.Render(strings.Repeat("─", 54)))
	b.WriteString("\n")
	b.WriteString(dim.Render("  This is DANGEROUS. Safety checks protect"))
	b.WriteString("\n")
	b.WriteString(dim.Render("  your system from accidental damage and"))
	b.WriteString("\n")
	b.WriteString(dim.Render("  prompt injection attacks."))
	b.WriteString("\n")
	b.WriteString("\n")
	b.WriteString(dangerStyle.Render("  Are you absolutely sure?"))
	b.WriteString("\n\n")

	buttons := []struct {
		label string
		key   string
		idx   int
	}{
		{"Enable YOLO", "y", 0},
		{"Cancel", "n", 1},
	}

	var btnStrs []string
	for _, btn := range buttons {
		if f.focused == btn.idx {
			btnStrs = append(btnStrs, boxStyle.Render(fmt.Sprintf("❯ [ %s ]", btn.label))+" "+dim.Render("("+btn.key+")"))
		} else {
			btnStrs = append(btnStrs, dim.Render(fmt.Sprintf("  [ %s ]", btn.label))+" "+dim.Render("("+btn.key+")"))
		}
	}

	b.WriteString("  " + strings.Join(btnStrs, "  "))
	b.WriteString("\n")
	b.WriteString(dim.Render("  ← → navigate · enter select · esc cancel"))
	b.WriteString("\n")

	return b.String()
}
