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
- [In-app terminals](reference/in-app-terminals.md) — the embedded terminal
  multiplexer: libghostty-vt PTY engine, pinned Ghostty environment.
- [Performance baselines](reference/performance-baselines.md) — recorded paint
  and allocation numbers; ceilings are CI-enforced via `make bench-gate`.
- [Design token bypasses](reference/design-token-bypasses.md) — the frozen
  allowlist of theme-system bypasses, mirrored from
  [bypass-manifest.json](reference/bypass-manifest.json).
- [bypass-manifest.json](reference/bypass-manifest.json) — machine-readable
  allowlist enforced by `scripts/tui_design_audit.sh`.

### Guides

- [Component authoring](guides/component-authoring.md) — recipes for building
  TUI screens with the native `tui/ui` component system.

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
- [Provider config plan](plans/provider-config-plan.md) — superseded by the
  capability-based provider protocol rework.
- [Chat projection handoff](plans/chat-projection-handoff.md) — **ACTIVE
  HANDOFF** for in-flight work; the only plan describing current work.
- [Shell frame padding review](plans/shell-frame-padding-review.md) — open
  hardening findings for the configurable shell frame padding feature.

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
