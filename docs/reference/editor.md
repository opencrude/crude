# Editor and Language Servers

> Status: Current reference — editor geometry, motion, dirty/save authority, gutter projection and LSP transport.

Live text in connected shells is owned by the core's shared document service
([core client protocol](core-client-protocol.md#shared-resources-and-private-state));
editor PieceTables are optimistic projections of it. This page covers the editor
engine and its invariants.

## Shared document publication

Every connected open registers one document topic. The first publication is a
snapshot boundary; subsequent publications carry ordered revision operations and
savepoint/path state, not another complete text fetch after each command. The
core remains the text, LSP, undo and save authority. Edit/undo/redo/format/save
results are private command evidence; they do not replace the shared publication
clock or consume another editor's pending draft.

On Update, the adapter validates the owner, document, base revision and exact
subscription token, prepares the full canonical update plus optimistic edit and
anchor rebase, then commits all prepared state atomically. Failed preparation
leaves the previous editor state intact and retains private recovery evidence.
Only successful application releases the topic's applied ACK. Cursors, selection,
scroll and pending optimistic text remain private. Ordinary typing, save and
idle ticks do not invoke `SyncDocument`; explicit recovery and final shutdown
reconciliation retain that API.

A warm retained-session resume continues the applied topic cursor. A cold reset
cannot prove that an unresolved private edit was applied: it enters conflict and
observes the original receipt instead of silently replacing or replaying text.
Close/reopen, draft resolution, workspace switch and shutdown invalidate exact
per-open generation tokens, including publications already queued to Update.
Switching away and back to the same port does not revive an old token. Explicit
Use shared backs up private evidence first; it grants no permission to overwrite
an external disk conflict. See [private shared-editor recovery](core-client-protocol.md#shared-resources-and-private-state).

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
- A plain text press places the insertion caret at the hit source unit's leading
  boundary, without selecting. A backward drag anchors at that unit's trailing
  boundary so the initially pressed character is included; forward drags retain
  the leading anchor and exclude the endpoint caret's character. Reversing uses
  the original unit bounds, and returning to that unit collapses the range.
  Wide/combining graphemes and tabs remain whole source units. Empty-line/EOL
  presses, Shift+click/drag, keyboard and programmatic selections retain ordinary
  half-open caret boundaries; a backward drag must not add an EOL terminator.
  The block caret denotes the insertion boundary before its covered grapheme:
  at `abc|def`, Shift+Left selects `c`, not `cd`. This is the shared text-control
  contract, not an editor-only exception; see [text input](text-input.md#selection-semantics-all-editable-text).
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
  waits for its exact receipt and canonical topic revision, then runs at the resulting
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
- Document Find is verbatim (case-insensitive by default); whole-word and regex
  are explicit options. It never uses fuzzy discovery. Ctrl+F/H seeds the
  selected source, quoting it when regex is retained; tabs/newlines use escaped
  literal regex text. The query field is selected for immediate replacement.
  Query changes preserve the document selection/caret. Find/Replace transforms
  retain query/replacement edits, options and navigation; invalid regex is shown
  in the actual popup status and cannot mutate text. Replace advances beyond
  the replaced source rather than matching its own output. Replace All is one
  exact-source undo group, including capture expansion and zero-width matches.
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

## Project search and replacement

Find in Files and Replace in Files share one retained workspace draft, result
owner, exact matcher (`textsearch`) and case/whole-word/regex controls. Selected
editor source seeds either command. Re-invocation focuses/selects the query;
mode changes preserve results and replacement text. Every exact occurrence is
separate, with source-byte ownership through planning and multiline end ranges.

Project discovery additionally uses fzf V2 (bounded V1 path for large inputs),
requiring every query rune in order. Weak scattered matches are rejected by its
score; a single-word query cannot assemble an abbreviation across unrelated
prose words. Exact hits rank before discovery suggestions. Filename-only and
fuzzy-only results are not replacement spans; regex/whole-word modes do not
introduce fuzzy content hits. The editor Find path never uses fzf.

Replacement plans are computed off Update from the canonical source, reject
stale/invalid spans, and deduplicate identical hits. Planning is bounded
to 16 MiB of source; preview is bounded to 1 MiB. An immutable, Cancel-default
review shows exact changes, file/replacement counts and skipped discovery hits.
Apply requires a Local History safety snapshot, unchanged preimages and no
dirty/pending/shared-document conflict. The workspace-edit transaction is the
only commit owner; a conflict aborts the complete operation. Cancel writes
nothing. Accepted execution is exactly once, owner/generation fenced, and joined
at shutdown; recovery uses the recorded Local History safety snapshot. Results
refresh after success without dropping the query/replacement draft.

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
  A Save receipt and its identical ordered publication share one disk-baseline
  generation. Re-observation cannot cancel a pending workspace switch; a changed
  baseline still advances the generation, including change-and-return ABA.
- Every Crude-owned writer, file operation, and Local History restore shares
  `fileio`'s cancellable mutation lease. This prevents autosave, agent tools,
  rename/delete, workspace edits, and restore from racing each other.
- Retained document checkpoints outlive files and folders. A missing/inaccessible
  path or failed watch is resource-local, never a workspace storage failure that
  disables unrelated editors or file actions. Startup reconciles every retained
  document. Deleted paths keep their exact text, savepoint, identity and history
  as conflicted recovery evidence; opening a retained document with a missing
  parent never recreates it or authorizes a save. Directory removal/rename drops
  its stale watches and reconciles descendants; reopening a recreated directory
  registers a fresh watch. Invalid durable storage still fails closed.
  Directory watches include their ancestors up to the workspace root because
  Windows reports a directory's rename to its parent, not to the watched contents.

## Language server transport

The Powernap transport closes the subprocess stream and joins JSON-RPC reader
disconnect instead of concurrently calling `Conn.Close`. The reader is the sole
owner of pending-call completion: an already-read response must settle before
EOF closes the other waiters. This prevents shutdown from sending a response
on a channel already closed by another goroutine.

LSP subprocess pipes must never block a caller goroutine. A synchronous,
context-unaware jsonrpc2 flush on a back-pressured server pipe once froze Bubble
Tea's update goroutine (the tab-close hang).

- `powernap` is vendored (`third_party/powernap`) with a `TransportWrap` hook in
  `ClientConfig`; `lspclient.LSPIntegration.Start` installs `lspPipeMonitor`
  (`lspclient/lsp_pipe.go`).
- `asyncPipeWriter.Write` only enqueues a copy; one dedicated writer goroutine
  performs pipe writes in FIFO order. The queue is byte-bounded
  (`lspWriteQueueCap`); a stalled server trips the cap and the jsonrpc2
  connection fails fast instead of blocking or corrupting framing. The bound
  applies to an empty queue too; short writes fail instead of replaying a frame.
  Writer Close shares one bounded join and reports an unjoined worker, never
  silent success after a timeout or a fresh detached waiter per caller.
- `lspPipeMonitor.Done()` closes when the connection dies for any reason (read
  EOF, write error, queue cap, graceful close). `workspace.LSPManager` observes
  `LSPClient.Done()`, marks that producer's retained diagnostics stale, marks it `LSPFailed` and
  stops the process. A lost server is not silently restarted on every keypress or
  open; the user restarts it explicitly. Test fakes return a nil `Done()` channel.
- `LSPClient.Stop() error` and the nested `Client.Kill() error` carry the exact
  joined retirement outcome. Concurrent stops join the same attempt. Failed
  startup with `ErrProcessCleanupUnverified` retains its reservation even when
  no usable client was returned. Unverified teardown keeps the exact client
  and server slot, rejects late diagnostics, and remains visible as Failed
  after Stop/disable instead of admitting a replacement or retry-spinning.
  A reaped server's own nonzero exit is an operational failure, not an occupied
  process slot; genuine containment, wait, pipe-close or worker-join failures
  remain failures.
- Workspace Trust and one core-wide process governor gate every installed LSP
  producer, including headless agent probes. Static only is the default.
  Native-root checks run off actor/Update locks; the final transport admission
  callback fences the exact launch epoch immediately before construction.
  Already-admitted starts remain owned across revocation and are cancelled and
  joined. Queued work cannot occupy an orphaned slot after its last wanted
  document closes, becomes oversized, or loses Trust. Only verified retirement
  releases a begun lease; unverified teardown retains global capacity.
  See [workspace Trust and limits](settings.md#workspace-analysis-trust).
- Unix group signaling occurs before leader reap and never targets a settled
  identity; unsupported/failed exit observation is unproven cleanup. Windows
  starts suspended, assigns the retained Job before resume, joins the leader
  and checks Job membership reaches zero. See the exact
  [fork lifetime limits](../../third_party/powernap/README.md): groups are not
  a sandbox and cannot prove absence of deliberately escaped descendants.
  Native Windows/macOS acceptance remains open.
- A start that fails because the executable is missing reports "`<cmd>` is not
  installed or not on PATH. Install it, then choose Retry." instead of the
  transport's wrapped errors. The Problems panel's server row offers **Retry**
  while failed or stopped and **Restart** / **Stop** while running; Retry is
  `RestartServer`, which re-resolves the command on the next start, so
  installing the server and choosing Retry is enough.
- `LSPIntegration.WithServerEnv` supplies explicit launch choices over one
  positive OS/toolchain inheritance policy in `lspclient/lsp_environment.go`.
  Arbitrary ambient variables, including unknown provider credentials, agent
  sockets, proxy credentials, loader options and tool-execution flags, are not
  inherited. Ambient PATH keeps only absolute directories outside the lexical
  workspace, with stable deduplication; symlink identity is not inferred.
  Explicit PATH must pass strict native validation. Environment maps and
  command arguments detach during preparation. Invalid environment data fails
  before Trust admission; the existing transport owner resolves the executable
  immediately before construction from that complete table, never ambient PATH.
  Missing executables retain Retry guidance. The
  [fork environment contract](../../third_party/powernap/README.md#complete-analyzer-environment)
  defines bounds, native search and Windows SystemRoot requirements.
  Explicit human choices can intentionally contain sensitive values; no raw
  environment or environment fingerprint is published. This is minimization,
  not a sandbox: trusted tools can still read home/configuration files and use
  native network access. Neither Trust nor a successful launch proves complete
  project context or credential isolation.
- `LSPServerSpec.ConfigurationScopes` carries complete effective module settings
  through the workspace runtime to one immutable, root-bound transport table.
  `workspace/configuration` respects each item's `scopeUri`, selects its longest
  directory match and preserves order, missing sections and null results. Module
  values never implicitly merge with another configuration. Unscoped requests
  use global settings; invalid or outside-root scope URIs return null.
  Preparation detaches caller maps and refuses invalid/ambiguous bindings before
  launch admission. URI matching is lexical, not native filesystem identity or
  permission. The [fork contract](../../third_party/powernap/README.md#launch-bound-configuration)
  defines encoded request/table/response bounds. Selecting and populating these
  bindings from a retained project model remains open.
- The client advertises `window.workDoneProgress`, answers
  `window/workDoneProgress/create` and tracks `$/progress` tokens.
  `AwaitDiagnostics` waits until the server's work settles (no active progress
  and a quiet interval) under the caller's deadline. This is observed quiescence,
  not proof of complete indexing, exact publication revision or whole-project
  coverage. Diagnostics carry `source` and `code`.
- `LSPManager` documents have holders: the editor view flag and a refcount of
  agent probes (`workspace/lsp_probe.go`). A document closes on the server only
  when no holder remains. Probes are allowed when the session's agent code
  intelligence is on and workspace execution Trust permits them even if editor
  LSP is off; a user-paused server refuses both. Probe results are plain data
  (`types.LSPProbeView`), never editor state.
  Probe readiness is published only after the transport has installed the exact
  captured document revision. An edit during the send remains pending; replaced
  document/server/client owners cannot publish a stale ready state. Releasing the
  last holder during an in-flight didOpen still closes that exact server view
  before a replacement at the same path opens.
  Probes borrow an immutable source lease from the shared document authority,
  including retained unsaved text after the last editor closes or disk disappears.
  They never read disk first and thereby hide retained work. Disk-only leases use
  bounded UTF-8 reads with exact handle/path checks; missing files, symlinks and
  replaced sources are not analyzed empty files. Final validation rejects changed
  canonical revisions (including equal-text undo), disk replacements and a new
  canonical view that appeared during a disk request. Rejected results expose no
  stale payload. A probe lease is not an editor view or execution permission.
  Canonical mutations enqueue their latest producer intent instead of waiting
  under the document lock for an in-flight server send.
  Probes return the same qualified diagnostic batch identity as connected
  editors and Problems. Complete project context and non-diagnostic semantic
  fact ownership remain [planned work](../plans/autonomous-code-intelligence.md),
  not a guarantee supplied by source leases or a quiet server alone.
- Language settings, resource limits and lifecycle are specified in
  [settings](settings.md#lsp); merely constructing a manager launches no server.
- Workspace shutdown publishes terminal admission and server retirement under
  the manager's same lock, then joins the worker. A worker must never observe
  `closed` before its owned servers are marked for shutdown: that gap can let the
  worker exit while an actual language-server process remains alive.

### Shared diagnostic facts

`DocumentService.Insight` owns the bounded published diagnostic store in
`insight/facts`. `LSPManager` remains the sole server/transport lifetime owner,
not another raw diagnostic cache. A batch identifies the workspace generation,
canonical document ID/path/revision and runtime lifetime, context generation,
actual producer instance, launch-configuration identity, publication sequence,
actual synchronized LSP wire version and optional server-reported version.
Canonical revision, send intent and wire version are separate clocks.
Runtime analysis identities travel with shared document publications but are
not persisted or reused across core restart.

Transport callbacks preserve immutable source provenance and optional versions.
Callbacks arriving before the manager records a send receipt occupy one pending
envelope for that exact send. Source range checks run against immutable sent text
off authority locks; final publication checks canonical source then producer under
the document-to-manager lock order. Old versions, equal-text undo, replaced
owners, malformed output and invalid UTF-16 endpoints cannot become a new current
answer. A nil reported version remains unversioned, including delayed URI-only
notifications that LSP cannot attribute to an earlier open lifetime.

The store retains one batch per observed document, including original stale
warnings and observed empty batches. Its initial bounds are 4,096 documents,
32,768 diagnostics and 16 MiB of accounted retained payload/metadata with bounded
status reasons. Capacity refusal is visible, never eviction or a truncated
clean answer. There is no per-document reclamation or persisted semantic cache
yet. Read snapshots detach nested slices/pointers. Status summaries are derived
once per fact generation and display policy; unchanged reads do not export all
diagnostics. Shells inspect generation/version cursors before copying changes.

Source edits, observed project inputs, settings/runtime replacement, producer
failure and view release conservatively invalidate the root-scoped context.
This initial context explicitly lacks a complete dependency/build graph,
inherited environment and verified executable digest. An exact wire-version
publication therefore remains **partial** even when empty. Observed units are
not the denominator of a complete project check. Neither quiescence nor zero
visible diagnostics certifies a clean project. Stop/restart keeps stale evidence.
Invalidating an already inactive boundary is idempotent: later asynchronous
teardown cannot restamp its first stale reason between shell snapshots. Only
observing a new active boundary admits another invalidation or publication.
Severity and editor/tree visibility filters are view policy, not mutations of
the published batch.

Editor, shared Buffer, Problems/tree/footer and agent diagnostics use the same
opaque batch ID. Editor projection requires actual canonical source identity,
including runtime lifetime, and acknowledged local text without pending edits,
history or conflict. Future batches wait for their source. Unversioned/stale
batches remain discoverable with provenance in Problems but cannot authorize
current paint, hover or navigation. Previously accepted same-batch ink can map
provisionally through unchanged rows without becoming current semantic evidence.

Agent source positions resolve against the immutable source leased for the
actual request, not an independent disk read. Diagnostic and same-source
location quotes use those captured bytes. Destination files lacking a separately
qualified source remain labelled raw UTF-16 positions without guessed disk
quotes. Automatic post-edit probes obey the same session, role-tool and read-policy
admission as explicit calls; a later revocation drops queued notices instead of
delivering or replaying them on the next model step.
Root-bound workspace Trust and shared LSP process admission are implemented.
General destination qualification, completion/hover/rename fact contracts,
project modeling, broader analysis work classes, pressure management and native
CPU/RSS containment remain open.

`insight/project` now provides passive compilation-database observations: a
bounded streaming parser retains exact artifact byte identity, multiple rows per
file, detached argv and opaque command-only spellings. It does not resolve paths,
tokenize shell commands, execute compiler drivers, authorize external reads or
prove freshness, artifact-set consistency, generated-input provenance or complete
project coverage. Retained discovery, configuration selection and provider binding
must supply those owners and preserve unknown inputs; parser success is not a
new current semantic fact.

`insight/project/cmake` adds passive CMake File API index, codemodel-v2 and
target observations over a caller-supplied bounded artifact set. It preserves
exact per-artifact identity and opaque compile fragments, validates references
and source/compile-group bindings, and returns detached observations for multiple
configurations. It performs no filesystem discovery, path resolution, CMake
execution or coverage attestation. A missing artifact or unsupported binding
cannot be an empty clean project. Retained discovery, freshness, configuration
selection and provider wiring remain separate work.

### Diagnostic positions

Insight batches, manager/replica and shared Buffer retain UTF-16 wire coordinates.
`Editor.SetDiagnostics` accepts the qualified batch and projects each endpoint
only against its matching acknowledged source. Its `Diagnostics` and F8 results are visual positions and
must not be submitted as wire positions again. Project-first-error, status links,
definition/reference and queued connected opens use the strict wire-navigation
API. Both endpoints are checked before cursor or selection changes; a late
destination open cannot move the previously active editor or steal newer focus.

Multiline underlines use a line-indexed projection capped at 256 rows per range
and 4,096 extra rows per publication, plus each diagnostic's start row. Complete
endpoints remain available for current-revision hover/navigation. This avoids
expanding an untrusted range across an entire source on Update. Display clamping
of malformed diagnostic columns remains tolerant; strict navigation does not
permit reversed, out-of-source or split-surrogate wire endpoints.

## Source projections and comparisons

The [native syntax owner](native-syntax.md) schedules selected grammars from
canonical shared documents. Connected clean editors consume its independent
revision-fenced topic; private optimistic text stays provisional. Standalone
and preview editors use the same native syntax for selected grammars; Chroma
continues lexical coverage for other languages, Markdown and diff consumers.
Both feed one source-byte-to-grapheme/theme projection. Syntax coverage is
shown separately from LSP diagnostics and project-check correctness.

- Syntax comes from one complete immutable document revision, never a lexical
  restart above/below the viewport. Long Python triple-quoted strings/docstrings,
  raw strings and block comments retain their original delimiters regardless of
  scroll order or terminal size. A source edit invalidates downstream parse context
  even when visible bytes did not change. While that revision is prepared,
  retained visible decorations map through the bounded exact line-edit journal:
  unchanged rows keep their ink, inline source edits reproject graphemes/tabs,
  and inserted/deleted lines shift surviving rows. These provisional colours are
  presentation only, replaced atomically by the complete new parse (including
  offscreen delimiter changes); they are never a viewport-rooted lexer result.
  Missing edit history, whole-source replacement and changed lines without a
  bounded mapping paint plain text rather than reuse unrelated offsets.
  Diagnostic paint follows the same source-line mapping: unchanged rows keep
  their underlines while LSP analyses, touched rows drop old ranges. Provisional
  decorations never authorize diagnostic hover or navigation; those require an
  exact current source revision and resume with the next server publication.
  Undo/redo, disk/peer replacement, path/language and
  theme changes fence older completions. Terminal-width changes reproject cell
  maps without re-lexing source.
- Document lexing runs off Update/Paint on append-only piece snapshots, with two
  concurrent workers, cancellation, a five-second preparation budget and exact
  editor/buffer/revision/generation completion. The shell delivers to its owner,
  including hidden tabs and read-only overlay previews; close cancels immediately
  and shutdown joins pending work. Lexer failure retains usable plain text and
  reports the unavailable highlighting. Chroma regex matching itself is not
  preemptible mid-token; cancellation is checked between tokens. Compact byte
  spans are source-owned; cell colour maps are lazily retained for visible lines
  in a bounded cache. Geometry/navigation never invokes the lexer.
- Source-code projections expand tabs at tab stops before width, wrapping,
  syntax coloring, and paint. Raw tab cells have no stable terminal geometry.
- Editor and comparison syntax share `rendering.SourceStyle`; never add a second
  token-to-theme map. Git/local diffs use one `screens/diffview` reader with
  source-aligned rows, native draggable split, one vertical scroll owner and
  exact source copy. Local comparisons are immutable, generation-fenced main
  tabs; Refresh is explicit and closing never consumes the source draft.
