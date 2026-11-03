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

After the program exits, shutdown fences document topics and reconciles pending
text against the exact canonical revision before using the same command capturer.
This source-readiness evidence is local to that exclusive flush and expires when
the revision changes. It does not fabricate a topic-applied ACK, unlock ordinary
Update admission, save the file implicitly or replay an uncertain command. A
refused read leaves pending private text available for the recovery barrier.

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
  The formatter is the one the [catalog](code-intelligence-catalog.md) names
  for the file's language (`FormatDocument` in `workspace/document_format.go`),
  run over standard input and output; a language without one keeps its exact
  bytes. Toggle comment uses the language's first catalog line comment token.
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
  The initial [retained artifact owner](project-context.md) now supplies bounded,
  generation-qualified explicit observations and core-owned invalidation, but
  does not yet populate provider scope tables or grant complete context.
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

### Qualified location navigation

The editor's **Code** menu and contextual keys use one navigation family:
**Alt+.** definition, **Alt+T** type definition, **Alt+M** implementation and
**Alt+;** references (including declarations). Definition settings gate the three
definition-family operations; References remains separate. Agent `lsp` probes
use the same raw-response normalization and provenance, including while editor
LSP is disabled under the existing session and workspace Trust policy.

One invocation binds acknowledged canonical text, private caret/selection, the
actual synchronized provider and the original semantic context. Unsaved
acknowledged text is valid; pending edits, shared conflicts and stale lifetimes
are not. Source changes, equal-text Undo, close/reopen, focus departure, workspace
switch and newer contexts retire the invocation. All answers, including literal
null, empty array and statically unsupported, cross the original pushed-context
barrier. Provider/protocol failures stay failures, never successful absence.

`lspnav` consumes original standard JSON, preserving homogeneous Location or
LocationLink targets, full target/selection ranges, optional origin and order.
Malformed, mixed, duplicate-member, noncanonical-coordinate and unsupported
shapes are refused atomically. Its bounds are 1 MiB raw JSON, 1,024 targets,
4,096 UTF-8 bytes per URI and 64 JSON container levels. Queried and selected
source have a 2 MiB navigation consumer limit. The existing shared-source lease
can read or clone up to its 16 MiB document limit before that selected-source
check. These are consumer bounds, not bounds on initial acquisition allocations,
the JSON-RPC library's initial frame allocation or native heap/RSS.

A single supported in-project definition-family target can jump directly.
Multiple targets, all references and external destinations use the native
chooser. Enter on a row inspects complete copyable URI/path, ranges and original
source/provider/context metadata without reading destination content. Detail
defaults to **Close**; **Back** returns to the list. **Open** explicitly selects
one target, with distinct outside-workspace wording. Non-file URIs, remote file
authorities, query/fragment-bearing or malformed paths stay inspectable but
unavailable. Listing never eagerly reads a private or dependency file.

The selected-target command alone observes current canonical unsaved text or
one bounded regular-file source off Update. Strict UTF-16 range checks reject
surrogate halves and invalid line/column boundaries. Opening reuses the existing
async core-document owner and verifies the observed hash/lifetime against the
actually opened snapshot before installing a target tab or moving the cursor.
Unresolved private target recovery or a failed recovery read refuses navigation
before tab installation; ordinary file-open remains its explicit recovery path.
During landing, the shell fences the original source producer's exact live
instance in the published server health, not the aggregate active-file status.
Opening a target can start a different server without retiring the source;
stopping, replacing or revoking the source producer still retires the landing.
Late source/focus results cannot open a tab. Neither the hash nor an exact
selection supplies a missing target version: standard LSP locations are
**unversioned targets**, and semantic destination freshness remains unverified.
The visible landing is non-editing and does not select text for replacement.

Agent listings preserve the same distinctions and unversioned warning, expose
at most 50 rows with an explicit omitted count, and never read destination files.
This manual navigation slice does not implement navigation history, hierarchy,
dynamic capability registration or complete semantic/project coverage.

### Read-only inlay hints

Code Intelligence's **Inlay hints** feature defaults **Off**. When explicitly
enabled under the existing master and workspace Trust gates, visible connected
editors request debounced, acknowledged-source line ranges with a small row
margin. **Show Inlay Hints**, **Hide Inlay Hints** and **Refresh Inlay Hints**
are palette actions; the editor View menu also exposes Show/Hide and inspection.
Show does not change settings or Trust. Hide retires only that private editor
view's request and labels, not another pane, shell or shared language server.
Source edits remain immediate; only read requests are debounced.

