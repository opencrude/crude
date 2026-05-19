Local Review for uncommitted changes
Summary
This change reorganizes the agent package from 5 files (with a 1129-line agent.go containing a 727-line Run god-method) into 8 focused files. Dead code is removed (complete(), ToolProgress, stepStatusMarker), retry helpers are moved to their sole caller (api.go), and the Run method is decomposed into executeToolCalls/executeOneToolCall (tool_exec.go) and makeStreamingDeltaCallback (run.go). A pre-existing indentation bug in the file-mutation deny branch is also fixed. The public API is preserved exactly.

Issues Found
Severity	File:Line	Issue
WARNING	agent/tool_exec.go:64	Data race on *lastReply across parallel goroutines (pre-existing, now more visible)
WARNING	agent/tool_exec.go:100,180	Concurrent goroutines reading from shared blockResolveCh can steal each other's confirmation responses (pre-existing)
SUGGESTION	agent/tool_exec.go:220	Variable path shadows the path variable declared at line 141/142/146/151/158/164 — while Go scoping handles this correctly, the shadow makes reasoning about which path is in scope harder
Detailed Findings
File: agent/tool_exec.go:64
Confidence: 90%
Problem: Multiple tool call goroutines execute in parallel via executeToolCalls. If two "reply" calls run concurrently, both write to *lastReply without synchronization. This is a data race (same as in the original code, but the *string pointer pattern makes it more explicit). In practice this is likely benign because the LLM rarely emits two reply calls in one turn, but it's technically undefined behavior.
Suggestion: If fixing in a follow-up, protect lastReply with a mutex or route reply results through the result channel (already done) and set lastReply only in the serial collection loop in Run.
File: agent/tool_exec.go:100,180
Confidence: 90%
Problem: When multiple tool calls need user confirmation simultaneously (e.g., two file-mutation tools in one turn), each goroutine reads from the same blockResolveCh. The first response from the TUI goes to whichever goroutine reads first, not necessarily the one that emitted the blocking event. This is a pre-existing design issue — the TUI likely serializes confirmations, but the channel consumption is racy if two goroutines block on it concurrently.
Suggestion: This should be addressed in a separate follow-up. One approach: serialize all confirmation-requiring tool calls through a single sequential path, or use per-goroutine confirmation channels keyed by internal ID.
File: agent/tool_exec.go:220
Confidence: 75%
Problem: path := getStr(params, "path") at line 220 redeclares path in a new scope, shadowing the filePath variable set in the switch at lines 137-167. The two variables hold the same value for write/edit/delete tools, but for move/copy tools, path at line 220 is from params["path"] while filePath at line 158/164 is from params["destination"]. Since getStr(params, "path") and getStr(params, "destination") are different keys for move/copy, the path at line 220 would be empty for those tools (which is correct — the post-execution code only uses path for write/edit). Still, the shadowing is confusing.
Suggestion: Rename the earlier filePath to something distinct, or use filePath consistently where the two diverge. Low priority.
Recommendation
APPROVE WITH SUGGESTIONS — The refactoring is clean, behavior-preserving, and significantly improves code organization. The two WARNING-level findings are pre-existing race conditions that are now more visible but not introduced by this change. They merit follow-up but should not block this refactor

# Error & Cancellation Robustness Plan

## Overview

The error handling, cancellation flow, and status presentation across the codebase is inconsistent and broken in several critical ways. This document catalogs every issue found during deep dive analysis, with implementation details for each fix.

---

## CRITICAL Issues

### C1: tool_result event never sets `error` flag — tools always show green dot

**Files:** `agent/agent.go:697-709`, `tui/widgets/messagelist.go:423-456`, `tui/tools/render.go:84-86`

**Problem:** The `tool_result` event data map never includes `"error": "true"`. The TUI's `UpdateToolResult` checks for this flag to set `ToolStatus = "error"` (red dot), but since it's always absent, all tool results render with `"success"` (green dot) — even when the tool returned `"Error: ..."`.

