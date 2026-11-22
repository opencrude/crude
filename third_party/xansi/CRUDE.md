# charmbracelet/x/ansi fork

Upstream: https://github.com/charmbracelet/x, module
`github.com/charmbracelet/x/ansi` (the folder name joins the repository and the
module names). Base version: v0.11.7 (the version required in Crude's
`go.mod`). The root `go.mod` replaces the module with this folder.

## Local changes

- `terminal_width.go`: string width follows the shared width profile probed by
  `bubbletea` ([terminal width](../../docs/reference/terminal-width.md)).

## Update

Diff the new upstream release against v0.11.7, apply it, reapply the
width-profile change, then run `make test`.
