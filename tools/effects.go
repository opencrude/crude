package tools

import captypes "crude/types/capability"

type ToolMetadata struct {
	Name        string
	Effect      captypes.Effect
	Scope       string
	Fingerprint string
	Dynamic     bool
}

type ExecutionIdentity struct {
	AgentID    string
	MessageID  string
	ToolCallID string
}

type ExecutionIdentityKey struct{}

func NativeMetadata(name string) (ToolMetadata, bool) {
	metadata := ToolMetadata{Name: name, Scope: "workspace"}
	switch name {
	case "read_file", "glob_files", "grep_files", "list_directory", "web_search", "web_fetch", "arxiv_search", "recall_memory", "search_history", "get_plan", "check_async_command", "read_async_output", "poll_sub_agent", "list_sub_agents", "wait", "capability_search", "capability_activate", "mcp_read_resource", "mcp_get_prompt":
		metadata.Effect = captypes.EffectRead
	case "reply", "question", "write_report", "update_plan", "board_read", "board_post", "remember", "update_memory", "forget_memory", "memory_feedback":
		metadata.Effect = captypes.EffectCommunication
	case "apply_patch", "write_file", "edit_file", "move_file", "copy_file":
		metadata.Effect = captypes.EffectWrite
	case "delete_file":
		metadata.Effect = captypes.EffectDelete
	case "exec_command", "write_stdin", "execute_command", "async_execute_command", "kill_async_command", "run_sub_agent", "cancel_sub_agent", "multi_tool_use.parallel":
		metadata.Effect = captypes.EffectExecute
	default:
		return ToolMetadata{}, false
	}
	return metadata, true
}
