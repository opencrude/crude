# Crude — Agent Working Rules

This file is the startup contract for every agent working in this repository.
It is injected into each request, so it holds only rules that apply to every
change plus a map to the detailed contracts. Area-specific contracts live in
[`docs/`](docs/README.md); read the ones your change touches before editing.

## Boot contract

1. Run `git status --short`; preserve unrelated user and agent changes.
2. Read this file, then the topic-map entries for every area you will touch.
   Read only the documents and sections relevant to the decision at hand; do not
   recursively load every linked document.
3. Discover code with search tools over the repository. Ignore `.claude/`,
   `third_party/` (unless the change is there) and build output.

## Engineering standard

Crude is held to production IDE standards. A change is complete only when the
system is coherent, dependable in real workloads, understandable to future
maintainers and pleasant to use. A narrow passing test is necessary, never
sufficient.

### Diagnose before changing

- Reproduce the user-visible failure through the real product path, preserving
  the workload shape that exposes it: resumed dense sessions, paced pointer
  input, nested panes, narrow terminals, dirty buffers, concurrent agents,
  cross-platform filesystems.
- Measure the stage the user says is slow or broken. Separate mutation,
  derivation/reflow, presentation, persistence and asynchronous completion. A
  proxy counter is not the outcome ("zero message renders" did not prove dense
  resize was fast while an O(N) export still ran each frame).
- Use profiles and call paths when cost scales with data; compare empty and
  dense/hydrated workloads.
- The user's observation is acceptance evidence. If the reported defect is still
  visible, the issue is open, whatever the tests say.
- When a fix does not improve the measured result, say so, remove the false
  assumption and go to the actual owner instead of layering patches.

### Choose the right-sized fix

- **No backward compatibility during active development.** Internal contracts and
  persisted formats may change incompatibly. Do not add upgrades, legacy readers,
  fallback adapters or compatibility branches.
- **Breaking a format never permits losing work.** Back up affected local data,
  align it manually outside product code, and verify content, identity,
  provenance, history and resume. One-off conversion tooling does not stay in
  the codebase.
- **No destructive Git rollbacks.** Never use `git checkout --`, `git reset`,
  `git restore`, `git clean` or equivalents to undo work. Reverse only your own
  edits by hand and preserve everything else.
- Check cost against benefit before broad work. Use the smallest correction when
  one owner is clearly wrong and the invariant can be guarded; research
  established designs when several caches, clocks or layers compete.
- **One authoritative owner per state.** Layout computes geometry, configurators
  publish it, projections derive rows, screens paint them; no second layer
  writes the same state in a frame.
- Eliminate bug classes, not screenshots: shared primitives must cover every
  consumer, and no second interaction, cursor, scrollbar, persistence or
  rendering path may remain to drift.

### End-to-end means end-to-end

- Follow the whole lifecycle: create, update, render, persist, resume, switch
  workspace/session/tab, resize, cancel, fail, retry, restore, prune, shut down.
  Verify generation boundaries and stale asynchronous results.
- Correctness includes focus/capture, loading/empty/error states, confirmations,
  recovery, discoverability and clear status. A backend API without usable
  product UX is not a finished feature.
- Safety-sensitive operations need immutable preview, precondition checks,
  transactional rollback, durable audit context and an undo path. Fail closed on
  ambiguity; never guess ownership or overwrite later work.
- Moving blocking work into another callback without ownership, cancellation and
  generation checks is not asynchronous architecture.

### Performance

- Optimize measured work, not frame suppression. Model mutations are never
  dropped, delayed or slowly replayed; presentation coalesces independently and
  commits the latest state within 16 ms.
- Never lock frames at zero FPS: idle cadence stays at least 2 FPS.
- Cache derived data by explicit source/geometry/theme generations. Focus, tab
  selection, ticks and presenting a frame are not invalidation.
- Add the benchmark that would have caught a regression before optimizing; gate
  mutation and presented-frame latency separately with dense content. Never
  loosen a gate to land a regression. If a required feature has an irreducible
  measured cost, document why and set the new ceiling from repeated
  measurements with limited headroom.
- CPU, allocation, memory, I/O and startup all count. Avoid per-item
  maximum-size buffers, per-frame O(N) exports, eager offscreen reflow and
  repeated verification of immutable data in hot paths.

### Validation and accountability

