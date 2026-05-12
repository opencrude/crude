package tui

import (
	"fmt"
	"strings"
	"time"

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
	{name: "/effort", label: "effort", desc: "Cycle thinking effort (none/low/medium/high/xhigh)"},
	{name: "/undo", label: "undo", desc: "Undo last file operation"},
	{name: "/undo-n", label: "undo N", desc: "Undo last N file operations"},
	{name: "/undo-all", label: "undo all", desc: "Undo all file operations this session"},
	{name: "/changes", label: "changes", desc: "Show files changed this session"},
	{name: "/yolo", label: "yolo", desc: "Disable ALL safety checks (DANGEROUS)"},
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
	b.WriteString(fmt.Sprintf("  thinking      %s\n", valueOr(s.ThinkingEffort, "none")))
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

// formatCost formats a USD cost into a compact string.
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
	if tool == "plan" || tool == "list_goal_status" {
		return pascalCaseTool(tool)
	}
	if tool == "mark_step_complete" || tool == "mark_step_failed" {
		if stepID := extractArg(argsStr, "step_id"); stepID != "" {
			return stepID
		}
		return pascalCaseTool(tool)
	}
	if tool == "report_blocker" {
		if msg := extractArg(argsStr, "message"); msg != "" {
			return truncateLine(msg, 50)
		}
		return pascalCaseTool(tool)
	}
	if tool == "resolve_blocker" {
		return pascalCaseTool(tool)
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

func workAnimation(frame int, phase string) string {
	moon := spinnerPhases[frame%len(spinnerPhases)]

	var label string
	var color lipgloss.Color
	switch phase {
	case "grounding":
		label = "gathering context"
		color = spinnerColorGrounding
	case "planning":
		label = "planning"
		color = spinnerColorPlanning
	case "executing":
		label = "executing"
		color = spinnerColorExecuting
	default:
		label = "thinking"
		color = spinnerColorDefault
	}

	return lipgloss.NewStyle().Foreground(color).Render(moon+" ") +
		lipgloss.NewStyle().Foreground(colorMainText).Render(label)
}

func streamAnimation(frame int, phase string) string {
	moon := spinnerPhases[frame%len(spinnerPhases)]
	return lipgloss.NewStyle().Foreground(spinnerColorPlanning).Render(moon + " streaming")
}

func formatToolDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	secs := d.Seconds()
	if secs < 1.0 {
		return fmt.Sprintf("%.1fs", secs)
	}
	if secs < 60 {
		return fmt.Sprintf("%ds", int(secs))
	}
	mins := int(secs) / 60
	remainingSecs := int(secs) % 60
	return fmt.Sprintf("%dmin %dseconds", mins, remainingSecs)
}

func buildToolTitle(msg *ChatMessage) string {
	target := msg.ToolProgressLabel
	if target == "" {
		target = msg.ToolArgs
	}

	switch msg.ToolPreviewPhase {
	case ToolPreviewPreparing:
		return "Preparing " + pascalCaseTool(msg.ToolName)
	case ToolPreviewStreaming:
		return buildStreamingLabel(msg.ToolName, target, msg.ToolLivedAdded, msg.ToolLivedRemoved, msg.ToolEstTokens)
	case ToolPreviewDone:
		dur := formatToolDuration(msg.ToolDuration)
		if msg.ToolStatus == "error" {
			label := pascalCaseTool(msg.ToolName)
			if target != "" {
				label += " " + target
			}
			label += " failed"
			if dur != "" {
				label += " " + dur
			}
			return label
		}
		return buildDoneLabel(msg.ToolName, target, msg.ToolLivedAdded, msg.ToolLivedRemoved, msg.ToolEstTokens, dur)
	default:
		if msg.ToolStatus == "success" || msg.ToolStatus == "error" {
			dur := formatToolDuration(msg.ToolDuration)
			return buildDoneLabel(msg.ToolName, target, 0, 0, 0, dur)
		}
		if target != "" {
			return pascalCaseTool(msg.ToolName) + " " + target
		}
		return pascalCaseTool(msg.ToolName)
	}
}

func buildStreamingLabel(tool, target string, added, removed, tokens int) string {
	switch tool {
	case "write_file":
		if target != "" {
			return fmt.Sprintf("Writing %s +%d ↓%d", target, added, tokens)
		}
		return fmt.Sprintf("Writing ↓%d", tokens)
	case "edit_file":
		if target != "" {
			return fmt.Sprintf("Editing %s -%d +%d ↓%d", target, removed, added, tokens)
		}
		return fmt.Sprintf("Editing -%d +%d ↓%d", removed, added, tokens)
	case "read_file":
		return fmt.Sprintf("Reading %s", target)
	case "execute_command":
		if target != "" {
			return fmt.Sprintf("Running %s", truncateLine(target, 50))
		}
		return "Running command"
	case "delete_file":
		return fmt.Sprintf("Deleting %s", target)
	case "move_file":
		return fmt.Sprintf("Moving %s", target)
	case "copy_file":
		return fmt.Sprintf("Copying %s", target)
	case "web_search":
		if target != "" {
			return fmt.Sprintf("Searching \"%s\"", truncateLine(target, 40))
		}
		return "Searching web"
	case "web_fetch":
		if target != "" {
			return fmt.Sprintf("Fetching %s", truncateLine(target, 50))
		}
		return "Fetching URL"
	case "grep_files":
		if target != "" {
			return fmt.Sprintf("Grepping %s", truncateLine(target, 50))
		}
		return "Grepping files"
	case "glob_files":
		return fmt.Sprintf("Globbing %s", target)
	case "list_directory":
		return fmt.Sprintf("Listing %s", target)
	case "run_javascript":
		return "Running JavaScript"
	case "arxiv_search":
		if target != "" {
			return fmt.Sprintf("Searching ArXiv \"%s\"", truncateLine(target, 40))
		}
		return "Searching ArXiv"
	case "consult_oracle":
		return "Consulting oracle"
	case "plan", "list_goal_status":
		return fmt.Sprintf("Running %s", pascalCaseTool(tool))
	case "mark_step_complete":
		if target != "" {
			return fmt.Sprintf("Completing %s", target)
		}
		return "Completing step"
	case "mark_step_failed":
		if target != "" {
			return fmt.Sprintf("Failing %s", target)
		}
		return "Failing step"
	case "report_blocker":
		return "Reporting blocker"
	case "resolve_blocker":
		return "Resolving blocker"
	default:
		if target != "" {
			return fmt.Sprintf("Running %s %s", pascalCaseTool(tool), target)
		}
		return fmt.Sprintf("Running %s", pascalCaseTool(tool))
	}
}

func buildDoneLabel(tool, target string, added, removed, tokens int, duration string) string {
	switch tool {
	case "write_file":
		label := "Wrote"
		if target != "" {
			label += " " + target
		}
		label += fmt.Sprintf(" +%d", added)
		if tokens > 0 {
			label += fmt.Sprintf(" ↓%d", tokens)
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "edit_file":
		label := "Edited"
		if target != "" {
			label += " " + target
		}
		label += fmt.Sprintf(" -%d +%d", removed, added)
		if tokens > 0 {
			label += fmt.Sprintf(" ↓%d", tokens)
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "read_file":
		return fmt.Sprintf("Read %s", target)
	case "execute_command":
		return fmt.Sprintf("Ran %s %s", truncateLine(target, 50), duration)
	case "delete_file":
		label := "Deleted " + target
		if duration != "" {
			label += " " + duration
		}
		return label
	case "move_file":
		label := "Moved " + target
		if duration != "" {
			label += " " + duration
		}
		return label
	case "copy_file":
		label := "Copied " + target
		if duration != "" {
			label += " " + duration
		}
		return label
	case "web_search":
		label := "Searched"
		if target != "" {
			label += fmt.Sprintf(" \"%s\"", truncateLine(target, 40))
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "web_fetch":
		label := "Fetched"
		if target != "" {
			label += " " + truncateLine(target, 50)
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "grep_files":
		label := "Grepped"
		if target != "" {
			label += " " + truncateLine(target, 50)
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "glob_files":
		label := "Globbed"
		if target != "" {
			label += " " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "list_directory":
		label := "Listed"
		if target != "" {
			label += " " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "run_javascript":
		label := "Ran JavaScript"
		if duration != "" {
			label += " " + duration
		}
		return label
	case "arxiv_search":
		label := "ArXiv"
		if target != "" {
			label += fmt.Sprintf(" \"%s\"", truncateLine(target, 40))
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "consult_oracle":
		label := "Consulted oracle"
		if duration != "" {
			label += " " + duration
		}
		return label
	case "plan", "list_goal_status":
		return pascalCaseTool(tool)
	case "mark_step_complete":
		label := "Completed"
		if target != "" {
			label += " " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "mark_step_failed":
		label := "Failed"
		if target != "" {
			label += " " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "report_blocker":
		label := "Blocked"
		if target != "" {
			label += ": " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "resolve_blocker":
		label := "Resolved blocker"
		if duration != "" {
			label += " " + duration
		}
		return label
	default:
		label := pascalCaseTool(tool)
		if target != "" {
			label += " " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	}
}
