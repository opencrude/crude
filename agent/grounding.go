package agent

import (
	"fmt"
	"strings"
	"time"

	"crude/rag"
	"crude/xmlcodec"
)

// groundingOp is a single read operation requested by the LLM during grounding.
type groundingOp struct {
	Tool       string
	Parameters map[string]string
}

// ground runs the grounding phase using structured text generation (no function calling).
// The LLM outputs XML for read operations; the system executes them and feeds results back.
func (a *Agent) ground(
	sessionMessages []APIMessage,
	systemPrompt string,
	emit func(AgentEvent),
	ragEngine *rag.Engine,
) string {
	groundingPrompt := systemPrompt + `

=== GROUNDING PHASE ===
You are gathering information relevant to the user's request. Output ONLY valid XML.

Format:
<grounding>
  <thinking>your reasoning</thinking>
  <observation>
    <tool>tool_name</tool>
    <parameters>
      <param name="key">value</param>
    </parameters>
  </observation>
</grounding>

Allowed tools: read_file, list_directory, glob_files, grep_files, execute_command (non-mutating), web_search, web_fetch, arxiv_search, run_javascript.

Path resolution rules:
- If the user mentions a project, files, or directories WITHOUT specifying a path, use "." (current working directory)
- Examples: "show me the project structure" → list_directory(".") ; "what files are there" → list_directory(".") ; "read the config file" → if you don't know the path, start with list_directory(".")
- NEVER guess or invent paths. If unsure, start by listing the current directory
- Relative paths are resolved from the current working directory

IMPORTANT: Only gather observations that are DIRECTLY relevant to the user's request. Do NOT probe unrelated systems or services.
For greetings, chitchat, or questions that don't require system information, respond with:
<grounding><thinking>ready to plan</thinking></grounding>
Output ONLY the XML, no markdown fences.`

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

		// Parse grounding XML.
		gResp, parseErr := xmlcodec.ParseGrounding(content)
		if parseErr != nil {
			// Try plain text GROUNDING_COMPLETE.
			if strings.Contains(content, "GROUNDING_COMPLETE") {
				break
			}
			trimmed := strings.TrimSpace(content)
			if !strings.HasPrefix(trimmed, "<") {
				// LLM produced conversational text, not XML — skip to planning
				break
			}
			emit(AgentEvent{Type: "thinking", Data: "Retrying grounding..."})
			msgs = append(msgs, APIMessage{Role: "user", Content: "Output valid XML only. No markdown fences."})
			continue
		}

		// Emit clean thinking text.
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

			paramsMap := op.Parameters.ToMap()
			emit(AgentEvent{Type: "tool_call", Data: map[string]string{
				"name": op.Tool,
				"args": mustXMLArgs(paramsMap),
			}})

			paramAny := make(map[string]any)
			for k, v := range paramsMap {
				paramAny[k] = v
			}
			a.logger.Info("tool.call", "tool", op.Tool, "params", fmt.Sprintf("%v", paramAny))
			toolStart := time.Now()
			result, err := a.ToolFn(op.Tool, paramAny)
			toolDur := time.Since(toolStart).Milliseconds()
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
			}

			if err != nil {
				a.logger.Error("tool.error", "tool", op.Tool, "error", err.Error(), "latency_ms", toolDur)
			} else {
				a.logger.Info("tool.result", "tool", op.Tool, "latency_ms", toolDur, "content", result)
			}

			emit(AgentEvent{Type: "tool_result", Data: map[string]string{
				"name":   op.Tool,
				"result": result,
			}})

			if ragEngine != nil {
				obsTitle := op.Tool + ":" + extractPathFromParams(paramsMap)
				_ = ragEngine.AddObservation(obsTitle, result)
			}

			resultParts = append(resultParts, fmt.Sprintf("### %s(%s)\n%s", op.Tool, mustXMLArgs(paramsMap), result))
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

// mustXMLArgs converts a map of parameters to a compact XML string for display/logging.
func mustXMLArgs(m map[string]string) string {
	var sb strings.Builder
	sb.WriteString("{")
	first := true
	for k, v := range m {
		if !first {
			sb.WriteString(", ")
		}
		sb.WriteString(k)
		sb.WriteString(": ")
		sb.WriteString(v)
		first = false
	}
	sb.WriteString("}")
	return sb.String()
}

// extractPathFromParams extracts a path-like value from tool parameters.
func extractPathFromParams(args map[string]string) string {
	for _, key := range []string{"path", "pattern", "command", "source"} {
		if v, ok := args[key]; ok {
			return v
		}
	}
	return ""
}
