# In-app terminals

> Status: Current reference for the in-app terminal (libghostty-vt PTY engine).

Crude embeds a terminal multiplexer in the TUI. Each session is a PTY whose
environment is pinned to **Ghostty 1.3.0** (`types.PinnedGhosttyVersion`,
`TERM=xterm-ghostty`). Emulation is **libghostty-vt** at the commit in
`deps/GHOSTTY_PIN`, linked through a narrow presentation-neutral cgo adapter in `terminalvt/`.
`CGO_ENABLED=0` builds compile a stub that returns an error from
`terminalvt.New`; they do not keep a hand-written parser.

`go.mitchellh.com/libghostty` requires Go 1.26. Crude stays on Go 1.25 and
owns the adapter instead of that module.

Bootstrap: `scripts/bootstrap_libghostty.sh` (Zig 0.16.x under `~/tools`,
`tic` from ncurses-bin).

## Terminfo runtime contract

Advertising `TERM=xterm-ghostty` requires a resolvable terminfo entry in
every child. The pinned entry is generated from the `GHOSTTY_PIN` commit,
compiled with `tic -x`, committed under `types/terminfo/`, and embedded via
`types/ghostty_terminfo.go`. At PTY start:

1. `workspace.TerminfoDir()` materializes the compiled entry at
   `~/.crude/terminfo/x/xterm-ghostty` (`CRUDE_TERMINFO_DIR` overrides).
2. `PTYEnviron` sets `TERMINFO` to that directory.

Guarded by `TestTerminfoDirMaterializesPinnedEntry` and
`TestPTYChildResolvesPinnedTerminfo` (real PTY child: `tput colors` = 256,
`infocmp -x xterm-ghostty` succeeds). Never advertise the Ghostty TERM
without this database: child rc scripts break (`tput colors` returns empty).

## Terminal capabilities

