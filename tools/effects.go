package tools

import (
	captypes "crude/types/capability"
)

type ToolMetadata struct {
	Name        string
	Effect      captypes.Effect
	Scope       string
	Fingerprint string
	Dynamic     bool
	// ContextResolved marks a native gateway whose actual effect is resolved
	// from the exact request before execution. Effect is intentionally empty:
	// callers may admit the gateway for resolution, but must not treat that
	// admission as read authority for the resolved operation.
	ContextResolved bool
}

type ExecutionIdentity struct {
	AgentID    string
	RunID      string
	MessageID  string
	ToolCallID string
}

type ExecutionIdentityKey struct{}

func NativeMetadata(name string) (ToolMetadata, bool) {
	metadata := ToolMetadata{Name: name, Scope: "workspace"}
	switch name {
	case "read_file", "glob_files", "grep_files", "list_directory", "recall_memory", "search_history", "get_plan", "check_async_command", "read_async_output", "wait", "capability_search", "capability_activate", "mcp_request_access", "mcp_read_resource", "mcp_get_prompt", "agent_list", "agent_inspect", "artifact_read", "agent_wait", "board_read", "crude_commands", "lsp", "get_goal":
		metadata.Effect = captypes.EffectRead
	case "crude_control":
		metadata.ContextResolved = true
	case "reply", "question", "update_plan", "remember", "update_memory", "forget_memory", "memory_feedback", "agent_send", "agent_respond", "publish_report", "create_goal", "update_goal":
		metadata.Effect = captypes.EffectCommunication
	case "apply_patch", "write_file", "edit_file", "move_file", "copy_file":
		metadata.Effect = captypes.EffectWrite
	case "delete_file":
		metadata.Effect = captypes.EffectDelete
	case "exec_command", "write_stdin", "execute_command", "async_execute_command", "kill_async_command", "multi_tool_use.parallel", "agent_start", "agent_control", "agent_update_claims", "agent_narrow_claims", "agent_configure":
		metadata.Effect = captypes.EffectExecute
	default:
		return ToolMetadata{}, false
	}
	return metadata, true
}
