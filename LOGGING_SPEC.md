# Logging Specification: Exhaustive Instrumentation

This document defines the complete logging strategy for Crude. Every decision point, user interaction, state transition, error path, and meaningful event must be logged. The goal is that someone reading only `session.log` can reconstruct exactly what happened during the entire session without reading source code.

---

## Logger API

```go
l.Info(TAG, "msg", "key", value, "key", value)
l.Warn(TAG, "msg", "key", value)
l.Error(TAG, "msg", "key", value)
l.Debug(TAG, "msg", "key", value)
```

- **TAG**: UPPERCASE, 2–8 chars, category. See registry below.
- **msg**: lowercase, 1–3 words, what happened. Not a sentence.
- **pairs**: alternating `key, value`. Keys are lowercase snake_case. Values are Go values.

---

## Tag Registry

Use existing tags or invent new ones following the UPPERCASE pattern:

| Tag | Domain |
|-----|--------|
| `SESSION` | App lifecycle, startup, shutdown |
| `CONFIG` | Settings loads, saves, changes, defaults |
| `CONV` | Conversation messages (user/assistant) |
| `AGENT` | Agent loop iterations, termination reasons |
| `LLM` | API calls, responses, token counts |
| `STREAM` | Streaming events, deltas, chunks |
| `TOOL` | Tool calls, results, errors, progress |
| `SAFETY` | Permission checks, blocks, denies, yolo toggles |
| `FILEIO` | File reads, writes, creates, deletes, renames, patches |
| `STATE` | State machine goals, plans, blockers, transitions |
| `COST` | Token cost accumulation, snapshots |
| `MODEL` | Model metadata, models.dev fetches |
| `PERM` | Permission grants, always-allow changes |
| `BLOCK` | Blocking/confirmation dialogs, user choices |
| `CANCEL` | Context cancellation, user aborts |
| `ASYNC` | Async command start/check/kill/output |
| `PLAN` | Plan creation, step status changes |
| `INPUT` | User input reception, parsing, slash commands |
| `UI` | TUI state changes, view switches, pane focus |
| `HISTORY` | Input history save/load |
| `EDITOR` | Journaled editor operations, undo chain |
| `JOURNAL` | Journal entries, undo operations |
| `RETRY` | API retry attempts, backoff durations |
| `ERROR` | Unexpected errors, panics, recoveries |
| `CTX` | Context window checks, truncation decisions |
| `CACHE` | Cache control breakpoints, cached token counts |
| `TOKENS` | Token estimation, counting |
| `VALIDATE` | Input validation, parameter checks |
| `DISPATCH` | Tool dispatch routing |
| `COMMAND` | Shell command execution, exit codes |
| `CLIPBOARD` | Clipboard operations |
| `SEARCH` | Web search, arxiv, fetch operations |
| `JS` | JavaScript engine operations |
| `DIFF` | Diff generation, patch application |
| `CODEC` | Hashline encoding/decoding |
| `ATOMIC` | Atomic file write operations |
| `LOOP` | Loop detection, step counting |
| `TRUNCATE` | Message truncation, context overflow |
| `SANITIZE` | Message sanitization, trailing tool cleanup |

---

## What to Log (exhaustive)

### 1. Every function entry that does meaningful work

```go
l.Debug("AGENT", "run_enter", "msgs", len(sessionMessages), "tools", len(allTools), "session_id", sessionID)
```

### 2. Every conditional branch taken (and NOT taken when interesting)

```go
if hasToolCalls {
    l.Debug("AGENT", "branch_has_tool_calls", "count", len(toolCalls))
} else {
    l.Debug("AGENT", "branch_text_only", "text_len", len(accumulatedText))
}
```

### 3. Every user interaction

```go
l.Info("INPUT", "received", "len", len(userText), "msg_id", currentMsgID, "is_blocked", isBlocked)
l.Info("INPUT", "slash_command", "cmd", cmd, "result_len", len(result.Desc))
```

### 4. Every state transition

```go
l.Info("STATE", "transition", "from", oldStatus, "to", newStatus, "goal_id", goalID)
l.Info("PLAN", "step_status", "step_id", stepID, "status", newStatus, "all_done", allDone)
```

