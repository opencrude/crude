package chatprojection

import (
	"encoding/json"
	"fmt"
	"strings"

	toolargs "crude/tools"
	"crude/tui/types"
)

// A batch is a provider envelope, not a second visual tool family. Derive
// ordinary tool leaves at source-change time and let the same tree, disclosure,
// diff, error and native-command renderers handle them.
func parallelMessages(msg *types.ChatMessage) []*types.ChatMessage {
	var input struct {
		Uses []struct {
			Name       string          `json:"recipient_name"`
			Parameters json.RawMessage `json:"parameters"`
		} `json:"tool_uses"`
	}
	var output struct {
		Results []*struct {
			Name    string `json:"recipient_name"`
			Result  string `json:"result"`
			Error   bool   `json:"error"`
			Phase   string `json:"phase"`
			Added   int    `json:"added"`
			Removed int    `json:"removed"`
		} `json:"results"`
	}
	_ = json.Unmarshal([]byte(msg.ToolInput), &input)
	_ = json.Unmarshal([]byte(msg.Content), &output)
	count := max(len(input.Uses), len(output.Results))
	roundID := activityRoundID(msg)
	stopped := msg.ToolStatus == types.ToolStatusSuccess || msg.ToolStatus == types.ToolStatusError ||
		msg.ToolStatus == types.ToolStatusCancelled || msg.ToolStatus == "interrupted"
	children := make([]*types.ChatMessage, 0, count)
	for i := 0; i < count; i++ {
		child := &types.ChatMessage{InternalID: fmt.Sprintf("%s:%d", msg.InternalID, i), ToolID: fmt.Sprintf("%s:%d", msg.ToolID, i), ToolRoundID: roundID, Role: types.RoleTool, Time: msg.Time, ToolStatus: "streaming", ToolPreviewPhase: types.ToolPreviewPreparing}
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
			child.ToolStatus, child.ToolPreviewPhase = "success", types.ToolPreviewDone
			if result.Phase == "running" {
				child.ToolStatus, child.ToolPreviewPhase = "calling", types.ToolPreviewStreaming
				if stopped {
					child.ToolStatus, child.ToolPreviewPhase = types.ToolStatusCancelled, types.ToolPreviewDone
					child.Content = "Stopped before a result was returned"
					if result.Result != "" {
						child.Content += "\n" + result.Result
					}
				}
			}
			if result.Error {
				child.ToolStatus, child.ToolPreviewPhase = "error", types.ToolPreviewError
			}
		} else if stopped {
			child.ToolStatus, child.ToolPreviewPhase, child.Content = types.ToolStatusCancelled, types.ToolPreviewDone, "No result returned"
		}
		if strings.TrimSpace(child.ToolName) == "" {
			continue // unresolved identity, not a standalone tool or a tree boundary
		}
		children = append(children, child)
	}
	return children
}
