package tui

import (
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
	{name: "/oracle-model", label: "oracle model", desc: "Select oracle (reasoning) model"},
	{name: "/api-key", label: "api key", desc: "Set API key"},
	{name: "/effort", label: "effort", desc: "Cycle thinking effort (auto/low/medium/high)"},
	{name: "/undo", label: "undo", desc: "Undo last file operation"},
	{name: "/undo-n", label: "undo N", desc: "Undo last N file operations"},
	{name: "/undo-all", label: "undo all", desc: "Undo all file operations this session"},
	{name: "/changes", label: "changes", desc: "Show files changed this session"},
	{name: "/help", label: "help", desc: "Show available commands"},
}

func (m *Model) updateSlashFilter() {
	m.slashFiltered = nil
	m.slashIdx = 0
	m.slashListOffset = 0
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
	b.WriteString(fmt.Sprintf("  oracle model  %s\n", valueOr(s.OracleModel, "-")))
	b.WriteString(fmt.Sprintf("  thinking      %s\n", valueOr(s.ThinkingEffort, "auto")))
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

// summarizeToolArgs extracts a compact, user-friendly summary from tool args.
func summarizeToolArgs(tool, argsStr string) string {
	path := extractArg(argsStr, "path")
	if path != "" {
		return path
	}
	if pattern := extractArg(argsStr, "pattern"); pattern != "" {
		return pattern
	}
	if command := extractArg(argsStr, "command"); command != "" {
		return truncateLine(command, 50)
	}
	if content := extractArg(argsStr, "content"); content != "" {
		return truncateLine(content, 50) + " (content)"
	}
	if query := extractArg(argsStr, "query"); query != "" {
		return truncateLine(query, 50)
	}
	if url := extractArg(argsStr, "url"); url != "" {
		return truncateLine(url, 50)
	}
	if source := extractArg(argsStr, "source"); source != "" {
		dest := extractArg(argsStr, "destination")
		if dest != "" {
			return fmt.Sprintf("%s → %s", source, dest)
		}
		return source
	}
	if code := extractArg(argsStr, "code"); code != "" {
		return truncateLine(code, 50)
	}
	return truncateLine(argsStr, 60)
}

func extractArg(str, key string) string {
	// Try Go map syntax: map[path:main.go] or {path: main.go}
	prefixes := []string{key + ":", key + ": "}
	for _, prefix := range prefixes {
		idx := strings.Index(str, prefix)
		if idx == -1 {
			continue
		}
		start := idx + len(prefix)
		if start >= len(str) {
			return ""
		}
		end := strings.IndexAny(str[start:], " ,}]")
		if end == -1 {
			return strings.TrimSpace(str[start:])
		}
		return strings.TrimSpace(str[start : start+end])
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
