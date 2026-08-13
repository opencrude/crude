# Terminal Integration Repair Plan

Status: IMPLEMENTATION LANDED (2026-08-13). The libghostty-vt engine, the
workspace terminal dock, reversible float/dock, the pinned terminfo runtime
contract, the idle 60 Hz fix, the OSC-title deadlock fix, selection/copy,
scrollback, mouse reporting, focus reporting, OSC 52, bell, OSC 7, and the
PTY process-group shutdown are all implemented and tested. See
`docs/tui/TERMINALS.md` for the current contract; this file is retained as
design context and research record.

Date: 2026-08-13

This document is the continuation contract for repairing Crude's in-app
terminal integration. It records the repository audit and upstream research so
future work does not repeat discovery or make a narrow dock-only fix.

## User Goal

The terminal feature must become a practical embedded terminal backed by real
`libghostty-vt`, shown on demand in its own workspace-level dock position with
terminal tabs. Any terminal can move from that dock into the existing floating
window system and dock back again without restarting its PTY. Floating windows
must retain the current WindowManager z-order, focus, drag, and resize behavior.

The user explicitly rejected the current terminal itself as unusable, not just
its placement. Both the emulation/input core and the shell integration must be
fixed and visually verified with `scripts/tuishot.sh`.

## Non-Negotiable Architecture

- `workspace.TerminalManager` continues to own workspace-scoped PTY lifecycle,
  I/O, size, placement snapshots, and terminal events. `workspace/` must not
  import `crude/tui`.
- `tui/term` owns the `libghostty-vt` bridge and conversion of Ghostty render
  state into Crude `CellBuffer` cells.
- A new workspace-level terminal dock owns docked terminal tabs. It must not be
  stored in a session projection or share the session `PanelDeck` used for AI
  settings, questions, confirmation, YOLO, and background jobs.
- `OverlayHost` + `wm.FloatingWindow` + `ui.WindowFrame` remain the floating
  terminal path. Existing WindowManager z-order and `FocusWindow` /
  `BringToTop` behavior must be preserved.
- Dock and float are two placements of the same retained terminal screen and
  PTY. Moving between them must not replay output, replace the emulator, or
  restart/kill the child process.
- The terminal dock is a shell slot under the one root `ui.Host`, painted into
  `mainSurf`. Do not reintroduce independent persistent shell surfaces or a
  frame-level content dirty cache.
- Host geometry is the one input/paint geometry source. Do not add manual
  screen-to-dock mouse translations outside a small mount adapter when a
  retained child Host truly requires one.
- All terminal colors and dock chrome use semantic theme tokens. Terminal
  cells retain application-provided RGB/palette styles while default
  foreground/background resolve against Crude's active theme.
- All cell widths must remain compatible with `TERMINAL_WIDTH.md` and Crude's
  shared grapheme-width path.

## What Exists Today

### PTY/domain layer

- `workspace/terminal.go` implements `TerminalManager` and
  `TerminalPlacement` (`TerminalDocked`, `TerminalFloating`).
- `workspace/terminal_pty_unix.go` uses `github.com/creack/pty` and starts the
  user's login shell. Windows currently returns an unsupported error.
- The manager publishes `TerminalOutputMsg`, `TerminalExitedMsg`,
  `TerminalClosedMsg`, and `TerminalTitleMsg` through the workspace event bus.
- `workspace/host.go` sets in-app child environment values including
  `TERM=xterm-ghostty`, `TERM_PROGRAM=ghostty`, and the pinned Ghostty version.

### Current terminal core

- `tui/term/vt.go`, `vt_csi.go`, and `vt_sgr.go` are a small hand-written VT
  emulator using only the vendored x/ansi parser.
- `tui/term/keys.go` is a hand-written legacy xterm key table.
- `tui/screens/shellterm/term.go` owns the emulator, paints cells, sends key
  and paste bytes, resizes the PTY, and exposes Float/Dock/Close actions.

### Current placement

- `tui/viewmodel/terminals.go` inserts docked terminals into the active AI
  session's `ui.PanelDeck` under IDs such as `terminal/t1`.
- The same file promotes a terminal into a dynamic OverlayHost ID such as
  `terminal/t1`; the floating path uses a `FloatingWindow` and
  `WindowFrameWidget`.
- `tui/viewmodel/activity.go` lists all workspace terminals and focuses either
  the docked or floating placement.
