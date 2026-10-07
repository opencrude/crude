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
| `plans/` | Active execution plans, handoffs, and checklists. Remove completed or superseded plans once current architecture/reference documents cover the result. |

## Index

### Immediate action required

- [Runtime integrity action report](../IMMEDIATE-ACTION-REQUIRED.md) — portable,
  self-contained root handoff for unfinished runtime integrity; implementation
  and verification first, backed-up manual existing-database alignment last.

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
- [Unfinished run recovery](reference/run-recovery.md) — supervised root/child
  attempts, durable pending work, actionable pauses and the five-minute retry ceiling.
- [Network transport](reference/network.md) — application-owned connection,
  proxy, TLS trust and exact-host client identity.
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
- [Workspace collaboration runtime](reference/workspace-collaboration.md)
  — durable agents/runs, automatic directed delivery, report revisions, live-only
  broadcast, explicit context admission, Board history and recovery.
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
- [Windows visual capture](guides/windows-visual-capture.md) — safe native
  Windows Terminal launch, exact-HWND input, NirCmd screenshots, and cleanup.

### Plans (active work only)

- [Input admission and runtime integrity acceptance ledger](plans/input-admission-integrity-audit.md)
  — authoritative active checklist for Stop/context preservation, guidance
  preflight, child settlement, claims, retained output, collaboration waits,
  accepted-stream recovery and release proof; implementation remains incomplete.
- [Chat projection handoff](plans/chat-projection-handoff.md) — **ACTIVE
  HANDOFF** for in-flight chat-projection work.
- [Shell frame padding review](plans/shell-frame-padding-review.md) — open
  hardening findings for the configurable shell frame padding feature.
- [End-to-end reliability and interaction hardening](plans/end-to-end-hardening-plan.md)
  — **ACTIVE** umbrella execution plan for transport liveness, safety,
  projections, shared interaction controls, editor latency, and undo delivery.
- [Tree picker and internal Fastwalk plan](plans/tree-picker-fastwalk-plan.md)
  — **IN PROGRESS, APPROVED** design for preserving the current New
  Workspace UI while replacing picker lists with bounded tree/path search.

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
   filename. Link the current architecture/reference contract, not a removed
   migration plan.
5. **Index everything.** New documents must be added to the index above (or to
   a sub-index) in the same change.
6. **Keep only actionable plans.** Once implementation lands and current
   architecture/reference documents describe the result, delete the obsolete
   execution plan instead of retaining duplicate archaeological truth.
7. **JSON manifests belong in `reference/`.** Enforcement artifacts consumed
   by scripts (e.g. `bypass-manifest.json`) are reference material, not
   narrative docs.
8. **No orphan docs.** Every file under `docs/` must be reachable from this
   index. `docs_check.sh` fails on orphans.
9. **One current contract per topic.** Link current references from related docs
   and active plans instead of copying policy or silently treating a plan as
   implementation truth. Prompt changes must update
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
