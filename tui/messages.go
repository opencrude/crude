package tui

import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"

	ttypes "crude/tui/types"
)

type tickMsg = ttypes.TickMsg
type errorMsg = ttypes.ErrorMsg
type statusMsg = ttypes.StatusMsg
type agentMsg = ttypes.AgentMsg
type openSettingsMsg = ttypes.OpenSettingsMsg
type fetchModelsMsg = ttypes.FetchModelsMsg
type saveSettingsMsg = ttypes.SaveSettingsMsg
type updateModelMsg = ttypes.UpdateModelMsg
type selectionTickMsg = ttypes.SelectionTickMsg
type clipboardCopiedMsg = ttypes.ClipboardCopiedMsg
type clipboardErrorMsg = ttypes.ClipboardErrorMsg

func formatSettingsSummary(s ttypes.Settings) string {
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

func toolLabel(tool, argsSummary string) string {
	if argsSummary != "" {
		return fmt.Sprintf("%s %s", tool, argsSummary)
	}
	return tool
}

func pascalCaseTool(name string) string {
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func truncateLine(s string, max int) string {
	if runewidth.StringWidth(s) <= max {
		return s
	}
	return runewidth.Truncate(s, max, "…")
}
