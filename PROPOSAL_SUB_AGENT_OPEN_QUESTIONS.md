# Sub-Agent System: Remaining Open Questions & Unresolved Issues

**Last Updated:** 2026-05-19

This document catalogs everything from the [PROPOSAL_SUB_AGENT_SYSTEM.md](./PROPOSAL_SUB_AGENT_SYSTEM.md) that remains unresolved, vague, or unaddressed — beyond the easy surface-level inconsistencies that have already been fixed.

---

## Part 1: Explicit Open Questions (from Section 19)

These 8 questions are acknowledged unknowns in the proposal itself. Each needs a decision before Phase 1 implementation.

### Q1: Blocking Channel Architecture
> *Should sub-agents share the parent's `blockResolveCh` or get their own?*

- **Option A:** Shared channel with agent-id tagging (simpler, user sees all prompts from one stream)
- **Option B:** Per-agent channels, TUI routes each prompt to the correct agent context (cleaner, more complex)
- **Recommendation in doc:** Start with A, migrate to B in Phase 4
- **Status:** ⚠️ Needs confirmation

### Q2: Sub-Agent `reply()` to User
> *Should sub-agents be able to call `reply()` to the user?*

- **Recommendation in doc:** No. Only structured results upward; parent decides what to show.
- **Alternative:** Allow `reply()` but tag it clearly as coming from a sub-agent in the TUI.
- **Status:** ⚠️ Needs confirmation
- **Note:** A `Log` field was added to `SubAgentResult` to accumulate `reply()` content as a middle ground.

### Q3: File Ownership Mandatory or Optional?
> *Should `file_ownership` be mandatory for write-capable sub-agents?*

- **Recommendation in doc:** Optional with strong warning when omitted
- **Alternative:** Require it when `max_concurrent > 1` and policy allows writes
- **Status:** ⚠️ Needs confirmation

### Q4: Model Without Tool Use Support
> *What happens when a sub-agent's model doesn't support tool/function calling?*

- **Recommendation in doc:** Fail immediately with `failed_policy` status: "Model X does not support tool use."
- **Status:** ⚠️ Needs confirmation

### Q5: Sub-Agent Session DB Access
> *Should sub-agents be able to access the session DB?*

- **Recommendation in doc:** No. Only through tools and ToolFn. DB persistence handled by parent session.
- **Status:** ⚠️ Needs confirmation

### Q6: Maximum Total Token Budget
> *Should we add a `max_total_sub_agent_tokens` config to prevent cost runaway?*

- **Recommendation in doc:** Yes, add in Phase 2. Default: 500k total tokens across all sub-agents per turn.
- **Status:** ⚠️ Needs confirmation

### Q7: `run_sub_agent` at Depth < Max Depth
> *Should `run_sub_agent` be available to sub-agents at depth < max_depth?*

- **Recommendation in doc:** Yes, up to `max_depth`. Enables hierarchical delegation.
- **Risk:** Recursive fan-out could consume massive tokens. Mitigated by `max_concurrent` and `max_depth`.
- **Status:** ⚠️ Needs confirmation

### Q8: Invalid Provider API Key
> *How to handle invalid provider API key for a sub-agent?*

- **Recommendation in doc:** Fail with `failed_quota` status; parent can fall back to its own provider.
- **Status:** ⚠️ Needs confirmation

---

## Part 2: Critical Gaps — Blocks Implementation

These are gaps that would prevent a working implementation as specified. They are **not** acknowledged in Section 19.

### GAP-A: Missing Companion Tools for `wait=false`

The `run_sub_agent` tool supports `wait=false` — return immediately with a handle ID for later polling. However:

- **No `poll_sub_agent(handle_id)` tool** is defined. The parent has no way to retrieve results asynchronously.
- **No `get_sub_agent_result(tool_call_id)` tool** is defined.
- **No `cancel_sub_agent(handle_id)` tool** is defined. Edge case 16.8 says the parent can cancel sub-agents, but no tool exists.
- **No `list_sub_agents()` tool** is defined. If 4 agents run with `wait=false`, the parent can't discover which are still running.

**Impact:** The `wait=false` path is unimplementable as specified. The interface is incomplete.

**Needs:**
- Define the companion tool(s) and their signatures
- Define how handle IDs are scoped (session-local? turn-local?)
- Define cancellation semantics (graceful vs kill)

