# In-app terminals

Crude embeds a terminal multiplexer in the TUI. Ghostty cannot be composited
into the cell buffer, so each session is a PTY whose environment is pinned to
**Ghostty 1.3.0** (`types.PinnedGhosttyVersion`, `TERM=xterm-ghostty`). VT
parsing uses vendored `third_party/xansi` (the DEC/xterm family Ghostty 1.3
implements). `libghostty-vt` is not linked.

## Layers

| Layer | Owns |
|-------|------|
| `workspace.TerminalManager` | PTY lifecycle, I/O, placement snapshot, events |
| `tui/term` | VT emulator painted into `CellBuffer` |
| `tui/screens/shellterm` | One session: paint + keys/paste into the PTY + Float/Dock/Close |
| `tui/viewmodel` | PanelDeck tabs, OverlayHost windows, activity list |

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

- **Docked:** closable `PanelInteractive` tabs on the active session
  `PanelDeck` (`terminal/<id>`), same region as questions.
- **Floating:** `OverlayHost` + `FloatingWindow` + `WindowFrame`,
  `DefaultOverlayPolicy` (KeepRoute, movable, resizable). Overlay IDs are
  dynamic: `ui.TerminalOverlayID(id)` → `"terminal/"+id`. They are **not**
  one of the 18 static overlay-contract IDs.
- **Promote / Dock:** action bar on the terminal screen. Promote closes the
  deck tab and opens a window; dock is the reverse. Closing a floating window
  kills the PTY unless the viewmodel is mid-promote (`suppressTermClose`).
- **Z-order:** `WindowManager.FocusWindow` / `BringToTop` on click and on
  focus from the Terminals activity list. Esc in a floating terminal is a PTY
  key, not close.

## Opening

- Command Palette: New Terminal / Toggle Terminal
- `Ctrl+Shift+\`` new, `Ctrl+\`` focus last or create
- Terminals activity list: **New Terminal**
- With a session deck: new terminals dock. Without a session: they float.

Windows does not start PTYs yet (`terminal_pty_windows.go`).
