package tools

import (
	"fmt"
	"strings"

	ccrude "crude/tools"
	"crude/tui/rendering"
	"crude/tui/types"
)

// ToolTitle keeps presentation roles separate from their text. Consumers must
// not search a formatted label to rediscover commands, paths, counts or times.
type ToolTitle struct {
	Action, Operand, Metadata string
	Failed                    bool
}

func (t ToolTitle) Text() string {
	parts := make([]string, 0, 3)
	for _, part := range []string{t.Action, t.Operand, t.Metadata} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, " ")
}

// Verbs vary with lifecycle; the action, operand and metadata roles do not.
var toolVerbs = map[string][3]string{
	"read_file":               {"Preparing to read", "Reading", "Read"},
	"exec_command":            {"Preparing command", "Running", "Ran"},
	"execute_command":         {"Preparing command", "Running", "Ran"},
	"async_execute_command":   {"Preparing command", "Running", "Started"},
	"write_stdin":             {"Preparing input", "Sending input", "Read output"},
	"write_file":              {"Preparing to write", "Writing", "Wrote"},
	"edit_file":               {"Preparing changes", "Editing", "Edited"},
	"apply_patch":             {"Preparing changes", "Editing", "Edited"},
	"delete_file":             {"Preparing to delete", "Deleting", "Deleted"},
	"move_file":               {"Preparing to move", "Moving", "Moved"},
	"copy_file":               {"Preparing to copy", "Copying", "Copied"},
	"grep_files":              {"Preparing search", "Searching", "Searched"},
	"glob_files":              {"Preparing file listing", "Finding", "Found files matching"},
	"list_directory":          {"Preparing file listing", "Listing", "Listed"},
	"web_search":              {"Preparing search", "Searching", "Searched"},
	"web_fetch":               {"Preparing fetch", "Fetching", "Fetched"},
	"arxiv_search":            {"Preparing search", "Searching ArXiv", "ArXiv"},
	"wait":                    {"Preparing wait", "Waiting", "Waited"},
	"check_async_command":     {"Preparing status check", "Checking", "Checked"},
	"read_async_output":       {"Preparing output read", "Reading output", "Read output"},
	"kill_async_command":      {"Preparing to stop command", "Stopping command", "Stopped command"},
	"update_plan":             {"Preparing plan update", "Updating plan", "Updated plan"},
	"get_plan":                {"Preparing plan read", "Reading plan", "Read plan"},
	"run_sub_agent":           {"Preparing sub-agent", "Running sub-agent", "Started sub-agent"},
	"poll_sub_agent":          {"Preparing status check", "Polling", "Polled"},
	"cancel_sub_agent":        {"Preparing to stop sub-agent", "Stopping sub-agent", "Stopped sub-agent"},
	"list_sub_agents":         {"Preparing agent list", "Listing sub-agents", "Listed sub-agents"},
	"write_report":            {"Preparing report", "Writing report", "Wrote report"},
	"multi_tool_use.parallel": {"Preparing", "Running", "Ran"},
	"auto_allowed":            {"Preparing approval", "Approving", "Auto-allowed"},
}

