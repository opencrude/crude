package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"crude/codexio"
)

// This file is the single authority for turning a tool call's arguments into the
// compact label every projection shows. Both producers of a transcript row call
// it: the live path (agent events → workspace.AgentState → the conversation) and
// the resumed path (the persisted tool snapshot → workspace.ConvertMessagesForDisplay).
// Keeping one implementation is the point — a second copy drifted before, and
// duplicated label logic is how live and hydrated rows start disagreeing.

// ExtractArg reads one argument out of a tool-call payload. Two encodings reach
// the UI: the JSON object the provider sent, and the presentation-neutral
// "key: value" summary AgentFormatToolArgs produces for events and snapshots.
// Values in the summary form are quoted when they contain spaces, so a pattern
// like `func main` survives instead of collapsing to its first token.
//
// The JSON branch delegates to encoding/json, which is the only correct decoder
// for the payload's escape table. A local scanner that handled \" \n \t but not
// \uXXXX turned a shell "&&" into "u0026u0026" in live command titles, because
// Go's json.Marshal writes HTML-escaping (\u0026 for &, \u003e for >) into every
// tool-call payload.
func ExtractArg(str, key string) string {
	var object map[string]any
	if json.Unmarshal([]byte(str), &object) == nil {
		if value, ok := object[key]; ok {
			switch typed := value.(type) {
			case string:
				return typed
			case float64, bool:
				return fmt.Sprint(typed)
			}
		}
		return "" // never search another field's JSON string for this key
	}
	if structuredArguments(str) {
		return "" // an unfinished envelope is not a key/value summary
	}
	for _, prefix := range []string{key + ": ", key + ":"} {
		idx := strings.Index(str, prefix)
		if idx == -1 {
			continue
		}
		rest := strings.TrimLeft(str[idx+len(prefix):], " \t")
		if rest == "" {
			continue
		}
		if rest[0] == '"' {
			if end := strings.IndexByte(rest[1:], '"'); end >= 0 {
				return rest[1 : 1+end]
			}
			return rest[1:]
		}
		if end := strings.IndexAny(rest, " ,}]"); end >= 0 {
			if end == 0 {
				continue
			}
			token := strings.TrimSpace(rest[:end])
			// "pattern: path: x" — the value is another key, so this key has no
			// value in the summary form.
			if strings.HasSuffix(token, ":") {
				continue
			}
			return token
		}
		return strings.TrimSpace(rest)
	}
	return ""
}

