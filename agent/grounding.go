package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"crude/rag"
)

// groundingOp is a single read operation requested by the LLM during grounding.
type groundingOp struct {
	Tool       string         `json:"tool"`
	Parameters map[string]any `json:"parameters"`
}

// groundingResponse is what the LLM outputs during the grounding phase.
type groundingResponse struct {
	Thinking     string        `json:"thinking"`
	Observations []groundingOp `json:"observations"`
}

// ground runs the grounding phase using structured text generation (no function calling).
// The LLM outputs a JSON array of read operations; the system executes them and feeds results back.
func (a *Agent) ground(
	sessionMessages []APIMessage,
	systemPrompt string,
	emit func(AgentEvent),
	ragEngine *rag.Engine,
) string {
	groundingPrompt := systemPrompt + `

=== GROUNDING PHASE ===
You are gathering information relevant to the user's request. Output ONLY valid JSON.

CRITICAL — JSON must be perfectly valid:
- All string values MUST escape: quotes as \", backslashes as \\, newlines as \n
- NEVER include raw newlines, unescaped quotes, or backslashes inside string values
- Output ONLY the JSON object, no markdown fences, no prose

Format:
{"thinking": "your reasoning", "observations": [{"tool": "tool_name", "parameters": {"key": "value"}}]}

Allowed tools: read_file, list_directory, glob_files, grep_files, execute_command (non-mutating), web_search, web_fetch, arxiv_search, run_javascript.

Path resolution rules:
- If the user mentions a project, files, or directories WITHOUT specifying a path, use "." (current working directory)
- Examples: "show me the project structure" → list_directory(".") ; "what files are there" → list_directory(".") ; "read the config file" → if you don't know the path, start with list_directory(".")
- NEVER guess or invent paths. If unsure, start by listing the current directory
- Relative paths are resolved from the current working directory

IMPORTANT: Only gather observations that are DIRECTLY relevant to the user's request. Do NOT probe unrelated systems or services.
For greetings, chitchat, or questions that don't require system information, respond with:
{"thinking": "ready to plan", "observations": []}
Output ONLY the JSON, no markdown fences.`

	msgs := make([]APIMessage, 0, len(sessionMessages)+1)
	msgs = append(msgs, APIMessage{Role: "system", Content: groundingPrompt})
	msgs = append(msgs, sessionMessages...)

	var allObservations strings.Builder

	for iter := 0; iter < maxGroundingIter; iter++ {
		resp, err := a.complete(msgs, nil)
		if err != nil {
			a.emitError(emit, err)
			return ""
		}
		content := resp.Message.Content
		if resp.Usage != nil {
			emit(AgentEvent{Type: "usage", Data: map[string]int{
				"prompt":     resp.Usage.PromptTokens,
				"completion": resp.Usage.CompletionTokens,
			}})
		}

		// Parse grounding response.
		jsonStr := stripJSONFences(content)
		var gResp groundingResponse
		if err := json.Unmarshal([]byte(jsonStr), &gResp); err != nil {
			// Try plain text GROUNDING_COMPLETE.
			if strings.Contains(content, "GROUNDING_COMPLETE") {
				break
			}
			trimmed := strings.TrimSpace(content)
			if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
				// LLM produced conversational text, not JSON — skip to planning
				break
			}
			emit(AgentEvent{Type: "thinking", Data: "Retrying grounding..."})
			msgs = append(msgs, APIMessage{Role: "user", Content: "Output valid JSON only. No markdown fences."})
			continue
		}

		// Emit clean thinking text (not raw JSON).
		if gResp.Thinking != "" {
			emit(AgentEvent{Type: "thinking", Data: gResp.Thinking})
		}

		// No more observations = grounding complete.
		if len(gResp.Observations) == 0 {
			break
		}

		// Execute each read operation.
		var resultParts []string
		for _, op := range gResp.Observations {
			if !isReadOnlyTool(op.Tool) {
				resultParts = append(resultParts, fmt.Sprintf("[%s: skipped — not a read tool]", op.Tool))
				continue
			}

			emit(AgentEvent{Type: "tool_call", Data: map[string]string{
				"name": op.Tool,
				"args": mustJSON(op.Parameters),
			}})

			if a.Logger != nil {
				a.Logger.LogToolCall(op.Tool, op.Parameters)
			}

			argsJSON, _ := json.Marshal(op.Parameters)
			toolStart := time.Now()
			result, err := a.ToolFn(op.Tool, op.Parameters)
			toolDur := time.Since(toolStart).Milliseconds()
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
			}

			if a.Logger != nil {
				a.Logger.LogToolResult(op.Tool, result, toolDur, err)
			}

			emit(AgentEvent{Type: "tool_result", Data: map[string]string{
				"name":   op.Tool,
				"result": result,
			}})

			if ragEngine != nil {
				obsTitle := op.Tool + ":" + extractPathFromArgs(string(argsJSON))
				_ = ragEngine.AddObservation(obsTitle, result)
			}

			resultParts = append(resultParts, fmt.Sprintf("### %s(%s)\n%s", op.Tool, mustJSON(op.Parameters), result))
		}

		// Feed results back for next iteration.
		observationText := strings.Join(resultParts, "\n\n")
		allObservations.WriteString(observationText)
		allObservations.WriteString("\n\n")

		msgs = append(msgs, APIMessage{
			Role:    "assistant",
			Content: content,
		})
		msgs = append(msgs, APIMessage{
			Role:    "user",
			Content: "Observations:\n" + observationText + "\n\nContinue gathering or output empty observations to proceed to planning.",
		})
	}

	return allObservations.String()
}

// isReadOnlyTool returns true for tools that only read data.
func isReadOnlyTool(tool string) bool {
	switch tool {
	case "read_file", "list_directory", "glob_files", "grep_files", "execute_command",
		"web_search", "web_fetch", "arxiv_search", "run_javascript", "reply":
		return true
	default:
		return false
	}
}

// stripJSONFences removes markdown code fences from a string.
func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		lines := strings.SplitN(s, "\n", 2)
		if len(lines) == 2 {
			s = lines[1]
		}
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

// mustJSON marshals a value to JSON, returning "{}" on failure.
func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// extractPathFromArgs extracts a path-like value from JSON-encoded tool arguments.
func extractPathFromArgs(argsJSON string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return ""
	}
	for _, key := range []string{"path", "pattern", "command", "source"} {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}
