# Editor and Language Servers

> Status: Current reference — editor geometry, motion, dirty/save authority, gutter projection and LSP transport.

Live text in connected shells is owned by the core's shared document service
([core client protocol](core-client-protocol.md#shared-resources-and-private-state));
editor PieceTables are optimistic projections of it. This page covers the editor
engine and its invariants.

## Motion

- Display geometry is never source text. PieceTable positions resolve through
  the shared grapheme/tab conversion; arrow motion, pointer landing, selections
  and deletion use complete source units. A tab is one unit, not its expanded
  spaces; a combining/ZWJ/modifier cluster is never split by a visible edit.
  Exact inverse edits use byte offsets, including inside a newly joined cluster.
- Backspace within leading whitespace removes to the previous indentation stop,
  never into following words. At line start it joins the previous line; Delete
  at a line end (including an empty line) removes the complete terminator. CRLF
  is one terminator, excluded from line content. Enter follows the document's
  first terminator, inherits indentation and opens a matching bracket pair onto
  separate lines. Paste retains exact bytes.
- Left/Right without Shift collapse an existing selection to its directional
  edge. Tab indents every selected line, including a single-line selection;
  Shift+Tab outdents. Vertical motion retains its desired column while snapping
  each landing to a valid source boundary.
- **`Editor.moveWordLeft` must keep climbing across blank lines**: word-left
  from a line start must skip whitespace-only lines and land on the previous
  line's last word start (guarded by `TestMoveWordLeft_CrossesLines`). A
  b323a3b rewrite once replaced the climb loop's `continue` with `return`,
  parking the cursor on blank lines — and SA4004 flagged it as a dead loop.
  When a "dead loop" warning appears, check for a swallowed `continue` first.

## Pointer selection and scrolling

- One gesture owns character, whole-word, or whole-line selection, including
  line-number drags. Double/triple-click-hold extends by its original unit in
  either direction; Shift+gutter uses the current selection/caret anchor.
- Dragging to a viewport edge continues scrolling with a stationary pointer,
  vertically and horizontally. A 50 ms repeat uses bounded distance-based speed,
  not the number of terminal motion events. Endpoints clamp to the text viewport
  and stay on source boundaries even while crossing gutters or scrollbar chrome.
  Document bounds stop the repeat; returning inside stops it immediately.
- Motion, wheel, repeat, and final release project through the same selection
  logic. Editable release keeps the range without copying; read-only release
  copies it and drops the highlight. Shared scrollbar thumbs also apply their
  final release coordinate, only for the button that began the gesture.
- Repeat delivery goes through the mounted capture owner, including floating
  editors and Find in Files previews, never keyboard focus. Exact editor and
  monotonic tick epochs fence late/duplicate callbacks after release, cancellation,
  replacement, unmount, close, resize, terminal blur, or a modal taking over.
  Peer deltas transform the gesture's original unit boundaries with view anchors.
- This follows the established captured-gesture/repeating-edge-scroll model in
  [CodeMirror mouse selection](https://github.com/codemirror/view/blob/main/src/input.ts),
  rather than separate one-off behavior for each kind of drag.

## Edit transactions and history

- Ctrl+Z undoes; Ctrl+Y and Ctrl+Shift+Z redo (Super equivalents when delivered
  by the terminal). Navigation/focus changes separate typing. Adjacent plain
  typing within 500 ms is one logical group; Enter, indentation, pair operations,
  line moves/duplicate/delete, cut/paste, completion, formatting and replacement
  are atomic commands. Replace All is one transaction, not one step per match.
- Logical groups originate at the editing command, never the network drain.
  Core history is author-specific and follows the same group across paced or
  burst RPCs. A new edit after Undo invalidates Redo. Whole groups are retained
  or retired; pruning cannot leave half a command.
- Every Undo/Redo gesture is ordered. Input arriving behind pending history
  waits for its exact receipt and full canonical sync, then runs at the resulting
  caret. Save/close wait too. Ordinary queued key/paste input is retained in private
  recovery evidence; restored evidence is not automatic permission to replay.
- Standalone views restore pre/post caret and selection with exact source bytes.
  Connected views restore private pre/post state only for an exact matching undo
  request/group. Peer or formatter changes invalidate historical coordinates;
  current anchors transform through deltas instead of jumping to guessed places.
- Core undo records explicit inverse provenance and normalizes cancelled pairs
  before rebasing. Disjoint peer edits survive; genuine overlap or ambiguous
  touching boundaries fail closed. Previously stored operations without inverse
  provenance remain opaque and can still refuse overlapping history traversal.
  History remains bounded, not unlimited.
- Find/replace stores original source byte spans, not case-folded byte offsets
  or display-column ranges. Unicode case folding cannot shift a match, and a
  match inside a grapheme can be replaced and undone exactly. Search projections
  refresh by buffer revision before navigation/replacement or visible paint.
- Diagnostic spans belong to the editor revision at installation. Editing or
  Undo/Redo cannot keep painting/hovering spans from the previous text, and
  history keys and new diagnostic publications dismiss the old tooltip.
- Regression journeys exercise literal expected bytes and views, randomized
  command sequences followed by repeated full Undo/Redo, Unicode/CRLF, delayed
  acknowledgements, peers, restart, recovery and history rolling. The transaction
  and selection design follows established
  [CodeMirror history](https://github.com/codemirror/commands/blob/main/src/history.ts)
  and source-range deletion principles from
  [VS Code](https://github.com/microsoft/vscode/blob/main/src/vs/editor/common/cursor/cursorDeleteOperations.ts),
  not terminal display-rune reconstruction.

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
- The HEAD baseline is Git's working-tree (checkout) form of the file
  (`git cat-file --filters`), and every gutter diff matches lines with
  `textdiff.ContentHunks`, which treats CRLF and LF terminators as one line
  ending while hunks keep exact bytes. A Windows CRLF checkout of an LF blob, or
  an LF file under `core.autocrlf`, is never "Not committed yet"; a pure
  terminator change is reported by Git status, not the gutter.
- `editor.Buffer` is the one live-text/savepoint authority shared by views.
  `workspace.DocumentSaveService` is the one autosave-deadline and disk-commit
  authority. Editor, PieceTable, watchers, and LSP must never write files or
  maintain competing dirty flags.
- Git gutter state is structured HEAD-to-buffer hunks, never reparsed rendered
  patches. Paint derives current-line rails and exact deletion boundaries from
  those hunks; diagnostics, numbers, change rails, and source spacing keep
  independent stable lanes. Stale async hunks never attach to new revisions.
- The gutter has its own explicit fill (`theme.Colors.Gutter`) on every body
  row, past the last line and beside the horizontal scrollbar, and the editor
  clears its whole surface before painting. No editor cell is transparent, so
  a window or any other mount never shows through it.
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