| Capability | Mechanism |
|------------|-----------|
| Key encoding | Ghostty key encoder (application cursor/keypad, Kitty flags incl. release events, modifyOtherKeys) |
| Paste | `EncodePaste` — bracketed-paste wrapping + unsafe-control sanitization per DEC 2004 |
| Mouse reporting | Ghostty mouse encoder when the child enables tracking (X10/normal/button/any, SGR/URXVT/UTF-8) |
| Shift override | Shift+click/drag bypasses tracking for selection (standard escape hatch) |
| Selection | Client-local `termview` click/drag, double-click word and triple-click line; theme TextSelection/TextSelectionFg preserves atomic wide glyphs independently of child reverse/faint |
| Copy | Copy-on-select + `Ctrl+Shift+C`; `Ctrl+Shift+A` selects all; client-local extraction uses projected soft-wrap metadata to unwrap and trim text |
| Control keys | A focused dock/window PTY owns Ctrl+C/D/Z/\\/S/Q and application bindings before shell selection handling. Ctrl+C is always child input; explicit Ctrl+Shift+C is copy-only even without a selection. Exact Shift-only history chords are local; extra modifiers pass through. |
| OSC 52 clipboard | Child writes become an offer, never an automatic system-clipboard write; the controlling shell exposes **Copy offered text** in the terminal menu. Clipboard reads are ignored |
| Scrollback | Wheel scrolls viewport; `Shift+PgUp/PgDn/Home/End` on the primary screen; typing/paste snaps to bottom; right-edge scrollbar overlay while scrolled |
| Alt screen wheel | No tracking + alternate screen → wheel sends arrow keys (3×), like Ghostty's alternate-scroll |
| Focus reporting | DEC mode 1004 → CSI I/O on real pane-focus transitions (`syncTerminalFocusReports` sweep, idempotent) |
| Cursor styles | DECSCUSR requests normalize to the shared reverse-video block geometry; embedded applications retain blink/steady timing, and the shell paints the projected cursor |
| Synchronized output | DEC 2026 holds one completed active-grid render state (cells, wraps, cursor and visual metadata) until reset, resize or a one-second broken-client timeout; parsing and query replies continue |
| Bell | BEL flags an unfocused dock tab or adds a `•` icon to an unfocused floating `WindowFrame`; focusing the terminal clears it |
| OSC titles | Collected after Feed, committed by the service and delivered as neutral effects outside the engine lock |
| OSC 7 pwd | Updates `TerminalInfo.CWD` (file:// URL parsed to a path) |
| Query replies | Only `TerminalService` enqueues DA/DSR/XTVERSION/size replies on the async `ptyWriter`; clients receive no reply bytes and cannot answer twice |

All libghostty effect callbacks (title, bell, pwd, clipboard) are collected
during `vt_write` and flushed after the C call returns — handlers must never
re-enter libghostty mid-parse.

The key adapter supplies printable pre-Control UTF-8, logical key and unshifted
codepoint to Ghostty; it must not erase modified printable text or manufacture
C0 bytes as a second encoder. Ghostty owns fixterms disambiguation (Ctrl+I/M/[
are distinct from physical Tab/Enter/Escape), Kitty flags and modifyOtherKeys.
Shifted ASCII symbols preserve their logical text and map to the matching key.

The PTY/child owns the resulting meaning: Unix ISIG/canonical/IXON modes provide
interrupt, suspend, quit, EOF and flow control; Windows ConPTY provides console
interrupts with processed input, while Ctrl+D/Z and other controls remain
application-specific. Raw-mode children receive bytes, not forced host signals.
Native re-exec fixtures exercise ConPTY interrupt delivery and raw control bytes
through real App key dispatch in dock and floating placement. Linux/macOS builds
share the adapter and have the same native fixture; cross-builds alone are not
native Unix signal acceptance.

Windows detached-core startup clears the Ctrl+C-ignore policy inherited from
`CREATE_NEW_PROCESS_GROUP` before creating any PTY children; it does not attach
to a shell console or sacrifice core independence. A real detached-core RPC
regression interrupts an unmodified native PowerShell foreground command.
ConPTY supplies that child's standard console handles; the launcher must not set
`STARTF_USESTDHANDLES` with null handles or inherit unrelated parent handles.
ConPTY's process waiter closes the pseudoconsole exactly once on natural exit
while the reader drains final output. Only after EOF and parsing does the
manager publish exited state. Natural completion and interruption therefore do
not leave a finished process labelled running. Resize rejects a closing console;
potentially blocking pseudoconsole close holds neither parser nor resize locks.

## Shell selection

Settings › Terminal (application, with field-level workspace overrides) owns the
shell, arguments, login-shell flag, extra environment, scrollback and where new
terminals open. The core resolves the launch in `TerminalManager.CreatePrepared`
from the effective settings it was given (`Workspace.applySettingsProjection` →
`TerminalManager.SetSettings`); shells never send a command for an ordinary new
terminal, and `startTerminalIO` receives an explicit command, arguments and
environment with no fallback of its own.

`workspace.DetectTerminalShells` runs on the core's machine (which may be remote
over `--ssh`) and returns installed shells in **Auto** preference order:

| Platform | Auto order | Also offered |
|---|---|---|
| Windows | PowerShell 7 (`pwsh`, PATH or Program Files) → Windows PowerShell 5.1 → Command Prompt (`%ComSpec%`) | Git Bash (located from its install or `git.exe`, never PATH `bash.exe`, which is the WSL launcher), WSL |
| macOS | `$SHELL` → zsh → bash → fish → sh | |
| Linux/other Unix | `$SHELL` → bash → zsh → fish → sh | |

The list travels to the settings form as `SettingsFetchResult.TerminalShells` /
`AppSettings.TerminalShells` (observation metadata, `json:"-"`, never persisted).
Auto names its target (`Auto (PowerShell 7)`) and the Command field previews the
resolved executable. A selected shell that is not installed — for example one
synced from another machine — stays visible as `zsh (not installed)` and terminal
creation fails with guidance; it is never silently replaced. Custom requires a
command. Login shell adds each shell's own login arguments (`-l`, Git Bash
`--login -i`); user arguments follow. Running terminals keep the configuration
they started with.

## PTY input and size contracts

PTY stdin is serialized by one dedicated `ptyWriter` goroutine. Its queue is
bounded at 1 MiB: a write that cannot fit in full returns
`workspace.ErrTerminalBackpressure`, reports zero accepted bytes, and leaves
the existing queue unchanged. Queue overflow must never return success while
discarding the new bytes. A later write also surfaces a retained asynchronous
sink error instead of silently accepting more input. The viewmodel reports
manager write and resize failures in the status bar.

