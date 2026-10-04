# Crude Documentation

> Status: Current — this index describes the documentation set as it exists
> today; the acceptance gate enforces it.

This directory is the complete documentation set for Crude — a three-layer IDE
(`app/` → `workspace/` → `tui/`) with a native TUI component system, embedded
terminals, and a provider/agent layer.

Canonical startup instructions live in the root [`AGENTS.md`](../AGENTS.md).
Read that first; this index only organizes the reference material.

## Layout

| Directory | Contents |
|-----------|----------|
| `architecture/` | How the system is built and why. Durable design documents that change only when the architecture changes. |
| `reference/` | Durable facts and contracts: catalogs, matrices, baselines, settings model, width rules. |
| `guides/` | How to do things: authoring components, running the design audit. |
| `plans/` | Time-stamped work: plans, handoffs, checklists. Read for history and context, not as current truth — every plan is superseded by the code eventually. |

## Index

### Architecture

- [Compositing engine](architecture/compositing-engine.md) — the multi-window
  compositor: surface tree, per-cell compositing, floating windows, blit
  semantics.

### Reference

- [Model prompts and project guidance](reference/prompt-architecture.md) - the
  canonical ownership/development contract, direct package APIs, bounded
  instruction intake, profile overlays, provider adapters, and continuity rules.
- [Adaptive context handling](reference/adaptive-context.md) — request budgeting,
  complete oversized-source extraction, atomic recovery, and harness comparison.
- [Agent orchestration](reference/agent-orchestration.md) — bounded delegation,
  evidence-based handoffs, completion delivery, and links to the canonical prompt
  architecture contract.
- [Codex tool adapters](reference/codex-tools.md) — native patch, command,
  permission, wire, and history contracts under the canonical Crude prompt.
- [Chat activity projection](reference/chat-activity.md) — grouped, expandable
  tool activity with matching live and recovered transcript behavior.
- [Terminal display width](reference/terminal-width.md) — canonical Unicode
  column-measurement contract shared by layout, surfaces, ultraviolet, and the
  renderer.
- [Surface catalog](reference/surface-catalog.md) — every user-visible surface
  and where it lives in the code.
- [Interaction matrix](reference/interaction-matrix.md) — keyboard/mouse
  contracts the component system must preserve.
- [Control usage map](reference/control-usage.md) — control categories and
  their `tui/ui` implementations.
- [Settings layers](reference/settings.md) — the three-level settings model
  (app → workspace → session) and what each layer owns.
- [Workspace board library](../board/README.md) — membership, retry deduplication,
  explicit durable context acceptance, human unread, search and archival.
- [In-app terminals](reference/in-app-terminals.md) — the embedded terminal
  multiplexer: libghostty-vt PTY engine, pinned Ghostty environment.
- [Performance baselines](reference/performance-baselines.md) — recorded paint
  and allocation numbers; leaf allocation ceilings (`scripts/perf_ceilings.txt`)
  and per-frame work-budget tests are both CI-enforced via `make bench-gate`,
  and live runs can be watched with `CRUDE_PERF_LOG`.
- [Design token bypasses](reference/design-token-bypasses.md) — the frozen
  allowlist of theme-system bypasses, mirrored from
  [bypass-manifest.json](reference/bypass-manifest.json).
- [bypass-manifest.json](reference/bypass-manifest.json) — machine-readable
  allowlist enforced by `scripts/tui_design_audit.sh`.
- [Dialog & prompt design language](reference/dialog-design-language.md) —
  the authoritative visual/interaction language for confirms and prompts.

### Guides

- [Component authoring](guides/component-authoring.md) — recipes for building
  TUI screens with the native `tui/ui` component system.
- [Capability workflows](guides/capability-workflows.md) — manual Skills/MCP,
  composer selection, Board, workspace Memory, Git review and validated limits.

### Plans (historical — check the status header before acting on them)

- [Workspace refactor plan](plans/workspace-refactor-plan.md) — the three-layer
  `app/` → `workspace/` → `tui/` split. **DONE.**
- [TUI component system plan](plans/tui-component-system-plan.md) — the
  component-system and design-language migration. **DONE 2026-08-03.**
- [Phase 15 checklist](plans/phase15-checklist.md) — stabilization checklist
  (lint gate, bench gate, race suites). **DONE.**
- [Terminal integration plan](plans/terminal-integration-plan.md) —
  libghostty-vt terminal integration. **LANDED.**
- [Terminal diagnostics handoff](plans/terminal-diagnostics-handoff.md) —
  terminal defect resolution record. **RESOLVED.**
- [Memory architecture proposal](plans/memory-architecture-proposal.md) —
  superseded proposal; native workspace memory is implemented.
- [Agent capabilities, shared board, and evidence-based memory plan](plans/agent-capabilities-board-memory-plan.md)
  — **LANDED** core runtime/product implementation and acceptance for skills,
  managed MCP, shared communication and evidence memory informed by Kilo and RRSI.
- [Workspace tools, capabilities, and TUI interaction plan](plans/workspace-tools-capabilities-tui-plan.md)
  — **LANDED** revision 2 core: calmer Git sidebar with main-area review,
  Skills/MCP settings catalogs and manual commands, terminal rail removal,
  workspace Memory footer access, dock ownership and interaction acceptance.
