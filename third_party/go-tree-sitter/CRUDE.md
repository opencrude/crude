# Crude native binding ownership

Upstream: `github.com/tree-sitter/go-tree-sitter` v0.25.0. The bundled C
runtime behavior and upstream MIT license are unchanged. This is a pinned Go module
replacement, not a second parser implementation.

Crude changes:

- Release every parser progress callback handle on return, including cancelled
  or invalid input, for UTF-8, UTF-16LE/BE and custom input.
- Retain query options in C memory for the complete cursor iteration. Release
  their Go callback and C storage when the cursor is closed or re-executed.
  Upstream retained Go callback handles indefinitely and kept a pointer to a
  Go-allocated options object past its cgo call.
- Release replaced logger handles and restore Logger from its actual Go handle
  instead of interpreting the opaque C index pointer as a function.
- Release custom-input C buffers, consistent with the UTF-8/UTF-16 paths.
- Treat an absent progress callback as no callback.
- Make the upstream concurrent-clone test self-contained: its Rust CLI source
  fixture is absent from the published Go module. A 4096-function Rust source
  retains the workload and exact child-count checks, with all clones released.
- Trim three upstream trailing-whitespace lines in `src/lexer.c` and
  `src/unicode/utf16.h`; no C token or license text changes.

`callback_lifetime_test.go` observes actual Go-object reachability after owner
release and forces GC between query exec and iteration. Its ten cases all
failed against the unchanged upstream code. Keep the upstream suite and these
regressions passing when changing or replacing this dependency.

Native parsers/cursors remain single-owner objects. The binding does not
authorize concurrent calls, omit Close, isolate scanner crashes or establish
cross-platform IDE acceptance.
