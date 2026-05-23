package tools

import (
	"crude/tui/rendering"
	"crude/tui/types"
	"fmt"
)

func BuildToolTitle(msg types.ChatMessage, phase string) string {
	target := msg.ToolProgressLabel
	if target == "" {
		target = msg.ToolArgs
	}

	switch phase {
	case types.ToolPreviewPreparing:
		return "Preparing " + rendering.PascalCaseTool(msg.ToolName)
	case types.ToolPreviewStreaming:
		return BuildStreamingLabel(msg.ToolName, target, msg.ToolLivedAdded, msg.ToolLivedRemoved, msg.ToolEstTokens)
	case types.ToolPreviewDone:
		dur := rendering.FormatDuration(msg.ToolDuration)
		if msg.ToolStatus == "error" {
			label := rendering.PascalCaseTool(msg.ToolName)
			if target != "" {
				label += " " + target
			}
			label += " failed"
			if dur != "" {
				label += " " + dur
			}
			return label
		}
		if msg.ToolStatus == types.ToolStatusCancelled {
			label := rendering.PascalCaseTool(msg.ToolName)
			if target != "" {
				label += " " + target
			}
			label += " cancelled"
			return label
		}
		return BuildDoneLabel(msg.ToolName, target, msg.ToolLivedAdded, msg.ToolLivedRemoved, msg.ToolEstTokens, dur)
	default:
		if msg.ToolStatus == "success" || msg.ToolStatus == "error" {
			dur := rendering.FormatDuration(msg.ToolDuration)
			return BuildDoneLabel(msg.ToolName, target, msg.ToolLivedAdded, msg.ToolLivedRemoved, msg.ToolEstTokens, dur)
		}
		if target != "" {
			return rendering.PascalCaseTool(msg.ToolName) + " " + target
		}
		return rendering.PascalCaseTool(msg.ToolName)
	}
}

func BuildStreamingLabel(tool, target string, added, removed, tokens int) string {
	switch tool {
	case "write_file":
		if target != "" {
			return fmt.Sprintf("Writing %s +%s ↓%s", target, rendering.FormatCount(added), rendering.FormatTokens(tokens))
		}
		return fmt.Sprintf("Writing ↓%s", rendering.FormatTokens(tokens))
	case "edit_file":
		if target != "" {
			return fmt.Sprintf("Editing %s -%s +%s ↓%s", target, rendering.FormatCount(removed), rendering.FormatCount(added), rendering.FormatTokens(tokens))
		}
		return fmt.Sprintf("Editing -%s +%s ↓%s", rendering.FormatCount(removed), rendering.FormatCount(added), rendering.FormatTokens(tokens))
	case "read_file":
		return fmt.Sprintf("Reading %s", target)
	case "execute_command":
		if target != "" {
			return fmt.Sprintf("Running %s", rendering.TruncateLine(target, 50))
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
			return fmt.Sprintf("Searching \"%s\"", rendering.TruncateLine(target, 40))
		}
		return "Searching web"
	case "web_fetch":
		if target != "" {
			return fmt.Sprintf("Fetching %s", rendering.TruncateLine(target, 50))
		}
		return "Fetching URL"
	case "grep_files":
		if target != "" {
			return fmt.Sprintf("Grepping %s", rendering.TruncateLine(target, 50))
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
			return fmt.Sprintf("Searching ArXiv \"%s\"", rendering.TruncateLine(target, 40))
		}
		return "Searching ArXiv"
	case "async_execute_command":
		if target != "" {
			return fmt.Sprintf("Running %s", rendering.TruncateLine(target, 50))
		}
		return "Running background command"
	case "check_async_command":
		if target != "" {
			return fmt.Sprintf("Checking %s", target)
		}
		return "Checking async command"
	case "kill_async_command":
		if target != "" {
			return fmt.Sprintf("Killing %s", target)
		}
		return "Killing async command"
	case "read_async_output":
		if target != "" {
			return fmt.Sprintf("Reading output %s", target)
		}
		return "Reading async output"
	case "wait":
		if target != "" {
			return fmt.Sprintf("Waiting %s", target)
		}
		return "Waiting"
	case "plan", "list_goal_status":
		return fmt.Sprintf("Running %s", rendering.PascalCaseTool(tool))
	case "mark_step_doing":
		if target != "" {
			return fmt.Sprintf("Starting %s", target)
		}
		return "Starting step"
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
	case "run_sub_agent":
		if target != "" {
			return fmt.Sprintf("Spawning %s", rendering.TruncateLine(target, 40))
		}
		return "Spawning sub-agent"
	case "poll_sub_agent":
		if target != "" {
			shortID := target
			if len(shortID) > 12 {
				shortID = shortID[:12] + "…"
			}
			return fmt.Sprintf("Polling %s", shortID)
		}
		return "Polling sub-agent"
	case "cancel_sub_agent":
		if target != "" {
			return fmt.Sprintf("Cancelling %s", target)
		}
		return "Cancelling sub-agent"
	case "list_sub_agents":
		return "Listing sub-agents"
	case "write_report":
		if added > 0 {
			return fmt.Sprintf("Writing report +%s ↓%s", rendering.FormatCount(added), rendering.FormatTokens(tokens))
		}
		return fmt.Sprintf("Writing report ↓%s", rendering.FormatTokens(tokens))
	default:
		if target != "" {
			return fmt.Sprintf("Running %s %s", rendering.PascalCaseTool(tool), target)
		}
		return fmt.Sprintf("Running %s", rendering.PascalCaseTool(tool))
	}
}

