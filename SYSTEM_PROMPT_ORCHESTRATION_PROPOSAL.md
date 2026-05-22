# System Prompt Redesign for True Agent Orchestration

**Date:** 2026-05-22
**Status:** Proposal
**Based on:** Analysis of Claude Code (Explore/Plan/Agent-Task), OpenAI Codex Subagents, AutoGen 0.4, and CrewAI patterns

---

## 1. The Problem: Why Current Prompt Fails at Orchestration

The current system prompt (agent/prompt.go) has a fundamental structural problem:

```
You are Crude — a terminal-based AI coding assistant.
    ↓
[30 sections about tool discipline]
    ↓
[Multi-Agent System section buried at the bottom]
    ↓
"USE IT ACTIVELY" (last-minute plea)
```

**Root cause:** The multi-agent orchestration identity is appended as an afterthought rather than being the **primary identity**. The agent defaults to "I am a coder who does everything myself" because that's the first thing it reads.

### Evidence from our session
When I was told to research similar systems and propose improvements, I started reading files and doing web searches **myself** instead of spawning parallel researcher sub-agents. The prompt says "delegate" but doesn't encode it as a hard behavioral rule.

### Comparison: Claude Code's approach
Claude Code's system prompt:
- The `Agent` (sub-agent spawning) tool is listed among the **primary tools** with detailed `whenToUse` examples
- The main prompt includes **delegation examples** showing when to delegate vs. when to do work directly
- Sub-agent prompts are **tight and focused** (Explore is ~575 tokens, not a full system prompt dump)
- The tool usage guidance explicitly says: *"avoid duplicating work that sub-agents are already doing - if you delegate research to a sub-agent, do not also perform the same searches yourself"*

---

## 2. What We Can Learn from Each System

| System | Key Insight | Apply to Crude |
|--------|-------------|----------------|
| **Claude Code Explore** | Sub-agent prompts are tight (~575 tokens), role-specific, no unnecessary baggage | Sub-agents don't need the full tool guide or file editing rules |
| **Claude Code Agent Tool** | Tool description includes `whenToUse` with concrete delegation examples | Embed delegation guidance INTO the tool description itself |
| **Claude Code Delegation Examples** | Shows exact patterns: "fork survey questions", "get second opinion", "don't duplicate work" | Add worked examples directly in system prompt |
| **OpenAI Codex Subagents** | Sandbox modes map to categories; parent runtime overrides re-apply to children | Policy inheritance must be explicit in prompt |
| **AutoGen 0.4** | Event-driven, supervisor pattern, structured results | Sub-agent results are structured events, not raw text |
| **CrewAI** | Role-based agents with tool scoping | Category system already exists, but prompt must reinforce it |

---

## 3. Proposed System Prompt Restructuring

### 3.1 New Identity: "Orchestrator First, Coder Second"

The opening line should establish the orchestration identity:

```diff
- You are Crude — a terminal-based AI coding assistant.
+ You are Crude — a multi-agent orchestrator that delegates work to specialized sub-agents.
+ CRUDE stands for Create, Read, Update, Delete, Execute — the five core operations
+ your sub-agents will perform under your coordination.
```

### 3.2 New Section Structure

```
1. IDENTITY — "You are an orchestrator" (moved to top, replaces flat identity)
2. ORCHESTRATION PRINCIPLES — "Delegate, don't do it yourself" (was buried at bottom)
3. WHEN TO DELEGATE — Decision framework with examples (new)
4. SUB-AGENT CATEGORIES — Researcher, Crafter, Aeron (exists, needs tightening)
5. PARALLEL EXECUTION PATTERNS — With worked examples (rewrite)
6. TOOL SELECTION GUIDE — Tools organized by orchestration role (rewrite)
7. OPERATIONAL DISCIPLINE — Read before write, etc. (moved down, compressed)
8. SUB-AGENT RULES — Lifecycle, results, ownership (tightened)
9. ENVIRONMENT — Dynamic section (unchanged)
```

### 3.3 The "Hard Rule" on Delegation

Add a non-negotiable rule early in the prompt:

