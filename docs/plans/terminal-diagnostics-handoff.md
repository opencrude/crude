# Terminal Diagnostics Handoff

> Status: RESOLVED (2026-08-13). Every defect identified below has landed with
regression tests; this file is a historical record. Resolution map:

- Terminfo deployment contract → FIXED: pinned entry generated/compiled by
  `scripts/bootstrap_libghostty.sh`, embedded via `types/ghostty_terminfo.go`,
  materialized by `workspace.TerminfoDir()` + `PTYEnviron` `TERMINFO`.
  Tests: `TestTerminfoDirMaterializesPinnedEntry`,
  `TestPTYChildResolvesPinnedTerminfo`.
- Idle 60 Hz output storm → FIXED: `wantsFastTick()` (launcher idle branch
  removed; `TestWantsFastTick`), `rendering.Log` gated behind
  `CRUDE_DEBUG_FLOATING=1`. Idle floating terminal ~0-2% CPU.
- OSC-title synchronous publish hang → FIXED: title effects defer to end of
  Feed; viewmodel publishes via async command.
  Tests: `TestTerminalTitleOutputDoesNotDeadlockOrDuplicate`,
  `TestEmulatorOSCTitleDeferredToEndOfFeed`.
- write_pty reply reentrancy risk → PROVEN SAFE + regression-tested:
  `TestTerminalReplyStormDoesNotDeadlockOrReenterBus` (500 DA queries in one
  Feed with a connected, undrained Program).
- Theme/color no-change guards → FIXED: `ComponentWidget.Render` skips
  same-pointer `SetTheme`; `Emulator.SetDefaultColors` no-ops when unchanged.
- The `heellp` display corruption → resolved by the terminfo contract fix
  (zsh/ZLE redraws render correctly once the advertised database resolves).

Date: 2026-08-13

Read this after root `AGENTS.md` and `docs/plans/terminal-integration-plan.md`. It
records the runtime investigation performed after another implementation pass
added the libghostty adapter and workspace terminal dock. Do not repeat this
investigation or patch the renderer from the screenshot alone.

## Worktree Context

The current worktree contains a large uncommitted terminal implementation from
another model. It includes:

- Pinned `libghostty-vt` under the ignored `deps/libghostty-vt` build prefix.
- A local cgo adapter in `tui/term`.
- Removal of the old hand-written VT parser/key table.
- A workspace-level dock in `tui/screens/termdock`.
- Reversible dock/floating-window plumbing.
- An asynchronous PTY writer in `workspace/terminal.go`.
- Build, CI, documentation, and tuishot changes.

Preserve these changes. Check `git status --short` before doing anything.

This diagnostic pass made no production-code change. It added one passing test,
`TestEmulatorZshRedrawDoesNotDuplicateCharacters`, to
`tui/term/emulator_test.go`. A deliberately incomplete failing trace replay was
removed before handoff.

## User-Visible Failure

The user's screenshot showed a floating terminal where typing a normal command
looked corrupted (`heellp`) and the application eventually appeared to hang.
The terminal activity row said `window`, and the action bar showed Dock, so the
failure was specifically visible in the floating placement.

## Exact Reproduction

Use the Command Palette, not the direct terminal shortcut:

1. `Ctrl+Alt+P`
2. Type `terminal`
3. Press Down once to select **New Terminal** (the first result is the
   Terminals activity action).
4. Press Enter.
5. In a 160x40 tuishot, the observed Float action was around cell `143,37`.
6. Type `help`, then Enter.

Repository command used:

```bash
COLS=160 ROWS=40 FONT_SIZE=11 PAINT_WAIT=2 STEP_WAIT=1 \
  scripts/tuishot.sh /tmp/crude-palette-float \
  key:ctrl+alt+p type:terminal key:Down key:Return sleep:2 \
  click:143,37 sleep:2 type:help key:Return sleep:3 shot:after-help
```

Observed result:

- Before Enter, the terminal displayed `heellp`.
- After Enter, zsh printed `Command 'help' not found, did you mean:`.
- Therefore zsh received and executed `help`, not `heellp`.

This distinction is important: do not "deduplicate" keyboard input or mutate
the displayed text heuristically.

## Proven Boundary Findings

### Keyboard encoder is not duplicating printable input

`tui/term.EncodeKey` has a printable fast path returning `msg.Text` exactly.
Existing tests construct `help` one key at a time and assert the result is
exactly `help`.

A live `strace` of the palette-created floating terminal confirmed each input
byte was written once to the PTY:

```text
read(stdin, "h", 1) -> write(pty, "h", 1)
read(stdin, "e", 1) -> write(pty, "e", 1)
read(stdin, "l", 1) -> write(pty, "l", 1)
read(stdin, "p", 1) -> write(pty, "p", 1)
read(stdin, "\r", 1) -> write(pty, "\r", 1)
```