### 5. Every error path (even if handled silently)

```go
if err != nil {
    l.Error("FILEIO", "read_failed", "path", path, "error", err.Error())
}
```

### 6. Every async operation lifecycle event

```go
l.Info("ASYNC", "start", "handle", handle, "command", cmd, "pid", info.PID)
l.Info("ASYNC", "status_check", "handle", handle, "status", info.Status, "pid", info.PID)
l.Info("ASYNC", "kill", "handle", handle)
l.Info("ASYNC", "output_read", "handle", handle, "bytes", len(out), "next_byte", nextByte)
```

### 7. Every safety/security decision

```go
l.Info("SAFETY", "check", "command", cmd, "risk", sr.Risk.String(), "needs_confirm", sr.Risk.NeedsConfirmation())
l.Info("SAFETY", "block_always", "command", cmd, "reason", sr.Reason)
l.Info("PERM", "grant", "operation", opStr, "tool", tool, "target", target)
l.Info("PERM", "auto_allowed", "operation", op, "tool", tool, "target", target)
```

### 8. Every cancellation/timeout

```go
l.Warn("CANCEL", "context_cancelled", "step", step, "reason", ctx.Err().Error())
l.Warn("CANCEL", "input_cancelled", "msg_id", currentMsgID)
```

### 9. Every data transformation or truncation

```go
l.Info("TRUNCATE", "messages", "before", len(msgs), "after", len(persisted), "tokens_used", used, "budget", tokenBudget)
l.Info("SANITIZE", "trailing_tools", "removed", removedCount, "remaining", len(msgs))
```

### 10. Every configuration change

```go
l.Info("CONFIG", "save", "provider", providerID, "model", model, "turn_logs", turnLogsEnabled)
l.Info("CONFIG", "provider_switch", "from", oldProvider, "to", newProvider)
l.Info("CONFIG", "model_change", "from", oldModel, "to", newModel)
```

### 11. Every cost/token tracking event

```go
l.Info("COST", "turn_usage", "model", model, "prompt", promptT, "completion", compT, "cached", cachedT, "turn_cost", turnCost, "session_total", s.cost.Snapshot().TotalCost)
```

### 12. Every journal/editor operation

```go
l.Info("EDITOR", "with_message", "msg_id", currentMsgID)
l.Info("JOURNAL", "undo", "remaining", len(journalLog), "desc", desc)
l.Info("JOURNAL", "undo_n", "count", n)
l.Info("JOURNAL", "undo_session", "ops_reverted", totalOps)
l.Info("EDITOR", "changed_files", "count", len(files))
```

### 13. Every streaming event milestone

```go
l.Debug("STREAM", "first_token", "model", a.Model, "latency_ms", time.Since(streamStart).Milliseconds())
l.Debug("STREAM", "tool_call_start", "id", tc.ID, "name", tc.Function.Name)
l.Debug("STREAM", "tool_call_delta", "id", tc.ID, "name", tc.Function.Name, "args_len", len(tc.Function.Arguments))
l.Debug("STREAM", "thinking_delta", "len", len(reasoningText))
l.Debug("STREAM", "usage_received", "prompt", promptTokens, "completion", completionTokens, "cached", promptTokensCached)
```

### 14. Every retry with backoff details

```go
l.Warn("RETRY", "attempt", "model", a.Provider.Name, "attempt", attempt, "backoff_ms", int(backoff.Milliseconds()), "error", err.Error())
```

### 15. Every dispatch routing decision

```go
l.Debug("DISPATCH", "route", "tool", tool, "has_custom_fn", a.ToolFn != nil)
```

### 16. Every cache control decision

```go
l.Debug("CACHE", "breakpoint", "msg_idx", lastToolResultIdx, "role", msgs[lastToolResultIdx].Role)
l.Debug("CACHE", "system_ephemeral", "position", 0, "len", len(systemPrompt))
```

### 17. Every loop detection check

```go
l.Debug("LOOP", "check", "steps", len(steps), "window", loopDetectWindow, "unique_sigs", len(sigCount))
l.Warn("LOOP", "detected", "repeats", sigCount[sig], "max", loopDetectRepeats, "tool", s.ToolName)
```

