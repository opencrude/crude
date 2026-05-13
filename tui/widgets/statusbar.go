package widgets

import (
	"fmt"
	"strings"
	"time"

	"crude/fileio"
	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/theme"
	"github.com/charmbracelet/lipgloss"
)

type StatusBar struct {
	theme  *theme.Theme
	width  int

	thinking, streaming, blocking bool
	isConfirming                   bool
	spinner    components.Spinner
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

func NewStatusBar(t *theme.Theme) *StatusBar {
	return &StatusBar{theme: t, spinner: components.NewSpinner(t)}
}

func (s *StatusBar) View() string {
	dim := lipgloss.NewStyle().Foreground(s.theme.Colors.Muted)
	pad := strings.Repeat(" ", s.theme.Spacing.PadLR)

	var left string
	if s.infoMsg != "" {
		left = lipgloss.NewStyle().Foreground(s.theme.Colors.Success).Render(
			rendering.TruncateLine(s.infoMsg, s.width/2))
	} else if s.retryMsg != "" && time.Since(s.retryTime) < 30*time.Second {
		left = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Render(
			rendering.TruncateLine(s.retryMsg, s.width/2))
	} else if s.lastError != "" {
		left = s.theme.Styles.ErrorText.Render(
			rendering.TruncateLine(s.lastError, s.width/2))
	} else if s.confirming() {
		left = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Render("⚠ confirming")
	} else if s.blocking {
		left = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Render("⚠ awaiting input")
	} else if s.streaming {
		left = s.spinner.StreamAnimation()
	} else if s.thinking {
		left = s.spinner.WorkAnimation()
	} else {
		left = ""
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
			percent = int(float64(s.contextUsed) / float64(s.contextWindow) * 100)
			parts = append(parts, fmt.Sprintf("%d%%", percent))
		}
		if s.sessionCost > 0 {
			parts = append(parts, formatCost(s.sessionCost))
		}
		right = dim.Render(strings.Join(parts, " "))
	} else {
		right = dim.Render("ready")
	}

	spacer := s.width - s.theme.Spacing.PadLR - lipgloss.Width(left) - lipgloss.Width(right) - s.theme.Spacing.PadLR
	if spacer < 0 {
		spacer = 0
	}

	return left + strings.Repeat(" ", spacer) + right + pad
}

func (s *StatusBar) confirming() bool {
	return s.isConfirming
}

func (s *StatusBar) SetConfirming(c bool) {
	s.isConfirming = c
}

func (s *StatusBar) SetSpinFrame(frame int) {
	s.spinFrame = frame
	s.spinner.SetFrame(frame)
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

func (s *StatusBar) ClearRetry() {
	s.retryMsg = ""
	s.retryTime = time.Time{}
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
		s.spinner.SetPhase(components.PhaseGrounding)
	case "planning":
		s.spinner.SetPhase(components.PhasePlanning)
	case "executing":
		s.spinner.SetPhase(components.PhaseExecuting)
	default:
		s.spinner.SetPhase(components.PhaseDefault)
	}
}

func (s *StatusBar) SetWidth(w int) {
	s.width = w
}

func (s *StatusBar) SetStatusLine(line string) {
	s.statusLine = line
}

func (s *StatusBar) StatusLine() string {
	return s.statusLine
}

func (s *StatusBar) Phase() string {
	return s.statusPhase
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
