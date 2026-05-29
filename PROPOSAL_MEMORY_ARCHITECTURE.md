# Crude Virtual Memory Architecture

## Merging MemGPT + ClawVM + Engram for Persistent Context Management

> **Date:** 2026-05-13
> **Status:** Design Proposal
> **Papers Referenced:**
> - MemGPT: Towards LLMs as Operating Systems (Packter et al., 2023) — arXiv:2310.08560
> - ClawVM: Harness-Managed Virtual Memory for Stateful Tool-Using LLM Agents (Rafique & Bindschaedler, 2026) — arXiv:2604.10352
> - Engram: Persistent memory system for AI coding agents (Gentleman-Programming, 2025-2026) — github.com/Gentleman-Programming/engram

---

## Table of Contents

1. [The Problem](#1-the-problem)
2. [Conceptual Model: The OS Analogy](#2-conceptual-model-the-os-analogy)
3. [Tiered Memory Architecture](#3-tiered-memory-architecture)
4. [Typed Pages & Minimum-Fidelity Invariants](#4-typed-pages--minimum-fidelity-invariants)
5. [Multi-Resolution Representation](#5-multi-resolution-representation)
6. [Lifecycle & Writeback Protocol](#6-lifecycle--writeback-protocol)
7. [SQLite Schema](#7-sqlite-schema)
8. [Agent Tools](#8-agent-tools)
9. [Integration Points in Crude](#9-integration-points-in-crude)
10. [Implementation Phases](#10-implementation-phases)
11. [Open Questions / Future Work](#11-open-questions--future-work)

---

## 1. The Problem

Crude currently has **in-memory-only message storage** (`s.msgs []provider.ChatMessage`). Every session starts from zero. The `truncateMessages()` function in `session.go` drops older messages when the context window is exceeded, permanently losing them.

Three recurring failure modes (documented in ClawVM's surveys of real agent traces):

| Failure | Symptom | Root Cause |
|---------|---------|------------|
| **Bootstrap loss** | Agent "forgets its protocol" after compaction | System instructions/pinned rules evicted |
| **Flush miss** | Decisions silently vanish on reset | Dirty state destroyed without writeback |
| **Silent recall** | Agent says "I don't know" when data exists | Empty search results without error codes |

These are not edge cases — they dominate issue trackers for long-running coding agents.

---

## 2. Conceptual Model: The OS Analogy

```
┌────────────────────────────────────┐
│  WORKING MEMORY (Context Window)   │  ← RAM: fast, scarce, volatile
│                                    │
│  What the LLM "sees" right now.    │
│  Managed by the harness via        │
│  paging decisions.                 │
└────────────────┬───────────────────┘
                 │ page in/out
                 ▼
┌────────────────────────────────────┐
│  RECALL STORE (Recent History)     │  ← Disk cache: fast search
│                                    │
│  Recent messages + tool calls.     │
│  FTS5-indexed, auto-pruned.       │
└────────────────┬───────────────────┘
                 │ promote to durable
                 ▼
┌────────────────────────────────────┐
│  ARCHIVAL STORE (Long-term Memory) │  ← Disk: durable, unlimited
│                                    │
│  Decisions, patterns, bugs,        │
│  session summaries, preferences.   │
│  SQLite + FTS5.                    │
└────────────────────────────────────┘
```

**Key insight from MemGPT:** The agent itself pages data between tiers via explicit tool calls, just as an OS pages between RAM and disk. The harness enforces invariants (ClawVM) — the agent drives upgrades.

---

## 3. Tiered Memory Architecture

### Tier 1: Working Memory (Context Window)

The LLM's context window. Everything here is visible to the model on every turn.

```
┌──────────────────────────────────────────────┐
│ RESERVED (cannot be used for paged state):   │
│  • System prompt              ~400 tokens    │
│  • Tool schemas               ~200 tokens    │
│  • Output budget              ~4000 tokens   │
│  • Safety margin              ~400 tokens    │
├──────────────────────────────────────────────┤
│ PAGED STATE (managed by harness):            │
│  • Hard-pinned pages (must fit)              │
│    - Bootstrap: system instructions          │
│    - Constraints: user rules/preferences     │
│  • Active pages (degradable)                 │
│    - Plan: current goal + step               │
│    - Evidence: recent tool outputs           │
│    - Conversation: last N turns              │
│    - Context injection: relevant memories    │
│  • Free budget: remaining for model output   │
└──────────────────────────────────────────────┘
```

**Budget management:** At each prompt assembly, the harness:
1. Reserves fixed costs (system prompt, tool schemas, output, safety margin)
2. Installs all hard-pinned pages at their minimum fidelity
3. If hard-pinned set exceeds budget → **observable fault** (pinned invariant miss)
4. Greedily upgrades remaining pages by utility-per-token until budget exhausted

### Tier 2: Recall Store (Recent History)

SQLite table for recent message history. Fast FTS5 search over the last N sessions.

- Auto-pruned (oldest entries removed when exceeding a configurable limit)
- FTS5-indexed for quick keyword search
- Used by the agent to answer "what did we do last session?"

### Tier 3: Archival/Memory Store (Long-Term Durable)

SQLite table for structured, durable memories. Never auto-pruned.

- Decisions, architecture choices, bug patterns, conventions
- Session summaries (auto-generated at session end)
- Paginated by type and tags
- FTS5-indexed for sub-millisecond BM25 search

---

## 4. Typed Pages & Minimum-Fidelity Invariants

> **From ClawVM:** Every piece of agent state is a *typed page* with a *minimum-fidelity invariant* — a guarantee that the page will never degrade below a certain resolution.

### Page Types

| Type | Min Fidelity | Scope | Degradation Path | Pin |
|------|-------------|-------|-----------------|-----|
| **Bootstrap** | Full | Session/Project | — (never degrades) | 🔒 Hard |
| **Constraint** | Structured | Session/Project | Full → Structured | 🔒 Hard |
| **Plan** | Pointer | Session | Full → Compressed → Structured → Pointer | Soft |
| **Preference** | Pointer | Project | Full → Compressed → Structured → Pointer | Soft |
| **Evidence** | Pointer | Session | Full → Compressed → Structured → Pointer | None |
| **Conversation** | Compressed | Session | Full → Compressed | None |
| **Decision** | Compressed | Project | Full → Compressed | None |
| **Session** | Structured | Session | Full → Compressed → Structured | None |

### Resolution Levels

| Level | Meaning | Token Cost | Can Reconstruct? |
|-------|---------|-----------|-----------------|
| **Full (F)** | Verbatim content | High | N/A (it's the source) |
| **Compressed (C)** | LLM-summarized or LLMLingua-compressed | Medium | Partial (gist preserved) |
| **Structured (St)** | Typed fields only (e.g., `{goal, step, status}`) | Low | Yes (via pointer resolution) |
| **Pointer (Pt)** | Resolvable handle + minimal metadata | Minimal | Yes (fetch from backing store) |

### Pin Types

| Pin | Behaviour |
|-----|-----------|
| 🔒 **Hard** | Must be resident at min fidelity. If budget can't fit → observable fault. |
| 📌 **Soft** | Should be resident if possible. Degraded before eviction. |
| — **None** | Free to evict under pressure. |

### Why This Matters

Without typed pages, compaction is **all-or-nothing**: the model's recursive summary might drop the current plan step, a critical constraint, or evidence that a conflict was already resolved. With typed pages, each page degrades *along its allowed path*, preserving enough information to reconstruct on demand.

---

## 5. Multi-Resolution Representation

> **From ClawVM:** Representations are generated at *page creation time*, not under budget pressure. The representation selector then chooses among pre-existing variants — no runtime LLM calls.

### Example: Plan Page Lifecycle

```
1. Agent starts task:
   → PLAN page created at FULL resolution
   → COMPRESSED and STRUCTURED variants pre-computed

2. Budget gets tight (70%+):
   → PLAN page downgraded to COMPRESSED (keeps goal + steps, drops details)

3. Budget critically tight:
   → PLAN page downgraded to STRUCTURED (just {id, goal, status})

4. Budget recovers (compaction frees space):
   → PLAN page upgraded back to FULL (restored from backing store)

5. Session ends:
   → PLAN page flushed to backing store at COMPRESSED resolution
```

### Upgrade Policy (Phase 2 of Prompt Assembly)

After hard-pinned pages are installed, remaining budget is spent upgrading pages by **marginal utility per token**:

```
utility(page, resolution) =
    w_pin × pin_boost
  + w_recency × recency_score
  + w_recompute × recompute_cost
  + w_scope × scope_utility

upgrade_value = Δutility / Δtokens

Sort upgrades by value descending, apply until budget exhausted.
```

From ClawVM's ablation study: **utility scoring is not needed for correctness** — LRU with the same structural features (pinning, writeback, pointer resolution) achieves identical fault elimination. Utility scoring only affects *output quality* (which pages the model sees at higher fidelity).

---

## 6. Lifecycle & Writeback Protocol

> **From ClawVM:** Dirty state must be committed *before* the runtime destroys the only copy. Today's harnesses treat flush as best-effort, causing "flush-miss" faults.

### Lifecycle Events in Crude

| Event | When | What Happens |
|-------|------|-------------|
| **Session Start** | User starts crude | Load pinned pages, inject context, signal agent |
| **Turn N** | Each user message | Normal operation with budget monitoring |
| **Context Pressure** | Working set > 70% budget | Emit `context_pressure` signal to agent |
| **Compaction** | Working set > 100% budget | Agent must degrade/evict pages → validated writeback → truncate |
| **Session End** | User closes crude | Flush all dirty pages → auto-summarize → store session page |
| **Reset** | `/new` command | Same as session end, but don't create new session page |

### Three-Phase Writeback (ClawVM's Protocol)

```
Phase 1: STRUCTURED STAGING
  Agent requests: {field, op, value, scope, evidence_ref}
  Ops allowed: append | merge | set-with-version
  → Appended to WritebackJournal (not yet applied)

Phase 2: DETERMINISTIC VALIDATION
  Check:
  • Schema correctness (field exists, types match)
  • Provenance (evidence_ref resolves)
  • Scope (principal has permission)
  • Non-destructive (no overwrite of committed content)
  • Policy (hard-pinned constraints not violated)
  → Rejected updates stay in journal with reason code

Phase 3: SCOPED COMMIT
  Validated updates commit via deterministic merge rules
  → Atomic, logged commit point
```

### Non-Destructiveness Rule

For each update targeting key `k`:
- **Append/Merge:** Valid only if prior committed entries for `k` are preserved
- **Set-with-version:** Valid only if staged version == last committed version
- Otherwise: rejected with `DESTRUCTIVE_OP` reason code

---

## 7. SQLite Schema

Using `modernc.org/sqlite` (pure Go, no CGO, FTS5 support).

### Table: `pages`

Typed state pages with resolution levels. This is the ClawVM concept.

```sql
CREATE TABLE pages (
    id          TEXT PRIMARY KEY,           -- "page_{uuid}"
    page_type   TEXT NOT NULL,              -- bootstrap|constraint|plan|preference|evidence|conversation
    scope       TEXT NOT NULL,              -- "session:{session_id}" | "project:{project_name}"
    provenance  TEXT,                       -- originating tool call or transcript span
    pin         TEXT NOT NULL DEFAULT 'none', -- hard|soft|none

    -- Multi-resolution variants (pre-computed)
    content_full        TEXT,               -- Full verbatim content
    content_compressed  TEXT,               -- LLM-summarized or compressed
    content_structured  TEXT,               -- JSON: typed fields
    content_pointer     TEXT,               -- Resolvable handle

    -- Current resolution level in working set
    current_resolution  TEXT NOT NULL DEFAULT 'full', -- full|compressed|structured|pointer

    -- Metadata
    token_cost_full         INTEGER NOT NULL DEFAULT 0,
    token_cost_compressed   INTEGER NOT NULL DEFAULT 0,
    token_cost_structured   INTEGER NOT NULL DEFAULT 0,
    token_cost_pointer      INTEGER NOT NULL DEFAULT 0,

    -- Lifecycle
    is_dirty    INTEGER NOT NULL DEFAULT 0, -- 1 = needs writeback
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE INDEX idx_pages_type ON pages(page_type);
CREATE INDEX idx_pages_scope ON pages(scope);
CREATE INDEX idx_pages_pin ON pages(pin);
```

### Table: `memories`

Structured durable memories (Engram-style). For decisions, patterns, bugs, conventions, session summaries.

```sql
CREATE TABLE memories (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    content     TEXT NOT NULL,
    memory_type TEXT NOT NULL,              -- decision|pattern|bug|architecture|convention|session|note
    tags        TEXT DEFAULT '[]',          -- JSON array: ["auth", "performance"]
    metadata    TEXT DEFAULT '{}',          -- JSON blob: extensible

    -- Resolution tracking
    resolution  TEXT NOT NULL DEFAULT 'full', -- full|compressed|structured

    -- Provenance
    session_id  TEXT,
    project     TEXT DEFAULT '',

    -- Lifecycle
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE VIRTUAL TABLE memories_fts USING fts5(
    content, tags, memory_type,
    content='memories',
    content_rowid='id'
);

-- Triggers to keep FTS in sync
CREATE TRIGGER memories_ai AFTER INSERT ON memories BEGIN
    INSERT INTO memories_fts(rowid, content, tags, memory_type)
    VALUES (new.id, new.content, new.tags, new.memory_type);
END;

CREATE TRIGGER memories_ad AFTER DELETE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, content, tags, memory_type)
    VALUES ('delete', old.id, old.content, old.tags, old.memory_type);
END;

CREATE TRIGGER memories_au AFTER UPDATE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, content, tags, memory_type)
    VALUES ('delete', old.id, old.content, old.tags, old.memory_type);
    INSERT INTO memories_fts(rowid, content, tags, memory_type)
    VALUES (new.id, new.content, new.tags, new.memory_type);
END;

CREATE INDEX idx_memories_type ON memories(memory_type);
CREATE INDEX idx_memories_project ON memories(project);
CREATE INDEX idx_memories_created ON memories(created_at);
```

### Table: `messages`

Raw conversation log. Full FTS5-indexed message history for complete recall.

```sql
CREATE TABLE messages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id      TEXT NOT NULL,
    role            TEXT NOT NULL,          -- user|assistant|tool|system
    content         TEXT,
    tool_name       TEXT,
    tool_arguments  TEXT,
    tool_result     TEXT,
    token_count     INTEGER DEFAULT 0,
    sequence_num    INTEGER NOT NULL,
    created_at      TEXT NOT NULL
);

CREATE VIRTUAL TABLE messages_fts USING fts5(
    content, tool_name, tool_result,
    content='messages',
    content_rowid='id'
);

CREATE INDEX idx_messages_session ON messages(session_id);
CREATE INDEX idx_messages_sequence ON messages(session_id, sequence_num);
```

### Table: `journal`

Writeback journal for the ClawVM three-phase commit protocol.

```sql
CREATE TABLE journal (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    event       TEXT NOT NULL,              -- lifecycle boundary: compaction|reset|flush|session_end
    page_id     TEXT NOT NULL REFERENCES pages(id),
    op          TEXT NOT NULL,              -- append|merge|set-with-version
    field       TEXT NOT NULL,
    value       TEXT NOT NULL,              -- staged value (JSON)
    status      TEXT NOT NULL DEFAULT 'staged', -- staged|validated|committed|rejected
    reason      TEXT,                       -- rejection reason code
    created_at  TEXT NOT NULL,
    committed_at TEXT
);

CREATE INDEX idx_journal_status ON journal(status);
CREATE INDEX idx_journal_page ON journal(page_id);
```

---

## 8. Agent Tools

New tools exposed to the agent in the `ToolFn` switch (session.go):

### `memorize(type, content, tags)`
Store a structured memory. The agent calls this when it learns something cross-session relevant.

```
Params:
  type:    "decision" | "pattern" | "bug" | "architecture" | "convention" | "note"
  content: Text of the memory
  tags:    ["keyword1", "keyword2"] (optional)
  project: Project scope (optional, defaults to current)

Returns:
  {id: 42, memory_type: "decision", token_cost: 87}
```

### `recall(query, types, limit)`
FTS5 search across memories + messages.

```
Params:
  query:  Search string (FTS5 syntax supported)
  types:  ["decision", "bug"] (optional, filter by type)
  limit:  Max results (default: 10)

Returns:
  {
    results: [
      {id: 42, type: "decision", content: "...", tags: [...], score: 0.95},
      ...
    ],
    total: 3,
    took_ms: 2
  }
```

### `pin_page(page_id, level)`
Mark a page as hard-pinned (cannot be evicted).

```
Params:
  page_id: "page_abc123"
  level:   "hard" | "soft" | "none"

Returns:
  {page_id: "page_abc123", pin: "hard", min_fidelity: "structured"}
```

### `degrade_page(page_id, resolution)`
Explicitly degrade a page's resolution to free budget.

```
Params:
  page_id:    "page_abc123"
  resolution: "compressed" | "structured" | "pointer"

Returns:
  {page_id: "page_abc123", resolution: "compressed", freed_tokens: 312}
```

### `context_status()`
Show the current working-set composition.

```
Returns:
  {
    budget: {total: 128000, reserved: 4600, used: 32000, free: 91400},
    pages: [
      {id: "page_abc", type: "bootstrap", resolution: "full", tokens: 400, pin: "hard"},
      {id: "page_def", type: "plan", resolution: "compressed", tokens: 120, pin: "soft"},
      ...
    ],
    pressure: "normal" | "warning" | "critical"
  }
```

---

## 9. Integration Points in Crude

### New Package: `crude/memory/`

```
memory/
├── store.go          # SQLite init, migrations, open/close
├── pages.go          # Page table operations (CRUD, degrade, pin)
├── memories.go       # Memory store operations (memorize, recall)
├── messages.go       # Message log operations (append, search)
├── lifecycle.go      # Lifecycle hooks (start, pressure, end, reset)
├── journal.go        # Writeback journal (stage, validate, commit)
├── policy.go         # Budget calculation, upgrade policy
├── fault.go          # Observable fault types and reporting
└── types.go          # Shared types (Page, Memory, Resolution, etc.)
```

### Integration Points in Existing Code

#### `session/session.go`

1. **`New()`** — Initialize memory store
   ```go
   memStore, err := memory.NewStore(filepath.Join(homeDir, ".crude", "memory.db"))
   ```

2. **`Run()` — Session start** — Auto-inject context
   ```go
   // After creating agent, before first turn:
   ctxSummary := memStore.GetContextInjection()  // last session summary + recent decisions
   msgsCopy = append([]provider.ChatMessage{{
       Role: "system",
       Content: ctxSummary,
   }}, msgsCopy...)
   ```

3. **`Run()` — Each turn** — Monitor budget
   ```go
   // After assembling prompt, check pressure:
   if memStore.Policy().BudgetPressure() > 0.7 {
       // Emit context_pressure signal via agent event
       tuiInst.SendAgentEvent("context_pressure", map[string]any{
           "usage_pct": 0.73,
           "budget": budget,
           "used": used,
       })
   }
   ```

4. **`Run()` — After agent completes** — Writeback
   ```go
   // Before truncation:
   memStore.Lifecycle().BeforeCompaction(s.sessionID)
   // ... then existing truncation logic
   ```

5. **`Close()`** — Lifecycle end
   ```go
   // Before closing:
   memStore.Lifecycle().SessionEnd(s.sessionID)
   memStore.Close()
   ```

#### `session/session.go` — ToolFn additions

In the `ToolFn` switch (line ~211), add new cases:

```go
case "memorize":
    return memory.HandleMemorize(s.memStore, params)
case "recall":
    return memory.HandleRecall(s.memStore, params)
case "pin_page":
    return memory.HandlePinPage(s.memStore, params)
case "degrade_page":
    return memory.HandleDegradePage(s.memStore, params)
case "context_status":
    return memory.HandleContextStatus(s.memStore, params)
```

---

## 10. Implementation Phases

### Phase 1: Foundation (Week 1)

- [ ] Add `modernc.org/sqlite` dependency to `go.mod`
- [ ] Create `memory/types.go` — shared types (Page, Memory, Resolution, PageType, PinLevel)
- [ ] Create `memory/store.go` — SQLite init, schema migrations, open/close
- [ ] Create `memory/pages.go` — CreatePage, GetPage, UpdateResolution, PinPage
- [ ] Create `memory/memories.go` — Memorize, Recall (FTS5 search)
- [ ] Create `memory/messages.go` — AppendMessage, SearchMessages
- [ ] Write tests for all CRUD operations

### Phase 2: Core Logic (Week 2)

- [ ] Create `memory/policy.go` — Budget calculation, utility scoring, upgrade selection
- [ ] Implement the two-phase prompt assembly (hard-pin guarantee → greedy upgrade)
- [ ] Create `memory/journal.go` — Three-phase writeback (stage → validate → commit)
- [ ] Create `memory/fault.go` — Observable fault types and reporting
- [ ] Create `memory/lifecycle.go` — Lifecycle hooks (Start, ContextPressure, BeforeCompaction, SessionEnd)
- [ ] Implement context injection (auto-retrieve last session + relevant decisions on start)
- [ ] Wire lifecycle hooks into `session/session.go`

### Phase 3: Agent Tools (Week 3)

- [ ] Add `memorize` tool handler to ToolFn
- [ ] Add `recall` tool handler to ToolFn
- [ ] Add `pin_page` tool handler to ToolFn
- [ ] Add `degrade_page` tool handler to ToolFn
- [ ] Add `context_status` tool handler to ToolFn
- [ ] Update system prompt to describe the new memory tools
- [ ] Add automatic session summarization on session end

### Phase 4: Polish (Week 4)

- [ ] TUI indicators for working-set pressure (status bar showing budget usage)
- [ ] `/memory` slash command for browsing memories
- [ ] Memory viewer in the TUI (search/browse)
- [ ] Configurable budget limits and page policies
- [ ] Migration path for existing session logs
- [ ] Performance benchmarks (sub-ms FTS5 target)
- [ ] Stress testing with adversarial workloads (ClawVM-style)

---

## 11. Open Questions / Future Work

### Semantic Correctness

ClawVM validates schema, provenance, scope, and non-destructiveness — but does NOT verify semantic truth. A model can produce a well-formed, properly scoped update that is factually wrong. Semantic verification could be layered on via domain-specific validation predicates on the writeback journal. For crude initially, we accept this limitation.

### Cross-Machine Sync

Engram supports git-based sync and cloud replication. For crude v1, local-only (single SQLite file). Future: `~/.crude/memory.db` could be synced via Dropbox/iCloud/git, or we could add an export/import mechanism.

### Vector Embeddings

FTS5-only for v1 (sub-ms BM25 search). Future: optional `sqlite-vec` extension for hybrid search (FTS5 + vector similarity). The schema already has extensible metadata fields.

### Multi-Project Isolation

Each project crude works on should have isolated memory. The `scope` column on pages and the `project` column on memories handle this. A global scope stores cross-project knowledge.

### Prompt Injection of Memories

How many memories to auto-inject on session start? ClawVM's approach: hard-pinned pages guaranteed, then greedy upgrade by utility. Default: inject last session summary + top 3 relevant decisions.

### Compaction Strategy

When does compaction fire? Options:
- Fixed threshold (e.g., 70% → warning, 100% → force)
- Adaptive (based on token growth rate)
- Agent-driven (agent calls `degrade_page` proactively)

Initial approach: 70% warning, 85% soft force, 100% hard force.

---

## References

1. **MemGPT: Towards LLMs as Operating Systems** — Packer, Wooders, Lin, Fang, Patil, Stoica, Gonzalez (2023)
   https://arxiv.org/abs/2310.08560

2. **ClawVM: Harness-Managed Virtual Memory for Stateful Tool-Using LLM Agents** — Rafique, Bindschaedler (2026)
   https://arxiv.org/abs/2604.10352

3. **Engram: Persistent Memory for AI Coding Agents** — Gentleman-Programming
   https://github.com/Gentleman-Programming/engram

4. **Recollect: Building Persistent Memory for AI Coding Assistants** — Jens Krämer (2026)
   https://jkraemer.net/2026/01/building-persistent-memory-for-ai-coding-assistants/

5. **Lost in the Middle: How Language Models Use Long Contexts** — Liu et al. (2023)
   https://arxiv.org/abs/2307.03172

6. **Why SQLite+FTS5 beats Vector DBs for AI Agent Memory** — DEV Community
   https://dev.to/fex_beck_27bfd4dccd05f062/why-sqlitefts5-beats-vector-dbs-for-ai-agent-memory-4inj