```markdown
## Hard Rule: Delegate First

When a task involves ANY of these:
- Exploring multiple areas of the codebase
- Gathering information from files, web, or docs
- Writing code in a file you haven't recently read
- Running tests or commands
- Any work that can be parallelized

You MUST spawn a sub-agent rather than doing it yourself.
Do NOT read files yourself if a researcher can do it.
Do NOT write code yourself if a crafter can do it.

The ONLY exception: tasks requiring 1-2 trivial tool calls that would
take more tokens to describe than to execute.
```

### 3.4 Delegation Decision Tree (New Section)

Replace the vague "when to use" list with a concrete decision tree:

```markdown
## Delegation Decision Framework

Before touching ANY tool, ask: "Should I delegate this?"

┌─ Is this a 1-2 step trivial op? ────── Yes → Do it yourself
└─ No
   └─ Can the task be described in a single paragraph? ── No → Do it yourself
   └─ Yes
      ├─ Is it read-only exploration? ── Yes → Spawn a researcher
      ├─ Is it writing/modifying code? ── Yes → Spawn a crafter
      ├─ Is it formatting/linting/grunt? ── Yes → Spawn an aeron
      └─ Multiple independent pieces? ── Yes → Spawn 2+ sub-agents in parallel
```

### 3.5 Tool Description with Embedded Delegation Guidance

The `run_sub_agent` tool description should include **usage examples**, following Claude Code's pattern:

```diff
- "Spawn a specialized sub-agent... Use for: parallel codebase exploration..."
+ "Spawn a specialized sub-agent to handle a well-defined subtask. The sub-agent gets
+ its own context window, tool set, permission policy, session, and TUI tab.
+
+ USE THIS TOOL FIRST — before doing work yourself. Check the Delegation Decision
+ Framework above.
+
+ Examples:
+ - 'Find all API routes that lack auth middleware' → spawn researcher
+ - 'Refactor the auth module based on these findings' → spawn crafter
+ - 'Run gofmt on the entire codebase' → spawn aeron
+ - User asks about two unrelated features → spawn parallel researchers
+
+ When using wait=false, you stay free to work on other things. Spawn multiple
+ sub-agents in a single message for true parallelism."
```

### 3.6 Sub-Agent Prompt Compression (BuildSubAgentPrompt)

Currently, `BuildSubAgentPrompt` passes the ENTIRE `SystemPromptBase` (30+ sections) to every sub-agent. This is wasteful. Claude Code's Explore sub-agent prompt is only ~575 tokens.

**Proposed sub-agent prompt structure:**

```
[100t] Identity: "You are a {category} sub-agent named '{name}'."
[50t]  Task: {description}
[100t] Category Briefing: role-specific guidance
[50t]  Policy Constraints: allowed/denied tools
[50t]  Environment: date, CWD, git branch (minimal)
[75t]  Result Format: write_report, gist rules
[50t]  Depth Limit: if applicable
───────
~475t total (vs. ~2500t+ currently)
```

