# Crude Documentation

> Status: Current — this index describes the documentation set as it exists
> today; the acceptance gate enforces it.

This directory is the complete documentation set for Crude — a three-layer IDE
(`app/` → `workspace/` → `tui/`) with a native TUI component system, embedded
terminals, and a provider/agent layer.

Canonical startup instructions live in the root [`AGENTS.md`](../AGENTS.md).
It holds only cross-cutting rules and a topic map; detailed contracts live here.

## Layout

| Directory | Contents |
|-----------|----------|
| `architecture/` | How the system is built and why. Durable design documents that change only when the architecture changes. |
| `reference/` | Durable facts and contracts: catalogs, matrices, baselines, settings model, width rules. |
| `guides/` | How to do things: authoring components, running the design audit. |
| `plans/` | Active execution plans, handoffs, and checklists. Remove completed or superseded plans once current architecture/reference documents cover the result. |

## Index

Start with the root [`AGENTS.md`](../AGENTS.md); its topic map says which of these
documents to read before changing each area.

### Architecture

- [Code map](architecture/code-map.md) — which package owns each responsibility,
  and the key viewmodel files.
- [Shell host and frame pipeline](architecture/shell-host.md) — the root Host,
  shell surface, floating/docked overlay ownership, unconditional repaint and
  popup frames.
- [Compositing engine](architecture/compositing-engine.md) — cells, surfaces,
  per-cell compositing, floating windows and blit semantics.

### Reference — runtime and agents

- [Model prompts and project guidance](reference/prompt-architecture.md) — prompt
  ownership, bounded guidance intake, profile overlays, provider adapters,
  Responses delta uploads and continuity rules.
- [Adaptive context handling](reference/adaptive-context.md) — request budgeting,
  compaction, continuity handoff, memory jobs and harness comparison.
- [Agent orchestration](reference/agent-orchestration.md) — bounded delegation,
  child reporting, ownership and connections.
- [Unfinished run recovery](reference/run-recovery.md) — supervised attempts,
  provider failure classification, durable pending work and cooldowns.
- [Workspace collaboration runtime](reference/workspace-collaboration.md) —
  durable agents/runs, directed delivery, reports, controls and recovery.
- [Skills, MCP and capabilities](reference/capabilities.md) — registration,
  binding, acquisition approval, session gates and built-in sources.
- [Crude semantic control](reference/control-contract.md) — shared native/MCP
  command catalog, exact-shell targeting and truthful receipts.
- [Managed execution contexts](reference/managed-executions.md) — worktree
  baselines, exact run/tool roots, guarded integration and retention.
- [Background commands](reference/background-commands.md) — tracked command
  lifetime, termination, completion delivery and bounded output.
- [Session commands](reference/session-commands.md) — Plan mode, verified goals,
  core-owned scheduled loops, session fork and where commands live.
- [Local history](reference/local-history.md) — immutable snapshots,
  operation-scoped agent undo and precondition-checked restore.
- [Codex tool adapters](reference/codex-tools.md) — native patch, command,
  permission, wire and history contracts.
- [Network transport](reference/network.md) — routing, proxy, TLS trust, client
  identity, timeouts and stalled-connection recovery.

### Reference — core, shell and lifecycle

- [Core client protocol](reference/core-client-protocol.md) — connected CLI,
  retained actors, core/shell ownership, shared editors and SSH, with explicit
  acceptance limits.
- [Shell port](reference/shell-port.md) — the `shellport.Workspace` interface the
  TUI consumes and the matching `coreprotocol` methods.
- [Async ownership and lifecycle](reference/async-lifecycle.md) — the Update
  goroutine, generation boundaries, shutdown ordering and retained resources.
- [Settings layers](reference/settings.md) — the app → workspace → session model,
  agent settings, LSP settings and semantic settings control.
- [In-app terminals](reference/in-app-terminals.md) — libghostty-vt PTY engine,
  placement, focus, core lane and input backpressure.

### Reference — interface

- [Settings design language](reference/settings-design.md) — the one page
  anatomy, control dialect, help strip, footer and live store search.
- [Dialog and prompt design language](reference/dialog-design-language.md) —
  confirms and prompts.
- [Chat activity projection](reference/chat-activity.md) — tool activity,
  reasoning, activity strip, header/footer telemetry and live/hydrated parity.