---

### GAP-B: ToolFn Routing Architecture Is Underdefined

Line 743 of the proposal states: *"All tool calls go through the parent's ToolFn."* But a sub-agent has its own `Agent.Run()` loop, its own LLM client, and its own tool-call lifecycle.

**Unanswered questions:**
- How does a sub-agent's tool call get intercepted and routed back through the parent's `ToolFn`?
- Does the sub-agent receive a wrapped `ToolFn` that proxies to the parent session?
- Does the parent session intercept every sub-agent tool invocation at the session layer?
- How does the parent's `PermissionStore` get consulted *before* the sub-agent's tool call executes?
- What happens when the sub-agent's tool call fails at the parent level? Does the error propagate back to the sub-agent's LLM?

**Impact:** The central architectural mechanism (permission enforcement for sub-agents) is hand-waved. The current codebase has `session.ToolFn` wired to the main agent — there's no sub-agent routing path.

**Needs:**
- Clear architecture diagram showing the call path: Sub-agent LLM → sub-agent Agent.Run() → ?? → session layer → ToolFn → safety check → execution
- Decision on whether sub-agents share the parent session's ToolFn or get a proxied variant

---

### GAP-C: Rate Limiting Between Concurrent Sub-Agents

If 4 concurrent sub-agents all use the same provider (e.g., `openrouter` with `gemini-flash`), they share the same API key.

**Unanswered questions:**
- Is there a shared rate limiter across sub-agents, or does each one throttle independently?
- When a sub-agent gets a 429 (rate limited), does it retry? With backoff? Does it fail immediately?
- What if one sub-agent exhausts the provider's rate limit, starving other sub-agents?
- Is there a per-provider token bucket shared across all sub-agents?

**Impact:** Uncontrolled concurrent API calls to the same provider will hit rate limits in practice. Users will see flaky sub-agent failures.

**Needs:**
- Decision on rate limiting strategy (shared token bucket? per-agent throttle? queue?)
- Retry/backoff policy for rate-limited sub-agents
- Consideration of provider-level vs API-key-level rate limits

---

### GAP-D: Sub-Agent Lifecycle Across Turns

The session model is currently: one user message → one `Session.Run()` → one `Agent.Run()` → response. Sub-agents break this model.

**Unanswered questions:**
- When the main agent finishes its turn and the user sends a new message, do running sub-agents keep running?
- Are they paused and resumed on the next turn?
- Are they cancelled automatically?
- If sub-agents survive across turns, how does the state machine track them persistently?
- Can a sub-agent outlive the main agent's turn and deliver results mid-conversation?

**Impact:** No decision here means the fundamental lifecycle is undefined. Different choices lead to radically different architectures (synchronous turn-gated vs async background agents).

**Needs:**
- Decision on lifecycle model (turn-scoped vs session-scoped)
- If cross-turn: persistence strategy, re-attachment mechanism, TUI implications

---

### GAP-E: Large Result Handling / Context Pollution

Sub-agent results flow back to the parent as structured data (`SubAgentResult.Summary`). But if a researcher reads 20 files and produces a 15,000-token summary, it **pollutes the parent's context window** — the very thing context isolation was meant to prevent.

**Unanswered questions:**
- Is there a maximum size for `Summary`? What happens if exceeded (truncate? fail? warn?)
- Is there a maximum for `FilesRead` / `FilesWritten` / `Errors` lists?
- Who is responsible for keeping the summary concise — the sub-agent (via prompt) or the system (via truncation)?
- Should large results be stored in the session DB and referenced by key in the parent's context?

**Impact:** Without limits, sub-agents can silently consume the parent's context budget, defeating the purpose of context isolation.

**Needs:**
- Define size limits for each field of `SubAgentResult`
- Define truncation/storage strategy
- Consider a `result_size` budget in the sub-agent's system prompt

---

### GAP-F: Sub-Agent Crash / Panic Recovery

Each sub-agent runs in its own goroutine with a `ResultCh` for delivering results.

**Unanswered questions:**
- What if a sub-agent's goroutine panics (nil pointer, unexpected error in tool call)?
- Is there a `recover()` in the launch path? Does it send an error result or silently die?
- If the sub-agent's `Agent.Run()` returns an unexpected error (not a `SubAgentResult`), what happens?
- Can a panicking sub-agent take down other sub-agents or the parent session?

