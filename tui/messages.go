package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type slashCmd struct {
	name  string
	label string
	desc  string
	icon  string
}

var availableCmds = []slashCmd{
	{name: "/settings", label: "settings", desc: "Open general settings"},
	{name: "/provider", label: "provider", desc: "Select provider"},
	{name: "/model", label: "model", desc: "Select model"},
	{name: "/embed-model", label: "embed model", desc: "Select embedding model"},
	{name: "/api-key", label: "api key", desc: "Set API key"},
	{name: "/backend", label: "backend", desc: "Toggle local / ssh"},
	{name: "/help", label: "help", desc: "Show available commands"},
}

func (m *Model) updateSlashFilter() {
	m.slashFiltered = nil
	m.slashIdx = 0
	s := strings.ToLower(m.slashFilter)
	for _, c := range availableCmds {
		if strings.HasPrefix(c.name, s) || strings.Contains(strings.ToLower(c.desc), s) {
			m.slashFiltered = append(m.slashFiltered, c)
		}
	}
}

func formatSettingsSummary(s Settings) string {
	var b strings.Builder
	b.WriteString("```\n")
	b.WriteString("settings\n")
	b.WriteString(fmt.Sprintf("  provider      %s\n", valueOr(s.ProviderID, "-")))
	b.WriteString(fmt.Sprintf("  base url      %s\n", valueOr(s.ProviderURL, "-")))
	b.WriteString(fmt.Sprintf("  api key       %s\n", maskOr(s.APIKey, "-")))
	b.WriteString(fmt.Sprintf("  model         %s\n", valueOr(s.Model, "-")))
	b.WriteString(fmt.Sprintf("  embed model   %s\n", valueOr(s.EmbedModel, "-")))
	b.WriteString(fmt.Sprintf("  backend       %s\n", valueOr(s.Backend, "-")))
	if s.Backend == "ssh" {
		b.WriteString("\n  ssh\n")
		b.WriteString(fmt.Sprintf("  host          %s\n", valueOr(s.SSHHost, "-")))
		b.WriteString(fmt.Sprintf("  port          %s\n", valueOr(s.SSHPort, "-")))
		b.WriteString(fmt.Sprintf("  user          %s\n", valueOr(s.SSHUser, "-")))
		b.WriteString(fmt.Sprintf("  password      %s\n", maskOr(s.SSHPassword, "-")))
		b.WriteString(fmt.Sprintf("  key file      %s\n", valueOr(s.SSHKeyFile, "-")))
	}
	b.WriteString("```")
	return b.String()
}

func valueOr(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	return s
}

func maskOr(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	return strings.Repeat("•", len(s))
}

// formatTokens formats a token count into a compact human-readable string.
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

// summarizeToolArgs extracts a compact, user-friendly summary from tool args JSON.
func summarizeToolArgs(tool, argsJSON string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return truncateLine(argsJSON, 60)
	}

	switch tool {
	case "read_file", "write_file", "delete_file":
		if p, ok := m["path"].(string); ok {
			return p
		}
	case "edit_file":
		p, _ := m["path"].(string)
		return p
	case "list_directory":
		if p, ok := m["path"].(string); ok {
			return p
		}
		return "."
	case "glob_files":
		if p, ok := m["pattern"].(string); ok {
			return p
		}
	case "grep_files":
		pat, _ := m["pattern"].(string)
		path, _ := m["path"].(string)
		if path != "" {
			return fmt.Sprintf("%s in %s", pat, path)
		}
		return pat
	case "execute_command":
		if cmd, ok := m["command"].(string); ok {
			return truncateLine(cmd, 60)
		}
	case "web_search":
		if q, ok := m["query"].(string); ok {
			return truncateLine(q, 60)
		}
	case "web_fetch":
		if u, ok := m["url"].(string); ok {
			return truncateLine(u, 60)
		}
	case "arxiv_search":
		if q, ok := m["query"].(string); ok {
			return truncateLine(q, 60)
		}
	case "run_javascript":
		if code, ok := m["code"].(string); ok {
			lines := strings.Split(code, "\n")
			if len(lines) > 1 {
				return truncateLine(lines[0], 60)
			}
			return truncateLine(code, 60)
		}
	case "reply", "blocking":
		return ""
	}

	// Fallback: show first string value found
	for _, v := range m {
		if s, ok := v.(string); ok {
			return truncateLine(s, 60)
		}
	}
	return ""
}

// toolLabel returns a compact display string for a tool invocation.
func toolLabel(tool, argsSummary string) string {
	if argsSummary != "" {
		return fmt.Sprintf("%s %s", tool, argsSummary)
	}
	return tool
}

// pascalCaseTool converts a snake_case tool name to PascalCase for display.
func pascalCaseTool(name string) string {
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// workAnimation returns a fun animated string for the status bar when agent is working.
func workAnimation(frame int, phase string) string {
	phaseLabel := phase
	switch phase {
	case "grounding":
		phaseLabel = "gathering context"
	case "planning":
		phaseLabel = "planning"
	case "executing":
		phaseLabel = "executing"
	default:
		phaseLabel = "thinking"
	}

	spinColor := spinnerColorDefault
	switch phase {
	case "grounding":
		spinColor = spinnerColorGrounding
	case "planning":
		spinColor = spinnerColorPlanning
	case "executing":
		spinColor = spinnerColorExecuting
	}

	spinStyle := lipgloss.NewStyle().Foreground(spinColor)
	dots := strings.Repeat(".", frame%4)
	return spinStyle.Render(spinnerFrames[frame%len(spinnerFrames)]+" ") +
		lipgloss.NewStyle().Foreground(colorMainText).Render(phaseLabel) +
		lipgloss.NewStyle().Foreground(colorMuted).Render(dots)
}

// streamAnimation returns a streaming indicator for the status bar.
func streamAnimation(frame int, phase string) string {
	phaseLabel := phase
	switch phase {
	case "replying":
		phaseLabel = "replying"
	case "executing":
		phaseLabel = "streaming response"
	default:
		phaseLabel = "streaming"
	}

	spinStyle := lipgloss.NewStyle().Foreground(spinnerColorPlanning)
	cursor := "█"
	if frame%2 != 0 {
		cursor = " "
	}
	return spinStyle.Render("●"+cursor+" ") +
		lipgloss.NewStyle().Foreground(colorMainText).Render(phaseLabel)
}
