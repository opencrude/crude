package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"crude/provider"
	"crude/safety"
	"crude/tools"
	ttypes "crude/types"
)

func (a *Agent) executeParallel(ctx context.Context, tc provider.ToolCall, params map[string]any, emit func(AgentEvent), responses <-chan ttypes.BlockResponse, lastReply *string) (tools.ToolResult, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return tools.ToolResult{}, err
	}
	var batch struct {
		Uses []struct {
			Name       string         `json:"recipient_name"`
			Parameters map[string]any `json:"parameters"`
		} `json:"tool_uses"`
	}
	if err := json.Unmarshal(raw, &batch); err != nil {
		return tools.ToolResult{}, err
	}
	if len(batch.Uses) == 0 {
		return tools.ToolResult{}, fmt.Errorf("parallel requires at least one tool call")
	}
	calls := make([]provider.ToolCall, 0, len(batch.Uses))
	for i, use := range batch.Uses {
		name := strings.TrimPrefix(use.Name, "functions.")
		if name == "multi_tool_use.parallel" {
			return tools.ToolResult{}, fmt.Errorf("nested parallel batches are not supported")
		}
		if findToolDef(name, a.FilteredTools) == nil {
			return tools.ToolResult{}, fmt.Errorf("tool %q is not available to this agent", name)
		}
		args, err := json.Marshal(use.Parameters)
		if err != nil {
			return tools.ToolResult{}, err
		}
		call := provider.ToolCall{ID: fmt.Sprintf("%s:%d", tc.ID, i), InternalID: fmt.Sprintf("%s:%d", tc.InternalID, i), Type: "function", Function: provider.FunctionCall{Name: name, Arguments: string(args)}}
		for _, def := range a.FilteredTools {
			if def.Function.Name == name && def.Type == "custom" {
				input, ok := use.Parameters["input"].(string)
				if !ok {
					return tools.ToolResult{}, fmt.Errorf("freeform tool %s requires raw input", name)
				}
				call.Type = "custom"
				call.Function.Arguments = input
			}
		}
		calls = append(calls, call)
	}
	// Publish child stages through the one batch message, never through extra
	// transcript rows. Serialize snapshots with the completion collector so a
	// late progress event cannot overwrite a finished child.
	ordered := make([]map[string]any, len(calls))
	indices := make(map[string]int, len(calls))
	for i, call := range calls {
		indices[call.ID] = i
	}
	var progressMu sync.Mutex
	publish := func() {
		body, err := json.Marshal(map[string]any{"results": ordered})
		if err != nil {
			return
		}
		emit(AgentEvent{Type: "tool_call_streaming", Data: map[string]any{"tool_id": tc.ID, "internal_id": tc.InternalID, "name": tc.Function.Name, "content": string(body), "args": string(raw)}})
	}
	childEmit := func(event AgentEvent) {
		switch event.Type {
		case "tool_call":
			data, _ := event.Data.(map[string]any)
			id, _ := data["tool_id"].(string)
			if i, ok := indices[id]; ok {
				progressMu.Lock()
				if ordered[i] == nil {
					ordered[i] = map[string]any{"recipient_name": calls[i].Function.Name, "phase": "running"}
					publish()
				}
				progressMu.Unlock()
			}
			return
		case "tool_progress", "tool_result", "tool_call_streaming":
			return
		default:
			emit(event)
		}
	}
	results := a.executeToolCalls(ctx, calls, childEmit, responses, lastReply, 0)
	attachments := make([][]provider.ContentPart, len(calls))
	result := tools.ToolResult{}
	for range calls {
		r := <-results
		progressMu.Lock()
		ordered[r.index] = map[string]any{"recipient_name": r.name, "result": r.result.Content, "error": r.toolError, "added": r.result.Added, "removed": r.result.Removed}
		publish()
		progressMu.Unlock()
		result.Added += r.result.Added
		result.Removed += r.result.Removed
		attachments[r.index] = r.result.Attachments
	}
	for _, parts := range attachments {
		result.Attachments = append(result.Attachments, parts...)
	}
	body, err := json.Marshal(map[string]any{"results": ordered})
	result.Content = string(body)
	if err == nil {
		for _, item := range ordered {
			if failed, _ := item["error"].(bool); failed {
				err = fmt.Errorf("one or more parallel tools failed")
				break
			}
		}
	}
	return result, err
}

// Permission-producing calls stay serial. Native read-only commands and empty
// polls can run concurrently without sharing the confirmation response channel.
func (a *Agent) parallelCommandAllowed(call provider.ToolCall) bool {
	if call.Function.Name != "exec_command" && call.Function.Name != "write_stdin" {
		return false
	}
	params, err := provider.ToolCallParams(call)
	if err != nil {
		return false
	}
	if call.Function.Name == "write_stdin" {
		return getStr(params, "chars") == ""
	}
	if getStr(params, "sandbox_permissions") == "require_escalated" {
		return false
	}
	return safety.CheckCommandSafetyWithPolicy(getStr(params, "cmd"), a.logger, a.CommandPolicy).Risk == safety.RiskSafe
}
