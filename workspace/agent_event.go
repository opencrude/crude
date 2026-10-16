package workspace

import (
	"unicode/utf8"

	"crude/tools"
	ttypes "crude/types"
)

// AgentEvent is the workspace-owned domain event produced by the backend
// orchestrator. The TUI converts ttypes.AgentEventMsg to this type at the
// boundary so workspace code never imports tui/types.
type AgentEvent struct {
	Type     string
	Data     any
	AgentID  string
	Identity ttypes.RuntimeEventIdentity
}

// StaleAgentUpload identifies late upload callbacks after a request has begun
// producing output. Ingress must not rewind either actor state or its status;
// a genuinely new request has a different request identity.
func (w *Workspace) StaleAgentUpload(evt AgentEvent) bool {
	if w == nil || evt.Type != "phase" || w.Status == nil {
		return false
	}
	snapshot, exists := w.Status.Agent(evt.AgentID)
	if !exists || snapshot.Phase != "thinking" && snapshot.Phase != "responding" {
		return false
	}
	phase, ok := ParseAgentPhase(evt.Data)
	data, mapped := evt.Data.(map[string]any)
	if !ok || !mapped {
		return false
	}
	requestID, _ := data["request_id"].(string)
	return requestID == snapshot.RequestID && (phase.Phase == "preparing" || phase.Phase == "waiting_provider")
}

// SanitizeForTerminal removes control characters and invalid UTF-8 from
// agent text before it enters conversation data. This is a domain concern
// (data hygiene), not a rendering concern; the workspace stores clean text
// and the TUI projects it.
func SanitizeForTerminal(s string) string {
	if s == "" {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		r, size := decodeRune(s[i:])
		if r == 0xFFFD && size == 1 {
			out = append(out, '?')
			i++
			continue
		}
		switch {
		case r == '\n' || r == '\t':
			out = append(out, s[i:i+size]...)
		case isControl(r):
			out = append(out, ' ')
		default:
			out = append(out, s[i:i+size]...)
		}
		i += size
	}
	// Strip carriage returns.
	result := make([]byte, 0, len(out))
	for _, b := range out {
		if b != '\r' {
			result = append(result, b)
		}
	}
	return string(result)
}

// TruncateLine truncates a string to max visible cells, appending an
// ellipsis. This is domain data normalization (keeping stored labels short),
// not terminal rendering.
func TruncateLine(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}

// ToolLabel builds a compact "tool args" status label. This is domain
// data formatting, not terminal rendering.
func ToolLabel(tool, argsSummary string) string {
	if tool == "multi_tool_use.parallel" {
		if argsSummary != "" {
			return "Running " + argsSummary
		}
		return "Running tools"
	}
	label := PascalCaseTool(tool)
	switch tool {
	case "write_file":
		label = "Writing"
	case "edit_file", "apply_patch":
		label = "Editing"
	case "read_file":
		label = "Reading"
	case "execute_command", "async_execute_command":
		label = "Running"
	case "grep_files", "glob_files", "web_search", "arxiv_search":
		label = "Searching"
	case "web_fetch":
		label = "Fetching"
	case "list_directory":
		label = "Listing"
	case "update_plan":
		label = "Updating plan"
	case "get_plan":
		label = "Reading plan"
	}
	if argsSummary != "" {
		return label + " " + argsSummary
	}
	return label
}

// SummarizeToolArgs produces a compact summary of tool arguments for
// display in conversation data. It delegates to the tool layer so the live
// transcript and a resumed session label rows through one implementation.
func SummarizeToolArgs(tool, argsStr string) string {
	return tools.SummarizeArgs(tool, argsStr)
}

// ExtractArg reads one argument out of a tool-call payload (JSON object or the
// presentation-neutral "key: value" summary). It is the sole operand reader for
// both a live tool event and a hydrated snapshot; do not add a second JSON
// scanner beside it. A hand-rolled string reader that decoded \" \n \t but not
// \uXXXX printed "u0026u0026" for a shell "&&" in live command titles.
func ExtractArg(str, key string) string {
	return tools.ExtractArg(str, key)
}

// PascalCaseTool converts a snake_case tool name to readable title words.
func PascalCaseTool(tool string) string {
	tool = tools.DisplayToolName(tool)
	upper := true
	out := make([]byte, 0, len(tool))
	for i := 0; i < len(tool); i++ {
		ch := tool[i]
		if ch == '_' {
			out = append(out, ' ')
			upper = true
			continue
		}
		if upper && ch >= 'a' && ch <= 'z' {
			ch -= 32
		}
		upper = false
		out = append(out, ch)
	}
	return string(out)
}

func decodeRune(s string) (rune, int) {
	return utf8.DecodeRuneInString(s)
}

func isControl(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r < 0xa0)
}