**Alt+J** opens the native read-only list. Enter inspects the selected original
row, never edits source, opens a label location or executes an adjunct command.
The copyable detail shows the complete accepted source/provider/context identity,
requested range, local wire source, expiry, labels/parts and tooltips, together
with inert edit/command/location metadata. Original and resolved JSON are escaped
literal previews with explicit byte-count truncation, not reconstructed JSON.
Detail defaults to **Close**. **Resolve tooltips** is explicit and available only
when advertised; only top-level and label-part tooltip enrichment is accepted.
**Back** or Escape from detail cancels the selected reader before returning to
the original list; Escape from the list closes it. An old reader cannot reopen
detail after Back or replace a newer selected row.

The core retains one original set per exact owner/view/monotonic sequence.
All answers, including null, empty array and statically unsupported, use the
same canonical source, synchronization, provider, settings and context fences.
Disable/re-enable has a separate feature epoch; it revokes old evidence without
restarting the producer or restamping diagnostics. Source changes, equal-text
Undo, close/reopen, detach, provider/context replacement, expiry, hide and workspace
boundaries retire the appropriate evidence. The shell waits for the exact
replicated context cursor before painting or reporting a negative result.
Standard inlay responses carry **no document version**; the wire value records
the synchronized request source, not provider-reported semantic freshness.

One shared per-line insertion projection maps source bytes to cells for paint,
caret, pointer hits, vertical desired columns and horizontal extent. Labels and
padding never become source, selection, copy, search, diagnostic spans, edits,
Undo/Redo or savepoint content. A label press is a zero-length source anchor,
including a backward drag; source presses retain their full source-unit rule.
Tabs remain source-column-based and graphemes clip as whole cells. Terminal
controls in labels are displayed as escapes. Surrogate halves, mismatched
byte/UTF-16 positions and unrenderable inside-grapheme placements fail closed
without clamping a label onto a different source anchor. Warm paint reuses the
projection; focus, presentation and idle ticks are not invalidations.

`lspinlay` passively consumes original JSON with a 1 MiB raw-response bound,
1,024 hints, 32 label parts, 4 KiB per label, 64 KiB combined labels and 64
container levels. Source is bounded to 2 MiB. Hint anchors include the requested
end, permitting zero-length and final empty-line EOF requests. The coordinator
permits 16 active views, separately bounded 128 sequence/tombstone records and
16 MiB of retained original-object bytes, with a five-minute lifetime. The
editor additionally bounds combined projection runs to 131,072; the inspector
caps complete display at 256 KiB with honest raw-preview limits. These are
consumer/accounting bounds, not initial RPC-frame, native heap/RSS or complete
project-context guarantees. Dynamic registration, global refresh notifications,
hint command/edit/location execution and broader IDE workflow acceptance are
outside this slice.

### Inline prediction projection (prerequisite)

The editor can prepare one private, literal, insertion-only **single-line**
candidate at an exact local buffer revision and byte caret. It uses the same
source-to-cell projection as inlay hints, not another paint/hit/caret path.
Inlays precede the caret; muted candidate text follows it. Candidate cells map
back to their zero-length source anchor and never become selection, search,
diagnostics, copy, canonical edits or savepoint bytes until explicitly accepted.

**Tab** accepts the whole candidate, **Ctrl+Right** one complete-grapheme word
segment (including its leading spaces), and **Escape** dismisses it before
closing the source tab. With no candidate, ordinary Tab still indents. Exact
typed-through source insertions retain only the remainder without another
request; mismatches, auto-pair side effects, peers, history, selection and true
caret/owner departure retire it. Terminal blur, a modal reader, another pane/tab,
workspace switch, attachment-epoch replacement, buffer replacement and shutdown
revoke private acceptance even when source bytes remain unchanged.
Fresh and late automatic LSP completions cannot steal a retained candidate's
keys; a fresh explicit completion invocation intentionally retires the candidate.

Preparation rejects empty, invalid UTF-8, multiline, control/bidi-bearing or
over-4-KiB candidates instead of trimming, escaping or repairing source code.
Source lines and combined projection runs retain the existing inlay bounds.
Warm painting reuses retained source and projection geometry.

This is an internal editor prerequisite, **not an enabled prediction service**.
No automatic provider request, privacy opt-in, budget meter, multiline candidate
or next-edit behavior is supplied by this projection. Those require the retained
coordinator and human-owned consent/accounting gates in the active plan.