### 18. Every context window calculation

```go
l.Debug("CTX", "window", "model", a.Model, "ctx_window", ctxWindow, "safety_limit", ctxLimit, "safety_pct", contextSafetyPct)
l.Warn("CTX", "overflow", "prompt_tokens", lastPromptTokens, "limit", ctxLimit, "window", ctxWindow)
```

### 19. Every blocking/confirmation interaction

```go
l.Info("BLOCK", "prompt", "operation", op, "tool", tool, "target", target, "question", question)
l.Info("BLOCK", "resolved", "response", resp, "operation", op, "tool", tool)
l.Info("BLOCK", "auto_allowed", "operation", op, "tool", tool, "target", target)
```

### 20. Every message sanitization step

```go
l.Debug("SANITIZE", "enter", "msgs", len(msgs))
l.Debug("SANITIZE", "trailing_tool_trim", "removed_count", n)
l.Debug("SANITIZE", "orphan_tool_calls", "expected_ids", len(expectedIDs), "missing", len(expectedIDs))
l.Debug("SANITIZE", "comm_tool_collapse", "content_len", len(content))
```

### 21. Every token estimation

```go
l.Debug("TOKENS", "estimate", "model", model, "msgs", len(msgs), "total", estimatedTotal)
l.Debug("TOKENS", "message_estimate", "role", msg.Role, "est", estTokens)
```

---

## Rules

1. **Never log full content in central log.** Use `len=` for strings >200 chars. Use `content_preview=` with first 80 chars + "…" for debugging value.
2. **Use Debug for high-frequency events** (streaming deltas, loop iterations, cache breakpoints). Use Info for meaningful events. Use Warn for unexpected-but-handled. Use Error for failures.
3. **Every `if` that changes behavior gets a log.** Both branches if meaningful.
4. **Every `return` from error path gets a log.** Before the return.
5. **Every goroutine launch gets a log.** At launch and at completion.
6. **Every channel send/receive that coordinates behavior gets a log.**
7. **Every mutex lock that guards a state change — log the state change, not the lock.**
8. **Add logger fields to any struct that does meaningful work but doesn't have one yet.** Pass logger through constructors. If a function has access to `*logger.Logger`, use it.
9. **For functions without direct logger access**: add a `lg *logger.Logger` parameter. Update all callers.
10. **Import `"crude/logger"` wherever needed.**
11. **Never break existing function signatures for unrelated code — add logger as last parameter if needed.**
12. **No dead code.** If you add a logger field, use it. If you add a log line, make it meaningful.
13. **Key names are lowercase snake_case.** Values are Go values, not stringified maps.
14. **Format tool params with `formatToolArgs(params)`**, not `fmt.Sprintf("%v", params)`.

---

## Files to modify (in order of importance)

1. `session/session.go` — the Run() loop, HandleSettingsSave, HandleUndo, Close, New
2. `agent/agent.go` — Run(), every step, every branch, loop detection, context checks
3. `agent/api.go` — completeStream(), complete(), every retry, every error
4. `fileio/editor.go` — every file operation, journal operations
5. `fileio/journal.go` — journal entries, undo operations
6. `statemachine/machine.go` — every state transition
7. `tools/dispatcher.go` — every tool dispatch
8. `safety/permissions.go` — every permission grant/check
9. `safety/command_checker.go` — every risk classification
10. `tui/app/update.go` — key TUI state changes
11. `tui/app/slash_commands.go` — slash command handling

---

## Example: fully-logged function

### Before

```go
func (s *Session) HandleUndo(cmd string) tui.UndoResultMsg {
    if s.editor == nil {
        return tui.UndoResultMsg{Error: "file editor not available"}
    }
    switch {
    case cmd == "/undo":
        if err := s.editor.UndoLast(); err != nil {
            return tui.UndoResultMsg{Error: err.Error()}
        }
        journalLog := s.editor.Log()
        desc := "Undo complete."
        if len(journalLog) > 0 {
            desc = fmt.Sprintf("Undo complete. %d remaining.", len(journalLog))
        }
        return tui.UndoResultMsg{Desc: desc}
    // ...
    }
}
```

