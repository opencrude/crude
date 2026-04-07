package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"crude/dag"
	"crude/provider"
	"crude/rag"
	"crude/safety"
	"crude/statemachine"
)

type apiCallError struct {
	Message          string
	RetriesExhausted bool
}

func (e *apiCallError) Error() string { return e.Message }

const (
	maxIterations    = 30
	maxGroundingIter = 10
	maxAPIAttempts   = 3
)

// chitchatPrefixes are greeting/casual message prefixes that don't need grounding.
var chitchatPrefixes = []string{
	"hello", "hi ", "hey", "greetings", "good morning", "good afternoon",
	"good evening", "howdy", "sup", "yo ", "what's up", "whats up",
	"how are you", "how r u", "hru", "thanks", "thank you", "ty ",
	"ok", "okay", "sure", "yes", "no ", "nope", "yep", "yeah",
	"bye", "goodbye", "see you", "cya", "later",
}

// isChitchat returns true if the message is a simple greeting or casual remark
// that doesn't require system grounding or tool execution.
func isChitchat(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))
	if len(lower) == 0 {
		return false
	}
	// Exact short matches
	if len(lower) <= 4 {
		switch lower {
		case "hi", "hey", "yo", "sup", "ok", "ty", "yes", "no", "nope", "yep", "bye", "cya":
			return true
		}
	}
	for _, prefix := range chitchatPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

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

// Agent calls an LLM provider with a three-phase loop: Ground → Plan → Execute.
type Agent struct {
	Provider        *provider.Provider
	Model           string
	Policy          safety.Policy
	AuditLogger     *safety.AuditLogger
	RollbackHandler *safety.RollbackHandler
}

// NewAgent creates an Agent.
func NewAgent(p *provider.Provider, model string) *Agent {
	if model == "" {
		model = "google/gemini-2.0-flash-exp:free"
	}
	return &Agent{
		Provider: p,
		Model:    model,
		Policy:   safety.DefaultPolicy(),
	}
}

// Run executes the three-phase agentic loop.
func (a *Agent) Run(
	sessionMessages []APIMessage,
	systemPrompt string,
	emit func(AgentEvent),
	ragEngine *rag.Engine,
	blockResolveCh <-chan string,
	sm *statemachine.ConversationStateMachine,
	sessionID string,
) {
	// ── Check for chitchat — skip grounding/planning entirely ──
	lastUserMsg := ""
	for i := len(sessionMessages) - 1; i >= 0; i-- {
		if sessionMessages[i].Role == "user" {
			lastUserMsg = sessionMessages[i].Content
			break
		}
	}

	if isChitchat(lastUserMsg) {
		emit(AgentEvent{Type: "phase", Data: "replying"})
		reply := a.directReply(sessionMessages, systemPrompt, emit)
		if reply != "" {
			emit(AgentEvent{Type: "done", Data: reply})
			return
		}
		// Fallback to full pipeline if direct reply fails.
	}

	// ── Phase 1: GROUNDING (no function calling) ─────────────────
	emit(AgentEvent{Type: "phase", Data: "grounding"})
	observations := a.ground(sessionMessages, systemPrompt, emit, ragEngine)

	// ── Phase 2: PLANNING ────────────────────────────────────────
	emit(AgentEvent{Type: "phase", Data: "planning"})
	execDAG := a.plan(sessionMessages, systemPrompt, observations, emit, ragEngine)
	if execDAG == nil {
		return
	}

	// ── Phase 3: EXECUTION ───────────────────────────────────────
	emit(AgentEvent{Type: "phase", Data: "executing"})
	a.execute(execDAG, emit, blockResolveCh, sm, sessionID)

	// ── Check for blocking node response ─────────────────────────
	var blockingResponse string
	for _, n := range execDAG.Nodes {
		if n.Tool == "blocking" && n.Status == dag.Successful && n.Result != "" {
			blockingResponse = n.Result
			break
		}
	}

	if blockingResponse != "" {
		// Blocking node captured user reply — that IS the conversation continuation
		emit(AgentEvent{Type: "done", Data: blockingResponse})
		return
	}

	// ── Check for reply node — skip summarize, emit directly ─────
	for _, n := range execDAG.Nodes {
		if n.Tool == "reply" && n.Status == dag.Successful && n.Result != "" {
			emit(AgentEvent{Type: "done", Data: n.Result})
			return
		}
	}

	// ── Phase 4: SUMMARY ─────────────────────────────────────────
	summary := a.summarize(sessionMessages, execDAG, systemPrompt, emit)
	emit(AgentEvent{Type: "done", Data: summary})
}