func BuildDoneLabel(tool, target string, added, removed, tokens int, duration string) string {
	switch tool {
	case "write_file":
		label := "Wrote"
		if target != "" {
			label += " " + target
		}
		label += fmt.Sprintf(" +%s", rendering.FormatCount(added))
		if tokens > 0 {
			label += fmt.Sprintf(" ↓%s", rendering.FormatTokens(tokens))
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
		label += fmt.Sprintf(" -%s +%s", rendering.FormatCount(removed), rendering.FormatCount(added))
		if tokens > 0 {
			label += fmt.Sprintf(" ↓%s", rendering.FormatTokens(tokens))
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "read_file":
		return fmt.Sprintf("Read %s", target)
	case "execute_command":
		return fmt.Sprintf("Ran %s %s", rendering.TruncateLine(target, 50), duration)
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
			label += fmt.Sprintf(" \"%s\"", rendering.TruncateLine(target, 40))
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "web_fetch":
		label := "Fetched"
		if target != "" {
			label += " " + rendering.TruncateLine(target, 50)
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "grep_files":
		label := "Grepped"
		if target != "" {
			label += " " + rendering.TruncateLine(target, 50)
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
			label += fmt.Sprintf(" \"%s\"", rendering.TruncateLine(target, 40))
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "async_execute_command":
		label := "Started"
		if target != "" {
			label += " " + rendering.TruncateLine(target, 50)
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "check_async_command":
		label := "Checked"
		if target != "" {
			label += " " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "kill_async_command":
		label := "Killed"
		if target != "" {
			label += " " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "read_async_output":
		label := "Read output"
		if target != "" {
			label += " " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "wait":
		label := "Waited"
		if target != "" {
			label += " " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "plan", "list_goal_status":
		return rendering.PascalCaseTool(tool)
	case "mark_step_doing":
		label := "Started"
		if target != "" {
			label += " " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
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
	case "auto_allowed":
		label := "✓ auto-allowed"
		if target != "" {
			label += " " + target
		}
		return label
	case "run_sub_agent":
		label := "Spawned"
		if target != "" {
			label += " " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "poll_sub_agent":
		if target != "" {
			return "Polled " + target
		}
		return "Polled sub-agent"
	case "cancel_sub_agent":
		return "Cancelled " + target
	case "list_sub_agents":
		return "Listed sub-agents"
	case "write_report":
		label := "Wrote"
		if target != "" {
			label += " " + target
		}
		label += fmt.Sprintf(" +%s", rendering.FormatCount(added))
		if tokens > 0 {
			label += fmt.Sprintf(" ↓%s", rendering.FormatTokens(tokens))
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	default:
		label := rendering.PascalCaseTool(tool)
		if target != "" {
			label += " " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	}
}

func IsPlanTool(name string) bool {
	switch name {
	case "plan", "mark_step_doing", "mark_step_complete", "mark_step_failed", "report_blocker", "resolve_blocker", "list_goal_status":
		return true
	default:
		return false
	}
}