`workspace.NormalizeTerminalSize` is the single grid-size contract for both
libghostty and the PTY: at least 8 columns by 2 rows. Tiny screen layouts send
that same normalized size to both sides; they must never leave a 2×1 emulator
attached to an 8×2 PTY. The PTY resize is attempted first; on failure the
emulator keeps its previous grid and the next layout retries rather than
committing a desynchronized size.

`workspace.TerminalService` is installed on an empty manager before PTY creation.
Its prepared-create hook installs the sole engine before the first output read.
PTY bytes enter this service even without a mounted screen; downstream readers
receive bounded detached rows, not raw output. There is no shell startup parser
or second query-reply source.

Input and resize require the exact `TerminalLease`: terminal ID, client ID,
attachment epoch and lease generation. A second controller cannot silently steal
it; release/detach invalidates only the exact owner. Viewers may read rows and
select/scroll locally without input authority. The standalone shell admits
key/paste/focus/resize commands on Update into a bounded 256-command FIFO, then
executes service calls off Update. Resize geometry is acknowledged from the
returned projection, not optimistically committed by Paint. Async results are
fenced to the service and workspace-switch generation; stale reads cannot
replace a newer page.

### Completed-frame publication

PTY read boundaries are not application frames: an animated child can erase the
screen and hide the cursor in one read, then finish its redraw in later reads.
`terminalvt.Engine` honors DEC 2026 at Ghostty-owned parser-ground/mode
transitions, including split sequences and end/begin within one read. It retains
the existing Ghostty render state for the completed active grid, not a second VT
parser, terminal or full-scrollback snapshot. All bytes still mutate the sole
terminal immediately; protocol replies and input encoding observe live state.
Unmarked erase/draw output remains visible as written, rather than guessing
application frame boundaries or dropping mutations.

During a synchronized update, detached same-history client pages remain usable.
New uncached historical rows wait for commit; a racing historical page request
returns the completed active grid instead of exporting mutable history or an
empty page. Scroll intents into unavailable history leave the last usable
viewport in place. A history-identity change at commit clears incompatible
selection once. Read sequences fence asynchronous pages even when timeout
release changes the visual state without advancing PTY ingress generation.

The normal shell cursor tick queues a fenced asynchronous row read after one
second while synchronization is outstanding. This releases an abandoned update
even when no further output arrives; the engine owns the deadline and has no
timer goroutine. The next ordinary idle tick may add up to its 400 ms cadence to
that retry. Resize releases synchronization immediately under Ghostty's contract.

`TestHighRateSynchronizedOutputNeverPresentsPartialFrame` exercises 120 split
alternate-screen redraws through the real engine, terminal screen and ANSI
generator. It originally exposed 360 partial/erased intermediate frames and 240
hidden-cursor frames over 480 presentations; the completed-frame contract keeps
both counts at zero. These deterministic ingress checks are not proof that a
native PTY transport preserves DEC 2026, or that an unmarked movie workload is
tear-free on every outer emulator.

The native Windows ConPTY transport preserves DEC 2026 in a paced PowerShell
child probe, but can emit the reset before its separately buffered cursor
home/show. The shell coalesces both terminal invalidations **and asynchronous
projection completions** to the same 16 ms latest-state deadline; every returned
projection is installed immediately. This avoids making every PTY page response
an accidental forced presentation. A native 120-update probe observed zero blank
projected grids with synchronization, versus 235 erased-grid reads when the same
child omitted DEC 2026. Per-read cursor-hidden observations still occur when
ConPTY moves show after reset, so parser-only counts are not perceived-host
cursor acceptance. `TestTerminalSynchronizedServiceJourneyDockFloatResizeAndTimeout`
covers actual service/App result routing, retained dock/window movement, resize,
focus, timeout without more output, and adjacent reset/show result coalescing.