- `tui/viewmodel/terminals_test.go` currently tests PanelDeck-to-overlay moves
  and floating z-order.

## Verified Root Causes

### 1. The implementation is not libghostty

The current docs say this directly: `docs/tui/TERMINALS.md` states that
`libghostty-vt` is not linked. The implementation only uses Ghostty's terminfo
name and a partial parser. Advertising `TERM=xterm-ghostty` therefore promises
capabilities the embedded terminal does not implement.

### 2. The VT emulator is too incomplete for interactive applications

The current parser lacks or mishandles practical terminal requirements,
including:

- Terminal-generated responses back to the PTY for device attributes, status,
  mode, size, and version queries.
- Application cursor-key and keypad modes.
- Kitty keyboard protocol and xterm `modifyOtherKeys` state.
- Bracketed-paste mode and Ghostty's paste sanitization/encoding rules.
- Mouse tracking encoders and terminal-aware wheel routing.
- Scrollback and viewport scrolling.
- Correct reflow during resize.
- A broad set of DEC/xterm modes and editing/control sequences.
- Efficient render-state iteration over full grapheme clusters and resolved
  terminal styles.

Full-screen programs can probe for responses that never arrive and receive key
sequences inconsistent with modes they enabled. This explains why the terminal
can render simple `cat` output in tests but is not usable as a shell/TUI.

### 3. The dock has the wrong owner

Docked terminals are currently put in `activeSessionDeck()`. This couples a
workspace PTY to whichever AI session happens to be active and makes terminals
compete with questions, confirmations, settings, YOLO, and background-job
panels for one region. Switching sessions can hide or rehome a terminal even
though its manager and activity-list identity are workspace-scoped.

### 4. New terminals can float merely because no AI deck exists

`openNewTerminal` currently docks only when `activeSessionDeck()` exists and
otherwise promotes immediately. A terminal dock should be available in the
workspace launcher, editor, and chat regardless of AI session existence.

### 5. Tests prove plumbing, not usability

Existing terminal tests cover basic text, SGR, CUP, alternate screen, title,
simple key encoding, PTY create/write/resize, dock/promote, and z-order. They do
not run a probing/full-screen terminal application, verify PTY query replies,
exercise mode-aware input, test bracketed paste or scrollback, or paint the
docked/floating terminal through the complete shell.

## Upstream libghostty Research

Research was performed against upstream on 2026-08-13.

- Ghostty checkout inspected at commit
  `d2c70a8c7b9b6893c13640c02d7b6f9a1624f3f0` (`1.3.2-dev`).
- `libghostty-vt` is a zero-dependency C/Zig terminal-state library. It does
  not require Ghostty's GPU renderer and is suitable for painting into Crude's
  cell buffer.
- Upstream's current C API is explicitly unstable, so Crude must pin an exact
  revision and isolate it behind a small local interface.
- Ghostty requires Zig 0.16.0 at the inspected revision. The local machine did
  not initially have Zig, Ghostty, or a system `libghostty-vt` pkg-config entry.
- Building from source is documented as:

  ```bash
  zig build -Demit-lib-vt -Doptimize=ReleaseFast --prefix <prefix>
  ```

- The build installs headers, shared/static libraries, and pkg-config entries
  including `libghostty-vt-static`.
- The official-in-practice Go binding inspected was
  `go.mitchellh.com/libghostty` at commit
  `56450a2c8c38e88b3fc306f9c4529ca554a0ce29`. Its module currently requires
  Go 1.26 while Crude uses Go 1.25.8/1.25.9. It also expects an installed
  `libghostty-vt-static` through pkg-config.
- Do not blindly add the current Go binding until the Go-version/build
  contract is resolved. Viable options are a pinned earlier binding commit or
  a minimal Crude-owned cgo adapter against the pinned C API. A local adapter
  is likely smaller and gives Crude control over the exact exposed surface.

The inspected upstream API provides everything required:

- Terminal creation, resize, VT writes, reset, scrollback limits, viewport
  scroll, terminal mode queries, title, active screen, and scrollbar data.
- Effect callbacks, especially `write_pty`, title change, size, XTVERSION,
  device attributes, color scheme, bell, clipboard, and unknown sequences.
- `GhosttyRenderState`, row iterator, cell iterator, resolved foreground and
  background colors, full grapheme extraction, cursor position/visibility/style,
  selection, and dirty state.
