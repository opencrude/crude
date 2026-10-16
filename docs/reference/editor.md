# Editor and Language Servers

> Status: Current reference — editor geometry, motion, dirty/save authority, gutter projection and LSP transport.

Live text in connected shells is owned by the core's shared document service
([core client protocol](core-client-protocol.md#shared-resources-and-private-state));
editor PieceTables are optimistic projections of it. This page covers the editor
engine and its invariants.

## Motion

- **`Editor.moveWordLeft` must keep climbing across blank lines**: word-left
  from a line start must skip whitespace-only lines and land on the previous
  line's last word start (guarded by `TestMoveWordLeft_CrossesLines`). A
  b323a3b rewrite once replaced the climb loop's `continue` with `return`,
  parking the cursor on blank lines — and SA4004 flagged it as a dead loop.
  When a "dead loop" warning appears, check for a swallowed `continue` first.

## Horizontal geometry

- Horizontal scrollbar extent is the whole document, never visible rows. The
  PieceTable revision and retained per-line width index rebuild once on open or
  width-profile change, update one line for ordinary edits, and reduce the max
  when the former widest line shrinks.
- Handle native `MouseWheelLeft`/`MouseWheelRight`; Shift+vertical-wheel is the
  compatibility fallback for terminals that encode horizontal trackpad motion
  that way. Horizontal gestures must not change vertical offset.

## Dirty state, gutter and save authority

- Dirty means byte identity, not revision identity. `Buffer` retains an immutable
  piece-span savepoint; normal edits, undo/redo and save acknowledgements use the
  same exact comparison. Never hash/export the whole file per keystroke or let
  a late acknowledgement mark identical newer text dirty.
- The gutter has one exact live HEAD-to-buffer projection. Immutable piece
  snapshots bound each changed line window, which is re-diffed through shared
  `textdiff` logic. Paint, deletion markers, inspection and navigation all use
  those hunks immediately; never reintroduce paint-only local claims. Same-HEAD
  async refreshes update blame without replacing the live projection.
- `editor.Buffer` is the one live-text/savepoint authority shared by views.
  `workspace.DocumentSaveService` is the one autosave-deadline and disk-commit
  authority. Editor, PieceTable, watchers, and LSP must never write files or
  maintain competing dirty flags.
- Git gutter state is structured HEAD-to-buffer hunks, never reparsed rendered
  patches. Paint derives current-line rails and exact deletion boundaries from
  those hunks; diagnostics, numbers, change rails, and source spacing keep
  independent stable lanes. Stale async hunks never attach to new revisions.
- Automatic saves are opt-in and text-only. They never format, reload, move the
  cursor, clear selection/undo, or emit synthetic text changes. Manual format
  runs on an immutable snapshot and applies as one revision-checked undo group.
- Save acknowledgements identify an exact buffer revision. Newer text remains
  dirty. Exact watcher reconciliation replaces time-based self-event suppression;
  known external changes fail closed and preserve both local and disk content.
- Every Crude-owned writer, file operation, and Local History restore shares
  `fileio`'s cancellable mutation lease. This prevents autosave, agent tools,
  rename/delete, workspace edits, and restore from racing each other.

## Language server transport

LSP subprocess pipes must never block a caller goroutine. A synchronous,
context-unaware jsonrpc2 flush on a back-pressured server pipe once froze Bubble
Tea's update goroutine (the tab-close hang).

- `powernap` is vendored (`third_party/powernap`) with a `TransportWrap` hook in
  `ClientConfig`; `lspclient.LSPIntegration.Start` installs `lspPipeMonitor`
  (`lspclient/lsp_pipe.go`).
- `asyncPipeWriter.Write` only enqueues a copy; one dedicated writer goroutine
  performs pipe writes in FIFO order. The queue is byte-bounded
  (`lspWriteQueueCap`); a stalled server trips the cap and the jsonrpc2
  connection fails fast instead of blocking or corrupting framing.
- `lspPipeMonitor.Done()` closes when the connection dies for any reason (read
  EOF, write error, queue cap, graceful close). `workspace.LSPManager` observes
  `LSPClient.Done()`, clears that server's diagnostics, marks it `LSPFailed` and
  stops the process. A lost server is not silently restarted on every keypress or
  open; the user restarts it explicitly. Test fakes return a nil `Done()` channel.
- `LSPIntegration.WithServerEnv` sets the server subprocess environment.
- The client advertises `window.workDoneProgress`, answers
  `window/workDoneProgress/create` and tracks `$/progress` tokens.
  `AwaitDiagnostics` waits until the server's work settles (no active progress
  and a quiet interval) under the caller's deadline, so agent diagnostics are
  not read from a half-indexed server. Diagnostics carry `source` and `code`.
- `LSPManager` documents have holders: the editor view flag and a refcount of
  agent probes (`workspace/lsp_probe.go`). A document closes on the server only
  when no holder remains. Probes are allowed when the session's agent code
  intelligence is on even if editor LSP is off; a user-paused server refuses
  both. Probe results are plain data (`types.LSPProbeView`), never editor state.
- Language settings, resource limits and lifecycle are specified in
  [settings](settings.md#lsp); merely constructing a manager launches no server.

## Source projections and comparisons

- Source-code projections expand tabs at tab stops before width, wrapping,
  syntax coloring, and paint. Raw tab cells have no stable terminal geometry.
- Editor and comparison syntax share `rendering.SourceStyle`; never add a second
  token-to-theme map. Git/local diffs use one `screens/diffview` reader with
  source-aligned rows, native draggable split, one vertical scroll owner and
  exact source copy. Local comparisons are immutable, generation-fenced main
  tabs; Refresh is explicit and closing never consumes the source draft.
