package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"time"

	"crude/logger"
	"crude/provider"
	"crude/safety"
	"crude/statemachine"
	"crude/tools"

	"crude/fileio"
)

type AgentEvent struct {
	Type    string
	Data    any
	AgentID string
}

const (
	maxSteps          = 10000
	loopDetectWindow  = 8
	loopDetectRepeats = 4
	contextSafetyPct  = 0.85
)

type StepRecord struct {
	Content      string
	ToolName     string
	ToolInput    string
	ToolResult   string
	FinishReason string
}

type stopCondition func(steps []StepRecord) bool

type Agent struct {
	Provider *provider.Provider
	Model    string
	Policy   safety.Policy
	ToolFn   func(tool string, params map[string]any) (tools.ToolResult, error)
	logger   *logger.Logger
	turn     *logger.TurnLogger
	Emit     func(AgentEvent)
	Ctx      context.Context
	Yolo     bool
	AsyncMgr *fileio.AsyncCommandManager
}

func NewAgent(p *provider.Provider, model string, lg *logger.Logger) *Agent {
	if model == "" {
		model = "google/gemini-2.0-flash-exp:free"
	}
	return &Agent{
		Provider: p,
		Model:    model,
		ToolFn:   func(tool string, params map[string]any) (tools.ToolResult, error) { return tools.ToolResult{}, nil },
		logger:   lg,
		turn:     lg.Turn(),
	}
}

func isStopConditionMet(conditions []stopCondition, steps []StepRecord) bool {
	for _, cond := range conditions {
		if cond(steps) {
			return true
		}
	}
	return false
}

func hasRepeatedToolCalls(steps []StepRecord, windowSize, maxRepeats int) bool {
	if len(steps) < windowSize {
		return false
	}
	window := steps[len(steps)-windowSize:]
	sigCount := make(map[string]int)
	for _, s := range window {
		if s.ToolName == "" {
			continue
		}
		h := sha256.New()
		io.WriteString(h, s.ToolName)
		io.WriteString(h, "\x00")
		io.WriteString(h, s.ToolInput)
		io.WriteString(h, "\x00")
		io.WriteString(h, s.ToolResult)
		io.WriteString(h, "\x00")
		sig := hex.EncodeToString(h.Sum(nil))
		sigCount[sig]++
		if sigCount[sig] > maxRepeats {
			return true
		}
	}
	return false
}