`BenchmarkTerminalHighRate` separates mutation from mutation plus detached row
publication at 80×24 and 160×40. Three 30-iteration Windows/amd64 Go 1.25.8,
i7-9850H measurements put 160×40 publication at 3.18–3.39 ms before and
3.21–3.40 ms after the synchronization boundary change. Mutation alone increases
from 12.8–16.0 µs to 39.6–41.5 µs for marked output (five small allocations,
176 B); unmarked mutation stays allocation-free and measures 21.9–24.0 µs
versus 13.0–16.7 µs before. The extra work locates exact Ghostty mode boundaries
and captures the completed render state; no timing or allocation ceiling is
relaxed. These measure adapter stages, not complete App/PTY/host latency.
The later validation run, concurrent with focused tests on this shared host,
measured 3.90–4.70 ms at 160×40; this is not a settled throughput guarantee or
evidence of a speedup. Native visual acceptance remains a separate gate.

The neutral engine caps grids at 1000×500, row pages at 512 rows and scrollback
at 10,000 lines with a 32 MiB byte budget; Settings › Terminal › Scrollback
lowers the line limit for new terminals.

In the core, terminal create, input, resize, control and row reads run on each
workspace actor's own terminal lane. Documents, file mutations and language
servers keep the separate resource lane, so their slow effects never delay a
keystroke or redraw (`TestTerminalOperationsDoNotWaitBehindDocumentLane`). Client-local history pages retain
history identity and soft-wrap metadata; selection is cleared on incompatible
history/grid transitions rather than copied from unrelated rows.

### Streaming scrollback publication

Scroll intent, presented viewport and PTY ingress have independent clocks. The
shell snapshots `Screen.ViewportRead` on Update and performs one bounded
`TerminalService.Rows(id, terminalvt.RowRequest)` off Update. Refreshes request
the desired historical viewport directly, not the active grid followed by a
second historical read. Count zero means one complete current-grid-height page;
start `-1` explicitly follows the live tail, including output arriving between
request admission and execution. Wheel, page, Home, End and thumb gestures do not
move the presented offset until every requested visible row is available. The
previous complete viewport remains painted during the asynchronous handoff, with
the scrollbar bound to the presented offset. No mutation or frame is hidden by
an additional debounce.

`HistoryEpoch` identifies incompatible reset, screen-switch and reflow boundaries.
`HistoryBase` counts proven oldest-row eviction within an epoch. Two Ghostty
tracked grid references establish displacement without comparing text or exporting
history: the prior oldest coordinate checks consistency, and the prior tail
coordinate survives ordinary oldest-row pruning. Lost or inconsistent references
advance the epoch rather than guessing. Each anchored row request carries the
epoch/base that supplied its coordinates; the engine adjusts its start under the
same service lock that reads cells and ingress generation. Thus a scrolled row and
selection retain their identities while other oldest rows are evicted. When their
own rows leave retention they clamp/clear, respectively. A request whose epoch
became incompatible returns one whole clamped current viewport with the new
identity; a selection read includes its recovery viewport, cancels pending copy
and does not display unrelated partial selection pages or enter a retry loop.

The client cache uses epoch-absolute keys, bounded to 512 plus two grid heights
for ordinary navigation. Explicit selections may retain the selected interval,
itself bounded by the service's retained history limit. Only rows already outside
the **previous** active grid are reusable across ingress generations. Previously
active rows are invalidated even if new output has since pushed them into history:
the child could have rewritten them first. A detached complete presented page
does not make stale mutable rows eligible for copy. Scroll-intent sequence,
ingress generation and engine read sequence reject independent stale response
classes; a dirty refresh coalesces to one subsequent current-viewport read.

`TestTerminalStreamingScrollViewportNeverClearsBetweenAsyncPages` originally
presented an entirely blank PTY immediately after Shift+Home on 2,000 primary
history rows, before its asynchronous page returned. The regression now retains
usable rows before the page and preserves the anchored viewport across 24
unmarked output publications. Related App/engine/screen/connected-RPC tests cover
delayed competing scroll responses, dirty coalescing, dock/floating acknowledged
resize, former-active-row rewrites, same-generation response ordering, scrollbar
capture, viewer/controller behavior, exact eviction displacement, selection/copy
identity and reset/reflow recovery. The DEC 2026 hold and timeout checks remain
in force. These are presented-buffer/ANSI and real service/wire checks; native
desktop/constrained-size scroll capture remains separate acceptance evidence.