- [Main view panes](reference/main-view-panes.md) — tab groups, splits, agent tab
  minimize, Git and local comparisons.
- [Editor and language servers](reference/editor.md) — editor geometry, motion,
  dirty/save authority, gutter and LSP transport.
- [Text input and composer](reference/text-input.md) — shared text controls,
  cursor, clipboard, composer pastes, attachments and prompt history.
- [Interaction matrix](reference/interaction-matrix.md) — keyboard/mouse
  contracts, collection navigation and file-tree geometry.
- [Control usage map](reference/control-usage.md) — control categories, tab bars,
  docks, hover and scrollbars.
- [Surface catalog](reference/surface-catalog.md) — every user-visible surface
  and where it lives in the code.
- [Terminal display width](reference/terminal-width.md) — the canonical Unicode
  column-measurement contract.
- [Performance baselines](reference/performance-baselines.md) — recorded paint
  and allocation numbers, CI-enforced ceilings and `CRUDE_PERF_LOG`.
- [Design token bypasses](reference/design-token-bypasses.md) — the frozen
  allowlist of theme-system bypasses, mirrored from
  [bypass-manifest.json](reference/bypass-manifest.json), which
  `scripts/tui_design_audit.sh` enforces.

### Guides

- [Validation and visual capture](guides/validation.md) — gates, test isolation,
  helper ownership and the Linux capture workflow.
- [Releases and installation](guides/releases.md) — native Linux/Windows packages,
  verified/instant release modes, beta-risk consent, setup advice, and
  public-launch requirements.
- [Website, shadcn/ui and GitHub Pages](guides/website.md) — React/Vite site,
  mandatory `@shadcn/lint` gate, local preview, command-only Pages deployment
  and custom-domain rules.
- [Component authoring](guides/component-authoring.md) — building TUI screens
  with the native `tui/ui` component system.
- [Core and shell workflows](guides/core-shell-workflows.md) — local/SSH
  commands, shared-editor recovery and review acceptance limits.
- [Capability workflows](guides/capability-workflows.md) — manual Skills/MCP,
  composer selection, Board, workspace Memory and Git review.
- [Windows visual capture](guides/windows-visual-capture.md) — native Windows
  Terminal launch, exact-HWND input, screenshots and cleanup.

### Plans (active work only)

- [First-use acceptance handoff](plans/broad-workspace-ux.md): remaining native
  Windows, distinct-host SSH, and reported memory-workload acceptance.
- [Agent experience roadmap](plans/agent-experience-roadmap.md) — vision and
  ordered roadmap for survivable, economical, frictionless agent work: working-set
  budgets, background compaction, lazy tool families, search providers, fast mode.
- [Agentic control, MCP capabilities, and worktree execution](plans/agentic-control-and-capabilities.md)
  — remaining integrated and native-platform acceptance.
- [Core protocol v2](plans/core-protocol-v2.md) — implemented refactor with measured
  local latency/GC/wire gates, full normal/race suites and native Linux visual/fuzz
  evidence. Native Windows, distinct-host/netem and the full per-feature visual
  matrix remain open; exact boundaries and implementation differences are recorded.
- [Core and shell separation](plans/core-shell-separation.md) — remaining native
  Windows, distinct-host and resource-baseline acceptance.
- [LSP acceptance](plans/lsp-acceptance.md) — process containment and
  installed-server acceptance.
- [Shell frame padding review](plans/shell-frame-padding-review.md) — open
  hardening findings for the configurable shell frame.
- [Tree picker and internal Fastwalk plan](plans/tree-picker-fastwalk-plan.md)
  — approved design replacing picker lists with bounded tree/path search.

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
   [model prompts and project guidance](reference/prompt-architecture.md); document
   tested guarantees and explicit limits separately.
10. **Cite only real paths.** A code span naming a repository file or directory
    must resolve; mention deleted files in plain text, not as code.
11. **Keep `AGENTS.md` lean.** It is injected into every agent request. Put
    area-specific contracts in the matching reference and add a topic-map line
    to `AGENTS.md`; only rules that apply to every change belong there.

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
- **Cited code paths** — a code span in `AGENTS.md` or `docs/` naming a
  repository file or directory must exist. Name deleted files in plain text.

A failure is a broken build: fix the link, the name, or the header — do not
weaken the gate.