- Every regression test must fail for the original reason and assert the user
  outcome as well as internal state.
- Run focused tests, affected packages, race paths, build, lint, docs and bench
  gates, and the full suite when feasible; cross-compile cross-platform code.
  See [validation](docs/guides/validation.md).
- Use the repository visual capture workflow for UI acceptance at desktop and
  constrained sizes: `scripts/tuishot.sh` on Linux, `scripts/tuishot.ps1` on
  Windows. It drives the real product and screenshots it; do not search for or
  install a screenshot or computer-use capability instead. Never run two
  captures at once; close every capture window.
- Never call a task complete while a known reproduction still fails. Report
  blocked or aborted gates explicitly.
- Keep architecture docs, plans, baselines and this file synchronized with the
  code. A stale invariant is a future defect.

## Architecture

```
app/        IDE shell: CLI, app config, core spawning, workspace catalog.
            No session, agent or orchestrator knowledge.
core/       Per-user core: retained workspace actors, serialized ingress,
            receipts. Shells attach over typed RPC (coreprotocol/corewire).
workspace/  Per-project state machine: sessions, agents, documents, LSP,
            files, terminals, Git, Local History. Never imports crude/tui.
tui/        Renderer and input router. Holds a shellport.Workspace, calls its
            methods, renders events. Owns no domain persistence.
types/      Neutral contracts shared by every layer.
```

- **TUI → workspace** is method calls; **workspace → TUI** is the event bus. No
  callbacks between layers. Presentation-neutral contracts live in `types/` or
  workspace view data. Swapping Bubble Tea for a web UI must leave
  `workspace/` and `app/` unchanged.
- Every CLI shell attaches to the user's core through `shellport/remote`;
  closing a shell detaches it and never stops sessions. Never attach two Apps to
  one mutable Workspace.
- The shell (`App`) owns selected session, agent, focus and tab visibility; the
  workspace owns inventory and canonical state. Neither keeps a shared "active"
  field for the other.
- The TUI is one component tree on one root `ui.Host` painted into one shell
  surface every frame; floating windows use `ui.WindowFrame`; session decks use
  `PanelDeck`.

Package ownership is in the [code map](docs/architecture/code-map.md).

## Rules that apply to every change

- **Never block Bubble Tea's `Update` goroutine.** No file, clipboard, Git, DB,
  network or subprocess I/O there; `EventBus.Send` is synchronous and must not be
  called from code already running on `Update`. Every async result carries an
  owner and generation and is dropped when stale.
- **Workspace switch and shutdown are generation boundaries**: stop watchers,
  cancel work, invalidate callbacks, then rebuild; shutdown joins what it owns.
- **The agentic loop is sacred.** Tool-level failures are tool results the model
  handles; only run-level faults stop a run, and stopped work stays recoverable.
- **Model instruction text lives only in `prompt/`.** Tool schemas and results
  live with their tools; provider differences live in the narrowest adapter.
- **Width:** `VisibleWidth` (shared grapheme profile) is the only layout width
  API; never use `uniseg`/`runewidth` directly or add platform corrections.
- **Theme:** hard-coded hex only in `tui/theme/`; raw ANSI SGR only in
  `tui/cell`, `tui/compositor`, `tui/rendering`. Use design tokens elsewhere.
- **Child bounds are parent-local** in both layout and paint.
- **Crude data lives under `buildinfo.Home`** (`CRUDE_HOME`); never call
  `os.UserHomeDir` for Crude paths. Tests that can reach it use
  `internal/testhome`.
- **Never print raw JSON tool arguments** in any title or status;
  `tools.SummarizeArgs` is the single operand authority.
- **Automatic retries** go through `retry/` (five-minute per-wait ceiling);
  replay safety stays with the operation owner.

## Topic map

Read the listed document before changing the area. Each line names the rule
most often broken there.