func BuildToolTitle(msg types.ChatMessage, phase string) ToolTitle {
	msg.ToolName = ccrude.DisplayToolName(msg.ToolName)
	target := msg.ToolProgressLabel
	if structuredToolLabel(target) {
		target = SummarizeToolArgs(msg.ToolName, target)
	}
	if target == "" {
		target = msg.ToolArgs
		if structuredToolLabel(target) {
			target = SummarizeToolArgs(msg.ToolName, target)
		}
	}
	if msg.ToolCommand != "" {
		target = msg.ToolCommand
		if structuredToolLabel(target) {
			target = SummarizeToolArgs(msg.ToolName, target)
		}
	}
	if msg.ToolName == "read_file" && msg.ToolStatus == types.ToolStatusSuccess && msg.ToolArgs != "" {
		target = msg.ToolArgs
		if structuredToolLabel(target) {
			target = SummarizeToolArgs(msg.ToolName, target)
		}
	}
	if msg.ToolName == "run_sub_agent" && (msg.ToolStatus == types.ToolStatusSuccess || msg.ToolStatus == types.ToolStatusError || msg.ToolStatus == types.ToolStatusCancelled) {
		if outcome, ok := subAgentOutcome(msg.Content); ok {
			if outcome.name != "" || outcome.id != "" {
				target = subAgentIdentityLabel(outcome.name, outcome.id)
			}
			duration := ""
			if msg.ToolDuration > 0 {
				duration = rendering.FormatDuration(msg.ToolDuration)
			}
			switch outcome.status {
			case "running":
				return ToolTitle{Action: "Started sub-agent", Operand: target, Metadata: duration}
			case "completed":
				return ToolTitle{Action: "Sub-agent completed", Operand: target, Metadata: duration}
			case "cancelled":
				return ToolTitle{Action: "Sub-agent cancelled", Operand: target, Metadata: duration}
			case "failed_timeout":
				// Also describes recorded results from sessions created before
				// task deadlines were removed.
				return ToolTitle{Action: "Sub-agent timed out", Operand: target, Metadata: duration, Failed: true}
			default:
				return ToolTitle{Action: "Sub-agent failed", Operand: target, Metadata: duration, Failed: true}
			}
		}
	}
	if msg.ToolName == "poll_sub_agent" || msg.ToolName == "cancel_sub_agent" {
		id := SummarizeToolArgs(msg.ToolName, msg.ToolArgs)
		if id == "" {
			id = target
		}
		outcome, _ := subAgentOutcome(msg.Content)
		if outcome.id != "" && id != "" && outcome.id != id {
			outcome.name = "" // a mismatched result cannot label this handle
		}
		if id == "" {
			id = outcome.id
		}
		target = subAgentIdentityLabel(outcome.name, id)
	}
	if msg.ToolName == "write_report" {
		if outcome, ok := subAgentOutcome(msg.Content); ok && outcome.status == "report_written" {
			if outcome.name != "" || outcome.id != "" {
				target = subAgentIdentityLabel(outcome.name, outcome.id)
			}
		}
	}
	if msg.ToolInput != "" {
		switch msg.ToolName {
		case "grep_files":
			target = "\"" + msg.ToolInput + "\""
			if msg.ToolScope != "" {
				target += " in " + msg.ToolScope
			}
		case "web_search", "web_fetch", "arxiv_search":
			target = msg.ToolInput
		}
	}
	target = strings.ReplaceAll(target, "\n", " ")
	duration := ""
	if msg.ToolDuration > 0 {
		duration = rendering.FormatDuration(msg.ToolDuration)
	}
	if msg.ToolStatus == types.ToolStatusCancelled {
		return ToolTitle{Action: "Stopped " + strings.ToLower(rendering.PascalCaseTool(msg.ToolName)), Operand: target, Metadata: duration}
	}
	if msg.ToolStatus == "interrupted" {
		return ToolTitle{Action: "Not run · " + strings.ToLower(rendering.PascalCaseTool(msg.ToolName)), Operand: target}
	}
	// A renderer's ambient phase is not evidence that this tool failed.
	failed := msg.ToolStatus == types.ToolStatusError || msg.ToolPreviewPhase == types.ToolPreviewError && msg.ToolStatus != types.ToolStatusSuccess
	if _, exit, session, ok := CommandResult(msg.ToolName, msg.Content); ok && (msg.ToolStatus == types.ToolStatusSuccess || failed) {
		title := ToolTitle{Action: "Ran", Operand: target, Metadata: duration}
		if msg.ToolName == "write_stdin" {
			title.Action = "Read output"
		}
		if exit != nil && *exit != 0 {
			title.Action = "Command failed"
			title.Metadata = strings.TrimSpace(fmt.Sprintf("(exit %d) %s", *exit, duration))
			title.Failed = true
		} else if failed {
			return errorTitle(msg.ToolName, target, duration, ToolErrorReason(msg.ToolName, msg.Content))
		} else if exit == nil && session > 0 && msg.ToolName != "write_stdin" {
			title.Action = "Started"
			title.Metadata = strings.TrimSpace(fmt.Sprintf("· session %d %s", session, duration))
		}
		return title
	}
	if failed {
		return errorTitle(msg.ToolName, target, duration, ToolErrorReason(msg.ToolName, msg.Content))
	}
	stage := 1
	if phase == types.ToolPreviewPreparing {
		stage = 0
	}
	if phase == types.ToolPreviewDone || msg.ToolStatus == types.ToolStatusSuccess {
		stage = 2
	}
	verbs, known := toolVerbs[msg.ToolName]
	if !known {
		name := rendering.PascalCaseTool(msg.ToolName)
		verbs = [3]string{"Preparing " + strings.ToLower(name), "Running " + name, name}
	}
	title := ToolTitle{Action: verbs[stage], Operand: target}
	if stage == 2 {
		title.Metadata = duration
	}
	if stage == 0 && target != "" {
		title.Action += " ·"
	}
	if (msg.ToolName == "web_search" || msg.ToolName == "arxiv_search") && target != "" && stage > 0 {
		title.Operand = "\"" + target + "\""
	}
	if msg.ToolName == "multi_tool_use.parallel" && target == "" {
		title.Operand = "tools"
	}
	if stage == 2 && msg.ToolName == "read_file" && msg.Content != "" {
		title.Metadata = strings.TrimSpace(readResultMetadata(msg.Content) + " " + duration)
	}
	if stage > 0 {
		counts := ""
		switch msg.ToolName {
		case "edit_file", "apply_patch":
			counts = fmt.Sprintf("-%s +%s", rendering.FormatCount(msg.ToolLivedRemoved), rendering.FormatCount(msg.ToolLivedAdded))
		case "write_file", "write_report":
			counts = "+" + rendering.FormatCount(msg.ToolLivedAdded)
		}
		if counts != "" {
			tokens := msg.ToolEstTokens
			if tokens > 0 {
				counts += " ↓" + rendering.FormatTokens(tokens)
			}
			title.Metadata = strings.TrimSpace(counts + " " + title.Metadata)
		}
	}
	return title
}