**Fix:**
1. In `agent/agent.go:697-709`, after building `resultData`, detect error content and add the flag:
   ```go
   if strings.HasPrefix(strings.TrimSpace(result.Content), "Error:") ||
      strings.HasPrefix(strings.TrimSpace(result.Content), "[SAFETY BLOCK]") {
       resultData["error"] = "true"
   }
   ```
2. Also set it for the inline error tool_results at lines 462-467, 486-492, 509-515, 584-590.
3. Verify `messagelist.go:423-456` already checks `data["error"]` — it does (the `errFlag` check).
4. Verify `render.go:84-86` error path now becomes live — it will render red `Spinner.Error` dot.

**Also set `ToolPreviewError`:**
- In `agent/agent.go`, when emitting tool_result with `"error": "true"`, also emit a `tool_progress` event with phase `"error"` so `ToolPreviewPhase` gets set correctly. Or add `"preview_phase": "error"` to the tool_result data map and handle it in `agent_state_events.go`.

---

### C2: Cancel during streaming emits `"error"` instead of `"done"` — inconsistent UX

**Files:** `agent/agent.go:368-371`, `agent/api.go:191-193`

**Problem:** When user presses Ctrl+C during streaming:
- If cancel detected at loop start → `"done"` with reason `"cancelled"` ✓
- If cancel detected during tool result wait → `"done"` with reason `"cancelled"` ✓
- If cancel detected during HTTP streaming → `emitError()` → `"error"` event ✗

The `"error"` path shows a raw API error message like `"API request cancelled: context canceled"` instead of a clean "Cancelled" indicator.

**Fix:**
1. In `agent/agent.go:368-371`, check if the error is a context cancellation before calling `emitError`:
   ```go
   if err != nil {
       if ctx.Err() != nil {
           emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
           return msgs
       }
       a.emitError(emit, err)
       return msgs
   }
   ```
2. In `agent/api.go`, add a method to `apiCallError` to distinguish cancellation:
   ```go
   func (e *apiCallError) IsCancellation() bool {
       return strings.Contains(e.Message, "context canceled") || strings.Contains(e.Message, "context cancelled")
   }
   ```
   Then in `agent.go:368`:
   ```go
   if err != nil {
       var apiErr *apiCallError
       if errors.As(err, &apiErr) && apiErr.IsCancellation() {
           emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
           return msgs
       }
       a.emitError(emit, err)
       return msgs
   }
   ```

---

### C3: Add `"cancelled"` ToolStatus — mark in-progress tools instead of deleting them

**Files:** `tui/types/types.go`, `tui/tools/render.go`, `tui/widgets/messagelist.go`

**Problem:** When agent is cancelled, `ClearStreamingTools()` **deletes** in-progress tool messages from the list entirely. The user sees them vanish with no indication of what happened.

**Fix:**
1. Add `"cancelled"` constant to `types.go` alongside other ToolStatus values.
2. Add a `MarkStreamingToolsCancelled()` method to `messagelist.go` (similar to `ClearStreamingTools` at line 469-485) that sets `ToolStatus = "cancelled"` instead of deleting.
3. Add `"cancelled"` rendering in `render.go:27-89` — use a yellow/amber dot:
   ```go
   } else if msg.ToolStatus == "cancelled" {
       dot = "●"
       dotColor = th.Colors.Warning  // yellow/amber
   }
   ```
4. In `agent_state_events.go`, replace `ClearStreamingTools()` calls on cancel/done with `MarkStreamingToolsCancelled()` when the reason is "cancelled", keep `ClearStreamingTools()` for normal completion.

---

### C4: Add cancellation/proper chat message in chat instead of raw API error

**Files:** `tui/viewmodel/agent_state_events.go:169-183`

**Problem:** On cancellation during streaming, the `"error"` event appends a `RoleError` message with raw text like `"API request cancelled: context canceled"`. This is not user-friendly.

