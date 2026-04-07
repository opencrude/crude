package agent

import (
	"fmt"
	"strings"

	"crude/dag"
	"crude/rag"
)

// plan generates the execution DAG from grounded context.
func (a *Agent) plan(
	sessionMessages []APIMessage,
	systemPrompt string,
	observations string,
	emit func(AgentEvent),
	ragEngine *rag.Engine,
) *dag.DAG {
	enrichedContext := ""
	if ragEngine != nil {
		llmCallFn := func(prompt string) (string, error) {
			singleMsg := []APIMessage{
				{Role: "system", Content: "You are a technical term analyzer. Answer concisely."},
				{Role: "user", Content: prompt},
			}
			resp, err := a.complete(singleMsg, nil)
			if err != nil {
				return "", err
			}
			return resp.Message.Content, nil
		}
		enricher := rag.NewEnricher(ragEngine, llmCallFn)
		userQuery := ""
		for i := len(sessionMessages) - 1; i >= 0; i-- {
			if sessionMessages[i].Role == "user" {
				userQuery = sessionMessages[i].Content
				break
			}
		}
		if ctx, _, err := enricher.Enrich(userQuery, 3); err == nil {
			enrichedContext = ctx
		}
	}

	observationsSection := ""
	if observations != "" {
		observationsSection = "\n\n<observations>\n" + observations + "\n</observations>"
	}
	if enrichedContext != "" {
		observationsSection += "\n\n<enriched_context>\n" + enrichedContext + "\n</enriched_context>"
	}

	planningPrompt := systemPrompt + observationsSection + `

=== PLANNING PHASE ===
Generate a JSON execution plan. Output ONLY valid JSON.

CRITICAL — JSON must be perfectly valid:
- All string values MUST escape: quotes as \", backslashes as \\, newlines as \n, tabs as \t
- NEVER include raw newlines, tabs, or unescaped quotes inside string values
- NEVER include code blocks, markdown, or prose inside JSON string values
- If a description or parameter contains code, keep it brief and escape all special chars
- Test your JSON mentally: every " inside a string must be \", every \ must be \\

Examples of CORRECT escaping:
  "description": "Edit the config file to update the API key"
  "description": "Run npm install to add the lodash package"
  "parameters": {"content": "line1\nline2\nline3"}

Examples of WRONG (will break parser):
  "description": "Edit file to add: const x = \"hello\""     ← unescaped quotes
  "description": "Path is C:\Users\test"                      ← unescaped backslashes
  "content": "first line
second line"                                                  ← raw newline in string

Available tools: read_file, write_file, edit_file, delete_file, execute_command, list_directory, glob_files, grep_files, web_search, web_fetch, arxiv_search, run_javascript, reply, blocking

Path resolution rules:
- If the user mentions a project, files, or directories WITHOUT specifying a path, use "." (current working directory)
- Examples: "show me the project structure" → list_directory(".") ; "what files are there" → list_directory(".") ; "read the config file" → if you don't know the path, start with list_directory(".")
- NEVER guess or invent paths. If unsure, start by listing the current directory
- Relative paths are resolved from the current working directory

Output format:
{"nodes": [{"id":"...","operation":"Create|Read|Update|Delete|Execute","description":"...","tool":"tool_name","parameters":{...},"depends_on":[]}]}

Rules:
- All node IDs must be unique strings
- depends_on must reference existing node IDs
- The graph must be a valid DAG (no cycles)
- Use concrete values from observations — never guess
- If the user's message requires no file/command/web operations, use the "reply" tool
- Only use "blocking" when you genuinely need the user to answer a question
- When using the "reply" tool, format the content parameter with markdown: use code blocks with language tags for code, inline code for paths/commands, bullet lists for items
- Output ONLY the JSON object, no prose, no markdown fences`

	planMsgs := make([]APIMessage, 0, len(sessionMessages)+1)
	planMsgs = append(planMsgs, APIMessage{Role: "system", Content: planningPrompt})
	planMsgs = append(planMsgs, sessionMessages...)

	const maxPlanRetries = 2
	for retry := 0; retry <= maxPlanRetries; retry++ {
		planResp, err := a.complete(planMsgs, nil)
		if err != nil {
			a.emitError(emit, err)
			return nil
		}
		if planResp.Usage != nil {
			emit(AgentEvent{Type: "usage", Data: map[string]int{
				"prompt":     planResp.Usage.PromptTokens,
				"completion": planResp.Usage.CompletionTokens,
			}})
		}

		planJSON := planResp.Message.Content
		logAgent("plan response (retry=%d): %s", retry, truncate(planJSON, 300))

		execDAG, parseErr := dag.Parse(planJSON)
		if parseErr == nil {
			return execDAG
		}

		logAgent("DAG parse failed (retry=%d): %v", retry, parseErr)

		trimmed := strings.TrimSpace(planJSON)
		if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "```") {
			// LLM produced conversational text — wrap in reply node
			replyNode := &dag.Node{
				ID:          "reply",
				Operation:   dag.OpRead,
				Description: "conversational response",
				Tool:        "reply",
				Parameters:  map[string]any{"content": trimmed},
				DependsOn:   []string{},
			}
			logAgent("wrapped conversational text in reply node")
			return &dag.DAG{
				Nodes:      map[string]*dag.Node{"reply": replyNode},
				Dependents: map[string][]string{},
			}
		}

		// Not the last retry — give error feedback and retry
		if retry < maxPlanRetries {
			logAgent("retrying plan with error feedback: %v", parseErr)
			planMsgs = append(planMsgs, APIMessage{Role: "assistant", Content: planJSON})
			planMsgs = append(planMsgs, APIMessage{Role: "user", Content: fmt.Sprintf(
				"Your JSON is invalid: %v\n\nFix the JSON errors and output ONLY the corrected JSON. Remember: escape all quotes as \\\", backslashes as \\\\, newlines as \\n. No markdown fences, no prose.",
				parseErr,
			)})
			continue
		}

		// All retries exhausted
		emit(AgentEvent{Type: "error", Data: fmt.Sprintf("Planning produced invalid DAG after %d retries: %v", maxPlanRetries+1, parseErr)})
		return nil
	}

	return nil
}
