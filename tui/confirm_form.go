package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	btnAllow  = 0
	btnAlways = 1
	btnDeny   = 2
)

type confirmForm struct {
	active    bool
	question  string
	operation string
	tool      string
	target    string
	risk      string
	reason    string
	suggestion string
	focused   int
}

func newConfirmForm() confirmForm {
	return confirmForm{focused: btnAllow}
}

type ConfirmResult struct {
	Choice    string
	Operation string
	Tool      string
	Target    string
}

const (
	ConfirmAllow  = "allow"
	ConfirmAlways = "always"
	ConfirmDeny   = "deny"
)

func (c *confirmForm) Reset(question, operation, tool, target string) {
	c.active = true
	c.question = question
	c.operation = operation
	c.tool = tool
	c.target = target
	c.risk = ""
	c.reason = ""
	c.suggestion = ""
	c.focused = btnAllow
}

func (c *confirmForm) ResetWithDetails(question, operation, tool, target, risk, reason, suggestion string) {
	c.active = true
	c.question = question
	c.operation = operation
	c.tool = tool
	c.target = target
	c.risk = risk
	c.reason = reason
	c.suggestion = suggestion
	c.focused = btnAllow
}

func (c *confirmForm) Dismiss() {
	c.active = false
}

func (c confirmForm) Update(msg tea.Msg) (confirmForm, ConfirmResult, bool) {
	if !c.active {
		return c, ConfirmResult{}, false
	}

	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return c, ConfirmResult{}, false
	}

	switch keyMsg.Type {
	case tea.KeyLeft:
		if c.focused > btnAllow {
			c.focused--
		}
	case tea.KeyRight, tea.KeyTab:
		if c.focused < btnDeny {
			c.focused++
		}
	case tea.KeyShiftTab:
		if c.focused > btnAllow {
			c.focused--
		}
	case tea.KeyEnter:
		return c.commit()
	case tea.KeyEsc:
		c.active = false
		return c, ConfirmResult{Choice: ConfirmDeny, Operation: c.operation, Tool: c.tool, Target: c.target}, true
	default:
		s := keyMsg.String()
		switch s {
		case "y":
			c.focused = btnAllow
			return c.commit()
		case "a":
			c.focused = btnAlways
			return c.commit()
		case "n":
			c.focused = btnDeny
			return c.commit()
		}
	}

	return c, ConfirmResult{}, false
}

func (c confirmForm) commit() (confirmForm, ConfirmResult, bool) {
	c.active = false
	choice := ConfirmAllow
	switch c.focused {
	case btnAllow:
		choice = ConfirmAllow
	case btnAlways:
		choice = ConfirmAlways
	case btnDeny:
		choice = ConfirmDeny
	}
	return c, ConfirmResult{Choice: choice, Operation: c.operation, Tool: c.tool, Target: c.target}, true
}

func (c confirmForm) View(spinFrame int) string {
	if !c.active {
		return ""
	}

	var b strings.Builder
	dim := lipgloss.NewStyle().Foreground(colorMuted)
	warnStyle := lipgloss.NewStyle().Foreground(colorWarning).Bold(true)
	boxStyle := lipgloss.NewStyle().
		Foreground(colorMainText).
		Background(lipgloss.Color("#1a1a2e")).
		Padding(0, 1)

	b.WriteString(warnStyle.Render("  ⚠  confirmation required"))
	b.WriteString("\n")
	b.WriteString(dim.Render("  " + strings.Repeat("─", 54)))
	b.WriteString("\n")

	if c.risk != "" {
		riskColor := colorWarning
		riskLabel := "medium"
		switch c.risk {
		case "blocked":
			riskColor = lipgloss.Color("#ff0000")
			riskLabel = "BLOCKED"
		case "destructive":
			riskColor = colorWarning
			riskLabel = "DANGEROUS"
		case "network":
			riskColor = colorAccent
			riskLabel = "network"
		case "project-write":
			riskColor = lipgloss.Color("#d4a017")
			riskLabel = "write"
		case "unknown":
			riskColor = colorMuted
			riskLabel = "unknown"
		default:
			riskColor = colorMuted
			riskLabel = c.risk
		}
		riskStyle := lipgloss.NewStyle().Foreground(riskColor).Bold(true)
		b.WriteString(dim.Render("  risk:      ") + riskStyle.Render(strings.ToUpper(riskLabel)))
		b.WriteString("\n")
	}

	if c.operation != "" {
		opStyle := lipgloss.NewStyle().Foreground(colorAccent2)
		b.WriteString(dim.Render("  operation: ") + opStyle.Render(c.operation))
		b.WriteString("\n")
	}
	if c.tool != "" {
		b.WriteString(dim.Render("  tool:      ") + accentStyle.Render(c.tool))
		b.WriteString("\n")
	}
	if c.target != "" {
		targetStyle := lipgloss.NewStyle().Foreground(colorWarning)
		targetDisplay := c.target
		if len(targetDisplay) > 80 {
			targetDisplay = targetDisplay[:77] + "..."
		}
		b.WriteString(dim.Render("  target:    ") + targetStyle.Render(targetDisplay))
		b.WriteString("\n")
	}

	if c.reason != "" {
		reasonStyle := lipgloss.NewStyle().Foreground(colorMainText).Italic(true)
		b.WriteString("\n")
		b.WriteString(dim.Render("  reason: ") + reasonStyle.Render(c.reason))
		b.WriteString("\n")
	}

	if c.suggestion != "" {
		suggStyle := lipgloss.NewStyle().Foreground(colorAccent).Italic(true)
		b.WriteString(dim.Render("  tip: ") + suggStyle.Render(c.suggestion))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(dim.Render("  " + c.question))
	b.WriteString("\n\n")

	buttons := []struct {
		label string
		key   string
		idx   int
	}{
		{"Allow", "y", btnAllow},
		{"Always Allow", "a", btnAlways},
		{"Deny", "n", btnDeny},
	}

	var btnStrs []string
	for _, btn := range buttons {
		if c.focused == btn.idx {
			btnStrs = append(btnStrs, boxStyle.Render(fmt.Sprintf("❯ [ %s ]", btn.label))+" "+dim.Render("("+btn.key+")"))
		} else {
			btnStrs = append(btnStrs, dim.Render(fmt.Sprintf("  [ %s ]", btn.label))+" "+dim.Render("("+btn.key+")"))
		}
	}

	b.WriteString("  " + strings.Join(btnStrs, "  "))
	b.WriteString("\n")
	b.WriteString(dim.Render("  ← → navigate · enter select · esc deny"))
	b.WriteString("\n")

	return b.String()
}

var accentStyle = lipgloss.NewStyle().Foreground(colorAccent)