**Fix:** After C2 is fixed (cancel now emits `"done"` with reason `"cancelled"`), the error path is only for real API errors. But we should also:
1. In `handleDone` (`agent_state_events.go:223-290`), when `reason == "cancelled"`, append a distinct chat message:
   ```go
   if reason == "cancelled" {
       target.AppendMessage(ttypes.ChatMessage{
           Role:    ttypes.RoleAssistant,
           Content: "⚠ Operation cancelled by user.",
           Time:    time.Now(),
       })
   }
   ```
2. In the `"error"` handler (`agent_state_events.go:169-183`), the `RoleError` message stays but `emitError` should already produce sanitized messages (see M1).

---

## HIGH Issues

### H1: Fix Ctrl+C on confirm dialog — send cancel signal to fully stop agent

**Files:** `tui/viewmodel/cancel.go:51-70`

**Problem:** When user presses Ctrl+C on a confirm dialog, it only sends `ConfirmDeny` to `blockResolveCh`. The agent receives the deny and **continues running** — it just retries with a different approach. The user expects full cancellation.

**Fix:**
1. In `cancel.go:51-70`, after denying the confirm, also send a cancel signal:
   ```go
   if m.Mode == ModeConfirm {
       m.confirm.Dismiss()
       m.blocking = false
       // ... existing tab/mode cleanup ...
       if m.OnConfirmResult != nil {
           m.OnConfirmResult(ttypes.ConfirmResult{Choice: ttypes.ConfirmDeny})
       }
       // Also cancel the agent
       select {
       case m.cancelCh <- struct{}{}:
       default:
       }
       m.statusbar.SetInfo("cancelling...")
       return nil
   }
   ```
2. The `blockResolveCh` deny unblocks the tool goroutine, and the `cancelCh` signal cancels the context. The agent will emit `"done"` with reason `"cancelled"` at the next checkpoint.

---

### H2: Fix ToolFn error detection — check all error prefixes

**Files:** `session/session.go:765-768`

**Problem:** Error detection only checks `"Error:"` prefix. Misses `"Command failed:"`, `"Search error:"`, `"ArXiv search error:"`, `"Fetch error:"`, `"JavaScript error:"` from `dispatcher.go`.

**Fix:**
1. Create a helper function:
   ```go
   func isToolError(content string) bool {
       prefixes := []string{"Error:", "Command failed:", "Search error:", "ArXiv search error:", "Fetch error:", "JavaScript error:", "[SAFETY BLOCK]"}
       for _, p := range prefixes {
           if strings.HasPrefix(strings.TrimSpace(content), p) {
               return true
           }
       }
       return false
   }
   ```
2. Replace `strings.HasPrefix(result.Content, "Error:")` at `session.go:765` with `isToolError(result.Content)`.
3. Use the same function in `agent/agent.go:697` to set the `"error"` flag in tool_result data.

---

## MEDIUM Issues

### M1: Sanitize cancellation error messages for user display

**Files:** `agent/api.go:422-436`

**Problem:** Messages like `"API request cancelled during retry: context canceled"` reach the user verbatim.

**Fix:** In `emitError`, add a check before the existing rules:
```go
if strings.Contains(errMsg, "context canceled") || strings.Contains(errMsg, "context cancelled") {
    // This should be handled as cancellation, not error (see C2)
    // But as a safety net, sanitize:
    errMsg = "Request was cancelled"
}
```

### M2: Close thinking block with visual indicator when interrupted

**Files:** `tui/widgets/chatmessage.go:253-274`, `tui/types/types.go`

**Problem:** Completed thinking and interrupted thinking look identical — both show a muted dot. No way to tell if thinking finished naturally or was cut short.

**Fix:**
1. Add a `ThinkingInterrupted bool` field to `ChatMessage` struct in `types/types.go`.
2. In `MarkAllThinkingDone()`, add a parameter or separate method `MarkAllThinkingInterrupted()` that sets `ThinkingActive = false` AND `ThinkingInterrupted = true`.
3. In `chatmessage.go:253-274`, render interrupted thinking with a different visual:
   ```go
   } else if msg.ThinkingInterrupted {
       // Use amber/warning dot instead of muted
       dot = lipgloss.NewStyle().Foreground(th.Colors.Warning).Render("● ")
   } else {
       dot = lipgloss.NewStyle().Foreground(th.Colors.Muted).Render("● ")
   }
   ```
