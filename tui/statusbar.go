package tui

import (
	"fmt"
	"strings"
	"time"

	"crude/fileio"
	"crude/tui/widget"
	"github.com/charmbracelet/lipgloss"
)

type StatusBar struct {
	theme  *widget.Theme
	width  int

	thinking, streaming, blocking bool
	isConfirming                   bool
	spinner    widget.Spinner
	statusLine string
	statusPhase string

	tokenPrompt, tokenCompletion, tokenCached int
	sessionCost   float64
	contextWindow int
	contextUsed   int

	lastError   string
	errorTime   time.Time
	infoMsg     string
	infoMsgTime time.Time
	retryMsg    string
	retryTime   time.Time

	asyncCommands []fileio.AsyncCmdInfo

	spinFrame int
}

func NewStatusBar(t *widget.Theme) *StatusBar {
	return &StatusBar{theme: t, spinner: widget.NewSpinner(t)}
}

func (s *StatusBar) View() string {
	dim := lipgloss.NewStyle().Foreground(s.theme.Colors.Muted)
	pad := strings.Repeat(" ", s.theme.Spacing.PadLR)

	var left string
	if s.infoMsg != "" {
		left = pad + lipgloss.NewStyle().Foreground(s.theme.Colors.Success).Render(
			widget.TruncateLine(s.infoMsg, s.width/2))
	} else if s.retryMsg != "" {
		left = pad + lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Render(
			widget.TruncateLine(s.retryMsg, s.width/2))
	} else if s.lastError != "" {
		left = pad + s.theme.Styles.ErrorText.Render(
			widget.TruncateLine(s.lastError, s.width/2))
	} else if s.confirming() {
		left = pad + lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Render("confirming...")
	} else if s.blocking {
		left = pad + lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Render("awaiting input...")
	} else if s.streaming {
		left = pad + s.spinner.StreamAnimation()
	} else if s.thinking {
		left = pad + s.spinner.WorkAnimation()
	} else {
		left = pad + dim.Render("ready")
	}

	right := ""
	if s.tokenPrompt > 0 || s.tokenCompletion > 0 {
		var parts []string
		parts = append(parts, fmt.Sprintf("↑%s", formatTokens(s.tokenPrompt)))
		if s.tokenCached > 0 {
			parts = append(parts, fmt.Sprintf("·%s", formatTokens(s.tokenCached)))
		}
		parts = append(parts, fmt.Sprintf("↓%s", formatTokens(s.tokenCompletion)))
		percent := 0
		if s.contextWindow > 0 {
			percent = (s.contextUsed * 100) / s.contextWindow
			parts = append(parts, fmt.Sprintf("%d%%", percent))
		}
		if s.sessionCost > 0 {
			parts = append(parts, formatCost(s.sessionCost))
		}
		right = dim.Render(strings.Join(parts, " "))
	}

	spacer := s.width - lipgloss.Width(left) - lipgloss.Width(right)
	if spacer < 0 {
		spacer = 0
	}

	return left + strings.Repeat(" ", spacer) + right
}

func (s *StatusBar) confirming() bool {
	return s.isConfirming
}

func (s *StatusBar) SetConfirming(c bool) {
	s.isConfirming = c
}

func (s *StatusBar) visibleAsyncCommands() []fileio.AsyncCmdInfo {
	var visible []fileio.AsyncCmdInfo
	for _, c := range s.asyncCommands {
		if c.Status != fileio.AsyncRunning && time.Since(c.EndedAt) > 15*time.Second {
			continue
		}
		visible = append(visible, c)
	}
	return visible
}

func (s *StatusBar) RenderAsyncBar() []string {
	visible := s.visibleAsyncCommands()
	if len(visible) == 0 {
		return nil
	}
	dim := lipgloss.NewStyle().Foreground(s.theme.Colors.Muted)
	var lines []string

	for _, c := range visible {
		var dot string
		var dotColor lipgloss.Color
		switch c.Status {
		case fileio.AsyncRunning:
			if s.spinFrame%2 == 0 {
				dot = "●"
			} else {
				dot = "○"
			}
			dotColor = s.theme.Colors.Accent2
		case fileio.AsyncCompleted:
			dot = "✓"
			dotColor = s.theme.Colors.Success
		case fileio.AsyncFailed:
			dot = "✗"
			dotColor = s.theme.Colors.Error
		case fileio.AsyncKilled:
			dot = "!"
			dotColor = s.theme.Colors.Warning
		default:
			dot = "?"
			dotColor = s.theme.Colors.Muted
		}
		cmd := c.Command
		if len(cmd) > 40 {
			cmd = cmd[:37] + "…"
		}
		var durStr string
		durStr = c.Duration.Truncate(time.Second).String()
		statusLine := dim.Render("│ ") +
			lipgloss.NewStyle().Foreground(dotColor).Render(dot+" ") +
			lipgloss.NewStyle().Foreground(s.theme.Colors.Accent2).Render("["+c.Handle+"]") + " " +
			dim.Render(cmd) + "  " +
			dim.Render(durStr)
		lines = append(lines, statusLine)
	}
	return lines
}

func (s *StatusBar) AsyncBarHeight() int {
	return len(s.visibleAsyncCommands())
}

func (s *StatusBar) SetSpinFrame(frame int) {
	s.spinFrame = frame
}

func (s *StatusBar) SetError(err string) {
	s.lastError = err
	s.errorTime = time.Now()
}

func (s *StatusBar) SetInfo(msg string) {
	s.infoMsg = msg
	s.infoMsgTime = time.Now()
}

func (s *StatusBar) SetRetry(msg string) {
	s.retryMsg = msg
	s.retryTime = time.Now()
}

func (s *StatusBar) ClearExpired() {
	if time.Since(s.errorTime) > 8*time.Second {
		s.lastError = ""
	}
	if time.Since(s.infoMsgTime) > 5*time.Second {
		s.infoMsg = ""
	}
	if time.Since(s.retryTime) > 30*time.Second {
		s.retryMsg = ""
	}
}

func (s *StatusBar) UpdateUsage(prompt, completion, cached int, cost float64, ctxWindow, ctxUsed int) {
	s.tokenPrompt = prompt
	s.tokenCompletion = completion
	s.tokenCached = cached
	s.sessionCost = cost
	s.contextWindow = ctxWindow
	s.contextUsed = ctxUsed
}

func (s *StatusBar) SetThinking(thinking, streaming, blocking bool) {
	s.thinking = thinking
	s.streaming = streaming
	s.blocking = blocking
}

func (s *StatusBar) SetPhase(phase string) {
	s.statusPhase = phase
	switch phase {
	case "grounding":
		s.spinner.SetPhase(widget.PhaseGrounding)
	case "planning":
		s.spinner.SetPhase(widget.PhasePlanning)
	case "executing":
		s.spinner.SetPhase(widget.PhaseExecuting)
	default:
		s.spinner.SetPhase(widget.PhaseDefault)
	}
}

func (s *StatusBar) SetWidth(w int) {
	s.width = w
}

func (s *StatusBar) SetAsyncCommands(cmds []fileio.AsyncCmdInfo) {
	s.asyncCommands = cmds
}

func formatTokens(n int) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func formatCost(cost float64) string {
	switch {
	case cost >= 1.0:
		return fmt.Sprintf("$%.2f", cost)
	case cost >= 0.01:
		return fmt.Sprintf("$%.4f", cost)
	case cost >= 0.0001:
		return fmt.Sprintf("$%.5f", cost)
	case cost > 0:
		return fmt.Sprintf("$%.6f", cost)
	default:
		return ""
	}
}