**Impact:** An unprotected goroutine panic in one sub-agent can crash the entire session, losing all work.

**Needs:**
- Panic recovery in the sub-agent launch goroutine
- Graceful error-to-`SubAgentResult` conversion for unexpected failures

---

## Part 3: Notable Gaps — Should Resolve Before Phase 1

These are less critical but will cause pain if not addressed early.

### GAP-1: Parent Runtime Overrides Not Re-Applied

Section 3 (Competitive Landscape) explicitly cites Codex's *"Parent runtime overrides re-applied to children"* as a feature to adopt — e.g., if user sets `--yolo` interactively, children inherit it even if their config says otherwise. But this pattern appears **nowhere** in the inheritance chain, `ComposePolicy`, or `FilterTools`.

**Needs:** Define how runtime-level overrides (CLI flags, interactive settings) propagate to sub-agents and where they slot into the inheritance chain.

---

### GAP-2: Session DB Persistence Schema

Phase 4 step 5 says *"Session DB persistence of sub-agent results"* but provides:
- No table schema
- No query interface
- No relationship to the state machine
- No retention policy

**Needs:** At minimum, a sketch of the persistence model before Phase 4 (to avoid designing the DB schema in isolation from the sub-agent architecture).

---

### GAP-3: Cost Tracking Architecture

`CostUSD` and `cost_usd` appear everywhere but there's no architecture for:
- How costs are aggregated across concurrent sub-agents
- Whether costs are tracked per-session, per-turn, or per-agent-tree
- How cost limits are enforced (kill sub-agent when budget exceeded?)
- How costs from different providers with different pricing models are normalized

**Needs:** Cost aggregation model and budget enforcement strategy.

---

### GAP-4: Sub-Agent Memory / State Reuse

Each sub-agent spawn is a fresh, stateless context. But what if the parent wants to reuse a sub-agent's accumulated knowledge across calls? The "blackboard" is Phase 2 and vaguely specified.

**Needs:** At least a note on whether Phase 1 should consider any state-sharing mechanism (even a simple one like passing accumulated context via instructions).

---

### GAP-5: Graceful Degradation for Tool Support

What if the sub-agent's resolved model doesn't support `run_javascript` (some models don't)? Or doesn't support parallel tool calls? Or has a very low max output limit?

**Needs:** Feature detection or graceful degradation strategy for model-specific tool limitations.

---

### GAP-6: Testing Strategy for Concurrency

Phase 2 mentions "comprehensive safety tests" but gives no approach for testing:
- Deterministic testing of concurrent sub-agent behavior
- Race condition detection in file ownership claims
- Simulating rate limits, timeouts, and quota exhaustion
- Permission boundary enforcement under concurrent access

**Needs:** High-level testing strategy — at least whether to use mocks, integration tests, or property-based testing for concurrent scenarios.

---

### GAP-7: Data Isolation Between Sub-Agents

All sub-agents share the same filesystem. If `fixer-A` writes a file containing an API key, `explorer-B` can read it. No sandboxing or data isolation is discussed beyond file ownership (which only prevents concurrent writes, not reads).

**Needs:** Decision on whether data isolation between sub-agents is a requirement. If yes: how to achieve it without full sandboxing.

---

## Part 4: Quick Design Decisions Needed

Smaller decisions that should be made early to avoid ambiguity:

| # | Question | Options |
|---|----------|---------|
| D1 | Profile instructions vs inline instructions — replace or merge? | Replace / Append / Merge with delimiters |
| D2 | What does "ALL agents" mean in "Always allow for ALL agents"? | All sub-agents of this parent / All agents in session / All agents globally |
| D3 | When exactly does file ownership release? | On completion / On cancellation / On crash / All of the above (defer) |
| D4 | Are sub-agents turn-scoped or session-scoped? | Turn (auto-cancelled) / Session (survive across turns) |
| D5 | `:free` suffix in model IDs — stay or strip? | Keep (OpenRouter-specific) / Strip (provider-agnostic config) |
| D6 | TOML profiles AND JSON config profiles — merge or pick one? | Merge (TOML wins) / Merge (JSON wins) / Support only one format |

---

*This document should be reviewed alongside the proposal itself. Each item here needs a decision (or explicit deferral) before the corresponding implementation phase.*