4. In `agent_state_events.go`, when handling `"done"` with reason `"cancelled"`, call `MarkAllThinkingInterrupted()` instead of `MarkAllThinkingDone()`.

### M3: Fix stale tool goroutine emit events after agent returns

**Files:** `agent/agent.go:449-712`, `session/session.go:1066-1240`

**Problem:** Tool goroutines continue running and emitting events after `ag.Run()` returns. Stale events can corrupt the next turn's TUI state.

**Fix:**
1. Add an `active` atomic flag to the agent or session's emit closure:
   ```go
   var emitActive atomic.Bool
   emitActive.Store(true)
   emit := func(evt AgentEvent) {
       if !emitActive.Load() {
           return  // Agent has returned, ignore stale events
       }
       // ... existing emit logic ...
   }
   ```
2. After `ag.Run()` returns at `session.go:1241`, set `emitActive.Store(false)`.
3. This prevents orphaned goroutines from corrupting the TUI state.

### M4: Fix ToolPreviewError never being used

**Files:** `tui/types/types.go:12`, `agent/agent.go:697-709`

**Problem:** `ToolPreviewError` is defined but never assigned. Tool preview phase always goes `preparing → streaming → done`, even for errors.

**Fix:** When emitting tool_result with `"error": "true"` (from C1), also emit a preceding tool_progress event with phase `"error"`:
```go
if isToolError {
    emit(AgentEvent{Type: "tool_progress", Data: map[string]any{
        "internal_id": internalID,
        "tool_id":     tc.ID,
        "name":        tc.Function.Name,
        "phase":        "error",
    }})
}
```

### M5: Fix statusbar display priority — error should override info

**Files:** `tui/widgets/statusbar.go:54-78`

**Problem:** `infoMsg` has higher priority than `lastError` in the View() method. A `SetInfo("✓ done")` can suppress a just-set error.

**Fix:** Reorder the priority in `View()`:
```go
// Current: infoMsg > retryMsg > lastError
// Fixed:   lastError > retryMsg > infoMsg
if s.lastError != "" && time.Since(s.lastErrorTime) < s.errorExpiry {
    left = errorStyle.Render(s.lastError)
} else if s.retryMsg != "" && time.Since(s.retryTime) < s.retryExpiry {
    left = retryStyle.Render(s.retryMsg)
} else if s.infoMsg != "" && time.Since(s.infoTime) < s.infoExpiry {
    left = infoStyle.Render(s.infoMsg)
}
```

### M6: Fix execute_command using context.Background() — orphaned processes

**Files:** `fileio/exec.go:90`

**Problem:** `execute_command` creates `context.Background()` instead of using the agent's cancel context. Shell processes survive user cancellation.

**Fix:**
1. Thread the agent's context through to `LocalExec.Execute`:
   - Add `ctx context.Context` parameter to `Execute()`.
   - Use `exec.CommandContext(ctx, ...)` instead of `exec.CommandContext(context.Background(), ...)`.
2. Update all callers to pass the context.
3. When context is cancelled, `CommandContext` will send `os.Kill` to the process.

### M7: Fix wait tool time.Sleep ignoring cancellation

**Files:** `session/session.go:742-749`

**Problem:** `time.Sleep` cannot be interrupted by context cancellation.

**Fix:**
```go
case "wait":
    seconds := tools.GetFloatArgAny(params, "seconds", 0)
    dur := time.Duration(seconds * float64(time.Second))
    select {
    case <-time.After(dur):
        result = tools.ToolResult{Content: fmt.Sprintf("Waited %.1f seconds", seconds)}
    case <-ctx.Done():
        result = tools.ToolResult{Content: "Wait cancelled"}
    }
```