- Terminal-aware key encoder supporting application modes, Kitty keyboard,
  `modifyOtherKeys`, alt behavior, and backarrow mode.
- Paste encoder that follows bracketed-paste state and sanitizes unsafe control
  data.
- Terminal-aware mouse encoder and tracking state.

Useful upstream references:

- `https://github.com/ghostty-org/ghostty/blob/main/include/ghostty/vt.h`
- `https://github.com/ghostty-org/ghostty/blob/main/example/c-vt-render/src/main.c`
- `https://github.com/ghostty-org/ghostling`
- `https://github.com/mitchellh/go-libghostty`

## Dependency Decision

Settled 2026-08-13.

- Pin Ghostty commit `d2c70a8c7b9b6893c13640c02d7b6f9a1624f3f0` (`1.3.2-dev`)
  with Zig 0.16.0. Recorded in `deps/GHOSTTY_PIN`. Bootstrap:
  `scripts/bootstrap_libghostty.sh` installs `libghostty-vt` into gitignored
  `deps/libghostty-vt/`.
- `go.mitchellh.com/libghostty` at `56450a2` requires **Go 1.26** and wraps
  the whole unstable C API (~80 files). Crude is Go 1.25, so that module is
  not used.
- Crude owns a narrow cgo adapter in `tui/term`. `CGO_ENABLED=0` compiles
  `stub.go`; `NewEmulator` returns `ErrNoCGO`. There is no hand-written VT
  fallback.
- `TERM=xterm-ghostty` and `TERM_PROGRAM_VERSION=1.3.0` stay as the host pin.
  Logical cell pixels for size reports are 8×16.

## Target TUI Design

### Dedicated terminal dock

Add one workspace-level retained dock, independent of all session projections.
It should have:

- A stable shell component/mount, for example `shellTerminalDockSlot`.
- A terminal-only tab strip using `ui.Tabs` with close affordances and active
  tab retention.
- One active retained `shellterm.Screen` at a time; inactive terminal screens
  retain emulator/scroll state but are not painted.
- A persisted or at least viewmodel-retained user height, clamped on resize.
  A shared `ui.SplitModel`/`ui.SplitDivider` should resize the dock vertically;
  do not add viewmodel drag flags.
- On-demand visibility. `Ctrl+\`` focuses/toggles the last terminal and
  `Ctrl+Shift+\`` creates a terminal in the dock. The activity list remains a
  direct way to focus any terminal.
- Availability in launcher, editor, and chat views. It must reserve space from
  the active main content without depending on chat input or an AI session.
- A clear empty/hidden policy: after the last docked terminal closes, hide the
  dock and restore the main content height.

The dock should generally sit above the status bar as a workspace tool area,
not inside the chat-only area between messages and composer. Session
`PanelDeck` geometry remains independently above the chat input. Compute all
vertical budgets together so terminal dock, session deck, chat/editor/launcher,
input, and status never overlap and the main content retains a minimum row.

### Reversible floating placement

- Float removes the terminal tab from the dock and opens the same screen in a
  dynamic OverlayHost/FloatingWindow.
- Dock closes the floating window under a scoped suppression flag and reinserts
  the same screen into the workspace terminal dock.
- Closing a floating window still closes/kills that PTY unless a Dock action is
  in progress.
- Multiple floating terminals remain separate windows. Clicking/focusing one
  must bring it to the top exactly as today.
- The floating terminal keeps `WindowFrame` visual chrome and standard WM
  interaction regions. Do not route it through the terminal dock to simulate
  floating.
- Preserve or restore the last floating size/position per terminal where
  feasible; at minimum, do not regress current resizing and z-order.

### Focus and input

Introduce terminal-dock focus as TUI presentation state, not a workspace domain
enum unless a presentation-neutral cross-layer need is demonstrated.

Expected precedence:

1. Focused floating window.
2. Blocking session panel.
3. Explicitly focused terminal dock.
4. Explicit sidebar/tab focus.
5. Launcher/editor/chat defaults.

While a docked terminal is focused, all ordinary keys including Esc, Ctrl+C,
and Ctrl+Q go to the PTY. Global terminal/palette commands need an intentional
escape hatch and must not accidentally consume shell input. Document and test
the exact chord used to leave/focus-toggle the terminal. Floating terminal Esc
continues to go to the PTY.