The shell's diagnostic containing `'help'` independently confirms this.

### PTY output contains shell redraws

The PTY emits an initial echo and then zsh/ZLE syntax-highlight redraws in many
small reads. Representative chunks included:

```text
"h"
"\x1b[32mh\x1b[39m"
"\x1b[38melp\x1b[39m\r  \x1b[32mh\x1b[39m"
"\r  \x1b[31mh\x1b[31me\x1b[39m"
"\x1b[31me\x1b[31ml\x1b[39m"
"\x1b[31ml\x1b[31mp\x1b[39m"
```

These chunks are stateful and interleaved with input. A partial subset is not
a valid standalone replay. The removed diagnostic test used an incomplete
subset and produced `hlp`, proving only that the fixture was incomplete.

The passing retained test verifies the simpler canonical ZLE redraw
`h\bhelp`; libghostty handles that correctly.

### The advertised Ghostty terminfo is missing

This is a proven contract defect and must be fixed before judging shell
editing behavior.

Crude unconditionally launches children with:

```text
TERM=xterm-ghostty
TERM_PROGRAM=ghostty
TERM_PROGRAM_VERSION=1.3.0
```

But the current bootstrap builds only `libghostty-vt`. It does not install the
pinned Ghostty terminfo. The following commands fail on the development host:

```bash
infocmp xterm-ghostty
# infocmp: couldn't open terminfo file .../xterm-ghostty

TERM=xterm-ghostty tput colors
# tput: unknown terminal "xterm-ghostty"
```

The child shell visibly reports the consequence on startup:

```text
/home/lenovo/.config/zsh/dic.zsh:8: bad math expression:
operand expected at `>= 8 '
```

`dic.zsh` line 8 runs `tput colors`; the command substitution is empty because
the advertised terminfo cannot be resolved. Do not patch the user's zsh file.
The terminal environment is lying about an unavailable capability database.

Upstream Ghostty has a reproducible terminfo generator/build path:

- `src/terminfo/ghostty.zig`
- `src/build/GhosttyResources.zig`
- build option `-Demit-terminfo`

The selected pinned Ghostty revision is already recorded in
`deps/GHOSTTY_PIN`.

### The apparent hang includes a 60 Hz full-frame output storm

With a floating terminal idle on the launcher, the Crude process used roughly
27% CPU. `strace` showed complete 160x40 ANSI frames emitted approximately
every 16-17 ms.

The scheduling path is visible in production code:

- `workspace.StreamingTickCmd()` schedules every 16 ms.
- `viewmodel.handleTick` selects it when an agent is active, a reveal is
  active, **or when the launcher has no agents/editor**.
- `View()` rebuilds and presents a full frame after every update.
- A floating terminal therefore sits inside a permanent launcher 60 Hz loop,
  even when no launcher animation needs that rate.

Additionally, `tui/rendering/debuglog.go` is always enabled. Every WM render
calls `rendering.Log`, which opens, appends, and closes
`/tmp/floating_debug.log`. `wm.Manager.Render` does this on every frame, and
`FloatingWindow.Render` also logs when content is dirty. This is production
hot-path filesystem I/O and must be removed or explicitly gated.

There is another invalidation amplifier:

- `ui.ComponentWidget.Render` calls `Host.SetTheme(ctx.Theme)` and recursively
  reapplies tokens whenever window content renders, even if the theme pointer
  is unchanged.
- `shellterm.Screen.SetTokens` calls `applyDefaultColors`.
- `term.Emulator.SetDefaultColors` always calls into libghostty and marks the
  terminal paint cache dirty, with no equality guard.

This chain needs a no-change fast path, but benchmark/trace it after fixing the
unconditional launcher tick and debug logging rather than treating it as the
sole hang cause.

### Synchronous callback reentrancy — resolved

libghostty invokes effects synchronously inside `Emulator.Feed`.

- PTY replies call `TerminalManager.Write`, which now enqueues through the
  asynchronous `ptyWriter`; this avoids blocking the Bubble Tea update loop on
  a full PTY input pipe.
- Title, bell, pwd, and clipboard effects are collected during `vt.Feed`.
  After `ghostty_terminal_vt_write` returns, the viewmodel applies local
  effects and schedules title publication as a `tea.Cmd`; it never calls the
  synchronous `EventBus.Send` path from inside Bubble Tea `Update`.
- Explicit terminal close is command-owned and no longer echoes a
  `TerminalClosedMsg` through the event bus.

The deadlock paths are guarded by
`TestTerminalTitleOutputDoesNotDeadlockOrDuplicate`,
`TestTerminalReplyStormDoesNotDeadlockOrReenterBus`, and
`TestWorkspaceSwitchTerminalCloseAllDoesNotReenterEventBus`.

## Theories Ruled Out

- Printable key encoding duplicating `help`: ruled out by unit test, strace,
  and the executed command.
- The asynchronous PTY writer writing each key twice: ruled out by strace.
- A simple `h\bhelp` libghostty parsing failure: ruled out by the retained test.
- A safe basis for patching `paint_cgo.go` from the screenshot: ruled out; the
  exact stateful stream and missing terminfo must be addressed first.

## Required Resume Order

### 1. Fix the TERM/terminfo deployment contract

Do this before terminal renderer changes.

- Build the exact pinned Ghostty terminfo alongside `libghostty-vt`, likely by
  adding `-Demit-terminfo` to the pinned bootstrap.
- Decide a real runtime resource location. A repository-only absolute path is
  insufficient for installed Crude binaries. Prefer a bundled/generated
  resource materialized under Crude's app data or an installed resource prefix.
- Set `TERMINFO` (or another correct ncurses search path) for child PTYs to that
  compiled database while keeping `TERM=xterm-ghostty`.
- Add an integration test that launches a PTY child and proves both
  `infocmp xterm-ghostty` and `tput colors` succeed in the exact child
  environment. Expect the color count defined by the pinned entry.
- Keep `TERM_PROGRAM` aligned with the implementation only after this passes.

Do not silently switch to `xterm-256color` unless the product explicitly chooses
that compatibility contract. The intended design is real pinned Ghostty
terminfo plus real libghostty-vt.

### 2. Remove idle 60 Hz and hot-path file logging

- Make the launcher use the normal 400 ms tick when no actual fast animation
  or loading spinner needs 16 ms.
- Keep 16 ms only for streaming/reveal states that visibly need it.
- Remove or environment/build-tag gate `rendering.Log`; production should not
  open `/tmp/floating_debug.log` per frame.
- Add a testable predicate for fast-tick selection rather than timing a
  `tea.Cmd` in tests.
- Re-run the idle floating-terminal trace/CPU check and demonstrate that full
  frames are no longer emitted at 60 Hz.

### 3. Make libghostty effects non-reentrant

- Keep terminal ownership serialized on Bubble Tea's update goroutine.
- During `Feed`, collect non-PTY effects such as title changes.
- Invoke workspace methods / event-bus publication only after the C VT write
  returns.
- Add a regression where an OSC title arrives through
  `TerminalOutputMsg` while the event bus is connected; Update must return
  promptly and one title event must be observed.

### 4. Add no-change theme/color guards

- `ComponentWidget.Render` should not call `Host.SetTheme` when the same theme
  is already installed.
- `Emulator.SetDefaultColors` should return when effective fg/bg are unchanged.
- Add focused tests proving repeated unchanged renders do not dirty/rebuild the
  Ghostty cache.

### 5. Re-test real editing before touching cell conversion

Use Command Palette creation and test both docked and floating placements:

- Type normal text.
- Backspace in the middle and at the end.
- Left/Right/Home/End edits.
- Ctrl+A, Ctrl+E, Ctrl+U, Ctrl+C.
- Bracketed paste.
- Execute the edited command and compare display with what the shell ran.

If corruption remains after terminfo is resolvable, capture the **complete** PTY
stream from prompt-ready through command submission. Feed the exact ordered
chunks into one Emulator test and compare:

1. libghostty render-state cells,
2. `paint_cgo.go` CellBuffer cells,
3. compositor ANSI/UV cells.

Only patch the first boundary where those representations diverge.

## Validation State

Commands already run:

```bash
. scripts/libghostty_env.sh && go build ./...
# passed

. scripts/libghostty_env.sh && \
  go test ./tui/term ./tui/screens/shellterm ./tui/viewmodel ./workspace
```

Results:

- `tui/term`: passed before the temporary incomplete replay; the retained test
  also passes.
- `tui/screens/shellterm`: no tests.
- `workspace`: passed.
- `tui/viewmodel`: fails an existing command-palette golden mismatch introduced
  by the broader uncommitted UI work:
  - got `9b658e0ae0dcb639d8613648490c577645f777a7b01ed11610b547dec56db62c`
  - want `b4b59ce97f35de88e3ff30220d1011923ed9c92e7c9b9ff6de75517d76eda15f`

Do not update that golden until the visual change is intentionally reviewed.

Temporary diagnostic artifacts were under `/tmp/crude-*` and
`/tmp/opencode/crude-live.strace`; they are not repository inputs and may
disappear.

## Final Gates After Fixes

```bash
. scripts/libghostty_env.sh && go test ./tui/term ./tui/screens/shellterm ./tui/viewmodel ./workspace
. scripts/libghostty_env.sh && go build ./...
. scripts/libghostty_env.sh && go test ./...
. scripts/libghostty_env.sh && go test -race ./...
golangci-lint run ./...
scripts/tui_design_audit.sh
```

Then repeat palette-driven `scripts/tuishot.sh` captures for docked, floating,
editing, and redocking flows. Close every CrudeShot window afterward.