func structuredToolLabel(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[")
}

func embeddedStructuredLabel(value string) bool {
	return strings.Contains(value, "{") && strings.Contains(value, `"`)
}

func errorTitle(tool, target, duration, reason string) ToolTitle {
	title := ToolTitle{Action: rendering.PascalCaseTool(tool) + " failed", Operand: target, Metadata: duration, Failed: true}
	switch tool {
	case "multi_tool_use.parallel":
		title.Action = "Tool batch failed"
	case "run_sub_agent":
		title.Action = "Failed to spawn sub-agent"
	case "exec_command", "write_stdin", "execute_command", "async_execute_command":
		title.Action = "Command failed"
	case "question":
		title.Action = "Question failed"
	case "update_plan":
		title.Action = "Update plan failed"
	case "get_plan":
		title.Action = "Read plan failed"
	}
	if reason != "" && !structuredToolLabel(reason) && !embeddedStructuredLabel(reason) {
		title.Metadata = strings.TrimSpace(title.Metadata + " — " + reason)
	}
	return title
}

func BuildErrorLabel(tool, target, duration string) string {
	return BuildErrorLabelWithReason(tool, target, duration, "")
}
func BuildErrorLabelWithReason(tool, target, duration, reason string) string {
	return errorTitle(tool, target, duration, reason).Text()
}

func IsPlanTool(name string) bool { return name == "update_plan" || name == "get_plan" }
