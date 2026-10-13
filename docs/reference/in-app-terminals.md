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
| OSC 52 clipboard | Child writes become an offer, never an automatic system-clipboard write; the controlling shell exposes **Copy offered text** in the terminal menu. Clipboard reads are ignored |
| Scrollback | Wheel scrolls viewport; `Shift+PgUp/PgDn/Home/End` on the primary screen; typing/paste snaps to bottom; right-edge scrollbar overlay while scrolled |
| Alt screen wheel | No tracking + alternate screen → wheel sends arrow keys (3×), like Ghostty's alternate-scroll |
| Focus reporting | DEC mode 1004 → CSI I/O on real pane-focus transitions (`syncTerminalFocusReports` sweep, idempotent) |
| Cursor styles | DECSCUSR requests normalize to the shared reverse-video block geometry; embedded applications retain blink/steady timing, and the shell paints the projected cursor |
| Bell | BEL flags an unfocused dock tab or adds a `•` icon to an unfocused floating `WindowFrame`; focusing the terminal clears it |
| OSC titles | Collected after Feed, committed by the service and delivered as neutral effects outside the engine lock |
| OSC 7 pwd | Updates `TerminalInfo.CWD` (file:// URL parsed to a path) |
| Query replies | Only `TerminalService` enqueues DA/DSR/XTVERSION/size replies on the async `ptyWriter`; clients receive no reply bytes and cannot answer twice |

All libghostty effect callbacks (title, bell, pwd, clipboard) are collected
during `vt_write` and flushed after the C call returns — handlers must never
re-enter libghostty mid-parse.

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

The neutral engine caps grids at 1000×500, row pages at 512 rows and scrollback
at 10,000 lines with a 32 MiB byte budget; Settings › Terminal › Scrollback
lowers the line limit for new terminals.

In the core, terminal create, input, resize, control and row reads run on each
workspace actor's own terminal lane. Documents, file mutations and language
servers keep the separate resource lane, so their slow effects never delay a
keystroke or redraw (`TestTerminalOperationsDoNotWaitBehindDocumentLane`). Client-local history pages retain
history identity and soft-wrap metadata; selection is cleared on incompatible
history/grid transitions rather than copied from unrelated rows.

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
Both the typed RPC and semantic control completion publish the same actor-owned
terminal inventory invalidation. Every replica unmounts removed IDs, including
docked and floating views, without closing an unrelated workspace panel. Hide
only changes presentation and is never a substitute for close.

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
the PTY. Escape hatches: `Ctrl+\`` (toggle/leave), `Ctrl+Shift+\`` (new),
and `Ctrl+Alt+P` (command palette).

## Opening

- Command Palette: New Terminal / Toggle Terminal
- `Ctrl+Shift+\`` new, `Ctrl+\`` focus last or create (toggles focus off when
  a terminal already owns keys)
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