| Area | Read | Most-broken rule |
|---|---|---|
| Plan mode, goals, fork, slash commands, palette | [session commands](docs/reference/session-commands.md) | Runtime enforces modes; only the verifier's verdict completes a goal |
| Prompts, guidance intake, instruction text | [prompt architecture](docs/reference/prompt-architecture.md) | One policy for every model; overlays are escaped and lower priority |
| Context budget, compaction, continuity, memory jobs | [adaptive context](docs/reference/adaptive-context.md) | Never delete history to fit; never replay accepted output |
| Children, delegation, reports | [agent orchestration](docs/reference/agent-orchestration.md) | `publish_report` is mandatory; child connections are complete |
| Run recovery, provider failures, stops | [run recovery](docs/reference/run-recovery.md) | Classify permanent only on positive evidence |
| Agent collaboration, Board, claims | [workspace collaboration](docs/reference/workspace-collaboration.md) | Delivery is automatic at safe boundaries; no polling |
| Skills, MCP, capability grants | [capabilities](docs/reference/capabilities.md), [workflows](docs/guides/capability-workflows.md) | Application-bound by default; approval binds exact bytes |
| Crude self-control tools | [control contract](docs/reference/control-contract.md) | Never replay or retarget an uncertain receipt |
| Worktrees, managed execution | [managed executions](docs/reference/managed-executions.md) | Worktrees are not sandboxes |
| Background commands | [background commands](docs/reference/background-commands.md) | Signal only the exact retained command tree |
| Local History, undo, restore | [local history](docs/reference/local-history.md) | Preview first; mandatory safety snapshot; fail closed |
| Network, timeouts, TLS | [network](docs/reference/network.md) | Proxy failure never falls back to direct |
| Core/shell protocol, shared documents | [core client protocol](docs/reference/core-client-protocol.md), [shell port](docs/reference/shell-port.md) | Shells are detached mirrors, never a second authority |
| Async I/O, workspace switch, shutdown | [async lifecycle](docs/reference/async-lifecycle.md) | Owner + generation on every async result |
| Settings model and persistence | [settings](docs/reference/settings.md) | Only explicit choices are stored; absent fields inherit live |
| Settings pages | [settings design](docs/reference/settings-design.md) | One page anatomy and control dialect |
| Shell layout, Host, overlays, frames | [shell host](docs/architecture/shell-host.md), [compositing engine](docs/architecture/compositing-engine.md) | No frame-level dirty flag; parent-local rects |
| New screens and components | [component authoring](docs/guides/component-authoring.md), [control usage](docs/reference/control-usage.md), [surface catalog](docs/reference/surface-catalog.md) | One shared `ui.Tabs` look; no bespoke controls |
| Keyboard/mouse behavior | [interaction matrix](docs/reference/interaction-matrix.md) | Esc is back; Ctrl+C is copy |
| Dialogs and confirms | [dialog design language](docs/reference/dialog-design-language.md) | Use the shared confirm frame |
| Main panes, tabs, agent tabs | [main view panes](docs/reference/main-view-panes.md) | Closing an agent tab minimizes, never terminates |
| Chat transcript, tool rows, activity strip | [chat activity](docs/reference/chat-activity.md) | Live and hydrated rows must paint identically |
| Composer, text fields, clipboard, cursor | [text input](docs/reference/text-input.md) | Lifecycle events never consume the draft |
| Editor, gutter, save, LSP | [editor](docs/reference/editor.md) | Dirty means byte identity; LSP pipes never block |
| Embedded terminals | [in-app terminals](docs/reference/in-app-terminals.md) | One parser in the core; PTY writes never block |
| Unicode width, ANSI | [terminal width](docs/reference/terminal-width.md) | One shared width profile |
| Performance work | [performance baselines](docs/reference/performance-baselines.md) | Benchmark first; never raise a ceiling to land |
| Performance dashboard, `metrics/` | [performance baselines](docs/reference/performance-baselines.md#performance-dashboard-and-dense-session-freezes-2026-10-22) | An observation never allocates or does I/O; snapshot only while visible |
| Gates, tests, visual capture | [validation](docs/guides/validation.md), [Windows capture](docs/guides/windows-visual-capture.md) | Never two captures at once; close the window |

Active plans are listed in the [docs index](docs/README.md#plans-active-work-only).
Treat a plan as remaining work, not as a description of current behavior.

## Gates

```bash
go build ./...
go test ./<affected>/... && go test ./... -count=1 -timeout=180s
golangci-lint run ./...
make design-audit docs-check bench-gate
```

Do not update golden output unless the visual change is intentional and
verified. Documentation rules are in the [docs index](docs/README.md#documentation-rules):
when a contract changes, update its reference in the same change and keep this
file to cross-cutting rules and topic-map lines.
