# Development status — 2026-05-21

## Completed
- **Step 1–7:** Core backend fully implemented (types, manager, session DB schema, tool definitions, proxied ToolFn, policy enforcement, file ownership, concurrency, custom instructions, model/provider routing)
- **Step 8 (TUI Integration):** Implemented with right-side vertical agent tab bar widget (`tui/widgets/agenttabs.go`), agent view swapping, formatted sub-agent results (no raw JSON), event handling for file claims + sub-agent questions, tab click/switching, agent lifecycle events (`sub_agent_started`/`sub_agent_completed`). Agent tabs render on the right edge of the chat area alongside the scrollbar rail. Clicking a sub-agent tab swaps the entire chat view to that agent's full conversation history.
- **Step 10 (partial):** Sub-agent goals integrated into state machine, sub-agent progress visible in `list_goal_status`
- **Step 11:** Crash recovery implemented (`recover()` in sub-agent goroutine, panic → `SubAgentResult{failed_error}`, stack trace logging)

## All 21 critical issues from validation review — FIXED
Issues #1–#21 documented below are resolved in the current implementation.

## Remaining
- **Step 9:** Aeron Monitor Pattern (monitor profile exists in config, but suspend/wake/escalation loop not wired)
- **Step 10 (cost):** Tree-based cost aggregation (parent = own + sum of children) — not yet plumbed into TUI display
- **Step 12:** Testing — no sub-agent specific tests exist yet
- **Stale-read detection (Layer 2):** Editor stale-read detection with agent identity — not yet wired

## Critical identified issues — ALL RESOLVED
  Deep Validation Review: Sub-Agent System Implementation (audit date: 2026-05-20, resolved: 2026-05-21)
All 21 issues below are fixed in current code. Retained for historical reference.
CRITICAL ISSUES
1. DEADLOCK: Sub-agents with Yolo=false block forever on user-confirmation tools
File: session/subagent.go:302 and agent/tool_exec.go:102-161, 163-225, 227-304
Problem: Sub-agents inherit a.Yolo = o.session.yolo. When yolo = false (the default), executeOneToolCall will wait on blockResolveCh (which is handle.BlockCh) for three tool categories:

question tool (line 152): resp := <-blockResolveCh
File operations (line 274): resp := <-blockResolveCh
execute_command needing confirmation (line 194): resp := <-blockResolveCh
Nobody ever writes to handle.BlockCh. The sub-agent's emit function (subagent.go:370-399) forwards "blocking" events to the TUI via o.sender.SendAgentEventWithID, but no response is ever sent back through handle.BlockCh. The sub-agent goroutine deadlocks permanently.

The buildProxiedToolFn has a question handler that returns immediately (subagent.go:491-506), but this is dead code because executeOneToolCall handles question entirely (lines 102-161) and returns before ToolFn is ever called.

Fix: Set Yolo = true for sub-agents since policy enforcement is already handled by PolicyAllows in buildProxiedToolFn. In session/subagent.go:302:

// Before:
a.Yolo = o.session.yolo

// After:
a.Yolo = true // sub-agents skip user confirmation; policy enforced via PolicyAllows
2. DEADLOCK AGGRAVATION: ensureToolExists forces question tool into sub-agent toolset
File: agent/tool_filter.go:100-103
Problem: FilterTools explicitly adds the question tool for sub-agents at depth > 0:

if depth > 0 {
    filtered = ensureToolExists(filtered, "write_report")
    filtered = ensureToolExists(filtered, "question")
}
Combined with issue #1, this guarantees that every sub-agent will have the question tool available, and calling it will deadlock.

Fix: Remove question from ensureToolExists for sub-agents. In agent/tool_filter.go:100-103:

// Before:
if depth > 0 {
    filtered = ensureToolExists(filtered, "write_report")
    filtered = ensureToolExists(filtered, "question")
}

// After:
if depth > 0 {
    filtered = ensureToolExists(filtered, "write_report")
    // Do not add "question" - sub-agents cannot resolve blocking prompts
}
3. RACE CONDITION: SubAgentHandle.StepCount accessed from multiple goroutines without synchronization
File: agent/subagent_types.go:78, session/subagent.go:395,167, agent/subagent_manager.go:127
Problem: StepCount is an int field that is:

Written in runSubAgentLoop (subagent.go:395): handle.StepCount++ — from the sub-agent goroutine
Read in PollSubAgent (subagent.go:167): handle.StepCount — from the main goroutine
Read in ListStatus (subagent_manager.go:127): h.StepCount — from any goroutine
This is a textbook data race under Go's memory model.

Fix: Change StepCount to use atomic operations. In agent/subagent_types.go:78:

// Before:
StepCount int

// After:
stepCount atomic.Int32  // use atomic for concurrent access
Then update all access sites:

session/subagent.go:395: handle.stepCount.Add(1) instead of handle.StepCount++
session/subagent.go:167: "steps_used": handle.stepCount.Load() instead of handle.StepCount
agent/subagent_manager.go:127: StepsUsed: int(h.stepCount.Load()) instead of h.StepCount
4. BUG: Agent.Ctx never set for sub-agents — timeout/cancellation status always wrong
File: session/subagent.go:296-307, session/subagent.go:420-428
Problem: In launchSubAgent, subCtx is created and passed to Agent.Run(ctx, ...), but a.Ctx is never assigned. In buildResultFromMessages (subagent.go:422):

if ctx := handle.Agent.Ctx; ctx != nil {
    if ctx.Err() == context.DeadlineExceeded {
        status = agent.SubAgentFailedTimeout
    } else if ctx.Err() == context.Canceled {
        status = agent.SubAgentCancelled
    }
}
Since handle.Agent.Ctx is always nil for sub-agents, this check is skipped. Sub-agents that time out or are cancelled always report SubAgentCompleted instead of SubAgentFailedTimeout or SubAgentCancelled.

Fix: Set a.Ctx = subCtx in launchSubAgent. In session/subagent.go, after line 304:

a.Ctx = subCtx
5. BUG: currentDepth() always returns 0 — depth limiting broken for nested sub-agents
File: session/subagent.go:544-546
Problem:

func (o *SubAgentOrchestrator) currentDepth() int {
    return 0
}
This means SubAgentSpec.Depth is always set to o.currentDepth() + 1 = 1 (subagent.go:77). All sub-agents are created at depth 1 regardless of nesting. Validate computes depth = 0 + 1 = 1, which passes the depth > maxDepth check (1 > 2 is false). A depth-1 sub-agent can then spawn a child that is ALSO at depth 1, defeating the depth limit entirely.

Fix: The RunSubAgent method should accept and use the parent agent's depth. The simplest approach is to store depth information in the SubAgentSpec and use it when computing child depth. In session/subagent.go:77:

// Before:
Depth: o.currentDepth() + 1,

// After:
Depth: o.depthFromContext(params) + 1,
Add a helper method:

func (o *SubAgentOrchestrator) depthFromContext(params map[string]any) int {
    // If called from a sub-agent tool function, the parent depth is available
    // via the agent's Depth field which was set during creation
    if o.session != nil {
        // The orchestrator is always at depth 0 (main session)
        // Sub-agents calling run_sub_agent go through the parent tool fn,
        // which is the main session's tool fn. The parent depth needs to
        // be passed explicitly in params or tracked per-handle.
    }
    return 0
}
A more robust fix: add a "parent_depth" parameter to run_sub_agent and pass it from the sub-agent's buildProxiedToolFn:

// In buildProxiedToolFn, when calling run_sub_agent:
// Add parent depth to params before forwarding
Alternatively, store the parent depth in the SubAgentOrchestrator and update it per-call context.

HIGH SEVERITY ISSUES
6. RACE CONDITION: SubAgentManager.Validate reads m.active without holding the mutex (TOCTOU)
File: agent/subagent_manager.go:46
Problem: Validate reads len(m.active) without holding m.mu:

if len(m.active) >= m.maxConcurrent {
Between Validate (subagent.go:99) and Register (subagent.go:328), another goroutine could register a sub-agent, allowing the concurrent limit to be exceeded.

Fix: Move the concurrent count check into Register under the lock. In agent/subagent_manager.go:

func (m *SubAgentManager) Register(handle *SubAgentHandle) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    if len(m.active) >= m.maxConcurrent {
        return fmt.Errorf("maximum concurrent sub-agents (%d) reached", m.maxConcurrent)
    }
    m.active[handle.Spec.ID] = handle
    return nil
}
Update the caller in session/subagent.go:328 to handle the error.

7. BUG: buildResultFromMessages fails to populate Errors, PromptTokens, CompTokens, CostUSD
File: session/subagent.go:420-463
Problem: The result always has zero values for these fields:

return agent.SubAgentResult{
    ...
    // Missing: Errors, PromptTokens, CompTokens, CostUSD
}
The runSubAgentLoop emit function (subagent.go:375-397) tracks usage events but only increments StepCount. The token counts and cost are not accumulated anywhere.

Fix: Add accumulator fields to SubAgentHandle and populate them from the usage events. In agent/subagent_types.go, add:

type SubAgentHandle struct {
    ...
    totalPromptTokens int
    totalCompTokens   int
    totalCostUSD      float64
    errors            []string
}
In session/subagent.go:375-397, accumulate usage data:

if evt.Type == "usage" {
    if data, ok := evt.Data.(map[string]any); ok {
        ...
        handle.totalPromptTokens += promptT
        handle.totalCompTokens += compT
        if cost, ok := data["cost"].(float64); ok {
            handle.totalCostUSD += cost
        }
    }
}
Then populate in buildResultFromMessages:

PromptTokens: handle.totalPromptTokens,
CompTokens:   handle.totalCompTokens,
CostUSD:      handle.totalCostUSD,
Errors:       handle.errors,
8. SEMANTIC BUG: FilesRead populated with all claimed files, FilesWritten always empty
File: session/subagent.go:460
Problem:

FilesRead: o.fileOwnership.ClaimsByAgent(handle.Spec.ID),
ClaimsByAgent returns ALL files the agent claimed (both read and written). FilesWritten is never populated. This misrepresents the data — a researcher agent that reads 10 files and claims them would show 10 files in FilesRead and 0 in FilesWritten, even though it only read them.

Fix: Track write claims separately. Add a writeClaims map to FileOwnership (or a flag in ClaimEntry), and update CheckOrClaim to record whether the claim was for a write operation. Add a ClaimsWrittenByAgent method:

type ClaimEntry struct {
    AgentID   AgentID
    AgentName string
    ClaimedAt time.Time
    IsWrite   bool
}
Then in buildResultFromMessages:

FilesRead:    o.fileOwnership.ReadClaimsByAgent(handle.Spec.ID),
FilesWritten: o.fileOwnership.WriteClaimsByAgent(handle.Spec.ID),
9. RACE: Session.Close() may close s.db while sub-agent goroutines are still running
File: session/session.go:214-229
Problem: Close() cancels sub-agents and immediately closes the DB:

if s.subAgentOrch != nil {
    for _, h := range s.subAgentOrch.Manager().ActiveHandles() {
        s.subAgentOrch.Manager().Cancel(h.Spec.ID)
        s.subAgentOrch.FileOwnership().Release(h.Spec.ID)
    }
}
...
s.db.Close()
Cancel() calls handle.Cancel() (context cancel), but the goroutine may still be running and trying to call persistResult(o.session.db, ...) when s.db.Close() is invoked. This can cause "database is closed" errors.

Fix: Wait for sub-agent goroutines to finish before closing the DB. Add a WaitGroup to the orchestrator:

type SubAgentOrchestrator struct {
    ...
    wg sync.WaitGroup
}
In launchSubAgent, before go o.runSubAgentLoop(...):

o.wg.Add(1)
go func() {
    defer o.wg.Done()
    o.runSubAgentLoop(subCtx, handle, prompt)
}()
In Session.Close(), before s.db.Close():

if s.subAgentOrch != nil {
    s.subAgentOrch.wg.Wait()
}
MEDIUM SEVERITY ISSUES
10. PANIC RECOVERY DOUBLE-SEND on ResultCh and double-Unregister
File: session/subagent.go:348-368, 401-404
Problem: If a panic occurs after handle.ResultCh <- result (line 403) but before the function returns, the defer will try to send another result to ResultCh. Since the channel has buffer size 1 and the first result may not have been consumed, the second send will block indefinitely, deadlocking the goroutine.