The sub-agent does NOT need:
- File editing rules (it's role-dependent, covered by category briefing)
- Command safety rules (policy-enforced at infrastructure level)
- Web fetching guide (it has the tool, it can figure it out)
- Hidden directories rules (it can look up tool params)
- User interruption rules (sub-agents don't receive user interruptions)
- The entire Tool Selection Guide (tools are filtered by policy)

### 3.7 Parallel Execution: Multi-Spawn in One Message

Add explicit instruction for true parallelism:

```markdown
## True Parallelism

To run sub-agents truly in parallel, send ALL run_sub_agent calls in a SINGLE message.
The system executes them concurrently. This is different from sequential spawning.

✅ CORRECT:
  run_sub_agent(name="explorer-a", ...)
  run_sub_agent(name="explorer-b", ...)
  [wait for both to complete]

❌ WRONG:
  run_sub_agent(name="explorer-a", ...)  → waits
  run_sub_agent(name="explorer-b", ...)  → sequential, wastes time
```

---

## 4. Concrete Changes to agent/prompt.go

### 4.1 Rewrite SystemPromptBase (lines 13-250)

Replace the opening identity and restructure sections. The new order:

1. **Lines 1-80:** Identity + Orchestration Principles + Hard Rule
2. **Lines 81-120:** Delegation Decision Framework + Parallel Patterns
3. **Lines 121-180:** Operational Discipline (compressed from current)
4. **Lines 181-250:** Tool Selection Guide (organized by orchestration role)
5. **Lines 251-270:** Sub-Agent Rules

### 4.2 Rewrite BuildSubAgentPrompt (lines 432-540)

Replace full `SystemPromptBase` injection with a compressed, role-specific prompt:

```go
func BuildSubAgentPrompt(spec SubAgentSpec, profileInstructions string) string {
    // Build a ~400-600 token prompt instead of dumping the full SystemPromptBase
    parts := []string{
        fmt.Sprintf("You are a %s sub-agent named '%s'.", spec.Category, spec.Name),
        fmt.Sprintf("Your task: %s", spec.Description),
        "",
        categoryBriefing(spec.Category),
    }
    // ... policy constraints, env, result format
    return strings.Join(parts, "\n")
}
```

### 4.3 Add delegation examples to `run_sub_agent` tool description (definitions.go)

Add concrete usage examples to the tool description so the LLM sees them in the tool schema itself.

---

## 5. Behavioral Changes Expected

| Before | After |
|--------|-------|
| Agent reads files itself first, delegates as an afterthought | Agent delegates first by default |
| Sub-agent prompt is ~2500+ tokens (full dump) | Sub-agent prompt is ~400-600 tokens (tight) |
| Tool description says "use for: parallel exploration" | Tool description says "USE THIS FIRST" with examples |
| Delegation guidance is one section at the bottom | Orchestration identity is the opening line |
| Vague "when to use" list | Concrete decision tree with yes/no branches |
| Sequential spawning pattern shown | Parallel multi-spawn pattern shown |
| "Do not use sub-agents when task is trivial" | "Do it yourself only if 1-2 trivial steps" (inverted default) |

---

## 6. Implementation Plan

### Phase 1: Prompt Restructure (minimal code changes)

1. Rewrite `SystemPromptBase` opening identity
2. Move Multi-Agent section to top, rename to "Orchestration Principles"
3. Add "Hard Rule: Delegate First" section
4. Add Decision Framework section
5. Compress Operational Discipline sections

### Phase 2: Sub-Agent Prompt Compression

6. Rewrite `BuildSubAgentPrompt` to NOT inject `SystemPromptBase`
7. Create a compact `SubAgentPromptBase` (~200 tokens) with just identity + rules
8. Add category-specific briefings inline

### Phase 3: Tool Description Enhancement

9. Add delegation examples to `run_sub_agent` tool description
10. Ensure `whenToUse` style guidance is embedded in tool parameter descriptions

### Phase 4: Behavioral Training (system reminder)

11. Add a **system reminder** that fires on every turn reminding the agent to delegate:
    ```
    [System Reminder] You are an orchestrator. Before making any tool call,
    ask: "Should I delegate this instead?" Check the Decision Framework.
    ```
    This follows Claude Code's pattern of having ~40 system reminders that repeat key behaviors.

---

## 7. Open Questions

1. **How aggressive should the "delegate first" rule be?** Too aggressive → agent delegates trivial 1-step tasks, adding latency and token cost. Too lenient → back to current behavior. Recommend: add a token-cost awareness statement.

2. **Should the sub-agent prompt categories be further differentiated?** Currently Researcher/Crafter/Aeron briefings are ~3-4 lines each. Consider expanding to ~8-10 lines per category with execution guidelines.

3. **System reminders vs. prompt bloat?** Claude Code uses ~40 system reminders (~2000 tokens total). Crude could add 3-5 key reminders focused on delegation without significant bloat.

4. **Should we add a `--aggressive-delegate` CLI flag?** For users who want maximum delegation even for small tasks (e.g., during complex refactoring sessions).

---

## 8. Summary of Key Changes

| Change | File | Impact |
|--------|------|--------|
| New opening identity | agent/prompt.go:13 | Low (text change) |
| Orchestration Principles section | agent/prompt.go (new) | Low (restructure) |
| Hard Rule: Delegate First | agent/prompt.go (new) | Low (text change) |
| Decision Framework | agent/prompt.go (new) | Low (text change) |
| Parallel multi-spawn pattern | agent/prompt.go (rewrite) | Low (text change) |
| Sub-agent prompt compression | agent/prompt.go:432-540 | **Medium** (logic change) |
| Tool description examples | tools/definitions.go:580 | Low (text change) |
| System reminders | agent/prompt.go (new) | Low (add to prompt) |
