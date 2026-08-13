# In-app terminals

Crude embeds a terminal multiplexer in the TUI. Each session is a PTY whose
environment is pinned to **Ghostty 1.3.0** (`types.PinnedGhosttyVersion`,
`TERM=xterm-ghostty`). Emulation is **libghostty-vt** at the commit in
`deps/GHOSTTY_PIN`, linked through a narrow cgo adapter in `tui/term`.
`CGO_ENABLED=0` builds compile a stub that returns an error from
`term.NewEmulator`; they do not keep a hand-written parser.

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
| Selection | Ghostty selection gestures: click anchor, drag, double-click word, triple-click line; painted via render-state row ranges (reverse video) |
| Copy | Copy-on-select + `Ctrl+Shift+C`; `Ctrl+Shift+A` selects all; text formatted with Ghostty clipboard semantics (unwrap + trim) |
| OSC 52 clipboard | Child clipboard writes land on the system clipboard (decoded MIME, text/plain preferred); reads are always ignored |
| Scrollback | Wheel scrolls viewport; `Shift+PgUp/PgDn/Home/End` on the primary screen; typing/paste snaps to bottom; right-edge scrollbar overlay while scrolled |
| Alt screen wheel | No tracking + alternate screen → wheel sends arrow keys (3×), like Ghostty's alternate-scroll |
| Focus reporting | DEC mode 1004 → CSI I/O on real pane-focus transitions (`syncTerminalFocusReports` sweep, idempotent) |
| Cursor styles | DECSCUSR: block/hollow invert the cell, bar/underline render `AttrUnderline` |
| Bell | BEL flags the dock tab (`● ` prefix) when the terminal does not own input |
| OSC titles | Deferred to end of Feed, published via async command (never synchronous bus publish) |
| OSC 7 pwd | Updates `TerminalInfo.CWD` (file:// URL parsed to a path) |
| Query replies | DA/DSR/XTVERSION/size replies enqueue on the async `ptyWriter` — never block or re-enter the bus (`TestTerminalReplyStormDoesNotDeadlockOrReenterBus`) |

All libghostty effect callbacks (title, bell, pwd, clipboard) are collected
during `vt_write` and flushed after the C call returns — handlers must never
re-enter libghostty mid-parse.

## Shutdown

`unixPTY.Close` sends SIGHUP to the child's process group (the shell is a
session leader via Setsid), waits briefly for the leader to be reaped, then
SIGKILLs the group. Descendants that stayed in the group (sh -c pipelines,
non-job-control children) cannot survive as orphans
(`TestTerminalCloseKillsProcessGroup`). Job-control shells forward SIGHUP to
their own jobs. `Workspace.Close` and project switches call
`TerminalManager.CloseAll` (`TestTerminalManagerCloseAllReapsChildren`).

## Layers

| Layer | Owns |
|-------|------|
| `workspace.TerminalManager` | PTY lifecycle, I/O, placement snapshot, events |
| `tui/term` | libghostty-vt bridge painted into `CellBuffer` |
| `tui/screens/shellterm` | One session: paint + keys/paste/mouse into the PTY + Float/Dock/Close |
| `tui/screens/termdock` | Workspace-level tab strip + resize handle under the root Host |
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

- **Docked:** workspace terminal dock (`tui/screens/termdock`) above the
  status bar, independent of AI session `PanelDeck`. Available in launcher,
  editor, and chat. Closing the last docked tab hides the dock.
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
- New terminals always dock when the dock can be shown.

Windows does not start PTYs yet (`terminal_pty_windows.go`).