Similarly, if a panic occurs after o.mgr.Unregister(handle.Spec.ID, result) (line 404), the defer will call Unregister again, creating a duplicate entry in m.completed.

Fix: Use a flag to track whether the result was already sent. In session/subagent.go:348-418:

func (o *SubAgentOrchestrator) runSubAgentLoop(ctx context.Context, handle *agent.SubAgentHandle, prompt string) {
    done := false
    defer func() {
        o.fileOwnership.Release(handle.Spec.ID)
        if r := recover(); r != nil {
            if !done {
                stack := debug.Stack()
                o.session.logger.Error("SUBAGENT", "panic", ...)
                result := agent.SubAgentResult{...}
                handle.ResultCh <- result
                o.mgr.Unregister(handle.Spec.ID, result)
                o.persistResult(result)
            }
        }
    }()

    agentMsgs := handle.Agent.Run(...)
    result := o.buildResultFromMessages(handle, agentMsgs)
    handle.ResultCh <- result
    o.mgr.Unregister(handle.Spec.ID, result)
    done = true
    o.persistResult(result)
    ...
}
11. CONTRADICTION: CatAeron has execute_command in both AllowedTools and DeniedTools
File: agent/tool_filter.go:38-50
Problem: CatAeron's CategoryToolDefaults lists execute_command in both AllowedTools (line 40) and DeniedTools (line 44). The filter applies deny first, then allow. Since execute_command is denied, it's removed. The allow filter then only keeps tools in the allowed list (which execute_command is no longer in since it was removed). The net effect is that execute_command is denied, but the contradiction is confusing and error-prone.

Fix: Remove execute_command from AllowedTools for CatAeron. In agent/tool_filter.go:38-50:

case CatAeron:
    return CategoryToolConfig{
        AllowedTools: []string{
            "read_file", "write_file", "edit_file", "glob_files", "grep_files",
            "list_directory",
            "write_report", "question", "reply",
            "list_sub_agents", "poll_sub_agent", "wait",
        },
        DeniedTools: []string{
            "delete_file", "move_file", "copy_file",
            "execute_command", "async_execute_command",
            "web_search", "web_fetch", "arxiv_search",
            "run_javascript",
        },
    }
12. EDGE CASE 20.4/20.18: Quota errors and API key errors produce wrong status
File: session/subagent.go:420-428
Problem: buildResultFromMessages only checks for context.DeadlineExceeded and context.Canceled. API errors (quota exhaustion, invalid API key, rate limiting) result in Agent.Run returning early via emitError, but the status is still SubAgentCompleted. The SubAgentFailedQuota status is defined but never produced.

Fix: Scan the agent's messages for error indicators. In buildResultFromMessages, add message scanning:

// Scan for error indicators in messages
for _, m := range msgs {
    if m.Role == "tool" && IsToolError(m.Content) {
        if strings.Contains(m.Content, "429") || strings.Contains(m.Content, "quota") {
            status = agent.SubAgentFailedQuota
        }
        result.Errors = append(result.Errors, m.Content)
    }
}
13. SubAgentFailedQuota status is defined but never produced
File: agent/subagent_types.go:18
Problem: The constant SubAgentFailedQuota exists but no code path sets this status. See issue #12.

14. Non-wait sub-agents have no cancellation path when parent is cancelled
File: session/subagent.go:109-131
Problem: When wait=false, RunSubAgent returns immediately. If the parent agent is subsequently cancelled, there's no mechanism to cancel the running sub-agent. The sub-agent continues running until it finishes or its own timeout expires, consuming resources even though the parent no longer cares about the result.

Fix: Register a context cancellation callback. In the wait=false path:

go func() {
    <-ctx.Done()
    o.mgr.Cancel(spec.ID)
}()
LOW SEVERITY ISSUES
15. DEAD CODE: agent.AllToolDefs() (exported) is never called
File: agent/util.go:42-44
Problem: AllToolDefs() is exported but never called from outside the package. Only the unexported allToolDefs() (which returns provider.ToolDef format) is used.

Fix: Remove the unused exported function, or use it in session/subagent.go:285 (which currently uses tools.AgentTools directly).

16. DEAD CODE: FileOwnership.Query() is never called
File: agent/file_ownership.go:82-89
Problem: The Query method is defined but never called from any code.

Fix: Remove or add a caller.

17. DEAD CODE: FileOwnership.AllClaims() is never called
File: agent/file_ownership.go:103-111
Problem: The AllClaims method is defined but never called from any code.

Fix: Remove or add a caller.

18. DEAD CODE: SubAgentManager.MaxConcurrent() is never called
File: agent/subagent_manager.go:146
Problem: MaxConcurrent() is defined but never called. Only MaxDepth() is used (in session/subagent.go:286).

Fix: Remove or add a caller.

19. DEAD CODE: question handler in buildProxiedToolFn is unreachable
File: session/subagent.go:491-506
Problem: The question tool case in buildProxiedToolFn can never be reached because executeOneToolCall (tool_exec.go:102-161) handles question entirely before ToolFn is called.

Fix: Remove the dead question handler from buildProxiedToolFn, or restructure executeOneToolCall to allow ToolFn to handle question.

20. Duplicate getStrParam function definitions across packages
File: session/tools.go:388-396, session/subagent.go (uses it), agent/util.go:12-23 (getStr)
Problem: getStrParam is defined in session/tools.go and used in session/subagent.go (same package, so it works). But agent/util.go has a similar getStr function. Not a bug, but a maintenance concern.

21. RateLimiter.Wait uses empty string key when provider is not specified
File: session/subagent.go:483
Problem: o.rateLimiter.Wait(ctx, spec.Provider) — if spec.Provider is empty, all sub-agents without a provider share the same rate-limiting bucket keyed by "", which is likely not the intended behavior.

Fix: Use the resolved provider name instead of the raw spec value:

resolvedProvider := spec.Provider
if resolvedProvider == "" {
    resolvedProvider = o.session.cfg.ActiveProvider
}
if err := o.rateLimiter.Wait(ctx, resolvedProvider); err != nil {
SUMMARY TABLE
#	Severity	File	Line	Problem
1	CRITICAL	session/subagent.go	302	Sub-agents deadlock on question/file ops/execute_command when Yolo=false
2	CRITICAL	agent/tool_filter.go	102	ensureToolExists("question") guarantees deadlock for sub-agents
3	CRITICAL	agent/subagent_types.go	78	StepCount data race across goroutines
4	CRITICAL	session/subagent.go	296-307	Agent.Ctx never set — timeout/cancel status always wrong
5	HIGH	session/subagent.go	544	currentDepth() always 0 — depth limiting broken for nesting
6	HIGH	agent/subagent_manager.go	46	Validate reads m.active without mutex (TOCTOU)
7	HIGH	session/subagent.go	420-463	Result missing Errors, PromptTokens, CompTokens, CostUSD
8	MEDIUM	session/subagent.go	460	FilesRead contains all claims, FilesWritten always empty
9	MEDIUM	session/session.go	214-229	Close() may close DB while sub-agent goroutines running
10	MEDIUM	session/subagent.go	348-368	Panic recovery can double-send on ResultCh / double-Unregister
11	MEDIUM	agent/tool_filter.go	38-50	CatAeron execute_command in both AllowedTools and DeniedTools
12	MEDIUM	session/subagent.go	420-428	Quota/API errors produce SubAgentCompleted instead of proper status
13	LOW	agent/subagent_types.go	18	SubAgentFailedQuota defined but never produced
14	LOW	session/subagent.go	109-131	Non-wait sub-agents not cancelled when parent cancelled
15	LOW	agent/util.go	42	AllToolDefs() exported but never called
16	LOW	agent/file_ownership.go	82	Query() never called
17	LOW	agent/file_ownership.go	103	AllClaims() never called
18	LOW	agent/subagent_manager.go	146	MaxConcurrent() never called
19	LOW	session/subagent.go	491	question handler in buildProxiedToolFn is unreachable
20	LOW	session/tools.go:388 / agent/util.go:12	—	Duplicate getStrParam/getStr across packages
21	LOW	session/subagent.go	483	RateLimiter uses "" key when provider unspecified
Issues #1 and #2 are the most urgent — they cause guaranteed deadlocks for any sub-agent that calls question (which is force-added by ensureToolExists) or any file operation when yolo=false (the default). These will make the sub-agent system completely non-functional in the default configuration.
# Crude Sub-Agent System: Design Proposal

**Author:** Crude Architect
**Date:** 2026-05-20
**Status:** Final Draft — All Open Questions Resolved

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [Current Architecture Analysis](#2-current-architecture-analysis)
3. [Competitive Landscape](#3-competitive-landscape)
4. [Community Pain Points](#4-community-pain-points)
5. [Design Principles](#5-design-principles)
6. [Core Architecture](#6-core-architecture)
7. [The `run_sub_agent` Tool](#7-the-run_sub_agent-tool)
8. [Async Companion Tools](#8-async-companion-tools)
9. [Custom Instructions System](#9-custom-instructions-system)
10. [Permission & Safety Model](#10-permission--safety-model)
11. [Model/Provider Routing](#11-modelprovider-routing)
12. [Inter-Agent Communication](#12-inter-agent-communication)
13. [Permission Escalation to User](#13-permission-escalation-to-user)
14. [Concurrency & Race Conditions](#14-concurrency--race-conditions)
15. [Sub-Agent Lifecycle & Monitor Pattern](#15-sub-agent-lifecycle--monitor-pattern)
16. [State Machine Integration](#16-state-machine-integration)
17. [TUI Integration](#17-tui-integration)
18. [Session Database Schema](#18-session-database-schema)
19. [Cost Tracking](#19-cost-tracking)
20. [Edge Cases & Nuances](#20-edge-cases--nuances)
21. [Configuration Schema](#21-configuration-schema)
22. [Testing Strategy](#22-testing-strategy)
23. [Implementation Roadmap](#23-implementation-roadmap)

---

## 1. Executive Summary

Crude currently operates as a single-agent system: one LLM loop, one set of tools, one context window, one permission profile. This proposal introduces a **sub-agent system** that allows Crude's main agent to spawn specialized child agents with scoped permissions, custom instructions, configurable model/provider, and isolated context windows.

The design is informed by deep analysis of OpenAI Codex subagents, Claude Code's Task tool, AutoGen 0.4's event-driven architecture, CrewAI's role-based agents, and LangGraph's supervisor pattern. It also incorporates lessons from academic research on agent privilege escalation (arXiv 2601.11893, 2504.11703), permission architecture (One Horizon), and community-identified pain points around approval fatigue, merge conflicts, and cross-agent config poisoning.

**Key design decisions:**

- Sub-agents are **separate sessions** — each sub-agent gets its own session in the DB, its own tab in the TUI, and its own `reply()` access
- **Four capability categories** — Main (orchestrator), Researcher (gatherer), Crafter (implementer), Aeron (grunt) — each with default model, policy, and tool access
- **Session-scoped lifecycle** — sub-agents survive across turns. The aeron monitor pattern keeps the main agent sleeping while a cheap aeron watches and escalates on events
- **Auto-claim file ownership** — files are claimed automatically on first write, not required upfront. Main agent is notified of all claims
- **Proxied ToolFn** — all sub-agent tool calls route through the parent session for policy enforcement
- **Per-agent permission channels** — each sub-agent gets its own `blockResolveCh` for approval prompts
- **Question tool** — sub-agents ask questions to the main agent (not the user). Main agent decides whether to answer or escalate to user
- **Category-based model/provider routing** — cheap models for research, powerful models for implementation, ultra-cheap for grunt work
- **No hard token budgets** — loop detection auto-kills runaway agents. Heavy but normal usage shows an orange warning with a kill button
- **JSON profiles only** — profiles live in `config.json`, no TOML files
- **Structured results** — each sub-agent produces a gist (2000 char max) + 2 path pointers (session log + result file) via the `write_report` tool
- **Runtime overrides at top of inheritance chain** — CLI flags like `--yolo` override everything

---

## 2. Current Architecture Analysis

### Current Crude Agent Loop

```
User Input → Session.Run() → newAgent() → Agent.Run() → [LLM call → tool calls → LLM call → ...] → final response
```

**Key structures:**

| Component | Current State | Sub-Agent Impact |
|-----------|--------------|-----------------|
| `Agent` struct | Single instance per turn, single `Provider`, `Model`, `Policy`, `ToolFn` | Must support spawning child `Agent` instances with different configs |
| `Agent.Run()` | Sequential tool-use loop with `blockResolveCh` for permission prompts | Sub-agents need their own `Run()` loops with per-agent `blockResolveCh` channels |
| `tools.AgentTools` | Flat list of 20+ tools exposed to every agent | Must be filterable per sub-agent (tool subsets) |
| `safety.Policy` | `DefaultPolicy()` (allow all) or `ReadOnlyPolicy()` | Must be composable per sub-agent with deny lists |
| `safety.PermissionStore` | Global per-session, shared across all operations | Must be scoped: sub-agent permissions ⊆ parent permissions |
| `statemachine.ConversationStateMachine` | Single state tree per session | Sub-agent plans should be visible in the parent's state tree |
| `fileio.Editor` | Journal-based, single writer | Multiple concurrent writers need file ownership / locking |
| `session.Config` | Single `ActiveProvider` + `Providers` map | Must support per-agent model/provider overrides |
| `sessiondb` | One session DB per conversation, `sessions` + `messages` + `cost_snapshots` | Must support sub-agent sessions with `parent_session_id` foreign key |

### What Needs to Change

1. **Agent struct** needs a `ParentID`, `Depth`, `AgentID`, and a `ToolFilter`
2. **ToolFn** needs a proxied variant that routes sub-agent tool calls through the parent session for policy enforcement
3. **New tools** must be added: `run_sub_agent`, `poll_sub_agent`, `cancel_sub_agent`, `list_sub_agents`, `write_report`, `question`
4. **Session** needs a `SubAgentManager` to track active sub-agents
5. **Permission escalation** must tag which agent caused the prompt (per-agent `blockResolveCh`)
6. **Editor** needs auto-claim file-ownership tracking to prevent concurrent write conflicts
7. **Config** needs per-category model/provider settings (Main → Researcher → Crafter → Aeron)
8. **Session DB** needs `sub_agent_results` and `file_claims` tables, plus `parent_session_id`, `agent_category`, `agent_name`, `depth` columns on `sessions`
9. **Aeron monitor pattern** for session-scoped sub-agent lifecycle management

---

## 3. Competitive Landscape

### OpenAI Codex Subagents (GA, March 2026)

**Architecture:** Manager → Worker pattern. Manager decomposes tasks, spawns workers in isolated cloud sandboxes. Workers execute autonomously and report results back. No direct worker-to-worker communication.

**Key features adopted:**
- Sandbox modes: `read-only`, `workspace-write`, `full-access` — maps directly to Crude's `Policy` system
- `max_threads=6`, `max_depth=1` — concurrency and depth limits with sensible defaults
- **Parent runtime overrides re-applied to children** — if user set `--yolo` interactively, children inherit it even if their config says otherwise
- Approval requests surface from inactive threads with thread label shown to user

**Key issues avoided:**
- Sub-agent quota exhaustion surfaces as "no response" instead of structured error
- No direct agent-to-agent communication (only manager-mediated)
- Merge conflicts when workers touch same files

### Claude Code Task Tool

**Architecture:** Main agent calls `Task` tool, which spawns a child agent with its own context window. Child runs autonomously and returns a text result.

**Key features adopted:**
- Context isolation — child gets a clean context window
- Custom `description` parameter — parent specifies what the sub-agent should do
- Built-in agent profiles: `default`, `worker` (execution), `explorer` (read-heavy)

**Key issues avoided:**
- Depth=1 too restrictive — we support configurable depth
- Sub-agents lose context tracking when delegating asynchronously
- Task tool not available inside sub-agents — we allow `run_sub_agent` at depth < max_depth
- Permission prompts from inactive threads break context flow

### AutoGen 0.4

**Architecture:** Event-driven, message-passing between agents. Each agent is a process with its own state.

**Key features adopted:**
- Message-based communication — structured results, not raw text
- Supervisor patterns — built-in orchestration
- Observable — telemetry events for monitoring, token usage, errors

**Key issues avoided:**
- Complexity overhead — keep it Go-native, simple
- Framework lock-in

### CrewAI

**Architecture:** Role-based agents with tools, delegation, and memory.

**Key features adopted:**
- Agent roles with tool scoping — each agent has a defined role and allowed tools
- Delegation — agents can delegate within constraints

**Key issues avoided:**
- LangChain dependency bloat
- Over-abstraction of simple patterns

### Devin / Security Lessons

**Critical lessons from privilege escalation incidents:**
- Devin ran `chmod +x` on blocked binaries without user approval
- **Root cause:** agents treat security boundaries as problems to solve, not hard stops
- **Kill chain:** capability-identity gap → runtime scope expansion → cross-agent config poisoning → dotfile persistence
- **98.9% of scraped agent configs have zero deny rules** — default is "allow everything"
- **In-process guardrails fail** because the agent IS the process — policy must live outside

---

## 4. Community Pain Points

### Approval Fatigue
- Anthropic reports **93% of Claude Code permission prompts are approved** — humans stop evaluating
- "Permission prompts feel safe until they become routine" — One Horizon analysis
- The right question is "what should this agent be allowed to do, inside which boundary?" not "allow this command?"

### Sub-Agent Context Tracking Breaks
- When sub-agents delegate asynchronously, the parent loses track of what happened
- Context isolation is good for preventing pollution but bad for result aggregation
- **Our solution:** Each sub-agent is a separate session with its own DB row and tab — full auditability

### Merge Conflicts Between Parallel Agents
- Multiple sub-agents working on the same repo produce PRs that merge cleanly but break behavior
- The problem is *emergent and relational* — it lives between agents, not inside any one
- **Our solution:** Auto-claim file ownership on first write + runtime stale-read detection

### Error Propagation
- Codex sub-agent quota exhaustion surfaces as "no response"
- **Our solution:** Structured failure states: `completed`, `failed_quota`, `failed_timeout`, `failed_error`, `failed_policy`, `cancelled`

### Nested Depth Limitations
- Claude Code enforces depth=1, preventing hierarchical patterns
- **Our solution:** Configurable depth with safety limits (default `max_depth=2`)

### Cross-Agent Config Poisoning
- Compromised agent writes to another agent's config files
- **Our solution:** Out-of-process policy enforcement — sub-agents cannot modify their own policy

---

## 5. Design Principles

1. **Sub-agents are separate sessions** — each sub-agent gets its own session in the DB, its own tab in the TUI, its own message log, and its own `reply()` access. This is not just context isolation — it's full session isolation.

2. **Permission narrows, never widens** — a sub-agent's permission set is always a subset of its parent's. A read-only parent cannot spawn a write-capable child.

3. **Context is isolated by default** — each sub-agent gets a clean context window. The parent injects a task description and custom instructions. Only the **structured result** (gist + path pointers) flows back into the parent's context.

4. **Results are structured and bounded** — sub-agents return a `SubAgentResult` with a gist (2000 char max) and two path pointers (session log, result file). The `write_report` tool lets sub-agents craft a tailored result. If never called, the last non-tool-call message becomes the result.

5. **Depth is limited, concurrency is limited** — configurable `max_depth` (default 2) and `max_concurrent` (default 4) prevent runaway spawning.

6. **Permission escalation preserves agent identity** — when a sub-agent needs user approval, the prompt clearly shows *which agent* is asking and *what scope* it's requesting. Each sub-agent has its own `blockResolveCh`.

7. **Auto-claim file ownership** — files are claimed automatically on first write. The main agent is notified of all claims and releases. Upfront `file_ownership` is optional for pre-declaring known paths.

8. **Out-of-process policy enforcement** — all sub-agent tool calls route through the parent session's proxied ToolFn. Safety checks run in the parent/session layer. A sub-agent cannot modify its own permission policy.

9. **Session-scoped lifecycle with aeron monitor** — sub-agents survive across turns. The main agent spawns a cheap aeron monitor that watches running sub-agents and escalates on events, waking the main agent when needed.

10. **Runtime overrides are supreme** — CLI flags and interactive settings override all other levels in the inheritance chain: `Runtime > Inline > Profile > Category > Parent`.

11. **Sub-agents ask the orchestrator, not the user** — the `question` tool routes sub-agent questions to the main agent. The main agent decides whether to answer or escalate to the user.

---

## 6. Core Architecture

### Agent Categories

Sub-agents are organized into four fundamental **categories** that define their capability tier, tool access, budget, and model defaults:

```
         ┌────────┐
         │  MAIN  │   Orchestrator — plans, delegates, interacts with user
        ┌┴────────┴┐   Best model, full tool access, highest reasoning
        │ CRAFTER  │   Implementer — writes code, edits files, runs tests
       ┌┴──────────┴┐  Good model, no-delete policy, write-capable
       │ RESEARCHER │   Gatherer — explores codebase, searches web, experiments
      ┌┴────────────┴┐ Cheap/fast model, read-only policy, limited execution
      │    AERON     │   Grunt — formats, lints, boilerplate, monitoring
      └──────────────┘  Cheapest model, highly restricted, minimal context
```

Each tier down = cheaper model, more restricted tools, less autonomy, lower budget.
Each tier up = more expensive model, broader tools, higher reasoning, higher budget.

Every sub-agent belongs to exactly one category. The category sets the **baseline defaults**. A named **profile** (e.g. `explorer`, `reviewer`, `fixer`) specifies which category it belongs to and can override individual fields (model, instructions, etc.).

### Agent Hierarchy

```
┌──────────────────────────────────────────────────────────┐
│  Session (session-scoped, sub-agents survive turns)       │
│  ┌──────────────────────────────────────────────────┐    │
│  │  MAIN (depth=0)                                    │    │
│  │  Category: main                                     │    │
│  │  Session: primary session DB                        │    │
│  │  Tab: primary chat tab                              │    │
│  │  Provider: openrouter, Model: claude-sonnet-4       │    │
│  │  Policy: Default (all tools)                        │    │
│  │  ToolFn: [all tools including run_sub_agent]        │    │
│  │  blockResolveCh: main channel                       │    │
│  │                                                     │    │
│  │  ┌─────────────────────┐ ┌─────────────────────┐   │    │
│  │  │ RESEARCHER (depth=1) │ │ CRAFTER (depth=1)   │   │    │
│  │  │ Profile: explorer    │ │ Profile: fixer      │   │    │
│  │  │ Session: sub-DB      │ │ Session: sub-DB     │   │    │
│  │  │ Tab: explorer tab    │ │ Tab: fixer tab      │   │    │
│  │  │ Model: gemini-flash  │ │ Model: claude-sonnet│   │    │
│  │  │ Policy: read-only    │ │ Policy: no-delete   │   │    │
│  │  │ Tools: read+search   │ │ Tools: read+write   │   │    │
│  │  │ blockResolveCh: own  │ │ blockResolveCh: own │   │    │
│  │  └─────────────────────┘ └─────────────────────┘   │    │
│  │                                                     │    │
│  │  ┌─────────────────────┐                             │    │
│  │  │ AERON (depth=1)     │                             │    │
│  │  │ Profile: monitor    │                             │    │
│  │  │ Session: sub-DB     │                             │    │
│  │  │ Tab: monitor tab    │                             │    │
│  │  │ Model: gemini-flash │                             │    │
│  │  │ Policy: restricted  │                             │    │
│  │  │ Tools: list+poll    │                             │    │
│  │  │ MaxSteps: 10        │                             │    │
│  │  └─────────────────────┘                             │    │
│  └──────────────────────────────────────────────────┘    │
│                                                           │
│  SubAgentManager: tracks by category + agent ID           │
│  FileOwnership: auto-claim on write, notify main          │
│  PermissionStore: global + per-agent scoped               │
│  Editor: stale-read detection with agent identity         │
│  StateMachine: sub-agent goals visible in tree            │
└──────────────────────────────────────────────────────────┘
```

### New Types

```go
// agent/subagent.go

type AgentID string

type SubAgentSpec struct {
    ID              AgentID          `json:"id"`
    Name            string           `json:"name"`
    Category        string           `json:"category"`        // "main" | "researcher" | "crafter" | "aeron"
    Description     string           `json:"description"`
    Instructions    string           `json:"instructions"`
    Model           string           `json:"model"`
    Provider        string           `json:"provider"`
    Policy          safety.Policy    `json:"policy"`
    AllowedTools    []string         `json:"allowed_tools"`
    DeniedTools     []string         `json:"denied_tools"`
    MaxSteps        int              `json:"max_steps"`
    Depth           int              `json:"depth"`
    FileOwnership   []string         `json:"file_ownership"`  // optional pre-declared paths
    ParentID        AgentID          `json:"parent_id"`
    TimeoutSeconds  int              `json:"timeout_seconds"`
}

type SubAgentResult struct {
    AgentID      AgentID          `json:"agent_id"`
    Name         string           `json:"name"`
    Category     string           `json:"category"`
    SessionID    string           `json:"session_id"`       // sub-agent's own session
    Status       SubAgentStatus   `json:"status"`
    SummaryGist  string           `json:"summary_gist"`     // max 2000 chars, enters parent context
    ResultPath   string           `json:"result_path"`      // path to full result file
    LogPath      string           `json:"log_path"`         // path to sub-agent session log dir
    FilesRead    []string         `json:"files_read"`
    FilesWritten []string         `json:"files_written"`
    Errors       []string         `json:"errors"`
    PromptTokens int              `json:"prompt_tokens"`
    CompTokens   int              `json:"completion_tokens"`
    CostUSD      float64          `json:"cost_usd"`
    DurationMs   int64            `json:"duration_ms"`
}

type SubAgentStatus string

const (
    SubAgentCompleted     SubAgentStatus = "completed"
    SubAgentFailedTimeout SubAgentStatus = "failed_timeout"
    SubAgentFailedQuota   SubAgentStatus = "failed_quota"
    SubAgentFailedError   SubAgentStatus = "failed_error"
    SubAgentFailedPolicy  SubAgentStatus = "failed_policy"
    SubAgentCancelled     SubAgentStatus = "cancelled"
)

type AgentCategory string

const (
    CategoryMain       AgentCategory = "main"
    CategoryResearcher AgentCategory = "researcher"
    CategoryCrafter    AgentCategory = "crafter"
    CategoryAeron      AgentCategory = "aeron"
)

type SubAgentManager struct {
    mu            sync.Mutex
    active        map[AgentID]*SubAgentHandle
    completed     []SubAgentResult
    maxConcurrent int
    maxDepth      int
}

type SubAgentHandle struct {
    Spec      SubAgentSpec
    Agent     *Agent
    Cancel    context.CancelFunc
    ResultCh  chan SubAgentResult
    BlockCh   chan BlockResolveMsg    // per-agent blocking channel
    StartedAt time.Time
    SessionID string                 // sub-agent's own session ID
    DB        *sql.DB                 // sub-agent's own session DB
}
```

---

## 7. The `run_sub_agent` Tool

### Tool Definition

```json
{
  "type": "function",
  "function": {
    "name": "run_sub_agent",
    "description": "Spawn a specialized sub-agent to handle a well-defined subtask. The sub-agent gets its own context window, tool set, permission policy, session, and TUI tab. It runs to completion and returns a structured result. Use for: parallel codebase exploration, focused implementation tasks, read-only audits, or any work that benefits from a clean context window. Sub-agents can spawn their own sub-agents up to max_depth.",
    "parameters": {
      "type": "object",
      "properties": {
        "name": {
          "type": "string",
          "description": "Short name for this sub-agent (e.g. 'explorer', 'fixer', 'reviewer'). Used for display and tracking."
        },
        "category": {
          "type": "string",
          "description": "Capability tier: 'researcher' (read-only, cheap, fast), 'crafter' (implementation, write-capable, mid-cost), or 'aeron' (grunt work, ultra-cheap, restricted). Default: 'crafter'. Category sets default model, policy, max_steps, timeout, and tool access — which can all be overridden individually.",
          "enum": ["researcher", "crafter", "aeron"]
        },
        "description": {
          "type": "string",
          "description": "The task this sub-agent should accomplish. Be specific: 'Find all API routes that lack authentication middleware and list them with file:line references' NOT 'check security'."
        },
        "instructions": {
          "type": "string",
          "description": "Custom instructions appended to the sub-agent's system prompt after profile instructions. Use to specialize behavior: 'Focus only on the backend API layer. Do not modify frontend code.' Empty = inherit from category/profile defaults only."
        },
        "model": {
          "type": "string",
          "description": "Model override for this sub-agent. Empty = inherit from category default."
        },
        "provider": {
          "type": "string",
          "description": "Provider key override. Empty = inherit from category default."
        },
        "policy": {
          "type": "string",
          "description": "Permission policy: 'default' (all tools), 'read-only' (no writes/execution), 'no-delete' (no delete_file), or 'no-execute' (no command execution). Default: inherited from category.",
          "enum": ["default", "read-only", "no-delete", "no-execute"]
        },
        "allowed_tools": {
          "type": "array",
          "items": {"type": "string"},
          "description": "Explicit tool whitelist. If provided, only these tools are available (intersected with category defaults)."
        },
        "denied_tools": {
          "type": "array",
          "items": {"type": "string"},
          "description": "Explicit tool blacklist. These tools are removed (unioned with category defaults)."
        },
        "file_ownership": {
          "type": "array",
          "items": {"type": "string"},
          "description": "Optional pre-declared paths this sub-agent claims exclusive write access to. Auto-claim still applies for any files not declared here."
        },
        "max_steps": {
          "type": "integer",
          "description": "Maximum tool-use steps. Default from category (researcher: 30, crafter: 60, aeron: 10). Max 200."
        },
        "timeout_seconds": {
          "type": "integer",
          "description": "Maximum runtime in seconds. Default from category (researcher: 120, crafter: 300, aeron: 60)."
        },
        "wait": {
          "type": "boolean",
          "description": "If true (default), block until the sub-agent completes and return its result. If false, return immediately with a handle ID for later polling via poll_sub_agent."
        }
      },
      "required": ["name", "description"]
    }
  }
}
```

### Execution Flow

```
Parent calls run_sub_agent(spec)
  │
  ├── SubAgentManager.Validate(spec)
  │   ├── Check max_depth not exceeded
  │   ├── Check max_concurrent not exceeded
  │   ├── Resolve Category defaults
  │   │   ├── Look up category config for spec.Category
  │   │   ├── Apply category defaults: model, provider, policy, max_steps, timeout
  │   │   ├── Intersect allowed_tools with category defaults
  │   │   ├── Union denied_tools with category defaults
  │   │   └── Apply runtime overrides (CLI flags top the chain)
  │   ├── Apply profile overrides (if named profile exists in config.json)
  │   ├── Apply inline overrides (inline > profile > category > parent)
  │   ├── Check policy ≤ parent policy (cannot widen permissions)
  │   ├── Check tool list is valid for resolved model (fail if no tool-use support)
  │   └── Check pre-declared file_ownership doesn't conflict with active agents
  │
  ├── Build Sub-Agent
  │   ├── Resolve final model: runtime > inline > profile > category > parent
  │   ├── Resolve final provider: runtime > inline > profile > category > parent
  │   ├── Create sub-agent session DB (new .db file, parent_session_id set)
  │   ├── Create Agent with resolved model and provider
  │   ├── Filter tool list (intersection of allowed, subtraction of denied)
  │   ├── Build custom system prompt (base + category identity + profile instructions + inline instructions + policy constraints + file ownership + depth limit)
  │   ├── Set max_steps from resolved value
  │   ├── Create per-agent blockResolveCh
  │   └── Wire proxied ToolFn (routes through parent session for policy enforcement)
  │
  ├── Launch Sub-Agent.Run()
  │   ├── Context with timeout
  │   ├── Per-agent blockResolveCh for permission prompts
  │   ├── recover() wrapper — panics → SubAgentResult{failed_error} + stack trace
  │   ├── File ownership: auto-claim on first write, notify main agent
  │   ├── Emit events tagged with agent_id + category for TUI
  │   └── Collect SubAgentResult on completion
  │
  └── Return result to parent
      ├── If wait=true: block, return SubAgentResult
      └── If wait=false: return handle ID immediately
```

---

## 8. Async Companion Tools

### `poll_sub_agent`

```json
{
  "type": "function",
  "function": {
    "name": "poll_sub_agent",
    "description": "Check the status and retrieve results from an asynchronously spawned sub-agent. Returns the SubAgentResult if completed, or current status if still running.",
    "parameters": {
      "type": "object",
      "properties": {
        "handle_id": {
          "type": "string",
          "description": "The handle ID returned by run_sub_agent(wait=false)."
        }
      },
      "required": ["handle_id"]
    }
  }
}
```

Returns:
- If completed: full `SubAgentResult`
- If still running: `{ "status": "running", "elapsed_seconds": 45, "steps_used": 12 }`
- If handle not found: `{ "status": "not_found", "error": "Handle ID not found" }`

Handle IDs are **session-scoped** — they are valid for the duration of the parent session.

### `cancel_sub_agent`

```json
{
  "type": "function",
  "function": {
    "name": "cancel_sub_agent",
    "description": "Cancel a running sub-agent. The sub-agent's context is cancelled, it returns a SubAgentResult with status 'cancelled', and all its file ownership claims are released.",
    "parameters": {
      "type": "object",
      "properties": {
        "handle_id": {
          "type": "string",
          "description": "The handle ID of the sub-agent to cancel."
        }
      },
      "required": ["handle_id"]
    }
  }
}
```

Returns: `SubAgentResult{Status: "cancelled"}`

### `list_sub_agents`

```json
{
  "type": "function",
  "function": {
    "name": "list_sub_agents",
    "description": "List all active and recently completed sub-agents. Shows status, category, model, elapsed time, and steps used.",
    "parameters": {
      "type": "object",
      "properties": {
        "include_completed": {
          "type": "boolean",
          "description": "If true, also include completed/cancelled/failed agents from this session. Default: false (active only)."
        }
      }
    }
  }
}
```

Returns:
```json
[
  {
    "handle_id": "fixer-a1b2c3",
    "name": "fixer",
    "category": "crafter",
    "status": "running",
    "elapsed_seconds": 45,
    "steps_used": 12,
    "max_steps": 60,
    "model": "claude-sonnet-4",
    "files_claimed": ["src/api/auth.go", "src/api/token.go"]
  }
]
```

---

## 9. Custom Instructions System

### How Instructions Are Injected

The sub-agent's system prompt is constructed as:

```
[SystemPromptBase]                          ← Same base prompt as main agent
+ [Environment Section]                     ← Same environment (OS, git, PATH)
+ [Sub-Agent Identity]                      ← "You are a {category} sub-agent '{name}'. Your task: {description}"
+ [Category Briefing]                       ← "You are in the {category} tier. This means: {category description, constraints}"
+ [Profile Instructions]                    ← Instructions from the named profile in config.json (if any)
+ [Inline Instructions]                     ← Instructions from the run_sub_agent call (APPENDED, not replacing)
+ [Policy Constraints]                      ← "You have the following tool restrictions..."
+ [File Ownership]                          ← "Files you write will be auto-claimed. Pre-claimed paths: ..."
+ [Depth Limit]                             ← "You cannot spawn further sub-agents." (if at max depth)
+ [Result Format]                           ← "Use write_report to produce your final result. If you don't, your last message becomes the result. Keep gists under 2000 chars."
```

The Category Briefing lets the sub-agent know what tier it's in, so it self-regulates:
- **Researcher:** "You are in the Researcher tier. Prioritize gathering information and analysis. Do not modify files. Return structured findings."
- **Crafter:** "You are in the Crafter tier. You have write access. Make changes carefully and verify them. You are responsible for the code you write."
- **Aeron:** "You are in the Aeron tier. You have minimal tools and a tight budget. Stay focused on the simple task at hand. Do not over-engineer."

### Custom Instructions from Config (JSON Only)

Profiles are defined in `config.json` under `agents.profiles`. Each profile declares which **category** it belongs to. The category provides the baseline defaults; the profile provides specialized behavior.

**Profile definition in `config.json`:**
```json
{
  "agents": {
    "profiles": {
      "explorer": {
        "category": "researcher",
        "description": "Read-only codebase explorer for gathering evidence before changes.",
        "instructions": "Stay in exploration mode. Trace execution paths, cite files and symbols. Prefer fast search over broad scans. Never modify any files.",
        "allowed_tools": ["read_file", "glob_files", "grep_files", "list_directory", "web_search", "web_fetch"]
      },
      "reviewer": {
        "category": "researcher",
        "model": "anthropic/claude-sonnet-4",
        "description": "Code reviewer focused on correctness, security, and missing tests.",
        "max_steps": 40,
        "timeout_seconds": 180,
        "instructions": "Review code for correctness, security, and missing tests. Lead with concrete findings. Include reproduction steps when possible."
      },
      "fixer": {
        "category": "crafter",
        "description": "Implementation-focused agent for targeted fixes.",
        "instructions": "Make the smallest defensible change. Keep unrelated files untouched. Run tests after each fix.",
        "denied_tools": ["delete_file"],
        "file_ownership": ["src/", "tests/"]
      },
      "formatter": {
        "category": "aeron",
        "description": "Quick formatting and linting pass.",
        "max_steps": 8,
        "timeout_seconds": 30,
        "allowed_tools": ["read_file", "write_file", "execute_command", "glob_files"],
        "instructions": "Run the project's formatter and linter. Only fix formatting issues. Do not change logic, structure, or behavior."
      },
      "monitor": {
        "category": "aeron",
        "description": "Watches running sub-agents and escalates important events to the main agent.",
        "max_steps": 200,
        "timeout_seconds": 1800,
        "allowed_tools": ["list_sub_agents", "poll_sub_agent", "question"],
        "instructions": "Monitor running sub-agents. When a sub-agent completes, fails, or needs approval, escalate to the main agent with a summary. Do not modify any files. Keep monitoring until all sub-agents finish."
      }
    }
  }
}
```

### Inheritance Chain

When a sub-agent is spawned, values are resolved in this order (first wins):

```
Runtime Overrides (CLI flags, interactive settings)
  └── Inline Parameters (run_sub_agent call)
       └── Named Profile (config.json agents.profiles)
            └── Category Defaults (config.json agents.categories)
                 └── Parent Agent's Values (fallback)
```

Example for `run_sub_agent(name="reviewer", category="researcher", model="claude-sonnet-4")`:
1. **model** → `claude-sonnet-4` (inline override wins)
2. **policy** → `read-only` (inherited from `researcher` category, not overridden)
3. **max_steps** → `40` (from `reviewer` profile, overrides category default of 30)
4. **instructions** → profile instructions + inline instructions (appended)

### Lookup Order

When `run_sub_agent` is called with `name="explorer"`:
1. Check `config.json` `agents.profiles.explorer` (project-scoped if `.crude/config.json` exists, else user-global)
2. If no profile found, use category defaults + inline parameters only

---

## 10. Permission & Safety Model

### Category Default Policies

| Category | Default Policy | Rationale |
|----------|---------------|-----------|
| **main** | `default` (all tools) | Orchestrator needs full access to delegate and manage |
| **researcher** | `read-only` | Gathers information, never modifies project state |
| **crafter** | `no-delete` | Writes code and runs tests, but should not delete files |
| **aeron** | `no-delete` + restricted tools | Grunt work only; minimal tool set |

### Policy Hierarchy

```
Runtime Override (e.g., --yolo forces all approvals off)
  └── Session Global Policy
       └── Category Default Policy (researcher | crafter | aeron)
            └── Profile Policy Override
                 └── Inline Spec Policy Override
                      └── Grandchild Policy (intersection again)
```

**Rule: Sub-agent policy is always ≤ parent policy.** A read-only parent cannot spawn a write-capable child. Additionally, a sub-agent's policy cannot exceed its **category** defaults — you cannot set `policy=default` on an aeron sub-agent; it starts from `no-delete`+restricted and can only be tightened.

### Category Default Resolution

```go
func CategoryDefaults(category AgentCategory) CategoryConfig {
    switch category {
    case CategoryResearcher:
        return CategoryConfig{
            Policy:      "read-only",
            MaxSteps:    30,
            TimeoutSec:  120,
            AllowedTools: []string{
                "read_file", "glob_files", "grep_files", "list_directory",
                "web_search", "web_fetch", "arxiv_search", "run_javascript",
                "write_report", "question",
            },
            DeniedTools: []string{
                "write_file", "edit_file", "delete_file", "move_file", "copy_file",
                "execute_command", "async_execute_command",
            },
        }
    case CategoryCrafter:
        return CategoryConfig{
            Policy:     "no-delete",
            MaxSteps:   60,
            TimeoutSec: 300,
            AllowedTools: nil, // all except denied
            DeniedTools: []string{
                "delete_file",
            },
        }
    case CategoryAeron:
        return CategoryConfig{
            Policy:     "no-delete",
            MaxSteps:   10,
            TimeoutSec: 60,
            DeniedTools: []string{
                "delete_file", "move_file", "copy_file",
                "web_search", "web_fetch", "arxiv_search",
                "run_javascript", "async_execute_command",
            },
        }
    default:
        return CategoryConfig{}
    }
}
```

### Policy Composition

```go
func ComposePolicy(parent safety.Policy, spec SubAgentSpec, category AgentCategory, runtimeOverrides RuntimeOverrides) safety.Policy {
    catCfg := CategoryDefaults(category)
    child := policyFromCategory(catCfg.Policy)

    if spec.PolicyName != "" {
        switch spec.PolicyName {
        case "read-only":
            child = safety.ReadOnlyPolicy()
        case "no-delete":
            child = safety.DefaultPolicy()
            child.AllowedCapabilities[safety.CanDelete] = false
        case "no-execute":
            child = safety.DefaultPolicy()
            child.AllowedCapabilities[safety.CanExecute] = false
        }
    }

    // Intersection with parent: child cannot exceed parent
    for cap, allowed := range child.AllowedCapabilities {
        if !parent.AllowedCapabilities[cap] {
            child.AllowedCapabilities[cap] = false
        }
    }

    // Intersection with category: child cannot exceed category defaults
    catPolicy := policyFromCategory(catCfg.Policy)
    for cap, allowed := range child.AllowedCapabilities {
        if !catPolicy.AllowedCapabilities[cap] {
            child.AllowedCapabilities[cap] = false
        }
    }

    // Runtime overrides (e.g., --yolo) override everything
    if runtimeOverrides.Yolo {
        child.AutoApprove = true
    }

    return child
}
```

### Tool Filtering

```go
func FilterTools(allTools []tools.ToolDef, spec SubAgentSpec, category AgentCategory, depth int) []tools.ToolDef {
    catCfg := CategoryDefaults(category)
    filtered := allTools

    if len(catCfg.DeniedTools) > 0 {
        denied := makeSet(catCfg.DeniedTools)
        filtered = filterTools(filtered, func(t tools.ToolDef) bool {
            return !denied[t.Function.Name]
        })
    }

    if len(catCfg.AllowedTools) > 0 {
        allowed := makeSet(catCfg.AllowedTools)
        filtered = filterTools(filtered, func(t tools.ToolDef) bool {
            return allowed[t.Function.Name]
        })
    }

    if depth >= maxDepth {
        filtered = removeTool(filtered, "run_sub_agent")
    }

    if len(spec.AllowedTools) > 0 {
        allowed := makeSet(spec.AllowedTools)
        filtered = filterTools(filtered, func(t tools.ToolDef) bool {
            return allowed[t.Function.Name]
        })
    }

    if len(spec.DeniedTools) > 0 {
        denied := makeSet(spec.DeniedTools)
        filtered = filterTools(filtered, func(t tools.ToolDef) bool {
            return !denied[t.Function.Name]
        })
    }

    if depth >= 2 {
        filtered = removeTool(filtered, "execute_command")
        filtered = removeTool(filtered, "delete_file")
    }

    // Always include write_report and question for sub-agents
    filtered = ensureTool(filtered, "write_report")
    filtered = ensureTool(filtered, "question")

    return filtered
}
```

### Proxied ToolFn Architecture

All sub-agent tool calls route through the parent session for policy enforcement:

```
Sub-agent LLM generates tool call
  → Sub-agent Agent.Run() detects tool call
  → Proxied ToolFn intercepts
  → Routes to parent session's interceptor
  → Parent session applies:
    ├── Policy check (ComposePolicy result)
    ├── Permission store lookup (scoped to agent ID)
    ├── File ownership check (auto-claim + stale-read detection)
    ├── Rate limiter check (shared token bucket)
    └── If approval needed → emit to sub-agent's per-agent blockResolveCh
  → If approved: execute tool, return result to sub-agent's LLM
  → If denied: return error to sub-agent's LLM
```

The sub-agent never touches tools directly. The proxied ToolFn is a closure that captures the parent session reference and the sub-agent's identity:

```go
func (s *Session) buildProxiedToolFn(handle *SubAgentHandle) tools.ToolFn {
    return func(ctx context.Context, call tools.ToolCall) (tools.ToolResult, error) {
        // Policy check
        if !handle.Spec.Policy.Allows(call.Name) {
            return tools.ToolResult{}, fmt.Errorf("[POLICY BLOCK] Tool %q is not allowed for this agent. Your policy is %q.", call.Name, handle.Spec.Policy.Name())
        }

        // Permission store check (scoped to agent ID)
        if !s.permStore.IsAllowed(call.Name, call.Target, string(handle.Spec.ID)) {
            // Escalate to user via per-agent blockResolveCh
            result := s.escalateToUser(handle, call)
            if !result.Approved {
                return tools.ToolResult{}, fmt.Errorf("Permission denied for %q", call.Name)
            }
        }

        // File ownership check for write operations
        if isWriteOperation(call.Name) {
            if err := s.fileOwnership.CheckOrClaim(string(handle.Spec.ID), call.Target); err != nil {
                return tools.ToolResult{}, err
            }
            // Notify main agent of claim
            s.notifyClaim(handle.Spec.Name, call.Target)
        }

        // Stale-read detection for edit operations
        if isEditOperation(call.Name) {
            if stale, byWhom := s.editor.IsStale(call.Target, handle.Spec.ID); stale {
                return tools.ToolResult{}, fmt.Errorf("File %q was modified by agent %q since you last read it. Re-read the file before editing.", call.Target, byWhom)
            }
        }

        // Rate limiter check
        if err := s.rateLimiter.Wait(ctx, handle.Spec.Provider); err != nil {
            return tools.ToolResult{}, fmt.Errorf("Rate limit exceeded for provider %q. Retrying with backoff.", handle.Spec.Provider)
        }

        // Execute via parent session's tool execution
        return s.toolFn(ctx, call)
    }
}
```

### Out-of-Process Policy Enforcement

1. **The sub-agent cannot modify its own policy** — policy is set at spawn time and is immutable
2. **The `run_sub_agent` tool does not expose policy modification parameters** — only the parent can set them
3. **All tool calls go through the proxied ToolFn** — the parent session intercepts every sub-agent tool call
4. **Permission store entries are tagged with agent IDs** — "always allow" for one agent doesn't bleed to another
5. **Runtime overrides (CLI flags) override everything** — `--yolo` forces auto-approve on all sub-agents

---

## 11. Model/Provider Routing

### Inheritance Chain

```
Runtime Override (CLI flags)
  └── Inline spec.model / spec.provider
       └── Named Profile model / provider
            └── Category Default model / provider
                 └── Parent Agent's model / provider
```

### Category Default Model Tiers

| Category | Default Model | Default Provider | Reasoning | Cost Tier |
|----------|--------------|-----------------|-----------|-----------|
| **main** | *(inherits from parent / active config)* | — | Highest — orchestrates everything | $$$$ |
| **crafter** | `qwen/qwen3.5-235b-a22b` | openrouter | High — writes real code | $$$ |
| **researcher** | `google/gemini-2.0-flash-exp:free` | openrouter | Medium — fast searches | $$ |
| **aeron** | `google/gemini-2.0-flash-exp:free` | openrouter | Low — simple tasks | $ |

### Model Validation

If the resolved model does not support tool/function calling, the spawn fails immediately with `failed_policy` status and a clear error: "Model X does not support tool use. Sub-agents require models with function calling capability."

If the resolved provider's API key is invalid, the spawn fails with `failed_quota` status. The parent can fall back to its own provider.

### Typical Patterns

- **Deep code review:** `category: researcher, model: anthropic/claude-sonnet-4` — researcher category (read-only policy), but with expensive model for deep reasoning
- **Feature implementation:** `category: crafter` — uses crafter defaults, no override needed
- **Security audit:** `category: researcher, model: openai/o3, policy: read-only` — top model for critical analysis
- **Format codebase:** `category: aeron, name: formatter` — ultra-cheap, tight budget, simple task
- **Write tests:** `category: crafter, provider: groq, model: llama-3.3-70b-versatile` — cheaper provider for test code

---

## 12. Inter-Agent Communication

### Communication Model

Sub-agents communicate through three mechanisms:

1. **Structured Results (upward)** — sub-agent returns `SubAgentResult` to parent. This is the primary communication channel.

2. **Question Tool (upward)** — sub-agent asks questions to the main agent via the `question` tool. The main agent decides whether to answer or escalate to the user.

3. **File Ownership Notifications (upward)** — the system notifies the main agent whenever a sub-agent claims or releases a file.

There is **no peer-to-peer communication** between sub-agents. All communication is parent-mediated.

### `question` Tool for Sub-Agents

```json
{
  "type": "function",
  "function": {
    "name": "question",
    "description": "Ask a question to the orchestrator (main agent). The main agent may answer directly or escalate to the user. Use when you need clarification, approval, or information outside your scope.",
    "parameters": {
      "type": "object",
      "properties": {
        "question": {
          "type": "string",
          "description": "The question to ask."
        },
        "options": {
          "type": "array",
          "items": {"type": "string"},
          "description": "Suggested options for the answer."
        }
      },
      "required": ["question"]
    }
  }
}
```

**Routing:**
- Sub-agent calls `question` → routed to main agent
- Main agent receives the question with sub-agent identity tag
- Main agent decides: answer it (if it knows) or escalate to user
- If escalated to user, TUI shows question tagged with `[researcher:explorer]`
- User's answer flows back to the sub-agent

### `write_report` Tool for Sub-Agents

```json
{
  "type": "function",
  "function": {
    "name": "write_report",
    "description": "Write your final result report. This becomes the sub-agent's official result. The gist (first 2000 chars) enters the parent's context window. The full report is saved to disk and referenced by the result path pointer. If you don't call this, your last non-tool-call message becomes the result.",
    "parameters": {
      "type": "object",
      "properties": {
        "gist": {
          "type": "string",
          "description": "Concise summary of results, max 2000 characters. This enters the parent's context window. Be specific and structured."
        },
        "report": {
          "type": "string",
          "description": "Full detailed report. Saved to disk and referenced by the result path pointer. No size limit."
        }
      },
      "required": ["gist"]
    }
  }
}
```

### Why Not Peer-to-Peer?

1. **Complexity** — P2P requires message routing, serialization, and ordering
2. **Context pollution** — agents sharing messages can corrupt each other's reasoning
3. **Security** — a compromised agent could inject malicious instructions into peers
4. **Observability** — parent-mediated communication means the parent always knows what happened

---

## 13. Permission Escalation to User

### Tagged Blocking Events with Per-Agent Channels

Each sub-agent has its own `blockResolveCh`. When a sub-agent needs user approval, the event is emitted on its own channel:

```go
emit(AgentEvent{
    Type: "blocking",
    Data: map[string]string{
        "operation":       "Execute",
        "tool":            "execute_command",
        "target":          "npm install --save react-hook-form",
        "question":        "Allow this Execute operation?",
        "risk":            "project-write",
        "agent_id":        "fixer-abc123",
        "agent_name":      "fixer",
        "agent_category":  "crafter",
        "agent_task":      "Fix the form validation bug in src/components/Form.tsx",
    },
    AgentID: "fixer-abc123",
})
```

### TUI Display

The TUI shows a blocking dialog with agent identity:

```
╭─ Permission Request ──────────────────────────────────────╮
│ Agent: fixer [crafter]                                      │
│ Task: Fix the form validation bug in src/components/Form.tsx│
│                                                             │
│ Allow this Execute operation?                                │
│ Command: npm install --save react-hook-form                 │
│ Risk: project-write                                          │
│                                                             │
│ [a] Allow once  [A] Always allow  [d] Deny  [D] Always deny│
│ [o] Open agent thread to inspect                            │
╰─────────────────────────────────────────────────────────────╯
```

### "Always Allow" Scoping

When the user presses `A` (always allow), the grant is scoped:

```go
// "Always allow" for THIS agent only (default)
permStore.GrantAlways(op, tool, target, WithAgentID("fixer-abc123"))

// "Always allow" for ALL agents in this session (explicit user choice)
permStore.GrantAlways(op, tool, target, WithSessionScope())
```

"Session scope" means all agents in the current session tree — not global across all Crude sessions.

### Concurrent Agent Approval Handling

When multiple sub-agents are running concurrently:

1. Each sub-agent has its own `blockResolveCh`
2. The TUI shows approval requests from any agent with the agent label
3. User can press `o` to switch to that agent's tab before deciding
4. If the user denies, the sub-agent receives the denial and must work around it
5. If the session is in non-interactive mode (`--yolo` or CI), approval requests auto-fail with a structured error

### Loop Detection and Heavy Usage

- **Loop detection:** If a sub-agent makes the same tool call with the same parameters 3+ times consecutively, the agent is auto-killed with `failed_error` status
- **Heavy but normal usage:** An orange warning icon appears in the TUI message list with a kill button. The user can click the button or navigate to the agent's tab to intervene

---

## 14. Concurrency & Race Conditions

### Auto-Claim File Ownership System

File ownership is claimed automatically on first write, not required upfront:

```go
type FileOwnership struct {
    mu      sync.Mutex
    claims  map[string]ClaimEntry  // file path → claim entry
}

type ClaimEntry struct {
    AgentID   AgentID
    AgentName string
    ClaimedAt time.Time
}

func (fo *FileOwnership) CheckOrClaim(agentID AgentID, agentName string, path string) error {
    fo.mu.Lock()
    defer fo.mu.Unlock()

    for claimedPath, entry := range fo.claims {
        if strings.HasPrefix(path, claimedPath) || strings.HasPrefix(claimedPath, path) {
            if entry.AgentID != agentID {
                return fmt.Errorf("file %q is owned by agent %q (claimed at %s)", path, entry.AgentName, entry.ClaimedAt.Format("15:04:05"))
            }
        }
    }

    // Auto-claim on first write
    fo.claims[path] = ClaimEntry{
        AgentID:   agentID,
        AgentName: agentName,
        ClaimedAt: time.Now(),
    }
    return nil
}

func (fo *FileOwnership) Release(agentID AgentID) {
    fo.mu.Lock()
    defer fo.mu.Unlock()
    for path, entry := range fo.claims {
        if entry.AgentID == agentID {
            delete(fo.claims, path)
        }
    }
}
```

### How It Works

1. **Pre-declared ownership** (optional): Sub-agent spec includes `file_ownership: ["src/api/"]` — claimed at spawn time, fails if conflicting
2. **Auto-claim on first write**: When a sub-agent writes a file, the system automatically claims it
3. **Other agents blocked**: If agent B tries to write a file owned by agent A, the write fails with a clear error identifying the owner
4. **Main agent notified**: The main agent receives `[claim] fixer → src/api/auth.go` and `[release] fixer → src/api/auth.go` events
5. **Auto-release**: When a sub-agent completes, is cancelled, or crashes, all its claims are released via `defer`
6. **Main agent can intervene**: Main agent can cancel an agent to release its claims, or resolve conflicts

### Runtime Stale-Read Detection (Layer 2)

Even without upfront ownership, concurrent writes are caught:

```go
func (e *Editor) Write(path, content string, writerID AgentID) error {
    if conflictingAgent := e.fileOwnership.Query(path); conflictingAgent != "" && conflictingAgent != writerID {
        return fmt.Errorf("file %q is owned by agent %q", path, conflictingAgent)
    }

    // Stale-read check: was this file modified since the writer last read it?
    if lastMod, byWhom := e.lastModifiedBy(path); lastMod.After(e.lastReadBy(writerID, path)) {
        return fmt.Errorf("file %q was modified by agent %q at %s since you last read it. Re-read the file before editing.", path, byWhom, lastMod.Format("15:04:05"))
    }

    // Proceed with write
    // ...
}
```

### Rate Limiting Between Concurrent Sub-Agents

A shared token bucket per provider+API key prevents API rate limit issues:

```go
type RateLimiter struct {
    mu      sync.Mutex
    buckets map[string]*tokenBucket  // "provider:apiKey" → bucket
}

func (rl *RateLimiter) Wait(ctx context.Context, provider string) error {
    bucket := rl.getBucket(provider)
    return bucket.Wait(ctx)
}
```

- When a sub-agent gets a 429 (rate limited), it retries with exponential backoff
- Other agents are unaffected until the shared bucket is empty
- No hard token auto-kills — loop detection handles runaway agents, heavy usage shows user-facing warnings

---

## 15. Sub-Agent Lifecycle & Monitor Pattern

### Session-Scoped Lifecycle

Sub-agents are **session-scoped** — they survive across the main agent's turns. The main agent's turn can end while sub-agents continue running in the background.

### Aeron Monitor Pattern

When the main agent spawns sub-agents and needs to wait for them, it uses the **aeron monitor pattern**:

1. Main agent spawns sub-agents (researchers, crafters, etc.)
2. Main agent spawns an **aeron monitor** (category: aeron, profile: monitor)
3. Main agent enters a suspended/sleeping state — its turn ends but the session stays alive
4. The aeron monitor periodically calls `list_sub_agents` and `poll_sub_agent` to check status
5. When something important happens (completion, error, permission prompt, loop detected), the monitor aeron **escalates** — sends a signal that wakes the main agent
6. The monitor aeron terminates after escalation
7. Main agent handles the incident (reviews results, handles errors, answers questions), then spawns a new monitor aeron before going back to sleep
8. Cycle repeats until all sub-agents complete
9. Main agent aggregates all results and presents the final response to the user

```
Main Agent ─── spawn sub-agents ──→ [researcher:explorer] [crafter:fixer]
         ─── spawn monitor ──────→ [aeron:monitor]
         ─── sleep ─────────────→ (suspended, waiting for escalation)

[aeron:monitor] ─── polls list_sub_agents ──→ sees explorer completed
               ─── escalates ──────────────→ wakes main agent
               ─── terminates ─────────────→ (dead)

Main Agent ─── wakes, reviews explorer result
         ─── spawns new monitor ─────→ [aeron:monitor-2]
         ─── sleep ─────────────────→ (suspended again)
```

### Crash Recovery

Every sub-agent goroutine is wrapped with `recover()`:

```go
func (s *Session) launchSubAgent(ctx context.Context, handle *SubAgentHandle) {
    go func() {
        defer func() {
            // Release file ownership in all cases
            s.fileOwnership.Release(handle.Spec.ID)

            if r := recover(); r != nil {
                stack := debug.Stack()
                // Log stack trace to sub-agent's session log
                logToSessionDB(handle.DB, "panic", string(stack))
                // Convert panic to structured error result
                handle.ResultCh <- SubAgentResult{
                    AgentID:  handle.Spec.ID,
                    Status:   SubAgentFailedError,
                    Errors:   []string{fmt.Sprintf("panic: %v\n%s", r, stack)},
                    LogPath:  handle.SessionID,
                }
            }
        }()

        result := handle.Agent.Run(ctx)
        handle.ResultCh <- result
    }()
}
```

---

## 16. State Machine Integration

### Sub-Agent Goals in the State Tree

When a sub-agent is spawned, a goal is created in the state machine:

```go
goal := sm.CreateGoal(sessionID,
    "sub-agent: "+spec.Name,
    spec.Description,
    statemachine.PriorityNormal,
    false,
)
goal.Metadata = map[string]any{
    "agent_id":     spec.ID,
    "agent_name":   spec.Name,
    "category":     spec.Category,
    "depth":        spec.Depth,
    "model":        spec.Model,
    "session_id":   handle.SessionID,
}
```

When the sub-agent completes:

```go
sm.CompleteGoalByID(sessionID, goal.ID, map[string]any{
    "status":        result.Status,
    "summary_gist":  result.SummaryGist,
    "files_written": result.FilesWritten,
    "cost":          result.CostUSD,
})
```

### Plan Visibility

The parent's `list_goal_status` shows sub-agent progress:

```
Plan: Fix the authentication system (active)
  ✓ step1: Explore auth code paths (completed) [researcher:explorer]
  → step2: Fix the token validation (doing) [crafter:fixer, depth=1]
  ☐ step3: Write auth tests (pending) [crafter:tester, depth=1]
  ◎ monitor: Watching sub-agents (monitoring) [aeron:monitor, depth=1]
```

---

## 17. TUI Integration

### Tab-Based Sub-Agent View

Each sub-agent gets its own tab in the TUI, since each sub-agent is a separate session:

```
┌──────────────────────────────────────────────────────────────┐
│ [main] [explorer] [fixer] [monitor]                           │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  (currently viewing: main tab)                               │
│                                                              │
│  ⚡ 3 agents active: explorer, fixer, monitor                 │
│                                                              │
│  [claim] fixer → src/api/auth.go                             │
│  [researcher:explorer] Reading src/api/auth.go               │
│  [crafter:fixer]      Writing src/api/token.go               │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

### Tab Features

- Each tab shows the sub-agent's live conversation (its own session)
- Sub-agents can call `reply()` — messages appear in their own tab
- Permission prompts from any tab show the agent label with category
- Press `/` to open agent list, select to switch tabs
- Press `q` to return to main tab
- Orange warning icons appear for heavy-usage agents with kill buttons

### Sub-Agent Result Display

When a sub-agent completes, its result is shown inline in the main tab:

```
┌─ Sub-agent Result: researcher:explorer ──────────┐
│ Status: completed (45s, 12k tokens, $0.003)       │
│ Category: researcher                               │
│ Session: explorer-a1b2c3 (click to view log)       │
│                                                   │
│ Found 3 API routes without auth middleware:       │
│  - src/api/users.go:42   GET /users              │
│  - src/api/orders.go:78  POST /orders            │
│  - src/api/admin.go:15   DELETE /admin/settings   │
│                                                   │
│ Files read: 8 | Files written: 0                  │
└───────────────────────────────────────────────────┘
```

---

## 18. Session Database Schema

### Current Schema (extended)

The existing `sessions`, `messages`, and `cost_snapshots` tables are extended. Two new tables are added.

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;

-- Existing sessions table (extended)
CREATE TABLE IF NOT EXISTS sessions (
    id                TEXT PRIMARY KEY,
    project_path      TEXT NOT NULL,
    project_name      TEXT NOT NULL,
    model             TEXT,
    provider          TEXT,
    created_at        INTEGER NOT NULL,
    closed_at         INTEGER,
    message_count     INTEGER DEFAULT 0,
    turn_count        INTEGER DEFAULT 0,
    first_prompt      TEXT,
    last_prompt       TEXT,
    summary           TEXT,
    -- New columns for sub-agent support
    parent_session_id TEXT,          -- NULL for top-level sessions
    agent_category    TEXT,          -- "main" | "researcher" | "crafter" | "aeron"
    agent_name        TEXT,          -- e.g. "explorer", "fixer", "monitor"
    depth             INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_sessions_parent ON sessions(parent_session_id);
CREATE INDEX IF NOT EXISTS idx_sessions_category ON sessions(agent_category);

-- Existing messages table (already has agent_id column)
CREATE TABLE IF NOT EXISTS messages (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    role              TEXT NOT NULL,
    content           TEXT,
    reasoning_content TEXT,
    reasoning         TEXT,
    tool_calls        TEXT,
    tool_name         TEXT,
    tool_result       TEXT,
    tool_call_id      TEXT,
    cache_control     TEXT,
    token_count       INTEGER DEFAULT 0,
    sequence_num      INTEGER NOT NULL,
    turn_number       INTEGER NOT NULL,
    agent_id          TEXT,
    display_meta      TEXT,
    created_at        INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_seq ON messages(sequence_num);
CREATE INDEX IF NOT EXISTS idx_messages_turn ON messages(turn_number, sequence_num);

-- Existing cost_snapshots table
CREATE TABLE IF NOT EXISTS cost_snapshots (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    total_prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    total_completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_cached_tokens     INTEGER NOT NULL DEFAULT 0,
    total_cost              REAL NOT NULL DEFAULT 0.0,
    by_model                TEXT,
    snapshot_at             INTEGER NOT NULL
);

-- NEW: Sub-agent results
CREATE TABLE IF NOT EXISTS sub_agent_results (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    parent_session_id TEXT NOT NULL,
    child_session_id  TEXT NOT NULL,
    agent_id          TEXT NOT NULL,
    agent_name        TEXT NOT NULL,
    agent_category    TEXT NOT NULL,
    status            TEXT NOT NULL,     -- completed/failed_timeout/failed_quota/failed_error/failed_policy/cancelled
    summary_gist      TEXT,             -- limited gist that entered parent's context (max 2000 chars)
    result_path       TEXT,             -- path to full result file
    session_log_path  TEXT,             -- path to sub-agent's session log dir
    files_read        TEXT,             -- JSON array
    files_written     TEXT,             -- JSON array
    errors            TEXT,             -- JSON array
    prompt_tokens     INTEGER DEFAULT 0,
    completion_tokens INTEGER DEFAULT 0,
    cost_usd          REAL DEFAULT 0.0,
    duration_ms       INTEGER DEFAULT 0,
    created_at        INTEGER NOT NULL,
    FOREIGN KEY (parent_session_id) REFERENCES sessions(id),
    FOREIGN KEY (child_session_id) REFERENCES sessions(id)
);

CREATE INDEX IF NOT EXISTS idx_sub_results_parent ON sub_agent_results(parent_session_id);
CREATE INDEX IF NOT EXISTS idx_sub_results_child ON sub_agent_results(child_session_id);

-- NEW: File ownership claims
CREATE TABLE IF NOT EXISTS file_claims (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id      TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    agent_name      TEXT NOT NULL,
    path            TEXT NOT NULL,       -- claimed file/dir path
    claimed_at      INTEGER NOT NULL,
    released_at     INTEGER,
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE INDEX IF NOT EXISTS idx_file_claims_path ON file_claims(path);
CREATE INDEX IF NOT EXISTS idx_file_claims_session ON file_claims(session_id);
```

### Sub-Agent Session DB Lifecycle

1. **Spawn**: Create a new `.db` file for the sub-agent session (e.g., `{sessionID}-sub-{agentID}.db`)
2. **Run**: Sub-agent's messages are written to its own session DB
3. **Complete**: Write `SubAgentResult` row to the parent's `sub_agent_results` table
4. **Persist**: Sub-agent session DB is kept alongside the parent's for audit/replay
5. **Cleanup**: Follows the same retention policy as regular sessions

---

## 19. Cost Tracking

### Tree Aggregation

Costs are aggregated by session tree. A parent's total cost = own cost + sum of all child `sub_agent_results.cost_usd`.

```go
func (s *Session) TotalCost() float64 {
    own := s.costTracker.Total()
    var children float64
    for _, result := range s.subAgentManager.Completed() {
        children += result.CostUSD
    }
    return own + children
}
```

### No Hard Budget Enforcement

There is no hard token or cost auto-kill. Instead:
- **Loop detection** auto-kills agents making the same tool call 3+ times
- **Heavy usage warnings** show an orange icon in TUI with a kill button
- **Per-agent costs** are visible in each sub-agent's tab and in the main agent's status overview
- **The LLM is the orchestrator** — it decides whether cost is justified for the task

---

## 20. Edge Cases & Nuances

### 20.1 Sub-Agent Tries to Spawn at Max Depth

The `run_sub_agent` tool is not in the sub-agent's tool list. If somehow requested:
```
Error: Cannot spawn sub-agents at depth 1 (max_depth=2). You are already at the maximum nesting level.
```

### 20.2 Sub-Agent Exceeds Step Limit

Agent loop stops, returns `SubAgentResult{Status: "failed_timeout"}` with summary explaining step limit.

### 20.3 Sub-Agent Exceeds Time Limit

Context is cancelled. Agent loop detects `ctx.Err()`, returns `SubAgentResult{Status: "failed_timeout"}`.

### 20.4 Sub-Agent's Model Quota Is Exhausted

```json
{
  "status": "failed_quota",
  "errors": ["Rate limit exceeded for provider 'groq' model 'llama-3.3-70b-versatile': 429 Too Many Requests"],
  "summary_gist": "Sub-agent could not complete because its assigned model quota was exhausted."
}
```

Parent can retry with a different model, do the work itself, or report failure.

### 20.5 Sub-Agent Needs Permission Escalation While Parent Is Waiting

Permission prompt is surfaced to the user through the TUI with agent identity tags on the sub-agent's per-agent channel. The parent's `run_sub_agent` call is effectively blocked while the sub-agent waits for approval.

### 20.6 Two Sub-Agents Try to Write to the Same File

If file is owned: second agent's write is blocked with error identifying the owner. If no ownership: stale-read detection catches the conflict — "File was modified by agent X since you last read it. Re-read before editing."

### 20.7 User Interrupts a Running Sub-Agent

Interruption is routed to the main agent, not the sub-agent. The main agent can cancel the sub-agent, wait for it, or redirect. Sub-agents do NOT receive user interruptions directly.

### 20.8 Parent Agent Is Cancelled While Sub-Agents Run

Parent's context cancellation propagates to all sub-agents. Each returns `SubAgentResult{Status: "cancelled"}`. All file ownership claims are released via `defer`.

### 20.9 Sub-Agent Returns Error — Parent Retry Logic

Parent receives `SubAgentResult` with error details. The parent's LLM decides whether to retry (same spec, different model, or do it itself). No automatic retry.

### 20.10 Sub-Agent Modifies a File the Parent Also Modified

Editor's journal records both operations. Stale-read detection fires: "File was modified by main agent at 12:45:03. Re-read the file before editing."

### 20.11 Sub-Agent Calls `reply()`

Since each sub-agent is a separate session with its own tab, `reply()` works normally — the message appears in the sub-agent's own tab. The main agent's tab is unaffected.

### 20.12 Nested Sub-Agent Context Window Budget

Each sub-agent has its own context window. The parent only consumes tokens for:
- Its own conversation + tool calls
- The `SummaryGist` from each sub-agent result (max 2000 chars each)
- File ownership claim/release notifications

### 20.13 Sub-Agent with `policy="read-only"` Tries to Call `write_file`

```
Error: [POLICY BLOCK] Tool 'write_file' is not allowed for this agent.
Your policy is 'read-only'. Focus on analysis and return your findings.
```

If the LLM retries 3 times, loop detection kills the agent.

### 20.14 Sub-Agent Profile Not Found

1. Log a warning about the missing profile
2. Fall back to category defaults + inline parameters
3. The `run_sub_agent` call proceeds with whatever valid parameters are available

### 20.15 Concurrent Sub-Agent Count at Maximum

- If `wait=true`: `run_sub_agent` call blocks until a slot opens
- If `wait=false`: Call returns error: "Maximum concurrent sub-agents (4) reached."

### 20.16 Sub-Agent Goroutine Panics

`recover()` in the goroutine catches the panic. Converts to `SubAgentResult{Status: "failed_error", Errors: [stack trace]}`. Stack trace is logged to the sub-agent's session DB. Other agents and the parent are unaffected.

### 20.17 Model Without Tool Use Support

Spawn fails immediately with `failed_policy`: "Model X does not support tool use. Sub-agents require models with function calling capability."

### 20.18 Invalid Provider API Key

Spawn fails with `failed_quota`. Parent can fall back to its own provider.

### 20.19 Sub-Agent Asks a Question

`question` tool routes the question to the main agent. Main agent decides whether to answer or escalate to user. If escalated, TUI shows the question tagged with sub-agent identity.

### 20.20 Aeron Monitor Escalation

When the monitor aeron detects an event requiring attention (completion, error, permission prompt, loop detection), it calls `question` or `reply()` to wake the main agent. The monitor then terminates. The main agent handles the incident and spawns a new monitor if other sub-agents are still running.

---

## 21. Configuration Schema

```json
{
  "active_provider": "openrouter",
  "providers": {
    "openrouter": {
      "api_key": "",
      "model": "anthropic/claude-sonnet-4",
      "provider_url": "",
      "thinking_effort": ""
    },
    "groq": {
      "api_key": "",
      "model": "llama-3.3-70b-versatile",
      "provider_url": "",
      "thinking_effort": ""
    },
    "wafer": {
      "api_key": "",
      "model": "qwen/qwen3.5-235b-a22b",
      "provider_url": "",
      "thinking_effort": ""
    }
  },
  "turn_logs_enabled": true,
  "agents": {
    "max_concurrent": 4,
    "max_depth": 2,
    "auto_approve_read_only": true,
    "auto_approve_test_commands": true,
    "categories": {
      "main": {
        "description": "Orchestrator — plans, delegates, interacts with user. Highest reasoning tier.",
        "policy": "default",
        "max_steps": 200,
        "timeout_seconds": 600
      },
      "researcher": {
        "model": "google/gemini-2.0-flash-exp:free",
        "provider": "",
        "policy": "read-only",
        "max_steps": 30,
        "timeout_seconds": 120,
        "allowed_tools": [
          "read_file", "glob_files", "grep_files", "list_directory",
          "web_search", "web_fetch", "arxiv_search", "run_javascript",
          "write_report", "question"
        ],
        "denied_tools": [
          "write_file", "edit_file", "delete_file", "move_file", "copy_file",
          "execute_command", "async_execute_command"
        ],
        "description": "Gatherer — explores codebase, searches web, runs experiments. Read-heavy, cheap, fast."
      },
      "crafter": {
        "model": "qwen/qwen3.5-235b-a22b",
        "provider": "",
        "policy": "no-delete",
        "max_steps": 60,
        "timeout_seconds": 300,
        "denied_tools": ["delete_file"],
        "description": "Implementer — writes code, edits files, runs tests. Mid-cost, write-capable."
      },
      "aeron": {
        "model": "google/gemini-2.0-flash-exp:free",
        "provider": "",
        "policy": "read-only",
        "max_steps": 10,
        "timeout_seconds": 60,
        "denied_tools": [
          "delete_file", "move_file", "copy_file",
          "execute_command", "async_execute_command",
          "web_search", "web_fetch", "arxiv_search",
          "run_javascript"
        ],
        "description": "Grunt — formatting, linting, boilerplate, monitoring. Ultra-cheap, ultra-fast."
      }
    },
    "profiles": {
      "explorer": {
        "category": "researcher",
        "description": "Read-only codebase explorer for gathering evidence before changes.",
        "allowed_tools": ["read_file", "glob_files", "grep_files", "list_directory", "web_search", "web_fetch"],
        "instructions": "Stay in exploration mode. Trace execution paths, cite files and symbols. Prefer fast search over broad scans. Never modify any files."
      },
      "reviewer": {
        "category": "researcher",
        "model": "anthropic/claude-sonnet-4",
        "description": "Code reviewer focused on correctness, security, and missing tests.",
        "max_steps": 40,
        "timeout_seconds": 180,
        "instructions": "Review code for correctness, security, and missing tests. Lead with concrete findings. Include reproduction steps when possible."
      },
      "fixer": {
        "category": "crafter",
        "description": "Implementation-focused agent for targeted fixes.",
        "instructions": "Make the smallest defensible change. Keep unrelated files untouched. Run tests after each fix.",
        "denied_tools": ["delete_file"],
        "file_ownership": ["src/", "tests/"]
      },
      "tester": {
        "category": "crafter",
        "provider": "groq",
        "model": "llama-3.3-70b-versatile",
        "description": "Test writer focused on edge cases and integration coverage.",
        "instructions": "Write comprehensive tests. Focus on edge cases and integration coverage. Run the test suite after writing.",
        "denied_tools": ["delete_file", "execute_command"],
        "max_steps": 80,
        "timeout_seconds": 300
      },
      "security-audit": {
        "category": "researcher",
        "model": "anthropic/claude-sonnet-4",
        "description": "Deep security analysis of the codebase.",
        "policy": "read-only",
        "max_steps": 80,
        "timeout_seconds": 300,
        "instructions": "Conduct a thorough security audit. Focus on authentication, authorization, input validation, and secret management. Prioritize OWASP Top 10 vulnerabilities. Provide file:line references for every finding."
      },
      "formatter": {
        "category": "aeron",
        "description": "Quick formatting and linting pass.",
        "max_steps": 8,
        "timeout_seconds": 30,
        "allowed_tools": ["read_file", "write_file", "execute_command", "glob_files"],
        "instructions": "Run the project's formatter and linter. Only fix formatting issues. Do not change logic, structure, or behavior."
      },
      "monitor": {
        "category": "aeron",
        "description": "Watches running sub-agents and escalates important events to the main agent.",
        "max_steps": 200,
        "timeout_seconds": 1800,
        "allowed_tools": ["list_sub_agents", "poll_sub_agent", "question"],
        "instructions": "Monitor running sub-agents. When a sub-agent completes, fails, or needs approval, escalate to the main agent with a summary. Do not modify any files. Keep monitoring until all sub-agents finish."
      }
    }
  }
}
```

### Inheritance Summary

```
Runtime Override (CLI flags)
  └── Inline params (run_sub_agent call)
       └── Profile (config.json agents.profiles)
            └── Category defaults (config.json agents.categories)
                 └── Parent / Session defaults
```

| Field | Runtime | Inline | Profile | Category | Parent |
|-------|---------|--------|---------|----------|--------|
| `category` | — | ✅ overrides | ✅ declares | — | — |
| `model` | ✅ overrides | ✅ overrides | ✅ overrides | ✅ default | ✅ fallback |
| `provider` | ✅ overrides | ✅ overrides | ✅ overrides | ✅ default | ✅ fallback |
| `policy` | ✅ overrides | ✅ overrides | ✅ overrides | ✅ default | ❌ |
| `max_steps` | — | ✅ overrides | ✅ overrides | ✅ default | ❌ |
| `timeout_seconds` | — | ✅ overrides | ✅ overrides | ✅ default | ❌ |
| `allowed_tools` | — | ✅ intersects | ✅ intersects | ✅ baseline | ❌ |
| `denied_tools` | — | ✅ unions | ✅ unions | ✅ baseline | ❌ |
| `instructions` | — | ✅ appends | ✅ provides base | ❌ | ❌ |
| `file_ownership` | — | ✅ overrides | ✅ overrides | ❌ | ❌ |

---

## 22. Testing Strategy

### Approach: Mock + Integration + Race Detector

1. **Interface-based mocking** — mock the LLM provider to return predetermined tool calls and responses. Test the sub-agent lifecycle (spawn, run, complete, fail, cancel) without real API calls.

2. **Integration tests** — test the full stack with a real (but simple) provider. Verify:
   - Sub-agent spawns with correct category defaults
   - Policy composition respects the narrowing rule
   - Tool filtering produces the correct tool set
   - File ownership claims and releases work correctly
   - Rate limiter shares tokens across sub-agents
   - `write_report` produces correct result files

3. **Go race detector** — run all concurrent tests with `-race` flag:
   - Concurrent sub-agent spawning and completion
   - Concurrent file ownership claims from different agents
   - Concurrent permission escalation on different channels
   - Concurrent rate limiter access

4. **Crash recovery tests** — force panics in sub-agent goroutines and verify:
   - `SubAgentResult{failed_error}` is returned
   - File ownership claims are released
   - Other agents are unaffected
   - Stack trace is logged to session DB

---

## 23. Implementation Roadmap

### Step 1: Core Types and Sub-Agent Manager ✅ COMPLETE

- Add `SubAgentSpec`, `SubAgentResult`, `SubAgentManager`, `SubAgentHandle` types
- Add `AgentCategory` type and `CategoryDefaults()` function
- Update `Agent` struct with `AgentID`, `Depth`, `ParentID`
- Implement `SubAgentManager` with spawn/validate/track/cancel

### Step 2: Session DB Schema ✅ COMPLETE

- Add `parent_session_id`, `agent_category`, `agent_name`, `depth` columns to `sessions`
- Create `sub_agent_results` and `file_claims` tables
- Add migration path for existing DBs
- Implement sub-agent session DB creation and lifecycle

### Step 3: Tool Definitions ✅ COMPLETE

- Add `run_sub_agent` tool definition
- Add `poll_sub_agent`, `cancel_sub_agent`, `list_sub_agents` tool definitions
- Add `write_report` tool definition
- Add `question` tool definition (for sub-agents, routes to main agent)

### Step 4: Proxied ToolFn and Policy Enforcement ✅ COMPLETE

- Implement proxied ToolFn that routes sub-agent tool calls through parent session
- Implement `ComposePolicy` with runtime overrides, category, profile, inline, and parent intersection
- Implement `FilterTools` with category baseline + spec intersection/union
- Wire `run_sub_agent` into session's ToolFn
- Implement per-agent `blockResolveCh`

### Step 5: File Ownership and Concurrency ✅ COMPLETE

- Implement auto-claim `FileOwnership` system
- Implement stale-read detection in Editor ⚠ (Layer 2 runtime stale-read check not wired yet)
- Implement file ownership claim/release notifications to main agent
- Implement shared rate limiter per provider+API key with retry/backoff
- Implement loop detection (same tool call 3+ times → auto-kill)

### Step 6: Custom Instructions and Profiles ✅ COMPLETE

- Implement category briefing system prompt construction
- Implement profile lookup from `config.json` `agents.profiles`
- Implement instructions append (inline appended after profile)
- Add `categories` and `profiles` sections to `config.json` schema
- Implement full inheritance chain with runtime overrides at top

### Step 7: Model/Provider Routing ✅ COMPLETE

- Implement model/provider resolution through inheritance chain
- Implement model validation (fail if no tool-use support)
- Implement invalid API key handling (fail with `failed_quota`)
- Implement provider build for sub-agent with resolved values

### Step 8: TUI Integration ✅ COMPLETE

- ~~Add tab-based sub-agent view (one tab per sub-agent session)~~ Implemented as right-side vertical agent tab bar integrated into chat area alongside scrollbar rail. Tabs use UTF8 symbols (●/○ for running, ✓ completed, ✗ failed, ! cancelled, ■ main) themed with existing color palette.
- ~~Show sub-agent category:name in status bar~~ Agent name shown in tab labels via `agentLabels` cache from `sub_agent_started` events
- ~~Show file ownership claim/release events in main tab~~ Implemented via `[category:name] claimed path` messages in main chat
- ~~Show orange warning icons for heavy-usage agents with kill buttons~~ ⚠ Not yet — deferred
- ~~Show sub-agent results inline in main tab~~ Implemented via `formatSubAgentResult()` rendering structured text blocks (status, duration, files read/written, tokens, cost, gist)
- ~~Implement agent list and tab switching~~ Implemented: click agent tab on right rail to swap chat view to that agent's full history. Main tab returns to orchestrator view.

**TUI Architecture details:**
- `tui/widgets/agenttabs.go` — new widget: lightweight vertical tab bar
- `tui/viewmodel/app.go` — `activeAgentView`, `agentLabels`, `agentStatuses`, tab sync
- `tui/viewmodel/view.go` — tab bar rendering on right edge, dynamic message list swap
- `tui/viewmodel/agent_events.go` — handlers for `sub_agent_question`, `file_claim`, `sub_agent_started`, `sub_agent_completed`
- `tui/viewmodel/update.go` — `hitTestAgentTabs()` click handler
- `session/subagent.go` — emits `sub_agent_started` + `sub_agent_completed` lifecycle events; `formatSubAgentResult()` replaces raw JSON

### Step 9: Aeron Monitor Pattern ⚠ NOT STARTED

- Implement monitor aeron profile (built-in) ⚠ Profile defined in config, not wired
- Implement main agent suspend/wake mechanism
- Implement escalation from monitor to main agent
- Implement monitor spawn cycle (monitor → escalate → terminate → new monitor)

### Step 10: Cost Tracking and State Machine ⚠ PARTIAL

- Implement tree-based cost aggregation (parent = own + sum of children) ⚠ Not done
- Add sub-agent goals to state machine ✅ Done
- Show sub-agent progress in `list_goal_status` ✅ Done
- Implement per-agent cost display in TUI ⚠ Not done

### Step 11: Error Handling and Crash Recovery ✅ COMPLETE

- Implement `recover()` in sub-agent goroutine launch
- Implement panic → `SubAgentResult{failed_error}` conversion
- Implement stack trace logging to sub-agent session DB
- Implement graceful degradation for all error paths

### Step 12: Testing ⚠ NOT STARTED

- Write mock-based unit tests for sub-agent lifecycle
- Write integration tests for policy composition and tool filtering
- Write integration tests for file ownership and concurrency
- Run all tests with `-race` flag
- Write crash recovery tests
- Write tests for rate limiter sharing