Paste must use libghostty's paste encoder and the terminal's bracketed-paste
mode. Mouse clicks/wheel should route to libghostty mouse encoding when the
child enabled tracking; otherwise wheel scrolls terminal scrollback. Selection
and copy need an explicit policy rather than being accidentally captured by
the shell root.

## Terminal Core Adapter Contract

Replace the current `Emulator` internals with an interface small enough to
unit-test and isolate the unstable C API. The concrete names may change, but it
must support:

- Create/close and explicit error return from construction.
- `Feed([]byte)` into the Ghostty terminal.
- `Resize(cols, rows)` with cell pixel dimensions set to a documented logical
  value for size-report compatibility in a cell TUI.
- Synchronous terminal-response callback to the PTY writer.
- Title-change callback.
- Render-state update and cell-buffer paint.
- Terminal-aware key encoding from Bubble Tea key press/release information.
- Paste encoding based on `ModeBracketedPaste`.
- Mouse encoding based on terminal tracking state.
- Scrollback viewport operations and scrollbar snapshot.
- Cursor visibility and visual style.

All calls for one Ghostty terminal must be serialized. Today PTY output enters
the Bubble Tea update loop through the event bus, and painting/input also run on
that loop, which is the desired single-threaded ownership model. Callbacks that
write terminal replies must not recursively feed output into the same terminal.

### Cell conversion

For every visible render-state cell:

- Extract the complete grapheme as UTF-8, not only the first rune.
- Derive width through Crude's canonical grapheme method; preserve continuation
  cells in `CellBuffer`.
- Map resolved Ghostty RGB colors to `cell.ColorTrueColor`.
- Use semantic terminal foreground/background defaults when Ghostty reports no
  explicit color.
- Map bold, faint, italic, underline, strikethrough, inverse, and cursor state
  to supported Crude `cell.Attrs` without inventing feature-local colors.
- Clear/fill every terminal viewport cell so old content never leaks after
  erase, shrink, alternate-screen transitions, or dock/float moves.

## Implementation Sequence

### Phase 1: Reproducible libghostty build spike

- Select and document exact Ghostty and optional Go-binding revisions.
- Install/use Zig 0.16.x outside the repository and prove the pinned
  `libghostty-vt` build.
- Compile a tiny Go/cgo spike that creates a terminal, feeds SGR/full-screen
  sequences, iterates render-state cells, receives a PTY query response, and
  encodes an application-mode arrow key.
- Update CI and local build workflow before deleting the old emulator.

Exit: clean checkout can follow one documented command path and run the spike.

### Phase 2: Replace terminal engine and input

- Implement the local libghostty adapter in `tui/term`.
- Replace hand-written VT parsing and key tables.
- Wire terminal responses to `TerminalManager.Write`.
- Add terminal-aware key, paste, mouse, scrollback, cursor, title, and resize.
- Keep the `shellterm.Screen` component boundary stable where practical.

Exit: an interactive shell and at least one real full-screen TUI work in a
focused screen-level test/harness.

### Phase 3: Workspace terminal dock

- Add terminal dock state/component and root-shell slot.
- Remove all terminal IDs/content from per-session `PanelDeck` paths.
- Rework layout budgeting and hit-region/focus policy.
- Make new terminals dock regardless of AI session/launcher/editor state.
- Preserve activity-list focusing and terminal title updates.

Exit: multiple docked terminals tab correctly and survive AI session switches.

### Phase 4: Float/dock and polish

- Preserve existing OverlayHost/FloatingWindow path and dynamic overlay IDs.
- Move the same retained screen between dock and window without PTY restart.
- Verify multiple floating windows stack and refocus correctly.
- Add dock resize, scroll indicators, exited state, narrow-screen behavior, and
  status help.

Exit: float/dock round trips preserve terminal content, process, mode, cursor,
scrollback, and z-order behavior.

### Phase 5: Documentation and deletion

- Delete obsolete hand-written parser/key files after replacement is proven.
- Update `docs/tui/TERMINALS.md`, `SURFACE_CATALOG.md`,
  `INTERACTION_MATRIX.md`, `COMPONENT_AUTHORING.md`, and Help topics.
- Update architecture comments that currently say terminal panels share the
  session deck.
- Update the design bypass manifest only if required by the established audit
  workflow; do not add ad-hoc rendering exceptions.

## Required Tests

### libghostty adapter

- Basic text, SGR RGB/palette/default colors, full graphemes, wide-cell
  continuations, erase, alternate screen, cursor style/visibility, and resize
  reflow.
