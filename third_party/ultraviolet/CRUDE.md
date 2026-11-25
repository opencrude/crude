# Ultraviolet fork

Upstream: https://github.com/charmbracelet/ultraviolet, module
`github.com/charmbracelet/ultraviolet`. Base version:
v0.0.0-20260416155717-489999b90468 (the version required in Crude's `go.mod`).
The root `go.mod` replaces the module with this folder.

## Local changes

- Cell and string measurement use the shared width profile owned by
  `bubbletea` and `xansi` ([terminal width](../../docs/reference/terminal-width.md)).
  `measured_width_test.go` covers it.
- `terminal_reader_windows.go` converts the native VT text stream to UTF-8:
  ordinary releases do not type a second copy, and Alt-code releases can
  carry committed UTF-16 units. `terminal_reader_windows_test.go` exercises
  both input shapes and bracketed paste.
- `terminal_renderer.go` can shift a row suffix only when it matches on
  whole-cell boundaries. Otherwise it overwrites the changed span. The
  [Windows emoji regression](../../docs/issues/2026-11-25-windows-emoji-input.md)
  checks the displayed terminal cells, not just the renderer's cache.

## Update

Diff the new upstream commit against the base version, apply it, reapply the
local changes, then run `make test` and the Windows input and VT replay tests.