The separate `provider.CompleteTextOnce` and `CompleteFIMOnce` libraries preserve
literal successful output and original-wire token-count presence. They make one
POST without retries, redirects or adapter-internal fallback; caller/account
timeouts apply beneath a 60-second hard ceiling. Event, total-wire and output
bounds fail closed. An accepted HTTP response does not prove remote inference
has stopped or that billing is complete. Only `UsageComplete` after a successful
terminal stream can justify treating both reported counters as final.
The `insight/predictionbudget` ledger reserves before an attempt, admits each
ticket only once and retains unknown-usage charges across restart. It does not
grant consent, send requests or create chat sessions, and is not yet wired into
an editor coordinator or visible meter.

The source now also contains workspace-only human consent preferences and an
internal bounded request-service composition. Consent is independent of project
execution Trust and never inherits application/session choices; see
[prediction consent](settings.md#workspace-prediction-consent). Its exact-root,
connection and positive-cap requirements do not themselves send source.
The internal service composes canonical source, the bounded partial context,
explicit text/FIM adapters, one joined local request per view, sequence
tombstones, policy generations, digest caching and durable accounting. Its local
HTTP-body join does not prove remote inference or billing stopped.
The service is not yet installed in the workspace/editor transport lifecycle;
there is still no product request action, automatic pause scheduling or visible
meter. Multiline candidates, next-edit, Explain/Fix and complete fact-context
assembly remain separate work, not implied by the single-line projection.

### Parameter information

**Alt+I** (or Ctrl+Shift+Space when delivered by the terminal) and the editor's
**Code > Parameter information** context action issue one explicit standard
signature-help request through the same retained server. The initialized provider
must advertise support; unsupported, failed and supported-but-empty answers stay
distinct. There is no language-name branch, new server or inferred call parser.

Ordinary native `(` and `,` edits also request parameter information. One
shell-local intent waits for the exact typed source and caret to be acknowledged,
then uses the same provider/request/context boundary as explicit inspection.
Input queued behind Undo triggers only when the original key actually executes.
Paste, API edits, peer publications and reopening a tab are not trigger input.
An intervening edit, selection, caret/focus/lifetime/workspace change or a
five-second acknowledgement deadline retires the intent without querying older
source. A ready-provider/source publication can lead the initial semantic-context
cursor; the same intent waits for that cursor rather than being dropped or
querying unobserved context. Automatic missing/unsupported/empty/failed answers
and timeouts stay quiet; they do not create loading toasts while typing.

The automatic WindowFrame/TextArea card is passive and anchored immediately
below or above the actual painted caret, with native Unicode, tabs, inlays,
scroll and nested-pane coordinates. It never captures the next typed key.
On short grids, the reader shrinks to the larger space above/below the caret and
retains native scrolling, rather than centering or covering the source caret.
Escape dismisses the pending intent/card before closing the editor.
**Alt+I** explicitly requests the complete focusable reader. That reader and
symbol documentation also open at the caret instead of the screen center.

The native read-only WindowFrame/TextArea reader shows the selected signature,
active parameter and its documentation, the other overload labels, provider,
canonical source revision, context generation and unknown inputs. It supports
normal reading/selection/copy, resize, Close, Escape and outside-click dismissal.
It never inserts text, chooses an overload on the server or edits an argument.
Only the two native delimiter inputs trigger automatic assistance; dynamic
capability registration, language-specific call parsing and full semantic-feature
acceptance remain outside this slice.

Requests require acknowledged canonical text: optimistic edits, pending history,
conflicts and standalone raw buffers cannot query an older canonical source.
The common editor source gate is shared with diagnostic projection. One invocation
owns its five-second request/result lifetime. A later invocation, caret/source
change, equal-text close/reopen, focus departure, workspace boundary or Stop
cancels it. A backend ignoring cancellation still cannot reopen its reader.
The coordinator stamps exact source/provider/context provenance. The shell waits
for the corresponding pushed semantic-context cursor if its replica trails the
result, and discards it on a newer/replacement context; progress/diagnostic status
updates alone do not invent a new context.
Supported empty/null evidence passes the same source/context publication barrier
before reporting absence. The loading toast belongs to the exact invocation and
is dismissed on publication or retirement without clearing unrelated feedback.
Docked terminals and nonmodal session panels are input owners too; their focus
retires a pending request rather than allowing its late reader to steal input.

Connected semantic reads use the common cancellable command exchange. It retains
the original request identity and waiter after the UI retires, issues exact
job cancellation only after confirmed actor enqueue, and joins/ACKs both outcomes.
Cleanup is client-owned and bounded to five seconds after cancellation; the
shared provider is not stopped. An uncooperative provider, failed carrier or
saturated command table can leave retirement/ACK cleanup unconfirmed. That is
reported with the original identity, never repaired by replaying a request under
a new attachment, dropping unknown retained work or raising the command limits.
This cooperative boundary is not native CPU/RSS containment.

The consumer admits at most 64 KiB of encoded signature JSON, 32 signatures and
64 parameters per signature. It preserves omitted versus explicit-zero active
parameter overrides and applies the protocol's invalid-index defaults. String
labels and UTF-16 offset pairs become detached parameter text, not editor ranges
or terminal columns. Invalid/split-surrogate offsets and unsupported null-active
extensions are refused rather than guessed. Markdown documentation is a basic
plain-text projection, not a complete Markdown layout engine. These are
consumer-data/work bounds, separate from transport framing or native heap/RSS
containment. All protocol and text preparation runs off Update.

### Reviewed symbol rename

F2 on a connected, saved and acknowledged editor opens the native symbol-name
prompt. Enter requests an immutable **Review rename** from the core, not a shell
read or a reconstructed `WorkspaceEdit`. The review shows exact quoted changed
spans, UTF-16 coordinates, paths and before/after hashes. Cancel is initially
selected. Enter on Cancel or Escape changes no source. Left/Right and Enter select
an explicit action; the read-only body supports pointer scrolling and keyboard
line/page movement.

The core binds the invoking canonical document lifetime/revision, attachment
epoch, actual provider instance, synchronized wire versions and semantic context.
Equal-text Undo, peer edits, close/reopen, detach and configuration/provider/context
replacement invalidate approval. Every target must be a clean reconciled regular
workspace file. Native file, parent and mode preimages are retained from review
and rechecked through the single staged FileOps commit/rollback writer. A foreign
same-bytes replacement is not the transaction's rollback authority.

`lspedit` is the passive strict UTF-16 transform and original-JSON edit normalizer.
It preserves numeric zero, explicit null and missing version tokens separately.
`documentChanges` takes precedence when both representations are supplied, but
both declarations must be supported and within aggregate bounds. Exact numeric
versions require that provider's synchronized canonical source; explicit null
cannot be rebound to an open or retained canonical target. Missing versions,
resource create/rename/delete, annotations, snippets, ambiguous equal-start or
overlapping edits and malformed ranges are refused atomically.

Bounds are 1 MiB original provider JSON, 200 targets, 1,024 edits, 64 KiB per
consumed string and 1 MiB aggregate replacement text. Reviewed source and result
are each at most 2 MiB per file, 16 MiB combined across the operation. Exact
quoted preview text is at most 64 KiB, at most four pending previews are retained,
and a pending preview expires after five minutes. These adapter bounds do not
bound the external JSON-RPC library's initial frame allocation.

Apply carries only the original opaque review ID/digest. A durable non-agent
intent precedes an atomic retained Local History safety reference for the exact
reviewed preimages. Supplied-byte selected capture does not walk or retain
unrelated project content. Native and owner/provider/context checks precede
commits; successful canonical publication carries the reviewed sparse edits and
savepoint, preserving peer-private anchors instead of replacing the whole document.
Recovery is through Local History, not another author's Ctrl+Z group.

Refusal, committed and unconfirmed outcomes are distinct. Unknown transport,
rollback, publication or persistence results never offer a second Apply.
**Check result** observes the original operation, including after a replacement
attachment for the same client; it never renews mutation authority. Close retains
committed/ambiguous safety references, with unresolved review/safety identities
recorded in Notices. No automatic age-based disposition of those references is
implemented. A success message requires a nonempty verified safety identity.

Local History's **Review safety references** reader administers these references
independently of the original rename dialog. It preserves original outcomes,
offers human-confirmed release only with exact settled evidence, and permits
re-protection only while the original snapshot still verifies. Ambiguous,
unattributed or corrupt evidence stays protected. This is retention metadata
administration, not another Apply or undo path; see
[safety protection](local-history.md#reviewed-edit-safety-protection).

This is a bounded manual text-only rename workflow, not general refactoring,
dynamic registration or `workspace/applyEdit` support.

### Manual code actions

The editor's **Code actions** context item and semantic `CodeActionsChord`
(**Alt+Enter** on legacy Unix terminals, **Ctrl+Enter** when modified Enter is
delivered or the host reserves Alt+Enter) request one explicit list for the saved,
acknowledged
canonical source and exact caret/selection. A strict editor query maps display
endpoints to UTF-8 source bytes, then the canonical coordinator maps them to
UTF-16. Split, ambiguous, reversed or out-of-source endpoints are refused, never
clamped or widened. The query does not mutate source, view state or history.

The native list keeps disabled and unsupported actions visible for inspection.
Enter opens complete quoted title/kind, eligibility, diagnostic inclusion detail
and source/provider/context provenance. Detail defaults to **Close**, not Preview.
**Preview changes** requests only the retained original row's edit, including lazy
resolution when the static provider supports it. It cannot execute a command.
The resulting **Review code action** composes the same read-only,
Cancel-default review, Apply/Release/Check-result state machine and selected Local
History safety used by rename. There is no second file writer or receipt machine.
Successful source effects arrive through canonical document topics, not a shell
reload or private result. Other shells retain their private cursors and drafts.

This slice accepts a literal `WorkspaceEdit.changes` map or exactly one ordinary
`TextDocumentEdit` in `documentChanges`, targeting the invoking clean canonical
file. A versioned target must carry a nonnull positive int32 version exactly
equal to the original synchronized provider wire clock. Missing, null, zero,
stale or future versions are not inferred. Commands (including edit-plus-command),
resource operations, annotations, notebooks, mixed representations,
multiple/foreign files and no-op edits remain visibly unsupported.
It never drops the unsupported part and
applies the remainder. Resolved objects may add only `edit`; changing/removing
original attributes, opaque data or numeric token spelling is refused. This is a
deliberate bounded protocol subset, not general provider interoperability or a
complete quick-fix/refactoring claim.

Original diagnostic JSON belongs to the transport's publication adjunct, never
reconstruction from display DTOs. Only exact versioned, nonlimited, strictly
validated source ranges matching the requested range are forwarded. Absent,
unversioned, stale, malformed or overbound evidence is omitted with a reason;
the request still carries `diagnostics: []`. The pinned
[`jsonrpc2` request decoder](../../third_party/jsonrpc2/README.md) preserves raw
params before notification dispatch rather than erasing duplicate/invalid-string
evidence or opaque number spelling. Consumer bounds do not limit its initial
frame allocation or establish native CPU/RSS containment.

The core retains at most four action sets, each for five minutes and bound to the
exact attachment epoch, canonical lifetime/revision, synchronized wire source,
provider and semantic context. Only set ID/digest/index cross back in a Preview
request; raw actions, original diagnostic objects and caller-authored edits do not.
Release, expiry, peer edits/equal-text Undo, document close/detach, provider/context
replacement and shutdown revoke pending preparation and Apply. Closing a chooser
cancels its request; a late result cannot reopen it and releases its retained set.
An action list transfers its lifetime to the shared review until that review
closes; explicit Cancel awaits both its pending review and related list release.
These are pending capability releases, not release of retained recovery
snapshots or automatic settlement of uncertain mutations.

Lists admit at most 1 MiB of original JSON, 64 items, 64 KiB per item, 64 nested
levels and 64 KiB of display evidence. Titles are at most 4 KiB and kinds 256 bytes.
Source/result, exact-preview and retained-review bounds are the rename bounds
above. Supported null/empty lists pass the same source/context replica barrier
as positive results. They mean only **No actions returned for this source and
selection**, never that the file or project is clean. Selection comparison during
Update is constant-time; source-byte conversion runs once per explicit request,
not once per tick or painted frame.

### Completion documentation and original insertion

Ctrl+Space and the automatic-completion debounce share one canonical completion
invocation. The native caret-anchored WindowFrame contains a List with standard
kind glyphs and detail, plus passive selected-row documentation. Wide frames put
the reader beside the list; constrained frames stack it below. Up/Down, paging,
Home/End and pointer selection use that one List. Ctrl+PgUp/PgDn pages the native
read-only TextArea without moving the selected completion; pointer selection and
Ctrl+C copy inert text. Ordinary typing dismisses the chooser and reaches the
editor exactly once. Escape/outside dismiss only the invocation. Ctrl+Space on an
active numeric choice field still opens **Snippet choices** first.

The core retains the original array or CompletionList item, original index and
opaque row ID, not a label-keyed reconstruction. Equal labels remain independent
rows. Null, empty, unsupported static capability, unsupported insertions and
provider failures remain distinct. `isIncomplete` and omitted unsupported-row
counts are visible; an incomplete result is never advertised as complete.
CompletionList defaults and dynamic registration are not advertised or accepted.

Initial documentation preserves string versus MarkupContent, plaintext versus
markdown, absence versus an explicitly empty value, and optional detail. Markdown,
links, terminal control bytes and commands are displayed literally, never
interpreted or executed. If the original provider advertises resolve, selecting a
row requests `completionItem/resolve` passively with its exact retained original
object. Only detail and documentation may change. Missing or changed original
fields, new insertion fields, changed opaque data/numeric tokens, sort/filter
changes, edits and commands are refused atomically. A resolved object never
replaces an insertion or its numeric plan. Revisiting a resolved row uses only
that invocation's display cache; a late former-row answer cannot replace the
selected reader.

Every request, including negative evidence, carries the acknowledged canonical
source, exact synchronized wire version, actual producer instance and original
context. Standard completion results do not report a source version and remain
labelled **unversioned**; unknown context inputs remain partial. The shell waits
for the original pushed context cursor before presenting semantic evidence.
Source/caret/selection, tab, focus, workspace, provider, settings and expiry
boundaries retire old callbacks. A dedicated feature generation prevents an
off/on settings cycle from reviving old evidence without restarting the server.
Automatic absence/failure stays quiet; explicit requests give bounded feedback.

Enter/Tab or row activation first inspects the exact retained set ID/digest/item
without resolve. The core validates all original primary/additional edits against
the still-current canonical source. The shell then rechecks its original editor
and applies only the initial insertion through the existing editor transaction.
Semantic activation reports local admission while that ordinary canonical edit
is pending; a successful documentation read is not a text/save receipt.
An omitted insertion may use its label; an explicitly empty insertion never does.
The shared `lspcompletion.PrimaryEdit` chooses the strict UTF-16 range or backward
Unicode identifier prefix, so validation and application cannot derive different
prefix edits. No request or documentation observation changes source, saved
bytes, author history or the private composer.

Bounds are 2 MiB source, 1 MiB encoded result, 256 original items, 64 KiB consumed
strings, 32 JSON container levels, 64 additional edits per item and 1 MiB
accounted normalized output. Malformed or over-bound evidence is refused, not
truncated. Per-document-service retention is at most 16 live views, 128 sequence
records and 16 MiB accounted original/display payload with a five-minute lifetime.
Closing a chooser cancels its reads and releases its exact private sequence;
late old releases cannot retire newer invocations. Close/detach/shutdown revoke
the corresponding source views. These are consumer/retention bounds, not decoder
allocation ceilings or native CPU/RSS containment.

### Numeric completion snippets

Standard completion items may carry a bounded numeric snippet plan through the
same retained provider and typed connected completion result. `snippet/` is a
passive, language-independent parser. It expands `$1`, `${1:default}`, nested
defaults, repeated/forward numeric mirrors and escaped literal choices. Variables,
transforms and provider commands are refused without reading environment values
or inserting raw snippet syntax as a fallback. Additional completion edits remain
literal UTF-16 source edits, never another snippet source. A primary TextEdit or
InsertReplaceEdit replacement takes precedence over InsertText and the label;
an explicitly empty expansion never inserts the label.

The initial transport plan uses source-preorder byte spans relative to only the
expanded primary insertion, with exact parent indices. Its SHA-256 pairs that plan
with its expanded text; it is a consistency check, not permission or source
authority. Validation rejects invalid UTF-8, crossed/overlapping siblings, cycles,
inconsistent mirror values/subtrees and children inside literal choices. Parser
bounds are 64 KiB source and expanded text, 256 occurrences, 64 alternatives per
choice, 64 KiB aggregate copied choice bytes, 16 containment edges, 2,048 parsed
nodes and stop IDs through 1,000,000. The canonical completion consumer admits at most 256
items, 64 additional edits per item, 64 KiB per consumed string and 1 MiB aggregate
input/accounted output. An oversized result is refused atomically, not silently
truncated or advertised complete. These limits bound consumer work after native
protocol decoding, not transport allocations or native RSS.

Accepting a completion by Tab/Enter or pointer commits the primary and additional
edits as one existing editor transaction. The exact-byte recorder commits one sparse
original-coordinate vector. Imports before the primary insertion
shift its private placeholder anchors, not the server's original ranges. Every
range and initial plan is checked before bytes, selection or history change;
unrepresentable joined-grapheme endpoints are refused. Empty or byte-identical
snippets can still install navigation without creating an edit. Accepting Tab does
not also advance the newly installed interaction.

Positive stop IDs are visited once in ascending numeric order with Tab. Shift+Tab
visits the previous stop; at the first it consumes without wrapping or outdenting.
The first surviving zero occurrence in source preorder supplies the final landing
at its end; if an outer replacement removed every zero, the transformed primary
end remains the fallback. Repeated/nested zero occurrences are not extra stops.
Reaching the final landing retires interaction; later Tab/Shift+Tab resumes ordinary
indentation. Escape first retires snippet navigation/selection without closing the
editor or changing accepted bytes.

Defaults use the normal source selection. Typing, paste, Enter and deletion use
the ordinary editor commands, including replacing a selected default when typing
a pair opener. A passive notification in the one PieceTable mutation dispatcher
only transforms anchors. The outer transaction finalizer then derives disjoint
mirrored splices and records them in the same logical edit group before the
canonical group and content notification close. The same PieceTable batch owner
handles ordinary Insert/Delete and multi-splice commands: it checks the whole
vector before mutation, advances source/geometry once and publishes every sparse
observation in reverse sequential coordinates after the complete source commits.
It does not rebuild the document cache or invalidate all line widths per mirror.
Multiple substitutions do not pretend to be one inline syntax mapping. It never
mutates recursively inside the PieceTable observer or takes over the canonical observer slot. Editing
a nested field updates its enclosing mirrors too. Replacing an outer default
retires touched copied descendants while preserving independent surviving fields.
Arbitrary user text is allowed in a choice field; initial choices are not a
validation rule for subsequent typing.

Ctrl+Space on a stop with alternatives opens **Snippet choices**, a native
WindowFrame/List with ordinary keys, pointer and scrolling. Enter/Tab or a row
activation applies the exact current alternative as one editor command. Empty
choices have an explicit label. Escape/outside dismisses only the picker; another
editor, source revision, selection, interaction or invocation cannot reuse an old
choice callback. Automatic completion does not steal an active snippet's Tab.

The core remains the only connected text and author-history owner. Private
snippet navigation is neither replicated nor persisted. Exact own receipts and
savepoint-only publications retain it. Canonical peer batches prepare private
anchor transforms as part of the same all-or-nothing publication; they use
simultaneous original coordinates, unlike local primitive observations. Strictly
disjoint edits can preserve navigation. Touching/intersecting peer edits retire
it and never initiate local mirrors. Failed publication preparation changes
neither text nor interaction. Undo/Redo intent, reset/recovery, source/buffer
replacement and true input-owner departure retire interaction without discarding
accepted work; Redo restores bytes, not an old private session. Terminal docks,
session decks, unrelated overlays, tab/workspace changes and terminal blur are
ownership boundaries too.

The [completion owner](#completion-documentation-and-original-insertion) now
retains original identity, presents incomplete-state evidence and resolves only
documentation/detail. CompletionList defaults, provider commands,
variables/transforms and richer semantic operations remain unsupported. Full
connected, native and whole-plan acceptance retain their own evidence and are
not established by parser tests or this contract.

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

#### Project producers and unopened files

`LSPManager.SetProjectProducers` takes project-scoped servers (a root and a
launch spec). A project producer starts without an open document, through the
same trust, master switch, start policy, pause, `MaxServers` and governor
admission as document servers, and shares a process with documents that resolve
to the same root and launch configuration. It is exempt from idle retirement
while it is wanted. It never causes `didOpen` for a file the user did not open.
The assembly service (below) is its caller.

The transport routes unversioned `publishDiagnostics` for files the client has
not opened to `OnUnopenedDiagnostics`. A versioned publication for an unopened
file is dropped, because the version names a text the client never sent. The
manager publishes such a report as a `disk` fact with document ID
`unopened:<path>`, bound to the producer instance and its context key, not to a
text revision. `InsightDiagnostics.FromProjectCheck` identifies these facts.
Only reports inside both the workspace root and the producer root are accepted.

- An open document owns its file: opening it removes the project fact, and
  unopened reports for it are ignored until it closes.
- Editor context changes do not make project facts stale. The server
  republishes the files an edit affects.
- Stopping, retiring or removing the producer removes its facts and releases
  their store capacity.
- Problems lists a project fact with the reason "This file is not open. The
  project check found these problems." Workspace health counts it. Go to
  diagnostic opens the file and applies the location unless the buffer has
  unsaved changes.

#### Assembly service and coverage

The assembly service (`workspace/lsp_assembly.go`) plans the project's code
intelligence from data. It runs no project code.

1. **Discovery.** A bounded listing of file names (`third_party/pathwalk`, two
   workers, at most 100,000 entries, hidden folders and symbolic links not
   followed) counts files per language through the
   [catalog](code-intelligence-catalog.md) and custom languages. A larger
   project is marked limited.
2. **Resolution.** For each language, launch resolution picks the server. The
   program is looked up without starting it: an absolute path from settings,
   then the tool store, then the system path. Path folders inside the
   workspace are skipped, so a project cannot supply its own server program.
3. **Plan.** One `types.AssemblyPlan` per workspace: each language with its
   file count, server, program, source, status (ready, not installed, off, no
   known server), checked scope and a plain reason. The manager publishes it
   in its snapshot (`Assembly()` on the shell port) and the control API's
   diagnostics `lsp` view lists it.
4. **Activation.** A ready server whose catalog scope is `project` becomes a
   project producer at its project root. A server found in the tool store
   launches from there. A server that is not installed never launches: the
   status names the missing program, and editing stays usable.

The workspace starts the service when its analysis runtime starts. Settings
changes and **Start Language Servers** run it again; a newer run cancels an
older one and a stale result is dropped. Close cancels and joins it.

Coverage is shown where the user looks for problems. Server rows show their
current state and declared scope as a capability, not proof of a complete
project check. **Project Languages** lists every language found, with the
installed server, its program and declared capability; installation alone does
not mean it is running. A language whose server is not installed offers
**Check again** after installation.

The language-server manager derives the active file's visible language status.
The editor header shows error and warning counts, or that status label. The
Problems footer shows counts and marks restricted or stopped services as off,
and failures as errors. Workspace health shows counts or the manager's status
message and routes its next action to the existing trust, settings or server
controls. No result is a clean-project claim. Diagnostic provenance remains in
the diagnostic details reader under **Details**, not in Problems rows.

Agent source positions resolve against the immutable source leased for the
actual request, not an independent disk read. Diagnostic and same-source
location quotes use those captured bytes. Destination files lacking a separately
qualified source remain labelled raw UTF-16 positions without guessed disk
quotes. Automatic post-edit probes obey the same session, role-tool and read-policy
admission as explicit calls; a later revocation drops queued notices instead of
delivering or replaying them on the next model step.
Root-bound workspace Trust and shared LSP process admission are implemented.
General destination qualification, completion/hover fact contracts,
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

### Diagnostic readers

The editor context menu's **Show diagnostics** replaces the centered label stack
with a native WindowFrame/read-only TextArea. It inspects the clicked source point
without consuming an existing selection; a keyboard-invoked menu uses the mounted
caret. Pointer hover and explicit details share the same reader builder and exact
fact-publication owner, not separate wrapping or source-coordinate models.

The source point projects through the editor's existing tab, grapheme, inlay and
scroll geometry plus the actual mounted pane offset. Readers fit below or above
that point, shrink to the available side, and re-anchor on the first resized
frame. An off-viewport source is unavailable, not clamped into a fictitious visible
anchor. Resizing retains the original TextArea, text and selection.

Complete original messages, severity, reported source/code, source-line preview,
partial-coverage qualification and fact/provider/context provenance are readable.
Provider control characters are escaped literally, never interpreted as terminal
commands. A separate 64 KiB reader budget refuses an oversized display with an
explicit notice rather than presenting a clipped apparently-complete answer. The
retained fact remains unchanged. Source-line previews above 4 KiB are omitted
explicitly; this does not truncate the diagnostic message.

Explicit details focus the native reader. Hover opens passively, accepts wheel
browsing without taking typing focus, and an explicit body click enables keyboard
reading. Page keys, selection and copy use the shared TextArea. Escape and the
explicit reader's Close/outside actions retire inspection without closing the
editor or consuming its private draft. Pointer departure retires passive hover.
With selected text, the first Escape clears that native selection; the next
Escape closes the reader. The shell does not bypass the shared text control's
selection/back handling.

A small publication cursor validates the original fact ID and eligible canonical
source without cloning a batch each tick. New publication, source/history/lifetime,
visibility, editor/selection, modal or workspace departure retires old inspection.
A context menu opened for an older fact cannot inspect its replacement. These are
source-bound partial observations, never proof of a clean or fully checked project.

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
