package agent

import (
	"fmt"
	"strings"

	"crude/dag"
	"crude/rag"
)

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
Generate an XML execution plan. Output ONLY valid XML.

CRITICAL — XML rules:
- Use <![CDATA[...]]> for any code, multi-line text, or text containing special characters (<, >, &, quotes, backslashes)
- CDATA lets you write content verbatim — no escaping needed inside CDATA
- For short simple values (file paths, single words), plain text is fine
- All string content inside CDATA is safe — no need to escape quotes, newlines, backslashes, etc.

Examples of CORRECT XML:
  <param name="path">config.yaml</param>
  <param name="content"><![CDATA[line1
line2
line3 with "quotes" and \backslashes]]></param>
  <param name="old_string"><![CDATA[func Old() { return "hello"; }]]></param>

Available tools: read_file, write_file, edit_file, delete_file, execute_command, list_directory, glob_files, grep_files, web_search, web_fetch, arxiv_search, run_javascript, reply, blocking

Path resolution rules:
- If the user mentions a project, files, or directories WITHOUT specifying a path, use "." (current working directory)
- Examples: "show me the project structure" → list_directory(".") ; "what files are there" → list_directory(".") ; "read the config file" → if you don't know the path, start with list_directory(".")
- NEVER guess or invent paths. If unsure, start by listing the current directory
- Relative paths are resolved from the current working directory

Output format:
<plan>
  <node id="unique_id" operation="Create|Read|Update|Delete|Execute" description="brief description" tool="tool_name" depends_on="other_id">
    <parameters>
      <param name="key">value or <![CDATA[raw content]]></param>
    </parameters>
  </node>
</plan>

Rules:
- All node IDs must be unique strings
- depends_on must reference existing node IDs (comma-separated if multiple, e.g. depends_on="id1,id2")
- The graph must be a valid DAG (no cycles)
- Use concrete values from observations — never guess
- If the user's message requires no file/command/web operations, use the "reply" tool
- Only use "blocking" when you genuinely need the user to answer a question
- When using the "reply" tool, format the content parameter with markdown: use code blocks with language tags for code, inline code for paths/commands, bullet lists for items
- Output ONLY the XML, no prose, no markdown fences`

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

		planXML := planResp.Message.Content
		a.logger.Info("agent.plan", "content", planXML, "nodes", 0)

		execDAG, parseErr := dag.Parse(planXML)
		if parseErr == nil {
			a.logger.Info("agent.plan", "content", planXML, "nodes", len(execDAG.Nodes))
			return execDAG
		}

		a.logger.Info("agent.plan", "content", fmt.Sprintf("parse failed: %v", parseErr), "retry", retry)

		trimmed := strings.TrimSpace(planXML)
		isXML := strings.HasPrefix(trimmed, "<") || strings.HasPrefix(trimmed, "```")
		if !isXML {
			replyNode := &dag.Node{
				ID:          "reply",
				Operation:   dag.OpRead,
				Description: "conversational response",
				Tool:        "reply",
				Parameters:  map[string]any{"content": trimmed},
				DependsOn:   []string{},
			}
			a.logger.Info("agent.decision", "reason", "wrapped conversational text in reply node", "action", "create_reply_node", "confidence", 1.0)
			return &dag.DAG{
				Nodes:      map[string]*dag.Node{"reply": replyNode},
				Dependents: map[string][]string{},
			}
		}

		if retry < maxPlanRetries {
			a.logger.Info("agent.plan", "content", fmt.Sprintf("retrying with error feedback: %v", parseErr), "retry", retry)
			planMsgs = append(planMsgs, APIMessage{Role: "assistant", Content: planXML})
			planMsgs = append(planMsgs, APIMessage{Role: "user", Content: fmt.Sprintf(
				"Your XML is invalid: %v\n\nFix the XML errors and output ONLY the corrected XML. Remember: use <![CDATA[...]]> for code and multi-line content. No markdown fences, no prose.",
				parseErr,
			)})
			continue
		}

		emit(AgentEvent{Type: "error", Data: fmt.Sprintf("Planning produced invalid DAG after %d retries: %v", maxPlanRetries+1, parseErr)})
		return nil
	}

	return nil
}