func (a *Agent) Run(
	ctx context.Context,
	sessionMessages []provider.ChatMessage,
	systemPrompt string,
	emit func(AgentEvent),
	blockResolveCh <-chan string,
	sm *statemachine.ConversationStateMachine,
	sessionID string,
) []provider.ChatMessage {
	l := a.logger
	allTools := allToolDefs()

	l.Debug("AGENT", "run_enter", "msgs", len(sessionMessages), "tools", len(allTools), "session_id", sessionID)

	lastReply := ""
	msgs := []provider.ChatMessage{
		{Role: "system", Content: systemPrompt, CacheControl: &provider.CacheControl{Type: "ephemeral"}},
	}
	for _, m := range sessionMessages {
		if m.Role != "system" {
			msgs = append(msgs, m)
		}
	}

	buildSMStatusMsg := func() *provider.ChatMessage {
		if sm == nil || sessionID == "" {
			return nil
		}
		parts := make([]string, 0, 2)
		if status := BuildStateMachineStatus(sm, sessionID); status != "" {
			parts = append(parts, "## Current State\n"+status)
		}
		if a.AsyncMgr != nil {
			if asyncStatus := BuildAsyncCommandStatus(a.AsyncMgr); asyncStatus != "" {
				parts = append(parts, asyncStatus)
			}
		}
		if len(parts) == 0 {
			return nil
		}
		return &provider.ChatMessage{Role: "system", Content: strings.Join(parts, "\n\n"), CacheControl: &provider.CacheControl{Type: "ephemeral"}}
	}

	var steps []StepRecord
	var totalPromptTokens, totalCompletionTokens int
	var lastPromptTokens int

	ctxWindow := a.Provider.ContextWindow()
	ctxLimit := int(float64(ctxWindow) * contextSafetyPct)
	l.Debug("CTX", "window", "model", a.Model, "ctx_window", ctxWindow, "safety_limit", ctxLimit, "safety_pct", contextSafetyPct)

	stopConditions := []stopCondition{
		func(steps []StepRecord) bool {
			return hasRepeatedToolCalls(steps, loopDetectWindow, loopDetectRepeats)
		},
		// Context overflow: prompt_tokens in the API response is the actual
		// size of the context sent this turn, NOT a delta. We must compare
		// the per-turn value against ctxLimit — comparing a cumulative sum
		// would falsely terminate the agent once the total spent across all
		// turns exceeds the limit, even if each individual turn fits easily.
		func(steps []StepRecord) bool {
			return ctxLimit > 0 && lastPromptTokens > ctxLimit
		},
	}

	for step := 0; step < maxSteps; step++ {
		if ctx.Err() != nil {
			l.Warn("CANCEL", "context_cancelled", "step", step, "reason", ctx.Err().Error())
			emit(AgentEvent{Type: "done", Data: "Cancelled."})
			return msgs
		}

		if smStatus := buildSMStatusMsg(); smStatus != nil {
			replaced := false
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i].Role == "system" && containsPrefix(msgs[i].Content, "## Current State") {
					msgs[i] = *smStatus
					replaced = true
					break
				}
			}
			if !replaced {
				msgs = append(msgs, *smStatus)
			}
		}

		var accumulatedText string
		var toolCalls []provider.ToolCall
		prevPreviewKeys := make(map[string]string)

		content, reasoningContent, finishReason, streamToolCalls, promptTokens, completionTokens, promptTokensCached, err := a.completeStream(ctx, msgs, allTools,
			func(token string) {
				emit(AgentEvent{Type: "stream_token", Data: token})
			},
			func(text string) {
				emit(AgentEvent{Type: "thinking", Data: text})
			},
			func(id, name string) {
				emit(AgentEvent{Type: "tool_call_streaming", Data: map[string]any{
					"tool_id": id,
					"name":    name,
					"preview": tools.FormatPreview(name, ExtractPreview(id, name, "").Params),
				}})
			},
			func(id, name, args string) {
				preview := ExtractPreview(id, name, args)
				path := getStr(preview.Params, "path")
				command := getStr(preview.Params, "command")
				urlStr := getStr(preview.Params, "url")
				query := getStr(preview.Params, "query")
				source := getStr(preview.Params, "source")
				dest := getStr(preview.Params, "destination")
				pattern := getStr(preview.Params, "pattern")
				handle := getStr(preview.Params, "handle")
				seconds := getStr(preview.Params, "seconds")

				var offsetVal int
				if o, ok := preview.Params["offset"]; ok {
					switch v := o.(type) {
					case float64:
						offsetVal = int(v)
					case int:
						offsetVal = v
					}
				}
				var limitVal int
				if l, ok := preview.Params["limit"]; ok {
					switch v := l.(type) {
					case float64:
						limitVal = int(v)
					case int:
						limitVal = v
					}
				}

				contentStr := getStr(preview.Params, "content")
				oldStr := getStr(preview.Params, "old_string")
				newStr := getStr(preview.Params, "new_string")
				if newStr == "" {
					newStr = getStr(preview.Params, "new_text")
				}

				contentLines := 0
				if contentStr != "" {
					contentLines = CountLines(contentStr)
				}
				oldLines := 0
				if oldStr != "" {
					oldLines = CountLines(oldStr)
				}
				newLines := 0
				if newStr != "" {
					newLines = CountLines(newStr)
				}

				previewKey := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%d|%d|%d|%d|%d",
					path, command, urlStr, query, source, dest, pattern,
					offsetVal, limitVal, contentLines, oldLines, newLines)
				if prev, ok := prevPreviewKeys[id]; ok && prev == previewKey {
					return
				}
				prevPreviewKeys[id] = previewKey

				data := map[string]any{
					"tool_id": id,
					"name":    name,
					"preview": tools.FormatPreview(name, preview.Params),
					"args":    args,
				}
				if path != "" {
					data["path"] = path
				}
				if command != "" {
					data["command"] = command
				}
				if urlStr != "" {
					data["url"] = urlStr
				}
				if query != "" {
					data["query"] = query
				}
				if source != "" {
					data["source"] = source
				}
				if dest != "" {
					data["destination"] = dest
				}
				if pattern != "" {
					data["pattern"] = pattern
				}
				if handle != "" {
					data["handle"] = handle
				}
				if seconds != "" {
					data["seconds"] = seconds
				}
				if offsetVal > 0 {
					data["offset"] = offsetVal
				}
				if limitVal > 0 {
					data["limit"] = limitVal
				}
				if contentLines > 0 {
					data["content_lines"] = contentLines
				}
				if oldLines > 0 {
					data["old_lines"] = oldLines
				}
				if newLines > 0 {
					data["new_lines"] = newLines
				}
				emit(AgentEvent{Type: "tool_call_streaming", Data: data})
			},
			func(attempt int, backoffSeconds float64) {
				if attempt <= 3 {
					return
				}
				emit(AgentEvent{Type: "retry", Data: map[string]any{
					"attempt":   attempt,
					"backoff":   backoffSeconds,
				}})
			},
		)
		if err != nil {
			a.emitError(emit, err)
			return msgs
		}

		totalPromptTokens += promptTokens
		totalCompletionTokens += completionTokens
		lastPromptTokens = promptTokens

		// Always emit a usage event so the TUI status bar updates.
		// When the API doesn't return token counts (some OpenAI-compatible
		// providers omit usage even with stream_options), estimate tokens
		// from the message content and mark them as estimated.
		estimated := false
		if promptTokens == 0 && completionTokens == 0 {
			promptTokens = provider.EstimateMessagesTotal(msgs, a.Model)
			completionTokens = provider.EstimateTokens(content, a.Model)
			estimated = true
		}

		// Compute cost for this turn using model pricing
		price := provider.LookupPrice(a.Model)
		turnCost := price.CalculateCost(promptTokens, completionTokens, promptTokensCached)
		emit(AgentEvent{Type: "usage", Data: map[string]any{
			"prompt":         promptTokens,
			"completion":     completionTokens,
			"prompt_cached":  promptTokensCached,
			"context_window": ctxWindow,
			"cost":           turnCost,
			"model":          a.Model,
			"estimated":      estimated,
		}})

		accumulatedText = content
		toolCalls = streamToolCalls

		a.logger.Info("LLM", "step_done", "text_len", len(accumulatedText), "tool_calls", len(toolCalls), "finish_reason", finishReason, "prompt_tokens", totalPromptTokens, "ctx_window", ctxWindow)

		if accumulatedText == "" {
			accumulatedText = lastReply
		}

		hasToolCalls := len(toolCalls) > 0
		if hasToolCalls {
			l.Debug("AGENT", "branch_has_tool_calls", "count", len(toolCalls), "step", step)
		} else {
			l.Debug("AGENT", "branch_text_only", "text_len", len(accumulatedText), "step", step)
		}

		assistantMsg := provider.ChatMessage{Role: "assistant", Content: accumulatedText, ReasoningContent: reasoningContent, Reasoning: reasoningContent}
		if hasToolCalls {
			assistantMsg.ToolCalls = toolCalls
		}
		msgs = append(msgs, assistantMsg)

		// LOOP TERMINATION (primary): When the LLM responds with text and NO tool calls,
		// the agentic loop ends naturally. The streamed text becomes the final message.
		// This is the standard termination mechanism — no "stop" tool is needed.
		// The LLM simply stops calling tools and its text response is delivered to the user.
		if !hasToolCalls {
			if accumulatedText != "" {
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": accumulatedText, "reason": "completed"}})
			} else {
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Done.", "reason": "completed"}})
			}
			return msgs
		}

		// Execute tool calls in parallel, collect results.
		// "reply" is a special tool: it emits intermediate messages to the user
		// but does NOT terminate the loop — the agent keeps working.
		type execResult struct {
			id     string
			params map[string]any
			name   string
			result tools.ToolResult
		}
		resultCh := make(chan execResult, len(toolCalls))

		for _, tc := range toolCalls {
			l.Debug("AGENT", "goroutine_start", "tool", tc.Function.Name, "step", step)
			go func(tc provider.ToolCall) {
				var params map[string]any
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &params); err != nil {
					errResult := "Error: invalid arguments: " + err.Error()
					emit(AgentEvent{Type: "tool_call", Data: map[string]any{
						"tool_id": tc.ID,
						"name":    tc.Function.Name,
						"args":    tc.Function.Arguments,
					}})
					emit(AgentEvent{Type: "tool_result", Data: map[string]any{
						"tool_id": tc.ID,
						"name":    tc.Function.Name,
						"result":  errResult,
					}})
					resultCh <- execResult{id: tc.ID, name: tc.Function.Name, result: tools.ToolResult{Content: errResult}}
					return
				}

				if tc.Function.Name == "reply" {
					content := getStr(params, "content")
					lastReply = content
					resultCh <- execResult{id: tc.ID, params: params, name: tc.Function.Name, result: tools.ToolResult{Content: content}}
					return
				}

				if tc.Function.Name == "execute_command" && !a.Yolo {
					cmd := getStr(params, "command")
					sr := safety.CheckCommandSafety(cmd, a.logger)
					if sr.Risk.IsAlwaysBlocked() {
						errMsg := fmt.Sprintf("[SAFETY BLOCK] This command was automatically blocked and will never execute. DO NOT retry this command or any variant of it.\nBlock reason: %s\nSuggestion: %s\nRaw command: %s\n\nThis is NOT a command error — the system prevented a dangerous operation. Move on to a different approach. Explain to the user why this was blocked and what you'll do instead.", sr.Reason, sr.Suggestion, cmd)
						a.logger.Warn("SAFETY", "blocked", "command", cmd, "reason", sr.Reason)
						emit(AgentEvent{Type: "tool_result", Data: map[string]any{
							"tool_id": tc.ID,
							"name":    "execute_command",
							"result":  errMsg,
						}})
						resultCh <- execResult{id: tc.ID, name: "execute_command", result: tools.ToolResult{Content: errMsg}}
						return
					}
					if sr.Risk.NeedsConfirmation() {
						emit(AgentEvent{Type: "blocking", Data: map[string]string{
							"operation":  sr.Risk.String(),
							"tool":       "execute_command",
							"target":     cmd,
							"question":   fmt.Sprintf("Allow this %s command?", sr.Risk.String()),
							"risk":       sr.Risk.String(),
							"reason":     sr.Reason,
							"suggestion": sr.Suggestion,
						}})
						resp := <-blockResolveCh
						if resp == "deny" {
							errMsg := fmt.Sprintf("[SAFETY BLOCK] The user denied this command. DO NOT retry it or any variant.\nRisk level: %s\nDeny reason: %s\nSuggestion: %s\nRaw command: %s\n\nThis is NOT a command error — the user explicitly rejected this operation. Move on to a different approach. Tell the user what you'll do instead.", sr.Risk.String(), sr.Reason, sr.Suggestion, cmd)
							a.logger.Warn("SAFETY", "denied", "command", cmd, "risk", sr.Risk.String())
							emit(AgentEvent{Type: "tool_result", Data: map[string]any{
								"tool_id": tc.ID,
								"name":    "execute_command",
								"result":  errMsg,
							}})
							resultCh <- execResult{id: tc.ID, name: "execute_command", result: tools.ToolResult{Content: errMsg}}
							return
						}
						emit(AgentEvent{Type: "blocking_complete"})
						if resp == "always" {
							emit(AgentEvent{Type: "confirmation_always", Data: map[string]string{
								"operation": sr.Risk.String(),
								"tool":      "execute_command",
								"target":    cmd,
							}})
						}
					}
				}

				// File mutation tools — require user confirmation before executing
				fileCheck := false
				fileOp := ""
				fileTarget := ""
				filePath := ""
				switch tc.Function.Name {
				case "write_file":
					fileCheck = true
					fileOp = "Create"
					filePath = getStr(params, "path")
					fileTarget = filePath
				case "edit_file":
					fileCheck = true
					fileOp = "Update"
					filePath = getStr(params, "path")
					fileTarget = filePath
				case "delete_file":
					fileCheck = true
					fileOp = "Delete"
					filePath = getStr(params, "path")
					fileTarget = filePath
				case "move_file":
					fileCheck = true
					fileOp = "Update"
					src := getStr(params, "source")
					dst := getStr(params, "destination")
					filePath = dst
					fileTarget = src + " → " + dst
				case "copy_file":
					fileCheck = true
					fileOp = "Create"
					src := getStr(params, "source")
					dst := getStr(params, "destination")
					filePath = dst
					fileTarget = src + " → " + dst
				}

				if fileCheck && !a.Yolo {
					question := fmt.Sprintf("Allow this %s operation on %s?", fileOp, fileTarget)
					emit(AgentEvent{Type: "blocking", Data: map[string]string{
						"operation":  fileOp,
						"tool":       tc.Function.Name,
						"target":     fileTarget,
						"question":   question,
						"risk":       "write",
						"reason":     fmt.Sprintf("The %s tool modifies files on disk", tc.Function.Name),
						"suggestion": "",
					}})
					resp := <-blockResolveCh
					if resp == "deny" {
						errMsg := fmt.Sprintf("[SAFETY BLOCK] The user denied this %s operation. DO NOT retry it or any variant.\nTool: %s\nTarget: %s\n\nThis is NOT a tool error — the user explicitly rejected this operation. Move on to a different approach. Tell the user what you'll do instead.", fileOp, tc.Function.Name, fileTarget)
						a.logger.Warn("SAFETY", "denied", "tool", tc.Function.Name, "target", fileTarget, "op", fileOp)
						emit(AgentEvent{Type: "tool_result", Data: map[string]any{
							"tool_id": tc.ID,
							"name":    tc.Function.Name,
							"result":  errMsg,
						}})
						resultCh <- execResult{id: tc.ID, name: tc.Function.Name, result: tools.ToolResult{Content: errMsg}}
						return
					}
					emit(AgentEvent{Type: "blocking_complete"})
					if resp == "always" {
						emit(AgentEvent{Type: "confirmation_always", Data: map[string]string{
							"operation": fileOp,
							"tool":      tc.Function.Name,
							"target":    filePath,
						}})
					}
				}

				// Format args as compact key=value pairs instead of Go's map[key:val]
				argsStr := formatToolArgs(params)
				emit(AgentEvent{Type: "tool_call", Data: map[string]any{
					"tool_id": tc.ID,
					"name":    tc.Function.Name,
					"args":    argsStr,
				}})

				path := getStr(params, "path")

				if tc.Function.Name == "write_file" {
					content := getStr(params, "content")
					lines := CountLines(content)
					emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
						"tool_id": tc.ID,
						"name":    tc.Function.Name,
						"phase":   "writing",
						"path":    path,
						"message": fmt.Sprintf("Writing %d lines to %s", lines, path),
					}})
				} else if tc.Function.Name == "edit_file" {
					oldStr := getStr(params, "old_string")
					newStr := getStr(params, "new_string")
					oldLines := CountLines(oldStr)
					newLines := CountLines(newStr)
					emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
						"tool_id": tc.ID,
						"name":    tc.Function.Name,
						"phase":   "patching",
						"path":    path,
						"message": fmt.Sprintf("Editing %s (−%d/+%d lines)", path, oldLines, newLines),
					}})
				} else if tc.Function.Name == "read_file" {
					if path != "" {
						emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
							"tool_id": tc.ID,
							"name":    tc.Function.Name,
							"phase":   "reading",
							"path":    path,
							"message": fmt.Sprintf("Reading %s", path),
						}})
					}
				} else if tc.Function.Name == "execute_command" {
					cmd := getStr(params, "command")
					if cmd != "" {
						display := cmd
						if len(display) > 60 {
							display = display[:60] + "\u2026"
						}
						emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
							"tool_id": tc.ID,
							"name":    tc.Function.Name,
							"phase":   "running",
							"message": fmt.Sprintf("Running %s", display),
						}})
					}
				}

				a.logger.Info("TOOL", "call", "name", tc.Function.Name, "params", formatToolArgs(params))
				toolStart := time.Now()
				result, _ := a.ToolFn(tc.Function.Name, params)
				toolDur := time.Since(toolStart).Milliseconds()

				if strings.HasPrefix(result.Content, "Error:") {
					a.logger.Error("TOOL", "error", "name", tc.Function.Name, "error", result.Content, "latency_ms", toolDur)
				} else {
					a.logger.Info("TOOL", "result", "name", tc.Function.Name, "latency_ms", toolDur, "len", len(result.Content))
				}

				if (tc.Function.Name == "write_file" || tc.Function.Name == "edit_file") && !strings.HasPrefix(result.Content, "Error:") {
					emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
						"tool_id":       tc.ID,
						"name":          tc.Function.Name,
						"phase":         "done",
						"path":          path,
						"message":       fmt.Sprintf("\u2212%d/+%d lines", result.Removed, result.Added),
						"lines_added":   result.Added,
						"lines_removed": result.Removed,
					}})
				}

				durSeconds := fmt.Sprintf("%.0f", time.Since(toolStart).Seconds())
				emit(AgentEvent{Type: "tool_result", Data: map[string]any{
					"tool_id":  tc.ID,
					"name":     tc.Function.Name,
					"result":   result.Content,
					"duration": durSeconds,
				}})

				resultCh <- execResult{id: tc.ID, params: params, name: tc.Function.Name, result: result}
			}(tc)
		}

		var lastToolResultIdx int
		for i := 0; i < len(toolCalls); i++ {
			select {
			case r := <-resultCh:
				msgs = append(msgs, provider.ChatMessage{
					Role:       "tool",
					ToolCallID: r.id,
					Content:    r.result.Content,
				})
				lastToolResultIdx = len(msgs) - 1
				steps = append(steps, StepRecord{
					Content:      accumulatedText,
					ToolName:     r.name,
					ToolInput:    fmt.Sprintf("%v", r.params),
					ToolResult:   r.result.Content,
					FinishReason: finishReason,
				})
				if r.name == "reply" && r.result.Content != "" {
					emit(AgentEvent{Type: "stream_token", Data: r.result.Content})
				}
			case <-ctx.Done():
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
				return msgs
			}
		}

		// Mark the last tool result as a cache breakpoint so the entire
		// conversation prefix up to this point is eligible for caching
		// on the next API call.
		if lastToolResultIdx > 0 && lastToolResultIdx < len(msgs) {
			msgs[lastToolResultIdx].CacheControl = &provider.CacheControl{Type: "ephemeral"}
			l.Debug("CACHE", "breakpoint", "msg_idx", lastToolResultIdx, "role", msgs[lastToolResultIdx].Role)
		}

		// LOOP TERMINATION (safety): Stop conditions catch infinite loops
		// (repeated identical tool calls) and context window overflow.
		if isStopConditionMet(stopConditions, steps) {
			reason := "stop_condition_met"
			if lastPromptTokens > ctxLimit {
				reason = fmt.Sprintf("context_limit_reached (%d/%d tokens)", lastPromptTokens, ctxWindow)
			}
			l.Warn("AGENT", "stop_condition", "reason", reason, "steps", len(steps), "prompt_tokens", lastPromptTokens, "total_prompt_tokens_spent", totalPromptTokens, "ctx_window", ctxWindow)
			if lastReply != "" {
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": lastReply, "reason": reason}})
			} else {
				emit(AgentEvent{Type: "done", Data: map[string]any{"content": accumulatedText, "reason": reason}})
			}
			return msgs
		}
	}

	l.Warn("AGENT", "max_steps", "steps", maxSteps, "total_prompt_tokens", totalPromptTokens)
	emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Reached maximum " + fmt.Sprint(maxSteps) + " steps. Task may be incomplete.", "reason": "max_steps_reached"}})
	return msgs
}