### M8: Fix panic recovery using SendAgentEvent without agent ID

**Files:** `session/session.go:942-946`

**Problem:** Panic recovery uses `SendAgentEvent` (no agent ID) while all other emissions use `SendAgentEventWithID`.

**Fix:** Change to `SendAgentEventWithID` with the current agent ID:
```go
defer func() {
    if r := recover(); r != nil {
        l.Error("SESSION", "panic", "error", fmt.Sprintf("%v", r))
        sender.SendAgentEventWithID("error", fmt.Sprintf("Internal error: %v", r), "main")
    }
}()
```

### M9: Fix partial assistant message left incomplete on cancel

**Files:** `tui/viewmodel/agent_state_events.go`

**Problem:** On cancellation, the incomplete assistant message persists in the message list with truncated content and no finality indicator.

**Fix:** In `handleDone` when reason is "cancelled", if there's a partial assistant message (streaming was true), mark it explicitly:
1. Add a `StreamCompleted` boolean or similar indicator.
2. Or simply append the cancellation notice as the final content of that message.
3. Most practically: the partial content stays (which is fine — user can see what was generated before cancel), but the cancellation message (from C4) clearly indicates the interruption.

### M10: Fix error tool results have no content rendering below title

**Files:** `tui/tools/render.go:92-146`

**Problem:** Only `"success"` results get content rendering (diffs, summaries, wrapped content). Error results show only the title line.

**Fix:** Add error content rendering in `render.go`:
```go
} else if msg.ToolStatus == "error" || msg.ToolStatus == "cancelled" {
    // Render error content below the title
    contentLines := rendering.WrapText(msg.Content, contentWidth)
    for _, line := range contentLines {
        result = append(result, borderPrefix+th.Styles.ErrorText.Render(line))
    }
}
```

---

## LOW Issues

### L1: Fix `'cancelling...'` statusbar message never explicitly cleared

**File:** `tui/viewmodel/cancel.go:78`

**Problem:** `SetInfo("cancelling...")` relies on 5-second auto-expiry. It persists even after cancellation completes.

**Fix:** After cancellation completes (in `processAgentResults` on `agentDoneResultMsg`), explicitly clear it. The `SetError`/`SetInfo` calls in `processAgentResults` already overwrite it, so this is mostly handled by C2.

### L2: Fix Partial TokenBuf loss on error

**File:** `tui/viewmodel/agent_state.go:170`

**Problem:** On `"error"` event, `a.TokenBuf = ""` discards up to 16ms of buffered tokens.

**Fix:** Flush TokenBuf to the message list before clearing it:
```go
case "error":
    if a.TokenBuf != "" {
        target.AppendToken(a.TokenBuf)
        a.TokenBuf = ""
    }
    // ... rest of error handling
```

### L3: Fix OpenRouter-specific string match — generalize for all providers

**File:** `agent/api.go:427`

**Problem:** `strings.Contains(errMsg, "OpenRouter 5")` is hardcoded.

**Fix:** Generalize to match any 5xx from any provider:
```go
if strings.Contains(errMsg, " 5") && (strings.Contains(errMsg, "API 5") || strings.Contains(errMsg, "50") || strings.Contains(errMsg, "529")) {
    errMsg = "API server error — please try again"
}
```
Or better: parse the HTTP status code from the error and match 5xx.

### L4: Fix double truncation in emitError

**File:** `agent/api.go:99-102` vs `api.go:429-433`

**Problem:** Error snippets are truncated to 240 runes in `complete()`/`completeStream()`, then re-truncated to 120 runes in `emitError()`.

**Fix:** Remove the truncation in `complete()`/`completeStream()` (keep full error for logging) and only truncate in `emitError()` (for display).

### L5: Fix inconsistent statusbar expiration times

**File:** `tui/widgets/statusbar.go:192-202`

**Problem:** Errors expire in 8s, info in 5s, retry in 30s. Inconsistent and errors disappear too fast.