`BenchmarkStreamingScrollViewport` measures Feed plus a detached historical
80×24 page, cache installation and paint with 100 versus 10,000 retained-source
lines. Before the streaming correction, three 30-iteration Windows/amd64
Go 1.25.8/i7-9850H runs measured 1.79–2.14 ms versus 1.28–2.78 ms and identical
166,657 B / 3,125 allocations. Timings vary with concurrent host work and are not
latency ceilings; this benchmark excludes native PTY and full App presentation.
After the correction, the final three 30-iteration runs measured 1.49–1.69 ms
with 100 source lines and 1.56–1.72 ms with 10,000, at 164,040–164,047 B /
3,112 allocations in both cases. A prior run concurrent with race builds ranged
from 3.39–7.91 ms, so no speedup or isolated latency-regression claim is inferred
from these host-dependent times. No allocation ceiling was raised.

Terminal IDs come from one process-wide monotonic sequence, not a
`TerminalManager`-local counter. Workspace switching replaces the manager while
old output/title/exit messages may still be queued; a replacement manager
therefore cannot reuse the old session's ID and stale messages cannot resolve
to a new PTY or lazily materialize its screen.

## Shutdown

`unixPTY.Close` sends SIGHUP to the child's process group (the shell is a
session leader via Setsid), waits briefly for the leader to be reaped, then
SIGKILLs the group. Descendants that stayed in the group (sh -c pipelines,
non-job-control children) cannot survive as orphans
(`TestTerminalCloseKillsProcessGroup`). Job-control shells forward SIGHUP to
their own jobs. `Workspace.Close` and project switches call
the terminal service/manager close path (`TestTerminalManagerCloseAllReapsChildren`).
Connected shells detach on project switch or exit; retained PTYs remain owned by
the core. Explicit close or core/workspace shutdown owns process termination.
Explicit close is command-owned and does not publish a `TerminalClosedMsg`:
workspace switches and close actions run on Bubble Tea's update goroutine, so
synchronously echoing through `EventBus.Send` would deadlock on `Program.Send`.
Successful creation and explicit close publish the same actor-owned terminal
inventory invalidation for both typed RPC and semantic control completion.
Existing peer shells discover even a silent PTY immediately; inventory never
waits for the child's first output, title or exit. Every replica unmounts removed
IDs, including docked and floating views, without closing an unrelated workspace
panel. Hide only changes presentation and is never a substitute for close.

## Layers

| Layer | Owns |
|-------|------|
| `workspace.TerminalManager` | PTY process lifecycle and bounded asynchronous I/O |
| `workspace.TerminalService` | One VT engine/reply owner, metadata, exact controller leases and detached row pages |
| `terminalvt/` | Neutral libghostty-vt engine, encoding, effects and row projections; no TUI imports |
| `tui/termview` | Client-local row cache, scroll, selection/copy and snapshot painting |
| `tui/screens/shellterm` | Immutable projection reader; key/paste/mouse/resize intents + Float/Dock/Close |
| `tui/screens/termdock` | Workspace bottom-panel host, resize handle, compact numbered terminal tabs, shared header controls, and retained PTY mounting |
| `tui/viewmodel` | Dock slot, OverlayHost windows, activity list, focus |

`workspace/` must not import `tui`.

## Host vs in-app

`workspace.DetectHost()` reads `TERM_PROGRAM` / `TMUX` / `ZELLIJ` / `STY` so
Crude can tell when it is nested in tmux/zellij/screen **and** whether the
outer emulator matches the Ghostty pin. In-app PTYs always set:

- `TERM=xterm-ghostty`
- `TERM_PROGRAM=ghostty`
- `TERM_PROGRAM_VERSION=1.3.0`
- `COLORTERM=truecolor`

## Placement

Dock/floating placement and focus belong to each shell (`App.terminalPlacements`),
not the PTY service. The service has one process/grid; a viewer does not acquire
resize authority merely by laying out a screen. See the
[core client protocol](core-client-protocol.md) for the implemented resource API
and the still-outstanding connected-product cutover.

