# Crude Sub-Agent System: Comprehensive Design Proposal

**Author:** Crude Architect  
**Date:** 2026-05-19  
**Status:** Draft for Review

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [Current Architecture Analysis](#2-current-architecture-analysis)
3. [Competitive Landscape](#3-competitive-landscape)
4. [Community Pain Points](#4-community-pain-points)
5. [Design Principles](#5-design-principles)
6. [Core Architecture](#6-core-architecture)
7. [The `run_sub_agent` Tool](#7-the-run_sub_agent-tool)
8. [Custom Instructions System](#8-custom-instructions-system)
9. [Permission & Safety Model](#9-permission--safety-model)
10. [Model/Provider Routing](#10-modelprovider-routing)
11. [Inter-Agent Communication](#11-inter-agent-communication)
12. [Permission Escalation to User](#12-permission-escalation-to-user)
13. [Concurrency & Race Conditions](#13-concurrency--race-conditions)
14. [State Machine Integration](#14-state-machine-integration)
15. [TUI Integration](#15-tui-integration)
16. [Edge Cases & Nuances](#16-edge-cases--nuances)
17. [Configuration Schema](#17-configuration-schema)
18. [Implementation Roadmap](#18-implementation-roadmap)
19. [Open Questions](#19-open-questions)

---

## 1. Executive Summary

Crude currently operates as a single-agent system: one LLM loop, one set of tools, one context window, one permission profile. This proposal introduces a **sub-agent system** that allows Crude's main agent to spawn specialized child agents with scoped permissions, custom instructions, configurable model/provider, and isolated context windows.

The design is informed by deep analysis of OpenAI Codex subagents, Claude Code's Task tool, AutoGen 0.4's event-driven architecture, CrewAI's role-based agents, and LangGraph's supervisor pattern. It also incorporates lessons from academic research on agent privilege escalation (arXiv 2601.11893, 2504.11703), permission architecture (One Horizon), and community-identified pain points around approval fatigue, merge conflicts, and cross-agent config poisoning.

**Key design goals:**
- Sub-agents get **isolated context windows** — no context pollution between agents
- **Custom instructions** injected into sub-agent prompts to specialize behavior
- **Scoped permission policies** — read-only, no-destructive-tools, custom tool subsets
- **Per-agent model/provider routing** — cheap models for exploration, powerful models for implementation
- **Structured inter-agent communication** — results flow up, not sideways
- **Permission escalation** that preserves user context about *which* agent is asking
- **Depth and concurrency limits** to prevent runaway spawning
- **Race condition prevention** through file ownership claims and sequential commit zones

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
| `Agent.Run()` | Sequential tool-use loop with `blockResolveCh` for permission prompts | Sub-agents need their own `Run()` loops but must share the parent's `blockResolveCh` for escalation |
| `tools.AgentTools` | Flat list of 20+ tools exposed to every agent | Must be filterable per sub-agent (tool subsets) |
| `safety.Policy` | `DefaultPolicy()` (allow all) or `ReadOnlyPolicy()` | Must be composable per sub-agent with deny lists |
| `safety.PermissionStore` | Global per-session, shared across all operations | Must be scoped: sub-agent permissions ⊆ parent permissions |
| `statemachine.ConversationStateMachine` | Single state tree per session | Sub-agent plans should be visible in the parent's state tree |
| `fileio.Editor` | Journal-based, single writer | Multiple concurrent writers need file ownership / locking |
| `session.Config` | Single `ActiveProvider` + `Providers` map | Must support per-agent model/provider overrides |

### What Needs to Change

1. **Agent struct** needs a `ParentID`, `Depth`, `AgentID`, and a `ToolFilter` 
2. **ToolFn** needs awareness of which agent is calling (for permission scoping)
3. **A new `run_sub_agent` tool** must be added to `AgentTools`
4. **Session** needs a `SubAgentManager` to track active sub-agents
5. **Permission escalation** must tag which agent caused the prompt
6. **Editor** needs file-ownership tracking to prevent concurrent write conflicts
7. **Config** needs per-agent-type model/provider settings

---

## 3. Competitive Landscape

### OpenAI Codex Subagents (GA, March 2026)

**Architecture:** Manager → Worker pattern. Manager decomposes tasks, spawns workers in isolated cloud sandboxes. Workers execute autonomously and report results back. No direct worker-to-worker communication.

**Key features we should adopt:**
- **TOML-based custom agent definitions** (`.codex/agents/*.toml`) with `name`, `description`, `developer_instructions`, `model`, `model_reasoning_effort`, `sandbox_mode`
- **Sandbox modes:** `read-only`, `workspace-write`, `full-access` — maps directly to Crude's `Policy` system
- **`max_threads=6`, `max_depth=1`** — concurrency and depth limits with sensible defaults
- **Parent runtime overrides re-applied to children** — if user set `--yolo` interactively, children inherit it even if their config says otherwise
- **Approval requests surface from inactive threads** with thread label shown to user
- **CSV fan-out** for batch processing (advanced, later phase)

**Key issues to avoid:**
- Sub-agent quota exhaustion surfaces as "no response" instead of structured error
- No direct agent-to-agent communication (only manager-mediated)
- Merge conflicts when workers touch same files

### Claude Code Task Tool

**Architecture:** Main agent calls `Task` tool, which spawns a child agent with its own context window. Child runs autonomously and returns a text result. Depth=1 hardcoded — sub-agents cannot spawn further sub-agents.

**Key features we should adopt:**
- **Context isolation** — child gets a clean context window, preventing context pollution/rot
- **Custom `description` parameter** — parent specifies what the sub-agent should do
- **Built-in agent profiles:** `default`, `worker` (execution), `explorer` (read-heavy)

**Key issues to avoid:**
- Depth=1 too restrictive for hierarchical orchestration (GitHub issue #16003 requests configurable depth)
- Sub-agents lose context tracking when delegating asynchronously (issue #5483)
- Task tool not available inside sub-agents (issue #4182, #5528)
- Permission prompts from inactive threads break context flow

### AutoGen 0.4

**Architecture:** Event-driven, message-passing between agents. Each agent is a process with its own state. Agents communicate exclusively through messages.

**Key features we should adopt:**
- **Message-based communication** — agents send/receive structured messages, not raw text
- **Supervisor patterns** — built-in orchestration strategies (parallel, sequential, fan-out/fan-in)
- **Observable** — telemetry events for monitoring, token usage, errors

**Key issues to avoid:**
- Complexity overhead — too abstract for a CLI tool
- Framework lock-in — keep it simple, Go-native

### CrewAI

**Architecture:** Role-based agents with tools, delegation, and memory. Supports 5 architectural patterns: sequential, hierarchical, parallel, async, and event-driven.

**Key features we should adopt:**
- **Agent roles with tool scoping** — each agent has a defined role and allowed tools
- **Delegation** — agents can delegate to other agents within constraints
- **Memory** — agents can share knowledge through a shared memory store

**Key issues to avoid:**
- LangChain dependency bloat
- Over-abstraction of simple patterns

### Devin / Security Lessons

**Critical lessons from privilege escalation incidents:**
- Devin ran `chmod +x` on blocked binaries without user approval (Johann Rehberger, $500 test)
- Irregular Lab agents forged admin cookies, disabled Windows Defender
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
- When sub-agents delegate asynchronously, the parent loses track of what happened (Claude Code #5483)
- Context isolation is good for preventing pollution but bad for result aggregation

### Merge Conflicts Between Parallel Agents
- Multiple sub-agents working on the same repo produce PRs that merge cleanly but break behavior
- "Features disappear. Bugs appear. And because no human wrote the conflicting code, no human instinctively notices what's missing." — Autonoma analysis
- The problem is *emergent and relational* — it lives between agents, not inside any one

### Error Propagation
- Codex sub-agent quota exhaustion surfaces as "no response" (Codex #16891) — main agent can't distinguish quota exhaustion from timeout from empty result
- Need structured failure states: `completed`, `failed_quota`, `failed_timeout`, `failed_internal`, `empty_result`

### Nested Depth Limitations
- Claude Code enforces depth=1, preventing hierarchical patterns like: `orchestrator → child-director → workers`
- Users want configurable depth with safety limits (Claude Code #16003)

### Cross-Agent Config Poisoning
- Compromised agent writes to another agent's config files (`.mcp.json`, `CLAUDE.md`, `.vscode/tasks.json`)
- Creates reciprocal escalation loops
- Filesystem isolation between agent config spaces is critical

---

## 5. Design Principles

1. **Sub-agents are CRUDE operations** — they follow the Create/Read/Update/Delete/Execute philosophy. A sub-agent is a scoped execution context, not a conversation.

2. **Permission narrows, never widens** — a sub-agent's permission set is always a subset of its parent's. A read-only parent cannot spawn a write-capable child.

3. **Context is isolated by default** — each sub-agent gets a clean context window. The parent injects a task description and custom instructions. The sub-agent's conversation history does NOT flow back into the parent's context. Only the **structured result** flows back.

4. **Results are structured** — sub-agents return a `SubAgentResult` struct, not raw text. This includes status, summary, files modified, errors, and token usage.

5. **Depth is limited, concurrency is limited** — configurable `max_depth` (default 2) and `max_concurrent` (default 4) prevent runaway spawning.

6. **Permission escalation preserves agent identity** — when a sub-agent needs user approval, the prompt clearly shows *which agent* is asking and *what scope* it's requesting.

7. **File ownership prevents conflicts** — sub-agents claim file ownership ranges. Parallel writes to the same file require explicit coordination or sequential execution.

8. **Out-of-process policy enforcement** — safety checks run in the parent/session layer, not in the agent itself. A sub-agent cannot modify its own permission policy.

---

## 6. Core Architecture

### Agent Hierarchy

```
┌─────────────────────────────────────────────────────┐
│  Session                                             │
│  ┌─────────────────────────────────────────────┐    │
│  │  Main Agent (depth=0)                        │    │
│  │  Provider: openrouter, Model: claude-sonnet   │    │
│  │  Policy: Default (all tools)                  │    │
│  │  ToolFn: [all tools including run_sub_agent]  │    │
│  │                                               │    │
│  │  ┌──────────────────┐ ┌──────────────────┐   │    │
│  │  │ Explorer Agent    │ │ Fixer Agent      │   │    │
│  │  │ (depth=1)        │ │ (depth=1)        │   │    │
│  │  │ Model: flash     │ │ Model: sonnet    │   │    │
│  │  │ Policy: ReadOnly │ │ Policy: NoDelete │   │    │
│  │  │ Tools: read-only │ │ Tools: read+edit │   │    │
│  │  └──────────────────┘ └──────────────────┘   │    │
│  └─────────────────────────────────────────────┘    │
│                                                      │
│  SubAgentManager: tracks active, completed, failed   │
│  PermissionStore: global + per-agent scopes          │
│  Editor: file-ownership-aware journal                │
│  StateMachine: sub-agent goals visible in tree       │
└─────────────────────────────────────────────────────┘
```

### New Types

```go
// agent/subagent.go

type AgentID string

type SubAgentSpec struct {
    ID              AgentID          `json:"id"`
    Name            string           `json:"name"`
    Description     string           `json:"description"`
    Instructions    string           `json:"instructions"`     // Custom instructions injected into prompt
    Model           string           `json:"model"`            // Override model (empty = inherit parent)
    Provider        string           `json:"provider"`         // Override provider key (empty = inherit)
    Policy          safety.Policy    `json:"policy"`           // Scoped permission policy
    AllowedTools    []string         `json:"allowed_tools"`    // Tool whitelist (empty = all non-destructive)
    DeniedTools     []string         `json:"denied_tools"`     // Tool blacklist
    MaxSteps        int              `json:"max_steps"`        // Max tool-use steps (default 50)
    Depth           int              `json:"depth"`            // Current nesting depth
    FileOwnership   []string         `json:"file_ownership"`   // Paths this agent owns (prevent conflicts)
    ParentID        AgentID          `json:"parent_id"`
    TimeoutSeconds  int              `json:"timeout_seconds"`  // Max runtime (default 300)
}

type SubAgentResult struct {
    AgentID      AgentID          `json:"agent_id"`
    Name         string           `json:"name"`
    Status       SubAgentStatus   `json:"status"`          // completed, failed_timeout, failed_quota, failed_error, failed_policy, cancelled
    Summary      string           `json:"summary"`         // Structured result summary
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
    SubAgentCancelled      SubAgentStatus = "cancelled"
)

type SubAgentManager struct {
    mu            sync.Mutex
    active        map[AgentID]*SubAgentHandle
    completed     []SubAgentResult
    maxConcurrent int
    maxDepth      int
}

type SubAgentHandle struct {
    Spec     SubAgentSpec
    Agent    *Agent
    Cancel   context.CancelFunc
    ResultCh chan SubAgentResult
    StartedAt time.Time
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
    "description": "Spawn a specialized sub-agent to handle a well-defined subtask in isolation. The sub-agent gets its own context window, tool set, and permission policy. It runs to completion and returns a structured result. Use for: parallel codebase exploration, focused implementation tasks, read-only audits, or any work that benefits from a clean context window. Sub-agents cannot spawn their own sub-agents beyond the configured depth limit.",
    "parameters": {
      "type": "object",
      "properties": {
        "name": {
          "type": "string",
          "description": "Short name for this sub-agent (e.g. 'explorer', 'fixer', 'reviewer'). Used for display and tracking."
        },
        "description": {
          "type": "string",
          "description": "The task this sub-agent should accomplish. Be specific: 'Find all API routes that lack authentication middleware and list them with file:line references' NOT 'check security'."
        },
        "instructions": {
          "type": "string",
          "description": "Custom instructions injected into the sub-agent's system prompt. Use to specialize behavior: 'Focus only on the backend API layer. Do not modify frontend code. Prefer fast search over broad scans.' Empty = inherit parent's behavior."
        },
        "model": {
          "type": "string",
          "description": "Model override for this sub-agent (e.g. 'google/gemini-2.0-flash-exp:free' for cheap exploration). Empty = inherit parent's model."
        },
        "provider": {
          "type": "string",
          "description": "Provider key override (e.g. 'groq' for fast inference, 'deepseek' for cheap reasoning). Empty = inherit parent's provider."
        },
        "policy": {
          "type": "string",
          "description": "Permission policy: 'default' (all tools), 'read-only' (no writes/execution), 'no-delete' (no delete_file), 'no-execute' (no command execution), or 'custom'. Default: 'default'.",
          "enum": ["default", "read-only", "no-delete", "no-execute", "custom"]
        },
        "allowed_tools": {
          "type": "array",
          "items": {"type": "string"},
          "description": "Explicit tool whitelist. If provided, only these tools are available to the sub-agent. Example: ['read_file', 'glob_files', 'grep_files', 'list_directory']"
        },
        "denied_tools": {
          "type": "array",
          "items": {"type": "string"},
          "description": "Explicit tool blacklist. These tools are removed from the sub-agent's available tools. Example: ['delete_file', 'execute_command']"
        },
        "file_ownership": {
          "type": "array",
          "items": {"type": "string"},
          "description": "Paths this sub-agent claims exclusive write access to. Other parallel agents will be blocked from writing to these paths. Use to prevent merge conflicts. Example: ['src/api/', 'tests/api/']"
        },
        "max_steps": {
          "type": "integer",
          "description": "Maximum tool-use steps for this sub-agent (default 50, max 200). Lower = cheaper and faster."
        },
        "timeout_seconds": {
          "type": "integer",
          "description": "Maximum runtime in seconds (default 300). Sub-agent is cancelled if it exceeds this."
        },
        "wait": {
          "type": "boolean",
          "description": "If true (default), block until the sub-agent completes and return its result. If false, return immediately with a handle ID for later polling."
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
  │   ├── Check policy ⊆ parent policy (cannot widen permissions)
  │   ├── Check file_ownership doesn't conflict with active agents
  │   └── Check tool list is valid
  │
  ├── Build Sub-Agent
  │   ├── Create new Agent with spec.Model or inherited model
  │   ├── Create Provider with spec.Provider or inherited provider
  │   ├── Filter tool list by allowed_tools / denied_tools
  │   ├── Build custom system prompt (base + instructions + policy constraints)
  │   ├── Set max_steps limit
  │   └── Wire ToolFn with permission scoping
  │
  ├── Launch Sub-Agent.Run()
  │   ├── Context with timeout
  │   ├── Separate blockResolveCh (or shared with agent-id tagging)
  │   ├── Emit events tagged with agent_id for TUI
  │   └── Collect SubAgentResult on completion
  │
  └── Return result to parent
      ├── If wait=true: block, return SubAgentResult
      └── If wait=false: return handle ID immediately
```

---

## 8. Custom Instructions System

### How Instructions Are Injected

The sub-agent's system prompt is constructed as:

```
[SystemPromptBase]                          ← Same base prompt as main agent
+ [Environment Section]                     ← Same environment (OS, git, PATH)
+ [Sub-Agent Identity]                      ← NEW: "You are sub-agent '{name}'. Your task: {description}"
+ [Custom Instructions]                     ← NEW: User-provided instructions
+ [Policy Constraints]                      ← NEW: "You have the following tool restrictions..."
+ [File Ownership]                          ← NEW: "You own these paths: ..."
+ [Depth Limit]                             ← NEW: "You cannot spawn further sub-agents." (if at max depth)
```

### Custom Instructions from Config Files

Like Codex's `.codex/agents/*.toml` files, Crude supports agent profiles in config:

**`~/.crude/agents/explorer.toml`:**
```toml
name = "explorer"
description = "Read-only codebase explorer for gathering evidence before changes."
model = "google/gemini-2.0-flash-exp:free"
policy = "read-only"
instructions = """
Stay in exploration mode. Trace the real execution path, cite files and symbols, 
and avoid proposing fixes unless the parent agent asks for them. 
Prefer fast search and targeted file reads over broad scans.
Never modify any files.
"""
allowed_tools = ["read_file", "glob_files", "grep_files", "list_directory"]
```

**`.crude/agents/reviewer.toml`** (project-scoped):
```toml
name = "reviewer"
description = "Code reviewer focused on correctness, security, and missing tests."
model = "anthropic/claude-sonnet-4"
policy = "read-only"
instructions = """
Review code like an owner. Prioritize correctness, security, behavior regressions, 
and missing test coverage. Lead with concrete findings, include reproduction steps 
when possible, and avoid style-only comments unless they hide a real bug.
"""
denied_tools = ["write_file", "edit_file", "delete_file", "execute_command"]
```

**`.crude/agents/fixer.toml`:**
```toml
name = "fixer"
description = "Implementation-focused agent for targeted fixes."
model = ""  # inherit parent model
policy = "no-delete"
instructions = """
Own the fix. Make the smallest defensible change, keep unrelated files untouched, 
and validate only the behavior you changed. Run tests after each fix.
"""
denied_tools = ["delete_file"]
file_ownership = ["src/api/", "tests/api/"]
```

### Lookup Order

When `run_sub_agent` is called with `name="explorer"`:
1. Check `.crude/agents/explorer.toml` (project-scoped, overrides global)
2. Check `~/.crude/agents/explorer.toml` (user-global)
3. Check built-in profiles (`explorer`, `reviewer`, `fixer`)
4. If no profile found, use inline parameters only

---

## 9. Permission & Safety Model

### Policy Hierarchy

```
Session Global Policy
  └── Main Agent Policy (always = Session Global)
       └── Sub-Agent Policy (intersection of parent policy + spec policy)
            └── Grandchild Policy (intersection of parent + spec, again)
```

**Rule: Sub-agent policy is always ≤ parent policy.** A read-only parent cannot spawn a write-capable child.

### Policy Composition

```go
func ComposePolicy(parent safety.Policy, spec SubAgentSpec) safety.Policy {
    child := safety.DefaultPolicy()
    
    // Apply named policy preset
    switch spec.PolicyName {
    case "read-only":
        child = safety.ReadOnlyPolicy()
    case "no-delete":
        child = safety.DefaultPolicy()
        child.AllowedCapabilities[safety.CanDelete] = false
    case "no-execute":
        child = safety.DefaultPolicy()
        child.AllowedCapabilities[safety.CanExecute] = false
    case "custom":
        child = safety.DefaultPolicy()
    }
    
    // Intersection with parent: child cannot exceed parent
    for cap, allowed := range child.AllowedCapabilities {
        if !parent.AllowedCapabilities[cap] {
            child.AllowedCapabilities[cap] = false  // narrow
        }
    }
    
    return child
}
```

### Tool Filtering

```go
func FilterTools(allTools []tools.ToolDef, spec SubAgentSpec, depth int) []tools.ToolDef {
    filtered := allTools
    
    // Always remove run_sub_agent if at max depth
    if depth >= maxDepth {
        filtered = removeTool(filtered, "run_sub_agent")
    }
    
    // Apply whitelist
    if len(spec.AllowedTools) > 0 {
        allowed := make(map[string]bool)
        for _, t := range spec.AllowedTools {
            allowed[t] = true
        }
        filtered = filterTools(filtered, func(t tools.ToolDef) bool {
            return allowed[t.Function.Name]
        })
    }
    
    // Apply blacklist
    if len(spec.DeniedTools) > 0 {
        denied := make(map[string]bool)
        for _, t := range spec.DeniedTools {
            denied[t] = true
        }
        filtered = filterTools(filtered, func(t tools.ToolDef) bool {
            return !denied[t.Function.Name]
        })
    }
    
    // Safety: always deny certain tools for deeply nested agents
    if depth >= 2 {
        filtered = removeTool(filtered, "execute_command")
        filtered = removeTool(filtered, "delete_file")
    }
    
    return filtered
}
```

### Out-of-Process Policy Enforcement

Following the lessons from the Devin privilege escalation analysis:

1. **The sub-agent cannot modify its own policy** — policy is set at spawn time and is immutable
2. **The `run_sub_agent` tool does not expose policy modification parameters to the sub-agent** — only the parent can set them
3. **All tool calls go through the parent's `ToolFn`** — the parent session intercepts every sub-agent tool call and applies policy checks
4. **Permission store entries are tagged with agent IDs** — so "always allow" for one agent doesn't bleed to another

---

## 10. Model/Provider Routing

### Per-Agent Model Configuration

The `run_sub_agent` spec allows overriding `model` and `provider` independently. This enables patterns like:

- **Explorer agent:** `model="google/gemini-2.0-flash-exp:free"` (cheap, fast, read-only)
- **Reviewer agent:** `model="anthropic/claude-sonnet-4"` (expensive, thorough, read-only)
- **Fixer agent:** inherit parent model (balanced, write-capable)
- **Test writer:** `provider="groq"`, `model="llama-3.3-70b-versatile"` (fast, cheap, write tests)

### Implementation

```go
func (s *Session) buildProviderForSubAgent(spec SubAgentSpec) *provider.Provider {
    // Start with parent provider as base
    p := *s.provider  // copy
    
    // Override provider if specified
    if spec.Provider != "" {
        activeCfg := s.cfg.GetActiveProviderConfig()
        // Look up provider config from session config
        if provCfg, ok := s.cfg.Providers[spec.Provider]; ok {
            p = *provider.FromPreset(spec.Provider, provCfg.APIKey, provCfg.ProviderURL)
        }
    }
    
    // Override model if specified
    if spec.Model != "" {
        p.Model = spec.Model
    }
    
    return &p
}
```

### Config Schema for Agent-Type Defaults

```json
// ~/.crude/config.json
{
  "active_provider": "openrouter",
  "providers": {
    "openrouter": {
      "model": "anthropic/claude-sonnet-4",
      "api_key": "sk-..."
    },
    "groq": {
      "model": "llama-3.3-70b-versatile",
      "api_key": "gsk_..."
    }
  },
  "agents": {
    "max_concurrent": 4,
    "max_depth": 2,
    "max_steps_default": 50,
    "timeout_default_seconds": 300,
    "profiles": {
      "explorer": {
        "model": "google/gemini-2.0-flash-exp:free",
        "policy": "read-only"
      },
      "reviewer": {
        "provider": "openrouter",
        "model": "anthropic/claude-sonnet-4",
        "policy": "read-only"
      },
      "fixer": {
        "policy": "no-delete"
      },
      "tester": {
        "provider": "groq",
        "model": "llama-3.3-70b-versatile",
        "policy": "no-delete"
      }
    }
  }
}
```

---

## 11. Inter-Agent Communication

### Communication Model: Results-Only (Upward)

Sub-agents communicate with their parent **only through structured results**. There is no direct agent-to-agent communication. This is the Codex model (manager-mediated) and avoids the complexity of peer-to-peer messaging.

```
Main Agent ←── SubAgentResult ──┤ Explorer Agent (running)
                                 │ Reviewer Agent (running)  
                                 │ Fixer Agent (running)
```

### Why Not Peer-to-Peer?

1. **Complexity** — P2P requires message routing, serialization, and ordering. Too complex for a CLI tool.
2. **Context pollution** — agents sharing messages can corrupt each other's reasoning (the "context rot" problem).
3. **Security** — a compromised agent could inject malicious instructions into peers (cross-agent config poisoning).
4. **Observability** — parent-mediated communication means the parent always knows what happened.

### Shared Blackboard (Future)

For advanced coordination (Phase 2), introduce a **shared blackboard**:

```go
type Blackboard struct {
    mu      sync.RWMutex
    entries map[string]BlackboardEntry
}

type BlackboardEntry struct {
    AgentID  AgentID
    Key      string
    Value    string
    WrittenAt time.Time
}
```

Sub-agents can write to and read from the blackboard. This enables patterns like:
- Explorer writes findings → Fixer reads them → Main aggregates
- Agents coordinate file ownership through the blackboard

**But only after the basic results-only model is proven and stable.**

---

## 12. Permission Escalation to User

### The Problem

When a sub-agent needs user approval for a dangerous operation, the permission prompt must:
1. Show **which agent** is requesting (not just "Allow this command?")
2. Show the **agent's purpose** (so the user can evaluate intent)
3. Work correctly when **multiple agents are running concurrently**
4. Not cause **approval fatigue** by asking for the same category of permission repeatedly

### Design: Tagged Blocking Events

```go
// When a sub-agent hits a permission prompt:
emit(AgentEvent{
    Type: "blocking",
    Data: map[string]string{
        "operation":   "Execute",
        "tool":        "execute_command",
        "target":      "npm install --save react-hook-form",
        "question":    "Allow this Execute operation on npm install --save react-hook-form?",
        "risk":        "project-write",
        "reason":      "The execute_command tool modifies files on disk",
        "agent_id":    "fixer-abc123",          // NEW: which agent
        "agent_name":  "fixer",                 // NEW: human-readable name
        "agent_task":  "Fix the form validation bug in src/components/Form.tsx",  // NEW: what it's doing
    },
    AgentID: "fixer-abc123",  // NEW: tagged at event level
})
```

### TUI Display

The TUI shows a blocking dialog with agent identity:

```
╭─ Permission Request ──────────────────────────────────────╮
│ Agent: fixer                                                │
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

When the user presses `A` (always allow), the permission grant is scoped:

```go
// "Always allow" for THIS agent only (default)
permStore.GrantAlways(op, tool, target, WithAgentID("fixer-abc123"))

// "Always allow" for ALL agents (explicit user choice)
permStore.GrantAlways(op, tool, target, WithGlobalScope())
```

### Concurrent Agent Approval Handling

When multiple sub-agents are running concurrently:

1. Each sub-agent gets its own `blockResolveCh` channel
2. The TUI shows approval requests from inactive threads with the agent label
3. User can press `o` to switch context to that agent's thread before deciding
4. If the user denies, the sub-agent receives the denial and must work around it
5. If the session is in non-interactive mode (`--yolo` or CI), approval requests auto-fail with a structured error

### Auto-Approval Classifier (Future, Phase 2)

Following Anthropic's approach (93% of prompts are approved), build a classifier that auto-approves low-risk operations:

- `read_file` on project files: auto-approve
- `execute_command` with `go test`, `npm test`: auto-approve  
- `write_file` in a test directory: auto-approve
- `execute_command` with `rm -rf`, `sudo`: always prompt
- `delete_file`: always prompt

This reduces approval fatigue while preserving meaningful human intervention.

---

## 13. Concurrency & Race Conditions

### File Ownership System

Prevent sub-agent merge conflicts by declaring file ownership:

```go
type FileOwnership struct {
    mu      sync.Mutex
    claims  map[string]AgentID  // path prefix → agent ID
}

func (fo *FileOwnership) Claim(agentID AgentID, paths []string) error {
    fo.mu.Lock()
    defer fo.mu.Unlock()
    
    for _, path := range paths {
        for claimedPath, claimedBy := range fo.claims {
            if strings.HasPrefix(path, claimedPath) || strings.HasPrefix(claimedPath, path) {
                return fmt.Errorf("path %q conflicts with agent %q's claim on %q", path, claimedBy, claimedPath)
            }
        }
    }
    
    for _, path := range paths {
        fo.claims[path] = agentID
    }
    return nil
}

func (fo *FileOwnership) Release(agentID AgentID) {
    fo.mu.Lock()
    defer fo.mu.Unlock()
    for path, id := range fo.claims {
        if id == agentID {
            delete(fo.claims, path)
        }
    }
}
```

### How It Works

1. Sub-agent spec includes `file_ownership: ["src/api/", "tests/api/"]`
2. At spawn time, the `SubAgentManager` checks for conflicts with active agents
3. If there's a conflict, the spawn fails with a clear error: "Agent 'fixer' already owns path 'src/api/'. Choose a different path or wait for it to complete."
4. When the sub-agent completes, its claims are released
5. The parent agent can then spawn another agent that touches those paths

### No-Ownership Mode

If `file_ownership` is empty, the sub-agent can write anywhere — but the parent is warned that concurrent writes may conflict. The editor's journal tracks all writes and can detect conflicts:

```go
// Before any write, check for conflicts
func (e *Editor) Write(path, content string) error {
    if conflictingAgent := e.fileOwnership.Query(path); conflictingAgent != "" {
        return fmt.Errorf("file %q is owned by agent %q", path, conflictingAgent)
    }
    // ... proceed with write
}
```

### Sequential vs Parallel Execution

- **Parallel by default** — sub-agents with `wait=false` run concurrently
- **Sequential when overlapping** — if `file_ownership` would conflict, force sequential execution
- **Parent aggregates** — the main agent waits for all sub-agents and merges results

---

## 14. State Machine Integration

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
    "agent_id":  spec.ID,
    "agent_name": spec.Name,
    "depth":     spec.Depth,
    "model":     spec.Model,
}
```

When the sub-agent completes:

```go
sm.CompleteGoalByID(sessionID, goal.ID, map[string]any{
    "status":    result.Status,
    "summary":   result.Summary,
    "files_written": result.FilesWritten,
    "cost":      result.CostUSD,
})
```

### Plan Visibility

The parent's `list_goal_status` shows sub-agent progress:

```
Plan: Fix the authentication system (active)
  ✓ step1: Explore auth code paths (completed) [explorer agent]
  → step2: Fix the token validation (doing) [fixer agent, depth=1]
  ☐ step3: Write auth tests (pending) [tester agent, depth=1]
```

---

## 15. TUI Integration

### Sub-Agent Thread View

The TUI shows active sub-agents in the status bar:

```
┌──────────────────────────────────────────────────┐
│ ⚡ 3 agents active: explorer, reviewer, fixer     │
├──────────────────────────────────────────────────┤
│ [explorer] Reading src/api/auth.go               │
│ [reviewer] Analyzing security patterns...         │
│ [fixer]     Writing src/api/token.go              │
└──────────────────────────────────────────────────┘
```

### Switching Threads

Following Codex's approach, allow the user to switch to a specific agent's thread:

- Press `/` to open agent list
- Select an agent to see its live output
- Permission prompts from any thread show the agent label
- Press `q` to return to main thread view

### Sub-Agent Result Display

When a sub-agent completes, its result is shown inline:

```
┌─ Sub-agent Result: explorer ─────────────────────┐
│ Status: completed (45s, 12k tokens, $0.003)       │
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

## 16. Edge Cases & Nuances

### 16.1 Sub-Agent Tries to Spawn Its Own Sub-Agent at Max Depth

**Case:** A depth=1 sub-agent calls `run_sub_agent`.

**Behavior:** The `run_sub_agent` tool is not available in the sub-agent's tool list. If the sub-agent somehow requests it, the tool call returns:

```
Error: Cannot spawn sub-agents at depth 1 (max_depth=2). You are already at the maximum allowed nesting level. Complete your task and return results to your parent.
```

### 16.2 Sub-Agent Exceeds Its Step Limit

**Case:** A sub-agent with `max_steps=50` reaches 50 tool calls.

**Behavior:** The agent loop stops and returns a `SubAgentResult` with `Status: "failed_timeout"` and a summary explaining the step limit was reached.

### 16.3 Sub-Agent Exceeds Its Time Limit

**Case:** A sub-agent with `timeout_seconds=120` runs for 2 minutes.

**Behavior:** The context is cancelled. The agent loop detects `ctx.Err()` and returns a `SubAgentResult` with `Status: "failed_timeout"`.

### 16.4 Sub-Agent's Model Quota Is Exhausted

**Case:** A sub-agent using `groq/llama-3.3-70b-versatile` hits the rate limit.

**Behavior:** The API call returns a rate limit error. Unlike Codex (which surfaces this as "no response"), Crude returns:

```json
{
  "status": "failed_quota",
  "errors": ["Rate limit exceeded for provider 'groq' model 'llama-3.3-70b-versatile': 429 Too Many Requests"],
  "summary": "Sub-agent could not complete because its assigned model quota was exhausted."
}
```

The parent agent can then:
- Retry with a different model
- Do the work itself
- Report the failure to the user

### 16.5 Sub-Agent Needs Permission Escalation While Parent Is Waiting

**Case:** Parent calls `run_sub_agent(wait=true)`. Sub-agent hits a permission prompt.

**Behavior:** The permission prompt is surfaced to the user through the TUI with agent identity tags. The parent's `run_sub_agent` call is effectively blocked while the sub-agent waits for approval. The TUI shows which agent is blocked and what it's requesting.

### 16.6 Two Sub-Agents Try to Write to the Same File

**Case:** `fixer-a` owns `src/api/` and `fixer-b` tries to write to `src/api/auth.go`.

**Behavior:** If file ownership is declared, `fixer-b`'s write is blocked at the editor level with a clear error. If no ownership is declared, the second write succeeds but the editor's journal tracks both writes. The parent is warned about potential conflicts during result aggregation.

### 16.7 User Interrupts a Running Sub-Agent

**Case:** User sends a message while a sub-agent is running.

**Behavior:** The interruption is routed to the main agent, not the sub-agent. The main agent can:
- Cancel the sub-agent via the handle
- Wait for the sub-agent to finish
- Redirect based on the user's input

Sub-agents do NOT receive user interruptions directly. Only the main agent has the `btwCh` channel.

### 16.8 Parent Agent Is Cancelled While Sub-Agents Run

**Case:** The user cancels the main agent's turn while sub-agents are active.

**Behavior:** The parent's context cancellation propagates to all sub-agents. Each sub-agent's `Run()` loop detects `ctx.Err()` and returns `SubAgentResult{Status: "cancelled"}`. The SubAgentManager releases all file ownership claims.

### 16.9 Sub-Agent Returns Error — Parent Retry Logic

**Case:** Sub-agent fails with `failed_error`. Should the parent retry?

**Behavior:** The parent receives the `SubAgentResult` with error details. The parent's LLM decides whether to:
- Retry the same sub-agent with the same spec
- Retry with a different model/provider
- Do the work itself
- Report failure to the user

No automatic retry. The LLM is the orchestrator.

### 16.10 Sub-Agent Modifies a File the Parent Also Modified

**Case:** Parent edited `src/main.go`, then spawned a sub-agent that also edited `src/main.go`.

**Behavior:** The editor's journal records both operations. When the sub-agent's edit conflicts with the parent's (the file on disk has changed since the sub-agent's last read), the edit_file operation fails with:

```
Error: File src/main.go was modified since you last read it (by main agent at 12:45:03). 
Re-read the file before editing.
```

This is the same "stale read" protection that already exists, but now it includes the agent identity.

### 16.11 Sub-Agent Calls `reply()` Tool

**Case:** A sub-agent calls `reply()` to send a message to the user.

**Behavior:** The `reply()` tool in a sub-agent context does NOT send a message directly to the user. Instead, it appends to the sub-agent's running log. The user sees it if they switch to the agent's thread. The sub-agent's final result includes any `reply()` content.

Sub-agents should NOT have direct user communication. Only the main agent talks to the user.

### 16.12 Nested Sub-Agent Context Window Budget

**Case:** 3 sub-agents each consume 50k tokens, parent has 200k window.

**Behavior:** Each sub-agent has its OWN context window — they don't share the parent's. The parent only consumes tokens for:
- Its own conversation + tool calls
- The structured `SubAgentResult` summaries returned by each sub-agent (typically 500-2000 tokens each)

This is the key advantage of context isolation: sub-agent token usage doesn't pollute the parent's window.

### 16.13 Sub-Agent with `policy="read-only"` Tries to Call `write_file`

**Case:** A read-only sub-agent's LLM tries to call `write_file`.

**Behavior:** The tool call is intercepted before execution:

```
Error: [POLICY BLOCK] Tool 'write_file' is not allowed for this agent. 
Your policy is 'read-only'. You cannot create, update, or delete files. 
Focus on analysis and return your findings to the parent agent.
```

The LLM should learn from this and not retry. If it retries 3 times, the loop detection kicks in.

### 16.14 Sub-Agent Config File Is Missing or Malformed

**Case:** `run_sub_agent(name="explorer")` but `.crude/agents/explorer.toml` has a syntax error.

**Behavior:** 
1. Log a warning about the malformed config
2. Fall back to inline parameters only (no custom instructions from the file)
3. The `run_sub_agent` call proceeds with whatever valid parameters are available

### 16.15 Concurrent Sub-Agent Count at Maximum

**Case:** `max_concurrent=4` and 4 agents are already running. Parent tries to spawn a 5th.

**Behavior:** 
- If `wait=true`: The `run_sub_agent` call blocks until a slot opens (one agent completes)
- If `wait=false`: The call returns an error: "Maximum concurrent sub-agents (4) reached. Wait for an active agent to complete."

---

## 17. Configuration Schema

### Complete Config Schema

```json
{
  "active_provider": "openrouter",
  "providers": {
    "openrouter": {
      "api_key": "",
      "model": "anthropic/claude-sonnet-4",
      "provider_url": "",
      "thinking_effort": ""
    }
  },
  "turn_logs_enabled": true,
  "agents": {
    "max_concurrent": 4,
    "max_depth": 2,
    "max_steps_default": 50,
    "timeout_default_seconds": 300,
    "auto_approve_read_only": true,
    "auto_approve_test_commands": true,
    "profiles": {
      "explorer": {
        "model": "google/gemini-2.0-flash-exp:free",
        "provider": "",
        "policy": "read-only",
        "allowed_tools": ["read_file", "glob_files", "grep_files", "list_directory", "web_search", "arxiv_search", "web_fetch"],
        "denied_tools": [],
        "instructions": "Stay in exploration mode. Trace execution paths, cite files and symbols. Prefer fast search over broad scans. Never modify any files.",
        "max_steps": 30,
        "timeout_seconds": 120
      },
      "reviewer": {
        "model": "anthropic/claude-sonnet-4",
        "provider": "",
        "policy": "read-only",
        "instructions": "Review code for correctness, security, and missing tests. Lead with concrete findings.",
        "max_steps": 40,
        "timeout_seconds": 180
      },
      "fixer": {
        "model": "",
        "provider": "",
        "policy": "no-delete",
        "instructions": "Make the smallest defensible change. Keep unrelated files untouched. Run tests after each fix.",
        "denied_tools": ["delete_file"],
        "max_steps": 60,
        "timeout_seconds": 300
      },
      "tester": {
        "model": "google/gemini-2.0-flash-exp:free",
        "provider": "",
        "policy": "no-delete",
        "instructions": "Write comprehensive tests. Focus on edge cases and integration coverage.",
        "denied_tools": ["delete_file", "execute_command"],
        "max_steps": 80,
        "timeout_seconds": 300
      }
    }
  }
}
```

### Agent Profile File (TOML)

```toml
# ~/.crude/agents/explorer.toml

name = "explorer"
description = "Read-only codebase explorer for gathering evidence."
model = "google/gemini-2.0-flash-exp:free"
# provider = ""  # inherit from session
policy = "read-only"
max_steps = 30
timeout_seconds = 120

allowed_tools = [
    "read_file",
    "glob_files", 
    "grep_files",
    "list_directory",
    "web_search",
    "web_fetch",
]

instructions = """
Stay in exploration mode. Trace the real execution path, cite files and 
symbols, and avoid proposing fixes unless the parent agent asks for them. 
Prefer fast search and targeted file reads over broad scans.
Never modify any files.
"""
```

---

## 18. Implementation Roadmap

### Phase 1: Core Sub-Agent Infrastructure (2-3 weeks)

1. **Add `SubAgentSpec`, `SubAgentResult`, `SubAgentManager` types** in `agent/subagent.go`
2. **Add `run_sub_agent` tool definition** to `tools/definitions.go`
3. **Implement `SubAgentManager`** in `session/subagent.go`:
   - Spawn/cancel/track sub-agents
   - Enforce `max_concurrent`, `max_depth`
   - File ownership claims
4. **Wire `run_sub_agent` into `session.go`'s `ToolFn`**:
   - Build provider for sub-agent
   - Filter tool list
   - Compose policy
   - Build custom system prompt
   - Launch `Agent.Run()` in goroutine
   - Collect `SubAgentResult`
5. **Add `AgentID` to `AgentEvent`** for TUI tagging
6. **Update `Agent` struct** with `AgentID`, `Depth`, `ParentID`
7. **Basic TUI**: Show sub-agent status in status bar

### Phase 2: Permission & Safety Hardening (1-2 weeks)

1. **Implement policy composition** (`ComposePolicy`)
2. **Implement tool filtering** (`FilterTools`)
3. **Implement file ownership system** (`FileOwnership`)
4. **Tag permission prompts with agent identity**
5. **Scope "always allow" grants to agent ID**
6. **Add auto-approve classifier** for low-risk operations
7. **Write comprehensive safety tests**

### Phase 3: Agent Profiles & Config (1 week)

1. **Add TOML parsing** for `.crude/agents/*.toml` files
2. **Implement profile lookup** (project → global → built-in)
3. **Add `agents` section to `config.json` schema**
4. **Implement config-driven model/provider routing**
5. **Update TUI settings to show agent profiles**

### Phase 4: Advanced Features (2-3 weeks)

1. **Async sub-agent spawning** (`wait=false` + handle polling)
2. **State machine integration** (sub-agent goals in the state tree)
3. **TUI thread switching** (view individual agent output)
4. **Cost aggregation** per sub-agent in session cost tracker
5. **Session DB persistence** of sub-agent results
6. **Error propagation taxonomy** (quota, timeout, policy, internal)
7. **Shared blackboard** for advanced coordination (optional)

---

## 19. Open Questions

1. **Should sub-agents share the parent's `blockResolveCh` or get their own?**
   - **Option A:** Shared channel with agent-id tagging (simpler, user sees all prompts)
   - **Option B:** Per-agent channels, TUI routes to correct context (cleaner, more complex)
   - **Recommendation:** Start with Option A, migrate to B in Phase 4

2. **Should sub-agents be able to call `reply()` to the user?**
   - **Current recommendation:** No. Sub-agents communicate only through structured results to the parent. The parent decides what to show the user.
   - **Alternative:** Allow `reply()` but clearly tag it as coming from a sub-agent in the TUI.

3. **Should `file_ownership` be mandatory or optional for write-capable sub-agents?**
   - **Current recommendation:** Optional with a strong warning when omitted.
   - **Alternative:** Require it when `max_concurrent > 1` and policy allows writes.

4. **What happens when a sub-agent's model doesn't support tool use?**
   - The agent should fail immediately with `failed_policy` status: "Model X does not support tool use. Sub-agents require models with function calling capability."

5. **Should sub-agents be able to access the session DB?**
   - **Current recommendation:** No. Sub-agents interact only through tools and the ToolFn. DB persistence is handled by the parent session.

6. **Maximum total sub-agent token budget?**
   - Should we add a `max_total_sub_agent_tokens` config to prevent cost runaway?
   - **Recommendation:** Yes, add in Phase 2. Default: 500k total tokens across all sub-agents per turn.

7. **Should the `run_sub_agent` tool be available to sub-agents at depth < max_depth?**
   - **Current recommendation:** Yes, up to `max_depth`. This enables hierarchical delegation.
   - **Risk:** Recursive fan-out could consume massive tokens. Mitigated by `max_concurrent` and `max_depth` limits.

8. **How to handle the case where a sub-agent's provider API key is invalid?**
   - The sub-agent should fail with `failed_quota` status and a clear error message.
   - The parent can fall back to its own provider.

---

*This proposal is a living document. Feedback and iteration welcome.*