**Fix:**
- Errors: 12 seconds
- Info: 5 seconds (unchanged)
- Retry: 30 seconds (unchanged)

### L6: Fix resultSummary returns empty for `"Error:"` content

**File:** `tui/tools/render.go:246-248`

**Problem:** `resultSummary` short-circuits on `"Error:"` prefix, returning `""`.

**Fix:** Generate a summary from the error content instead:
```go
if strings.HasPrefix(strings.TrimSpace(content), "Error:") {
    summary := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(content), "Error:"))
    if len(summary) > 60 {
        summary = summary[:59] + "…"
    }
    return summary
}
```

### L7: Fix anyActive() false-positive from sub-pane Done state lag

**File:** `tui/viewmodel/app.go:186-198`

**Problem:** After agent finishes, sub-pane `Done` may still be false briefly, causing `anyActive()` to return true.

**Fix:** In `handleSubPaneCompletion`, set `sp.Done = true` before any async operations.

### L8: Fix apiCallError.RetriesExhausted never set

**File:** `agent/api.go:18-23`

**Problem:** `RetriesExhausted` field exists but is never populated.

**Fix:** Set it to `true` in the retry path when `attempt >= maxAttempts`:
```go
if attempt >= maxAttempts {
    return ..., &apiCallError{Message: "...", RetriesExhausted: true}
}
```

### L9: Fix denied permission tag creates separate message — potential duplicate

**File:** `tui/viewmodel/update.go:273-280`

**Problem:** On deny, a new `RoleTool` message is appended with `PermTag: "denied"`, but the original tool message from streaming may still exist.

**Fix:** Before appending the denied message, find and update the existing streaming tool message's `PermTag` instead of creating a new one.

### L10: Fix stream scanner errors retried identically to HTTP 500

**File:** `agent/api.go:360-372`

**Problem:** Scanner errors (partial stream death) are retried identically to server errors, potentially losing already-received tokens.

**Fix:** For scanner errors after receiving some content, return the partial content instead of retrying. Only retry if no content was received.

---

## Implementation Order

Execute in this order to maximize impact and avoid conflicts:

1. **C2** — Cancel emits `"done"` not `"error"` (unifies the cancel path)
2. **C4** — Add cancellation chat message (user sees clear feedback)
3. **C3** — Add `"cancelled"` ToolStatus (tools show cancelled, not vanish)
4. **C1** — Add `error` flag to tool_result events (tools show red dot on failure)
5. **H1** — Fix Ctrl+C on confirm dialog (full agent stop)
6. **H2** — Fix error prefix detection (catch all tool error types)
7. **M1** — Sanitize cancel messages (user-friendly text)
8. **M2** — Interrupted thinking indicator (visual distinction)
9. **M3** — Stale emit guard (prevent post-return corruption)
10. **M5** — Statusbar priority fix
11. **M6** — execute_command context threading
12. **M7** — wait tool cancellation
13. **M10** — Error tool content rendering
14. **M8** — Panic recovery agent ID
15. **M4** — ToolPreviewError usage
16. **L1-L10** — Low priority fixes in any order
17. **Build & test**

---

## Files Modified (Summary)

| File | Issues Addressed |
|------|-----------------|
| `agent/agent.go` | C1, C2, H2, M3, M4 |
| `agent/api.go` | C2, M1, L3, L4, L8, L10 |
| `session/session.go` | H2, M6, M7, M8 |
| `session/dispatcher.go` | H2 (reference for prefixes) |
| `fileio/exec.go` | M6 |
| `tui/types/types.go` | C3, M2 |
| `tui/viewmodel/agent_state_events.go` | C3, C4, M2, M9, L2 |
| `tui/viewmodel/cancel.go` | H1, L1 |
| `tui/widgets/messagelist.go` | C3, M4 |
| `tui/widgets/chatmessage.go` | M2 |
| `tui/widgets/statusbar.go` | M5, L5 |
| `tui/tools/render.go` | C3, M10, L6 |
| `tui/viewmodel/update.go` | L9 |