func getStr(params map[string]any, key string) string {
	if v, ok := params[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		if v == nil {
			return ""
		}
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func isRetryable(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

func retryBackoff(attempt int) time.Duration {
	if attempt <= 1 {
		return time.Duration(500+rand.Intn(1500)) * time.Millisecond
	}
	base := time.Duration(1<<min(attempt-1, 6)) * time.Second
	jitter := time.Duration(rand.Intn(int(base))) / 4
	return base + jitter
}

func allToolDefs() []provider.ToolDef {
	ephemeral := &provider.CacheControl{Type: "ephemeral"}
	result := make([]provider.ToolDef, len(tools.AgentTools))
	for i, td := range tools.AgentTools {
		result[i] = provider.ToolDef{
			Type:         td.Type,
			Function:     provider.FunctionDef{
				Name:        td.Function.Name,
				Description: td.Function.Description,
				Parameters:  td.Function.Parameters,
			},
			CacheControl: ephemeral,
		}
	}
	return result
}

func containsPrefix(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return s[:len(prefix)] == prefix
}

// formatToolArgs produces a compact key:value string from a params map
// instead of Go's default map[key:val] formatting.
func formatToolArgs(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}
	// Collect and sort keys for deterministic output
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		v := params[k]
		switch val := v.(type) {
		case string:
			if len(val) > 80 {
				parts = append(parts, k+": "+val[:80]+"…")
			} else {
				parts = append(parts, k+": "+val)
			}
		case []any:
			// Format arrays as comma-separated values
			var items []string
			for _, item := range val {
				if s, ok := item.(string); ok && s != "" {
					items = append(items, s)
				}
			}
			parts = append(parts, k+": "+strings.Join(items, ","))
		default:
			parts = append(parts, fmt.Sprintf("%s: %v", k, v))
		}
	}
	return strings.Join(parts, " ")
}

func BuildStateMachineStatus(sm *statemachine.ConversationStateMachine, sessionID string) string {
	// ... unchanged from original, see original file
	if sm == nil {
		return ""
	}

	state := sm.GetState(sessionID)
	if state == nil {
		return ""
	}

	focus := sm.GetFocusGoal(sessionID)
	if focus == nil && len(state.ThreadRoots) == 0 {
		return ""
	}

	var sb strings.Builder

	if focus != nil {
		statusLabel := goalStatusLabel(focus.Status)
		isPlan := strings.HasPrefix(focus.Intent, "plan:")

		if isPlan {
			objective := strings.TrimPrefix(focus.Intent, "plan: ")
			// Truncate the objective to keep the status label visible.
			// Format: "Plan: [objective] (status)" — reserve ~10 chars for "Plan: " and ~12 for " (status)".
			maxObjRunes := 80
			objRunes := []rune(objective)
			if len(objRunes) > maxObjRunes {
				objective = string(objRunes[:maxObjRunes]) + "…"
			}
			sb.WriteString(fmt.Sprintf("Plan: %s (%s)\n", objective, statusLabel))

			rawStatuses, _ := state.GetFromContext("plan_step_statuses")
			rawLabels, _ := state.GetFromContext("plan_step_labels")
			stepStatuses, _ := rawStatuses.(map[string]string)
			stepLabels, _ := rawLabels.(map[string]string)

			if stepStatuses != nil {
				total := 0
				completed := 0
				rawSteps, _ := state.GetFromContext("plan_steps")
				if steps, ok := rawSteps.([]map[string]any); ok {
					total = len(steps)
					for _, step := range steps {
						id, _ := step["id"].(string)
						desc := stepLabels[id]
						status := stepStatuses[id]
						marker := persistentStepMarker(status)
						if status == "completed" {
							completed++
						}
						line := fmt.Sprintf("  %s %s: %s", marker, id, desc)
						sb.WriteString(line + "\n")
					}
				} else {
					for id, status := range stepStatuses {
						marker := persistentStepMarker(status)
						desc := stepLabels[id]
						line := fmt.Sprintf("  %s %s: %s", marker, id, desc)
						if status == "completed" {
							completed++
						}
						total++
						sb.WriteString(line + "\n")
					}
				}

				var summaryParts []string
				summaryParts = append(summaryParts, fmt.Sprintf("%d/%d steps", completed, total))
				if focus.CurrentBlocker != nil {
					summaryParts = append(summaryParts, fmt.Sprintf("blocker: %s", focus.CurrentBlocker.Message))
				}
				if len(summaryParts) > 0 {
					sb.WriteString(fmt.Sprintf("  %s\n", strings.Join(summaryParts, "  |  ")))
				}
			}
		} else {
			sb.WriteString(fmt.Sprintf("Goal: %s (%s)\n", focus.Intent, statusLabel))
			if focus.OriginalQuery != "" {
				sb.WriteString(fmt.Sprintf("  request: %s\n", focus.OriginalQuery))
			}
			if focus.CurrentBlocker != nil {
				sb.WriteString(fmt.Sprintf("  blocker [%s]: %s\n", focus.CurrentBlocker.Type.String(), focus.CurrentBlocker.Message))
			}
		}
	}

	suspended := sm.GetSuspendedGoals(sessionID)
	if len(suspended) > 0 {
		sb.WriteString("Suspended:\n")
		for _, g := range suspended {
			sb.WriteString(fmt.Sprintf("  %s (%s)\n", g.Intent, goalStatusLabel(g.Status)))
		}
	}

	return strings.TrimSpace(sb.String())
}

func persistentStepMarker(status string) string {
	switch status {
	case "completed":
		return "✓"
	case "doing":
		return "→"
	case "active":
		return "→"
	case "blocked":
		return "!"
	case "failed":
		return "✗"
	default:
		return "☐"
	}
}

func stepStatusMarker(s statemachine.GoalStatus) string {
	switch s {
	case statemachine.GoalCompleted:
		return "✓"
	case statemachine.GoalActive:
		return "→"
	case statemachine.GoalBlocked:
		return "!"
	case statemachine.GoalFailed:
		return "✗"
	default:
		return "☐"
	}
}

func goalStatusLabel(s statemachine.GoalStatus) string {
	switch s {
	case statemachine.GoalPending:
		return "pending"
	case statemachine.GoalActive:
		return "active"
	case statemachine.GoalBlocked:
		return "blocked"
	case statemachine.GoalCompleted:
		return "completed"
	case statemachine.GoalFailed:
		return "failed"
	default:
		return "unknown"
	}
}

func BuildAsyncCommandStatus(mgr *fileio.AsyncCommandManager) string {
	commands := mgr.List()
	if len(commands) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Async Commands\n")
	shown := 0
	for _, c := range commands {
		if c.Status != fileio.AsyncRunning && time.Since(c.EndedAt) > 60*time.Second {
			continue
		}
		shown++
		marker := "?"
		switch c.Status {
		case fileio.AsyncRunning:
			marker = "→"
		case fileio.AsyncCompleted:
			marker = "✓"
		case fileio.AsyncFailed:
			marker = "✗"
		case fileio.AsyncKilled:
			marker = "!"
		}
		var dur string
		dur = c.Duration.Round(time.Second).String()
		sb.WriteString(fmt.Sprintf("  %s [%s]: %s (PID %d, %s)", marker, c.Handle, c.Status, c.PID, dur))
		if c.Status == fileio.AsyncFailed && c.Error != "" {
			sb.WriteString(" — " + c.Error)
		}
		sb.WriteString("\n")
		if tail := mgr.OutputTail(c.Handle, 250); tail != "" {
			lines := strings.Split(strings.TrimRight(tail, "\n"), "\n")
			for _, line := range lines {
				sb.WriteString("    | " + line + "\n")
			}
		}
	}
	if shown == 0 {
		return ""
	}
	return strings.TrimSpace(sb.String())
}
