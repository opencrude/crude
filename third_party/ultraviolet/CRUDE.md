# Ultraviolet fork

Upstream: https://github.com/charmbracelet/ultraviolet, module
`github.com/charmbracelet/ultraviolet`. Base version:
v0.0.0-20260416155717-489999b90468 (the version required in Crude's `go.mod`).
The root `go.mod` replaces the module with this folder.

## Local changes

- Cell and string measurement use the shared width profile owned by
  `bubbletea` and `xansi` ([terminal width](../../docs/reference/terminal-width.md)).
  `measured_width_test.go` covers it.

## Update

Diff the new upstream commit against the base version, apply it, reapply the
width-profile change, then run `make test`.