// directReply handles chitchat/greetings without grounding or planning.
// It asks the LLM for a brief conversational response.
func (a *Agent) directReply(sessionMessages []APIMessage, systemPrompt string, emit func(AgentEvent)) string {
	replyMsgs := make([]APIMessage, 0, len(sessionMessages)+2)
	replyMsgs = append(replyMsgs, APIMessage{
		Role:    "system",
		Content: systemPrompt + "\n\nRespond to the user's message conversationally. Be brief and friendly. Do NOT use tools or perform system operations.\n\nFormat your response in Markdown: use code blocks with language tags for any code, inline code for paths/commands, and proper formatting.",
	})
	replyMsgs = append(replyMsgs, sessionMessages...)

	content, _, err := a.completeStream(replyMsgs, nil, func(token string) {
		emit(AgentEvent{Type: "stream_token", Data: token})
	})
	if err != nil {
		logAgent("direct reply failed: %v", err)
		return ""
	}
	return content
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

			argsJSON, _ := json.Marshal(op.Parameters)
			tc := ToolCall{
				ID:   fmt.Sprintf("ground-%d", iter),
				Type: "function",
				Function: FunctionCall{
					Name:      op.Tool,
					Arguments: string(argsJSON),
				},
			}
			result := executeTool(tc)

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

// execute runs the DAG with safety integration.
func (a *Agent) execute(
	execDAG *dag.DAG,
	emit func(AgentEvent),
	blockResolveCh <-chan string,
	sm *statemachine.ConversationStateMachine,
	sessionID string,
) {
	// Register goal in state machine.
	if sm != nil {
		sm.CreateGoal(sessionID, "execute_dag", "DAG execution", statemachine.PriorityNormal, false)
	}

	// Pre-check: capture rollback data for mutating operations.
	if a.RollbackHandler != nil {
		for _, n := range execDAG.Nodes {
			if n.Operation.IsDestructive() {
				if err := a.RollbackHandler.CapturePreState(n); err != nil {
					logAgent("rollback capture failed for %s: %v", n.ID, err)
				}
			}
		}
	}

	// Pre-check: mark nodes needing confirmation.
	for _, n := range execDAG.Nodes {
		if a.Policy.NeedsConfirmation(n.Operation) {
			n.RequiresConfirmation = true
			n.ConfirmationReason = fmt.Sprintf("%s operation: %s", n.Operation, n.Description)
		}
		if !a.Policy.IsAllowed(n.Operation) {
			n.Status = dag.Err
			n.RunErr = fmt.Errorf("operation %s not allowed by policy", n.Operation)
		}
	}

	toolFn := func(tool string, params map[string]any) (string, error) {
		argsJSON, err := json.Marshal(params)
		if err != nil {
			return "", fmt.Errorf("marshal tool params: %w", err)
		}
		tc := ToolCall{
			ID:   fmt.Sprintf("dag-%s", tool),
			Type: "function",
			Function: FunctionCall{
				Name:      tool,
				Arguments: string(argsJSON),
			},
		}
		result := executeTool(tc)
		if strings.HasPrefix(result, "Error:") {
			return result, fmt.Errorf("%s", result)
		}
		return result, nil
	}

	eventFn := func(e dag.Event) {
		switch e.Type {
		case "node_start":
			argsJSON, _ := json.Marshal(e.Node.Parameters)
			emit(AgentEvent{Type: "tool_call", Data: map[string]string{
				"id":   e.Node.ID,
				"name": e.Node.Tool,
				"args": string(argsJSON),
			}})
		case "node_done":
			result := ""
			if s, ok := e.Data.(string); ok {
				result = s
			}
			emit(AgentEvent{Type: "tool_result", Data: map[string]string{
				"id":     e.Node.ID,
				"name":   e.Node.Tool,
				"result": result,
			}})
		case "node_error":
			errMsg := fmt.Sprintf("%v", e.Data)
			emit(AgentEvent{Type: "thinking", Data: "[node error: " + errMsg + "]"})
		case "node_skipped":
			reason := fmt.Sprintf("%v", e.Data)
			emit(AgentEvent{Type: "thinking", Data: "[node skipped: " + reason + "]"})
		case "blocking":
			question := fmt.Sprintf("%v", e.Data)
			emit(AgentEvent{Type: "blocking", Data: question})
		case "complete":
			// handled below
		}
	}

	executor := dag.New(execDAG, toolFn, eventFn, blockResolveCh)

	// Wire safety components.
	if a.RollbackHandler != nil {
		executor.SetRollbackFunc(a.RollbackHandler.Rollback)
	}
	executor.SetConfirmFunc(func(node *dag.Node) (bool, error) {
		// Emit blocking event with confirmation question.
		question := fmt.Sprintf("Confirm %s: %s? (yes/no)", node.Operation, node.Description)
		emit(AgentEvent{Type: "blocking", Data: question})
		answer, ok := <-blockResolveCh
		if !ok {
			return false, fmt.Errorf("confirmation channel closed")
		}
		return strings.TrimSpace(strings.ToLower(answer)) == "yes", nil
	})
	if a.AuditLogger != nil {
		executor.SetAuditFunc(a.AuditLogger.Record)
	}

	if runErr := executor.Run(); runErr != nil {
		logAgent("DAG execution error: %v", runErr)
	}

	if sm != nil {
		hasFailure := false
		for _, n := range execDAG.Nodes {
			if n.Status == dag.Err {
				hasFailure = true
				break
			}
		}
		if hasFailure {
			sm.FailCurrentGoal(sessionID, "DAG execution had failures")
		} else {
			sm.CompleteCurrentGoal(sessionID, nil)
		}
	}
}

// summarize asks the LLM to produce a final answer based on execution results.
func (a *Agent) summarize(sessionMessages []APIMessage, execDAG *dag.DAG, systemPrompt string, emit func(AgentEvent)) string {
	summaryText := execDAG.Summary()
	if summaryText == "" {
		summaryText = "Execution complete. No operations were performed."
	}

	summaryMsgs := make([]APIMessage, 0, len(sessionMessages)+3)
	summaryMsgs = append(summaryMsgs, APIMessage{Role: "system", Content: systemPrompt})
	summaryMsgs = append(summaryMsgs, sessionMessages...)
	summaryMsgs = append(summaryMsgs, APIMessage{Role: "assistant", Content: "Execution results:\n" + summaryText})
	summaryMsgs = append(summaryMsgs, APIMessage{Role: "user", Content: `Present the results of your work. Show any data you found (file listings, contents, search results) directly. Do not just say what you did — show the actual results.

CRITICAL — format your response in proper Markdown:
- Use code blocks with language tags for any code or file contents: ` + "```go\n...\n```" + `
- Use bullet lists for file listings or multiple items
- Use inline code for file paths, commands, and short references
- Use headings (##, ###) to organize sections
- Use bold for emphasis on key findings
- NEVER dump raw text without formatting — always use appropriate markdown decorations`})

	content, _, err := a.completeStream(summaryMsgs, nil, func(token string) {
		emit(AgentEvent{Type: "stream_token", Data: token})
	})
	if err != nil {
		return summaryText
	}
	if content != "" {
		return content
	}
	return summaryText
}

// complete sends one request to OpenRouter and returns the first choice.
func (a *Agent) complete(msgs []APIMessage, tools []ToolDef) (*struct {
	Message      APIMessage
	FinishReason string
	Usage        *struct{ PromptTokens, CompletionTokens int }
}, error,
) {
	req := ChatRequest{
		Model:    a.Model,
		Messages: msgs,
		Tools:    tools,
		Stream:   false,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	logAgent("calling %s model=%s msgs=%d tools=%d", a.Provider.Name, a.Model, len(msgs), len(tools))
	client := &http.Client{Timeout: 120 * time.Second}

	for attempt := 1; attempt <= maxAPIAttempts; attempt++ {
		apiStart := time.Now()

		httpReq, err := http.NewRequest("POST", a.Provider.ChatEndpoint(), bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set(a.Provider.AuthHeader, a.Provider.AuthValue())
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(httpReq)
		if err != nil {
			if attempt < maxAPIAttempts {
				bo := retryBackoff(attempt)
				logAgent("request failed attempt=%d/%d err=%v retrying in %s", attempt, maxAPIAttempts, err, bo)
				time.Sleep(bo)
				continue
			}
			return nil, &apiCallError{
				Message:          fmt.Sprintf("API request failed after %d attempts: %v", maxAPIAttempts, err),
				RetriesExhausted: true,
			}
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read response: %w", readErr)
		}

		if resp.StatusCode != 200 {
			snippet := truncate(string(respBody), 240)
			if isRetryable(resp.StatusCode) && attempt < maxAPIAttempts {
				bo := retryBackoff(attempt)
				logAgent("retryable status=%d attempt=%d/%d retrying in %s body=%s",
					resp.StatusCode, attempt, maxAPIAttempts, bo, snippet)
				time.Sleep(bo)
				continue
			}
			if isRetryable(resp.StatusCode) {
				return nil, &apiCallError{
					Message:          fmt.Sprintf("OpenRouter %d after %d attempts: %s", resp.StatusCode, maxAPIAttempts, snippet),
					RetriesExhausted: true,
				}
			}
			return nil, fmt.Errorf("OpenRouter %d: %s", resp.StatusCode, string(respBody))
		}

		var chatResp ChatResponse
		if err := json.Unmarshal(respBody, &chatResp); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}
		if chatResp.Error != nil {
			return nil, fmt.Errorf("API error: %s", chatResp.Error.Message)
		}
		if len(chatResp.Choices) == 0 {
			return nil, fmt.Errorf("empty response from model")
		}

		choice := chatResp.Choices[0]
		logAgent("response  status=%d took=%s finish=%s tools=%d content=%s",
			resp.StatusCode, time.Since(apiStart).Round(time.Millisecond),
			choice.FinishReason, len(choice.Message.ToolCalls),
			truncate(choice.Message.Content, 80))

		var usage *struct{ PromptTokens, CompletionTokens int }
		if chatResp.Usage != nil {
			usage = &struct{ PromptTokens, CompletionTokens int }{
				PromptTokens:     chatResp.Usage.PromptTokens,
				CompletionTokens: chatResp.Usage.CompletionTokens,
			}
		}

		return &struct {
			Message      APIMessage
			FinishReason string
			Usage        *struct{ PromptTokens, CompletionTokens int }
		}{
			Message:      choice.Message,
			FinishReason: choice.FinishReason,
			Usage:        usage,
		}, nil
	}

	return nil, &apiCallError{Message: "API request retries exhausted", RetriesExhausted: true}
}

// completeStream sends a streaming request to the API and calls emit for each token chunk.
// Returns the full accumulated content, finish reason, and any error.
func (a *Agent) completeStream(
	msgs []APIMessage,
	tools []ToolDef,
	emit func(token string),
) (string, string, error) {
	req := ChatRequest{
		Model:    a.Model,
		Messages: msgs,
		Tools:    tools,
		Stream:   true,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", "", fmt.Errorf("marshal request: %w", err)
	}

	logAgent("calling %s model=%s (streaming) msgs=%d tools=%d", a.Provider.Name, a.Model, len(msgs), len(tools))
	client := &http.Client{Timeout: 300 * time.Second}

	for attempt := 1; attempt <= maxAPIAttempts; attempt++ {
		httpReq, err := http.NewRequest("POST", a.Provider.ChatEndpoint(), bytes.NewReader(body))
		if err != nil {
			return "", "", fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set(a.Provider.AuthHeader, a.Provider.AuthValue())
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")

		resp, err := client.Do(httpReq)
		if err != nil {
			if attempt < maxAPIAttempts {
				bo := retryBackoff(attempt)
				logAgent("stream request failed attempt=%d/%d err=%v retrying in %s", attempt, maxAPIAttempts, err, bo)
				time.Sleep(bo)
				continue
			}
			return "", "", &apiCallError{
				Message:          fmt.Sprintf("Stream request failed after %d attempts: %v", maxAPIAttempts, err),
				RetriesExhausted: true,
			}
		}

		if resp.StatusCode != 200 {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			snippet := truncate(string(respBody), 240)
			if isRetryable(resp.StatusCode) && attempt < maxAPIAttempts {
				bo := retryBackoff(attempt)
				logAgent("retryable status=%d attempt=%d/%d retrying in %s body=%s",
					resp.StatusCode, attempt, maxAPIAttempts, bo, snippet)
				time.Sleep(bo)
				continue
			}
			return "", "", fmt.Errorf("OpenRouter %d: %s", resp.StatusCode, snippet)
		}

		var content strings.Builder
		finishReason := ""
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 1024*64), 1024*64)

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var chunk ChatResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			if len(chunk.Choices) == 0 {
				continue
			}

			choice := chunk.Choices[0]
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}

			delta := choice.Delta.Content
			if delta != "" {
				content.WriteString(delta)
				emit(delta)
			}
		}
		resp.Body.Close()

		if err := scanner.Err(); err != nil {
			if attempt < maxAPIAttempts {
				bo := retryBackoff(attempt)
				logAgent("stream scan failed attempt=%d/%d err=%v retrying in %s", attempt, maxAPIAttempts, err, bo)
				time.Sleep(bo)
				continue
			}
			return "", "", fmt.Errorf("stream read error: %w", err)
		}

		logAgent("stream response finish=%s content_len=%d", finishReason, content.Len())
		return content.String(), finishReason, nil
	}

	return "", "", &apiCallError{Message: "Stream request retries exhausted", RetriesExhausted: true}
}

// ── Helpers ──────────────────────────────────────────────────────

func isReadOnlyTool(tool string) bool {
	switch tool {
	case "read_file", "list_directory", "glob_files", "grep_files", "execute_command",
		"web_search", "web_fetch", "arxiv_search", "run_javascript", "reply":
		return true
	default:
		return false
	}
}

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

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func isRetryable(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

func retryBackoff(attempt int) time.Duration {
	if attempt <= 1 {
		return time.Second
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…[truncated]"
}

func logAgent(format string, args ...any) {
	log.Printf("[agent] "+format, args...)
}

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

func (a *Agent) emitError(emit func(AgentEvent), err error) {
	errMsg := err.Error()
	// Clean up API errors — extract short readable message
	if strings.Contains(errMsg, "429") {
		errMsg = "Rate limited — please wait a moment and try again"
	} else if strings.Contains(errMsg, "OpenRouter 5") {
		errMsg = "API server error — please try again"
	} else if idx := strings.Index(errMsg, "API error: "); idx >= 0 {
		errMsg = errMsg[idx+len("API error: "):]
		if len(errMsg) > 120 {
			errMsg = errMsg[:120] + "…"
		}
	}
	emit(AgentEvent{Type: "error", Data: errMsg})
}
