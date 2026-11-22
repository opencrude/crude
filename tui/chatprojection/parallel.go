package chatprojection

import (
	"encoding/json"
	"fmt"
	"strings"

	toolargs "crude/agent/tools"
	"crude/types/conversation"
)

// A batch is a provider envelope, not a second visual tool family. Derive
// ordinary tool leaves at source-change time and let the same tree, disclosure,
// diff, error and native-command renderers handle them.
func parallelMessages(msg *conversation.ChatMessage) []*conversation.ChatMessage {
	var input toolargs.ParallelInput
	var output toolargs.ParallelOutput
	_ = json.Unmarshal([]byte(msg.ToolInput), &input)
	_ = json.Unmarshal([]byte(msg.Content), &output)
	count := max(len(input.Uses), len(output.Results))
	roundID := activityRoundID(msg)
	stopped := msg.ToolStatus == conversation.ToolStatusSuccess || msg.ToolStatus == conversation.ToolStatusError ||
		msg.ToolStatus == conversation.ToolStatusCancelled || msg.ToolStatus == "interrupted"
	children := make([]*conversation.ChatMessage, 0, count)
	for i := 0; i < count; i++ {
		child := &conversation.ChatMessage{InternalID: fmt.Sprintf("%s:%d", msg.InternalID, i), ToolID: fmt.Sprintf("%s:%d", msg.ToolID, i), ToolRoundID: roundID, Role: conversation.RoleTool, Time: msg.Time, ToolStatus: "streaming", ToolPreviewPhase: conversation.ToolPreviewPreparing}
		if i < len(input.Uses) {
			use := input.Uses[i]
			child.ToolName = strings.TrimPrefix(use.Name, "functions.")
			args := string(use.Parameters)
			child.ToolArgs = toolargs.SummarizeArgs(child.ToolName, args)
			if toolargs.RetainsStructuredToolInput(child.ToolName) {
				child.ToolInput = args
			}
			switch child.ToolName {
			case "exec_command":
				child.ToolCommand = toolargs.ExtractArg(args, "cmd")
			case "execute_command", "async_execute_command":
				child.ToolCommand = toolargs.ExtractArg(args, "command")
			case "grep_files":
				child.ToolInput, child.ToolScope = toolargs.ExtractArg(args, "pattern"), toolargs.ExtractArg(args, "path")
			case "web_search", "arxiv_search":
				child.ToolInput = toolargs.ExtractArg(args, "query")
			case "web_fetch":
				child.ToolInput = toolargs.ExtractArg(args, "url")
			case "apply_patch":
				child.ToolArgs = toolargs.SummarizeArgs(child.ToolName, toolargs.ExtractArg(args, "input"))
			}
		}
		if i < len(output.Results) && output.Results[i] != nil {
			result := output.Results[i]
			child.ToolName = strings.TrimPrefix(result.Name, "functions.")
			child.Content, child.ToolLivedAdded, child.ToolLivedRemoved = result.Result, result.Added, result.Removed
			child.ToolStatus, child.ToolPreviewPhase = "success", conversation.ToolPreviewDone
			if result.Phase == "running" {
				child.ToolStatus, child.ToolPreviewPhase = "calling", conversation.ToolPreviewStreaming
				if stopped {
					child.ToolStatus, child.ToolPreviewPhase = conversation.ToolStatusCancelled, conversation.ToolPreviewDone
					child.Content = "Stopped before a result was returned"
					if result.Result != "" {
						child.Content += "\n" + result.Result
					}
				}
			}
			if result.Error {
				child.ToolStatus, child.ToolPreviewPhase = "error", conversation.ToolPreviewError
			}
			child.ToolOutcome = result.Outcome
			if result.Outcome.State != "" {
				child.ToolStatus = result.Outcome.Status(result.Error)
				child.ToolPreviewPhase = result.Outcome.PreviewPhase(result.Error)
			}
		} else if stopped {
			child.ToolStatus, child.ToolPreviewPhase, child.Content = conversation.ToolStatusCancelled, conversation.ToolPreviewDone, "No result returned"
		}
		if strings.TrimSpace(child.ToolName) == "" {
			continue // unresolved identity, not a standalone tool or a tree boundary
		}
		children = append(children, child)
	}
	return children
}
