package tools

import (
	"fmt"

	captypes "crude/types/capability"
)

type StructuralFailureKind string

const (
	StructuralFailureOwnershipScope    StructuralFailureKind = "ownership_scope"
	StructuralFailureOwnershipConflict StructuralFailureKind = "ownership_conflict"
)

// StructuralFailureError identifies a tool failure that cannot be repaired by
// retrying the same attempt. The normal tool result remains the user-visible
// evidence; supervisors use this typed cause to park only the blocked attempt.
type StructuralFailureError struct {
	Kind           StructuralFailureKind
	Tool           string
	Target         string
	OwnerAgentID   string
	OwnerAgentName string
	Cause          error
}

func (e *StructuralFailureError) Error() string {
	if e == nil {
		return "structural tool failure"
	}
	prefix := string(e.Kind)
	if e.Tool != "" {
		prefix += " tool=" + e.Tool
	}
	if e.Target != "" {
		prefix += " target=" + e.Target
	}
	if e.OwnerAgentID != "" {
		prefix += " owner=" + e.OwnerAgentID
	}
	if e.Cause != nil {
		return prefix + ": " + e.Cause.Error()
	}
	if e.Target != "" {
		return fmt.Sprintf("%s: %s", prefix, e.Target)
	}
	return prefix
}

func (e *StructuralFailureError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

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
	case "read_file", "glob_files", "grep_files", "list_directory", "web_search", "web_fetch", "arxiv_search", "recall_memory", "search_history", "get_plan", "check_async_command", "read_async_output", "wait", "capability_search", "capability_activate", "mcp_request_access", "mcp_read_resource", "mcp_get_prompt", "agent_list", "agent_inspect", "artifact_read", "agent_wait", "board_read":
		metadata.Effect = captypes.EffectRead
	case "reply", "question", "update_plan", "remember", "update_memory", "forget_memory", "memory_feedback", "agent_send", "agent_respond", "publish_report":
		metadata.Effect = captypes.EffectCommunication
	case "apply_patch", "write_file", "edit_file", "move_file", "copy_file":
		metadata.Effect = captypes.EffectWrite
	case "delete_file":
		metadata.Effect = captypes.EffectDelete
	case "exec_command", "write_stdin", "execute_command", "async_execute_command", "kill_async_command", "multi_tool_use.parallel", "agent_start", "agent_control", "agent_update_claims", "agent_narrow_claims":
		metadata.Effect = captypes.EffectExecute
	default:
		return ToolMetadata{}, false
	}
	return metadata, true
}
