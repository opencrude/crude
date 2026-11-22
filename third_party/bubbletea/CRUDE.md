# Bubble Tea fork

Upstream: https://github.com/charmbracelet/bubbletea, module `charm.land/bubbletea/v2`.
Base version: v2.0.6 (the version required in Crude's `go.mod`). The root
`go.mod` replaces the module with this folder.

## Local changes

- `width_probe.go`: probes the terminal's grapheme width behavior and shares
  one width profile with `xansi` and `ultraviolet`, so every layer measures
  text the same way ([terminal width](../../docs/reference/terminal-width.md)).
- `host_bidi.go`: negotiates the host terminal's BiDi mode
  ([bidi](../../docs/reference/bidi.md)).
- `WithPanicHandler` in `options.go` and `tea.go`: an observation hook in both
  recovery paths. Crude writes the panic and stack before terminal cleanup;
  the framework keeps its normal restoration and error result
  ([crash reports](../../docs/reference/crash-reports.md)).

## Update

Diff the new upstream release against v2.0.6, apply it, reapply the changes
above, then run `make test` and the width and BiDi tests in this folder.