// SummarizeArgs produces the compact label a tool row shows for its operands:
// the file, command, pattern, query, or handle the call acted on, never the raw
// argument payload when a known operand exists.
func SummarizeArgs(tool, argsStr string) string {
	if tool == "multi_tool_use.parallel" {
		var batch struct {
			Uses []json.RawMessage `json:"tool_uses"`
		}
		if json.Unmarshal([]byte(argsStr), &batch) == nil && len(batch.Uses) > 0 {
			if len(batch.Uses) == 1 {
				return "1 tool"
			}
			return fmt.Sprintf("%d tools", len(batch.Uses))
		}
		return ""
	}
	if tool == "exec_command" {
		return ExtractArg(argsStr, "cmd")
	}
	if tool == "write_stdin" {
		if session := ExtractArg(argsStr, "session_id"); session != "" {
			return "session " + session
		}
		return ""
	}
	if tool == "apply_patch" {
		input := argsStr
		if structuredArguments(argsStr) {
			input = ExtractArg(argsStr, "input")
		}
		if patch, err := codexio.Parse(input); err == nil {
			return strings.Join(patch.Paths(""), ", ")
		}
		if strings.Contains(input, "*** Begin Patch") {
			paths, _, _ := codexio.Preview(input)
			return strings.Join(paths, ", ")
		}
		if structuredArguments(argsStr) {
			return ""
		}
		return argsStr
	}
	if tool == "update_plan" || tool == "get_plan" {
		return ExtractArg(argsStr, "plan_id")
	}
	if tool == "agent_start" {
		// The assignment is the useful operand. Raw launch arguments can include
		// lengthy instructions and policy fields; neither belongs in a chat title.
		if description := ExtractArg(argsStr, "description"); description != "" {
			if category := subAgentCategoryLabel(ExtractArg(argsStr, "category")); category != "" {
				return truncateLabel(category+" · "+description, 80)
			}
			return truncateLabel(description, 80)
		}
		if name := ExtractArg(argsStr, "name"); name != "" {
			return name
		}
		if !structuredArguments(argsStr) && !strings.Contains(argsStr, ":") {
			return argsStr // an already summarized assignment in a canonical snapshot
		}
		return ""
	}
	switch tool {
	case "question", "reply":
		return "" // these calls have no useful operand in their argument envelope
	case "remember":
		return ExtractArg(argsStr, "title")
	case "update_memory", "forget_memory":
		return ExtractArg(argsStr, "id")
	case "capability_activate":
		return ExtractArg(argsStr, "id")
	case "agent_list":
		return firstOperand(ExtractArg(argsStr, "query"), ExtractArg(argsStr, "session_id"))
	case "agent_inspect":
		return ExtractArg(argsStr, "agent_id")
	case "agent_send":
		kind := ExtractArg(argsStr, "kind")
		return firstOperand(kind, ExtractArg(argsStr, "request_id"))
	case "agent_respond":
		return joinOperands(ExtractArg(argsStr, "disposition"), ExtractArg(argsStr, "request_id"))
	case "agent_control":
		return joinOperands(ExtractArg(argsStr, "operation"), ExtractArg(argsStr, "agent_id"))
	case "publish_report":
		return firstOperand(ExtractArg(argsStr, "summary"), ExtractArg(argsStr, "artifact_id"))
	case "artifact_read":
		return ExtractArg(argsStr, "artifact_id")
	case "agent_wait":
		for _, key := range []string{"run_id", "agent_id", "artifact_id"} {
			value := ExtractArg(argsStr, key)
			if value != "" && !structuredArguments(value) && !strings.Contains(value, `{"`) {
				return value
			}
		}
		return ""
	case "board_read":
		if thread := ExtractArg(argsStr, "thread_id"); thread != "" {
			return "thread " + thread
		}
		if query := ExtractArg(argsStr, "search"); query != "" {
			return query
		}
		if since := ExtractArg(argsStr, "since"); since != "" {
			return "after #" + since
		}
		return ""
	case "mcp_read_resource":
		return joinOperands(ExtractArg(argsStr, "connection_id"), ExtractArg(argsStr, "uri"))
	case "mcp_request_access":
		return ExtractArg(argsStr, "connection_id")
	case "mcp_get_prompt":
		return joinOperands(ExtractArg(argsStr, "connection_id"), ExtractArg(argsStr, "name"))
	case "memory_feedback":
		return ""
	case "crude_commands":
		return ExtractArg(argsStr, "query")
	case "crude_control":
		return ExtractArg(argsStr, "command")
	case "agent_update_claims":
		return ExtractArg(argsStr, "agent_id")
	case "agent_narrow_claims":
		return ""
	}
	if tool == "grep_files" {
		pattern := ExtractArg(argsStr, "pattern")
		path := ExtractArg(argsStr, "path")
		label := ""
		if pattern != "" {
			label = "\"" + pattern + "\""
		}
		if path != "" && path != "." {
			if label != "" {
				label += " in " + path
			} else {
				label = path
			}
		}
		if label != "" {
			return label
		}
	}
	if path := ExtractArg(argsStr, "path"); path != "" {
		return path
	}
	if pattern := ExtractArg(argsStr, "pattern"); pattern != "" {
		return pattern
	}
	if command := ExtractArg(argsStr, "command"); command != "" {
		return command
	}
	if query := ExtractArg(argsStr, "query"); query != "" {
		return query
	}
	if url := ExtractArg(argsStr, "url"); url != "" {
		return url
	}
	if source := ExtractArg(argsStr, "source"); source != "" {
		if dest := ExtractArg(argsStr, "destination"); dest != "" {
			return source + " → " + dest
		}
		return source
	}
	if code := ExtractArg(argsStr, "code"); code != "" {
		return truncateLabel(code, 50)
	}
	if tool == "async_execute_command" {
		handle := ExtractArg(argsStr, "handle")
		if command := ExtractArg(argsStr, "command"); command != "" {
			if handle != "" {
				return truncateLabel(command, 40) + " [" + handle + "]"
			}
			return truncateLabel(command, 50)
		}
		if handle != "" {
			return handle
		}
	}
	if tool == "check_async_command" || tool == "kill_async_command" || tool == "read_async_output" {
		if handle := ExtractArg(argsStr, "handle"); handle != "" {
			return handle
		}
	}
	if tool == "wait" {
		if seconds := ExtractArg(argsStr, "seconds"); seconds != "" {
			return seconds + "s"
		}
	}
	if structuredArguments(argsStr) || strings.Contains(argsStr, "map[") {
		return "" // no structured payload is a safe fallback title
	}
	return truncateLabel(argsStr, 60)
}

func subAgentCategoryLabel(category string) string {
	category = strings.TrimSpace(category)
	if category == "" {
		return ""
	}
	words := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(category))
	for i, word := range words {
		if word != "" {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

// RetainsStructuredToolInput identifies tools whose result alone omits useful
// authored content or attribution. Live events, canonical snapshots and batch
// leaves retain their exact input before deriving a title; rendering never owns
// or rewrites that audit evidence.
func RetainsStructuredToolInput(name string) bool {
	switch name {
	case "agent_list", "agent_inspect", "agent_start", "agent_send", "agent_respond", "agent_control",
		"publish_report", "artifact_read", "agent_wait", "board_read",
		"remember", "recall_memory", "update_memory",
		"forget_memory", "memory_feedback", "search_history", "capability_search",
		"capability_activate", "mcp_request_access", "mcp_read_resource", "mcp_get_prompt",
		"agent_update_claims", "agent_narrow_claims", "crude_commands", "crude_control":
		return true
	}
	return strings.HasPrefix(name, "mcp__")
}

func firstOperand(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func joinOperands(first, second string) string {
	if first != "" && second != "" {
		return first + " · " + second
	}
	return firstOperand(first, second)
}

func structuredArguments(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[")
}

// DisplayToolName accepts a tool identifier, never an argument envelope or
// an unbounded provider-provided string.
func DisplayToolName(name string) string {
	if name == "" || len(name) > 128 {
		return "tool"
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '.' || ch == '-' {
			continue
		}
		return "tool"
	}
	return name
}

// truncateLabel shortens a label on a rune boundary, counting the ellipsis in
// the budget, so the painted row never exceeds the width the caller reserved and
// a wide glyph is never cut in half.
func truncateLabel(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit || limit <= 0 {
		return value
	}
	return string(runes[:limit-1]) + "…"
}
