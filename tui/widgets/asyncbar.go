package widgets

import (
	"time"

	"crude/fileio"
	"crude/tui/components"
	"github.com/charmbracelet/lipgloss"
)

func (s *StatusBar) VisibleAsyncCommands() []fileio.AsyncCmdInfo {
	var visible []fileio.AsyncCmdInfo
	for _, c := range s.asyncCommands {
		if c.Status != fileio.AsyncRunning && time.Since(c.EndedAt) > 15*time.Second {
			continue
		}
		visible = append(visible, c)
	}
	return visible
}

func (s *StatusBar) visibleAsyncCommands() []fileio.AsyncCmdInfo {
	return s.VisibleAsyncCommands()
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
			if components.BlinkOn(s.spinFrame) {
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

func (s *StatusBar) SetAsyncCommands(cmds []fileio.AsyncCmdInfo) {
	s.asyncCommands = cmds
}