### After

```go
func (s *Session) HandleUndo(cmd string) tui.UndoResultMsg {
    l := s.logger
    l.Info("INPUT", "undo", "cmd", cmd)
    if s.editor == nil {
        l.Warn("EDITOR", "undo_no_editor", "cmd", cmd)
        return tui.UndoResultMsg{Error: "file editor not available"}
    }
    switch {
    case cmd == "/undo":
        l.Info("JOURNAL", "undo_last", "journal_len", len(s.editor.Log()))
        if err := s.editor.UndoLast(); err != nil {
            l.Error("JOURNAL", "undo_failed", "error", err.Error())
            return tui.UndoResultMsg{Error: err.Error()}
        }
        journalLog := s.editor.Log()
        remaining := len(journalLog)
        l.Info("JOURNAL", "undo_ok", "remaining", remaining)
        desc := "Undo complete. No more operations to undo."
        if remaining > 0 {
            desc = fmt.Sprintf("Undo complete. %d operation(s) remaining in journal.", remaining)
        }
        return tui.UndoResultMsg{Desc: desc}
    // ...
    }
}
```

---

## Session log output example

After full instrumentation, a session.log should read like this:

```
# session a3f7b2c1 | 2026-05-15T19:14:41Z

19:14:41.000 INFO [SESSION] start version=1.0
19:14:41.000 INFO [SESSION] config provider=openrouter model=anthropic/claude-3.5-sonnet turn_logs=on
19:14:41.000 INFO [MODEL] load_ok count=342
19:14:42.123 INFO [INPUT] received len=24 msg_id=msg-1 is_blocked=false
19:14:42.123 INFO [CONV] user_msg msg_id=msg-1 len=24
19:14:42.123 INFO [AGENT] loop_start msg_id=msg-1
19:14:42.125 DEBUG [CTX] window model=anthropic/claude-3.5-sonnet ctx_window=200000 safety_limit=170000 safety_pct=0.85
19:14:42.125 INFO [LLM] prompt model=openrouter msgs=2 tools=25
19:14:42.300 DEBUG [STREAM] first_token model=anthropic/claude-3.5-sonnet latency_ms=175
19:14:43.500 DEBUG [STREAM] thinking_delta len=142
19:14:44.200 DEBUG [STREAM] tool_call_start id=call_abc name=read_file
19:14:44.500 DEBUG [STREAM] usage_received prompt=2847 completion=156 cached=1200
19:14:44.501 INFO [LLM] response model=openrouter tool_calls=1 prompt_tokens=2847 completion_tokens=156 cached_tokens=1200 latency_ms=2376
19:14:44.502 DEBUG [LLM] turn_dump file=001.md
19:14:44.502 INFO [TOOL] call name=read_file params="path: /tmp/test.txt"
19:14:44.510 DEBUG [TOOL] progress name=read_file phase=reading path=/tmp/test.txt
19:14:44.550 INFO [TOOL] result name=read_file latency_ms=40 len=1024
19:14:44.551 DEBUG [CACHE] breakpoint msg_idx=4 role=tool
19:14:44.551 DEBUG [LOOP] check steps=1 window=8 unique_sigs=1
19:14:44.552 DEBUG [AGENT] branch_has_tool_calls count=1
19:14:44.600 INFO [LLM] prompt model=openrouter msgs=4 tools=25
19:14:46.800 INFO [LLM] response model=openrouter tool_calls=0 prompt_tokens=3120 completion_tokens=245 cached_tokens=0 latency_ms=2200
19:14:46.801 DEBUG [AGENT] branch_text_only text_len=189
19:14:46.801 INFO [AGENT] loop_end len=189
19:14:46.802 INFO [CONV] assistant_msg msg_id=msg-1 len=189
19:14:46.900 INFO [COST] turn_usage model=anthropic/claude-3.5-sonnet prompt=5967 completion=401 cached=1200 turn_cost=0.0023 session_total=0.0023
19:15:01.000 INFO [SESSION] end duration=19s
```