- **Docked:** Terminal mode in the workspace bottom panel (`tui/screens/termdock`)
  above the footer, independent of AI session `PanelDeck`. Available in launcher,
  editor, and chat. The footer's labeled `Terminals` control toggles this mode.
  PTYs use numbered `ui.Tabs` (`Terminal 1`, `Terminal 2`, …) beside
  picker/new/float/close/maximize/hide controls on the same row. Titles truncate
  within 16 columns with the number retained; cramped strips use `T 1`, `T 2`, ….
  Overflow supports horizontal trackpad/wheel, vertical wheel over the strip,
  and visible `‹`/`›` controls without changing the active PTY. Browsing stays
  put across repaint/title updates; choosing a tab reveals it. Unseen bells
  retain the shared attention marker. The picker exposes matching terminal
  numbers with readable Docked/Floating placement. The baseline and PTY body
  span the full dock width. The divider plus
  two-row tab strip still reserve at least two PTY body rows.
  Closing the last docked terminal closes Terminal mode without affecting the
  other workspace panel views. A shell's natural exit removes its instance;
  exited PTYs never remain as stale `process killed` rows requiring manual close.
- **Floating:** `OverlayHost` + `FloatingWindow` + `WindowFrame`,
  `DefaultOverlayPolicy` (KeepRoute, movable, resizable). Overlay IDs are
  dynamic: `ui.TerminalOverlayID(id)` → `"terminal/"+id`.
- **Promote / Dock:** action bar on the terminal screen. The same retained
  `shellterm.Screen` and PTY move; the process is not restarted. Closing a
  floating window kills the PTY unless the viewmodel is mid-promote
  (`suppressTermClose`).
- **Z-order:** `WindowManager.FocusWindow` / `BringToTop` on click and on
  focus from the Terminals activity list. Esc in a focused terminal is a PTY
  key, not close.

## Focus

While a docked or floating terminal owns keys, Esc, Ctrl+C, and Ctrl+Q go to
the PTY. Escape hatches: **Alt+F12** (summon/leave; `Ctrl+\`` and
`Ctrl+Alt+T` aliases), `Ctrl+Shift+\`` (new),
and `Ctrl+Alt+P` (command palette).

## Opening

- Command Palette: New Terminal / Toggle Terminal
- **Alt+F12** summons the last-used terminal in its existing placement or creates
  the first one; repeating returns focus to content without closing the PTY.
  `Ctrl+\`` and `Ctrl+Alt+T` are aliases, and `Ctrl+Shift+\`` creates a new PTY
  (including shifted `~` encoding). Alt+F12 avoids layout-specific backtick/AltGr
  delivery. Modal/dirty decisions keep input ownership; the footer shows summon,
  new and scrollback keys while a terminal owns input.
- Terminals activity list: **New Terminal**
- New terminals dock by default; Settings › Terminal › Open new terminals can
  open them floating instead. Either placement can be changed afterwards.

Windows uses native ConPTY (`CreatePseudoConsole` + `STARTUPINFOEX`) and assigns
the suspended shell to the same kill-on-close Job Object authority used by
background commands before resuming it. Resize, async input, exact process-tree
termination and manager CloseAll have native Windows tests. The UTF-16 child
environment block is explicitly double-null terminated. Noninteractive agent
commands are separate from the PTY path and use `CREATE_NO_WINDOW`/`HideWindow`,
so tools such as WinGet cannot paint directly into Crude's host terminal; their
stdout/stderr appears only in the captured tool/background-job channel.

## Core lane and input backpressure

- **Terminals have their own core lane and settings-owned shells.** Terminal
  create/input/resize/control/rows run on the actor's `terminalLane`, never
  behind document/file/LSP effects on `resourceLane`. The core resolves an
  ordinary new terminal's shell from effective `TerminalSettings` with
  `workspace.DetectTerminalShells` (Auto: PowerShell 7 → Windows PowerShell →
  cmd; `$SHELL` first on Unix); `startTerminalIO` has no shell fallback. A
  missing selected shell fails with guidance, never a silent substitute. The
  detected list is core-host metadata (`AppSettings.TerminalShells`, `json:"-"`).
- **PTY input is bounded and non-blocking.** `ptyWriter.Write` either queues the
  complete payload or returns `ErrTerminalBackpressure`; never silently drop
  bytes. `NormalizeTerminalSize` is the shared emulator/PTY 8×2 floor, and a
  failed PTY resize must not commit the emulator grid.