- [Shared board live communication assessment](plans/shared-board-live-assessment.md)
  — **DONE** two live agents: board-only behavior revision, sibling verification,
  authority boundaries, timing/overhead, and coordination limitations.
- [Workspace collaboration runtime](plans/workspace-collaboration-runtime-plan.md)
  — **IN PROGRESS** single-cutover specification for workspace-wide agents,
  unified delivery, reports, controls, recovery and exact live/hydration parity.
- [Provider config plan](plans/provider-config-plan.md) — superseded by the
  capability-based provider protocol rework.
- [Chat projection handoff](plans/chat-projection-handoff.md) — **ACTIVE
  HANDOFF** for in-flight chat-projection work.
- [Shell frame padding review](plans/shell-frame-padding-review.md) — open
  hardening findings for the configurable shell frame padding feature.
- [Message-scoped undo and local history plan](plans/undo-local-history-plan.md)
  — **LANDED** JetBrains-quality, cache-safe workspace history and
  per-user-message restore.
- [End-to-end reliability and interaction hardening](plans/end-to-end-hardening-plan.md)
  — **ACTIVE** umbrella execution plan for transport liveness, safety,
  projections, shared interaction controls, editor latency, and undo delivery.
- [Render authority and editor scrolling hardening](plans/render-authority-hardening-plan.md)
  — **LANDED** single-authority plan for frame coalescing, warm tab
  transitions, cursor cadence, and document-wide horizontal scrolling.
- [Editor gutter and non-disruptive autosave](plans/editor-gutter-autosave-plan.md)
  — **LANDED** exact Git hunks, native gutter inspection, revision-aware
  asynchronous saves, shared mutation ownership, and opt-in autosave without
  LSP or watcher interference.
- [Tree picker and internal Fastwalk plan](plans/tree-picker-fastwalk-plan.md)
  — **IN PROGRESS, APPROVED** design for preserving the current New
  Workspace UI while replacing picker lists with bounded tree/path search.
- [Status system overhaul](plans/status-system-overhaul-plan.md)
  — **LANDED**: active-item
  header, preserved quiet footer, status inspection, scoped accounting, health,
  operations, notifications, and selective hints.
- [Context compaction and runtime continuity](plans/context-compaction-runtime-plan.md)
  — **LANDED**: provider-grounded context accounting, four-stage Aeron
  extraction with code-authored root and child checkpoints, durable boundary
  lifecycle, typed runtime messages, memory boundaries, and passive
  resume for new sessions.

## Documentation rules

These rules are enforced by `scripts/docs_check.sh` (run in CI, `make
docs-check`). A docs change that breaks them fails the build.

1. **Categorize by kind, not by team.** New documents go in
   `architecture/`, `reference/`, `guides/`, or `plans/` by what they
   describe, not by who wrote them.
2. **kebab-case filenames.** Lowercase, hyphen-separated, no spaces, no
   `SCREAMING_CASE`, no underscores. The filename should describe the
   document (`terminal-width.md`, not `TERMINAL_WIDTH.md`).
3. **Every document has a status header.** Line 1 is the `#` title; a
   `> Status: …` blockquote must follow within the first 5 lines and
   contain one of: `Current`, `Current reference`, `Current architecture`,
   `ACTIVE HANDOFF`, `DONE`, `LANDED`, `RESOLVED`, `SUPERSEDED`,
   `IN PROGRESS`.
4. **Cross-reference by path, not by bare filename.** Links must resolve
   from the file's own location (relative paths) and use the current
   filename. A link to `PLAN.md` instead of `plans/workspace-refactor-plan.md`
   is broken documentation.
5. **Index everything.** New documents must be added to the index above (or to
   a sub-index) in the same change.
6. **Plans are append-only history.** Do not delete completed plans — move
   them to `plans/`, mark them DONE/LANDED/RESOLVED/SUPERSEDED in the status
   header, and stop treating them as current truth. New work gets a new plan
   document.
7. **JSON manifests belong in `reference/`.** Enforcement artifacts consumed
   by scripts (e.g. `bypass-manifest.json`) are reference material, not
   narrative docs.
8. **No orphan docs.** Every file under `docs/` must be reachable from this
   index. `docs_check.sh` fails on orphans.
9. **One current contract per topic.** Link current references from related docs
   and historical plans instead of copying policy or silently treating old plans
   as implementation truth. Prompt changes must update
   [model prompts and project guidance](reference/prompt-architecture.md) and the
   root agent rules; document tested guarantees and explicit limits separately.

## Acceptance gate

`scripts/docs_check.sh` enforces, on every CI run:

- **Naming** — all doc files are kebab-case.
- **Status headers** — every `.md` under `docs/` carries a `> Status:` header.
- **Links** — every relative `.md` link resolves to an existing file.
- **Cross-references from code** — doc references in Go comments, shell
  scripts, workflow YAML, and vendored `go.mod` comments point at existing
  files under `docs/`.
- **Orphans** — every file under `docs/` is listed in this index.
- **Stale references** — no reference to a moved/renamed legacy doc name
  (`TERMINAL_WIDTH.md`, `docs/tui/…`, etc.).

A failure is a broken build: fix the link, the name, or the header — do not
weaken the gate.