- Scrollback population and viewport scroll.
- Device/status/mode/size query emits response bytes through the PTY callback.
- Application cursor mode changes arrow encoding.
- Kitty keyboard flags or at least one mode-aware key case.
- Bracketed paste enabled/disabled, newline handling, and unsafe marker input.
- Mouse tracking enabled routes encoded mouse; disabled wheel scrolls viewport.
- OSC title updates the screen, dock tab, activity row, and floating frame.

### terminal dock/viewmodel

- New terminal docks with no AI session and while launcher/editor/chat is
  active.
- Multiple terminals are tabs in the dedicated dock, never session PanelDeck.
- Switching AI sessions does not hide, duplicate, move, or restart terminals.
- Closing a tab kills only its PTY and activates a deterministic neighbor.
- Closing the last docked terminal hides the dock and restores layout.
- Dock layout never overlaps status, chat input, session deck, launcher, or
  editor at narrow/standard/wide dimensions.
- Dock click/key/wheel routes through root Host geometry.
- Float removes dock tab, opens dynamic overlay, and preserves PTY/emulator.
- Dock from floating closes only the window and restores the tab.
- Closing a floating window kills its PTY.
- Two or more floating terminals preserve WM z-order on open and focus.
- Theme switching updates dock chrome/default terminal colors and open floating
  window chrome without destroying explicit application colors.

### practical integration

Use PTY-driven fixtures with deterministic commands where possible:

- Shell prompt plus command echo and exit.
- A program that sends DA/DSR/size queries and asserts responses.
- A small alternate-screen fixture that enables application cursor keys and
  bracketed paste.
- If installed, a smoke run of `vim`, `less`, or another full-screen program;
  do not make optional host tools mandatory for unit tests.

## Visual Verification

Use only the repository workflow in `AGENTS.md`:

```bash
COLS=160 ROWS=40 FONT_SIZE=11 scripts/tuishot.sh /tmp/crude-terminal-dock \
  key:ctrl+shift+grave
```

The exact key token may need adjustment after checking `tuishot.sh` syntax.
Capture at least:

1. One docked terminal in launcher or editor context.
2. Multiple docked terminals showing the tab strip.
3. A floating terminal over the editor/chat.
4. Two overlapping floating terminals proving z-order/focus.
5. A docked terminal after returning from floating.
6. A narrow terminal-size layout.

Inside the PTY, render deterministic content that includes normal text, ANSI
colors, a wide grapheme, enough lines for scrollback, and an alternate-screen
program where feasible.

After every capture, close the Alacritty/Crude window. Keep `KEEP_OPEN=0` and
verify no capture window remains:

```bash
xdotool search --class CrudeShot
```

## Validation Gates

Run focused checks first, then the repository baseline:

```bash
go test ./tui/term ./tui/screens/shellterm ./tui/viewmodel ./workspace
go build ./...
go test ./...
golangci-lint run ./...
scripts/tui_design_audit.sh
```

For the cgo adapter, also run the race suite if supported by the pinned
library/build:

```bash
go test -race ./...
```

Do not update unrelated goldens. Any intentional visual golden update must be
supported by inspected tuishot captures.

## Investigation State At Handoff

- `git status --short` was clean before investigation.
- No source code was edited during investigation.
- This plan file is the only intended workspace change at handoff.
- Temporary upstream clones exist outside the workspace at
  `/tmp/opencode/ghostty` and `/tmp/opencode/go-libghostty`; they are not project
  dependencies and may disappear.
- No subagents were used, per user instruction.
- The task list at handoff was:
  - Map core/libghostty/dock/floating paths: complete enough to proceed.
  - Repair terminal emulation/input: not started.
  - Implement dedicated dock: not started.
  - Add tests: not started.
  - Run gates and tuishot: not started.

## Resume Checklist

1. Read root `AGENTS.md`, this file, `docs/tui/TERMINALS.md`,
   `TUI_COMPONENT_SYSTEM_PLAN.md` sections 15.4 and root Host, and
   `TERMINAL_WIDTH.md`.
2. Check `git status --short` and preserve unrelated changes.
3. Do not repeat broad repository or web research unless an API/build detail is
   missing.
4. Start with the dependency/build spike, not UI edits.
5. Once the spike is green, implement the terminal core before the dock so the
   visual shell is not built around a broken emulator.
6. Preserve floating WindowManager z-order throughout.
