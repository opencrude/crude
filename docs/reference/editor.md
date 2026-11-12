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
  A selected source range returns an owned string copied from that range, not a
  copy of the whole document; later edits cannot change the returned text.
- Backspace within leading whitespace removes to the previous indentation stop,
  never into following words. At line start it joins the previous line; Delete
  at a line end (including an empty line) removes the complete terminator. CRLF
  is one terminator, excluded from line content. Enter follows the document's
  first terminator, inherits indentation and opens a matching bracket pair onto
  separate lines. Paste gives every pasted line break (LF, CRLF or a lone CR)
  that same terminator, so a paste never mixes terminators or leaves a bare CR
  inside a line. A terminal sends the line breaks of a bracketed paste as CR;
  see [text input](text-input.md#clipboard).
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

### Native editing actions

The editor owns one typed action path. Keyboard shortcuts, the editor menu,
**Editor actions** (Alt+/), and exact-view semantic controls use the same
operations. The searchable action picker reuses the command-palette frame and
filter; the global palette still omits context-bound commands. A retired picker
or menu cannot edit a different view or a newer source/selection.

- Ctrl+D duplicates the line or selection; Ctrl+Shift+D remains an alternate.
  Alt+Up/Down moves lines; Ctrl+Shift+K deletes them. Ctrl+Shift+J joins lines
  with one space. Joining is lexical, not a language-aware rewrite.
- Shift+Enter starts a line below; Ctrl+Alt+Enter starts one above. On terminals
  without modified Enter, the shared chord labels show Ctrl+Alt+O and
  Ctrl+Alt+Shift+O. Each uses the document's indentation and line terminator.
- Ctrl+L selects the line, then extends by a line on repetition. Alt+W expands
  the selection; Alt+Q restores its exact prior range and caret. Current
  native syntax nodes and lexical bracket interiors precede line/paragraph/file
  ranges by containment. Independent selection, movement, source or buffer changes
  retire the growth chain. No stale parse is used as a source range.
- Ctrl+Up/Down moves between paragraph boundaries; Shift extends the selection.
  Alt+B/F moves across identifier parts, including camel case, acronym boundaries,
  underscores and digits without splitting a grapheme. Alt+Shift+Left/Right
  extends the selection (Shift+Alt+F remains Format Document).
- Ctrl+Alt+B goes to the matching bracket; Shift extends the selection. Exact
  syntax/lexical results exclude strings and comments. Both delimiter cells use
  restrained accent ink. Missing or stale results never manufacture a match.
- The action picker also offers case conversion, line sorting/reversal,
  character transposition, indentation and comment toggling. Text mutations
  are one native transaction with one Undo step; read-only views expose only
  selection and movement. Ctrl+W still closes; Ctrl+Y still redoes.

An action queued behind connected Undo retains its action ID in private recovery
alongside keys and paste. Recovery validates the ID and reconciles the original
history receipt before replay; it does not turn an old menu into authorization.
The tests cover native keys, picker filtering, menus, exact-view controls,
Unicode/CRLF, two shells, imported snippets, history and deferred recovery.

### Indentation and block-only comments

Tab, Enter, Backspace and selected-line indentation use one policy on the shared
source table. Explicit file choices override applicable EditorConfig, workspace
and application choices, detected indentation, then defaults. Indent size, tab
width and tabs/spaces remain separate. Changes reproject existing source anchors
without editing bytes or history. Linked panes share the policy; another shell
has its own explicit file choices.

The header's **Spaces: N** / **Tabs: N** opens the existing native menu. It offers
style, indent size, tab width, Detect from text, Use project settings and Reload
EditorConfig. The two conversion actions rewrite only leading whitespace, preserve
its display width and line terminators, and undo once. Indent/outdent replace the
complete leading run: a two-column step inside an eight-column tab does not delete
the whole tab or misplace a selection. Explicit Reset wins over late pane restore.
The action picker, editor context menu and semantic controls share these operations.

Detection inspects at most 2,000 lines and 256 leading spaces per line. It caches
by shared PieceTable revision and uses the most frequent neighboring space-indent
step of 2–8 columns, or tabs. Ambiguous content uses the effective tab width. Enter
between a padded matching pair consumes the old padding and aligns the closing
line in the same Undo transaction.

The pinned official EditorConfig parser owns parent traversal, `root`, section
order, glob matching and `unset`. Crude consumes `indent_style`, `indent_size`
(including `tab`) and `tab_width`; it never executes project code or rewrites a file
on open. Reads run on the selected core, fenced by live document owner/path and
shell generation. Initial file-open preparation supplies policy before mounting.
Saved project-file changes refresh retained views; Reload also checks ancestor
files outside the workspace watcher. Four background refresh reads may run per
shell; cancellation retains a slot until the exact read retires. Shutdown joins
all reads through the existing client-I/O owner. Invalid or oversized configuration
keeps the last valid policy and offers Reload; it is not a successful partial read.
Configuration files must be regular and at most 1 MiB. Widths use the editor's
existing 1–32-column bound. Private pane memory restores sparse file overrides;
EditorConfig and inherited preferences are read again, never stored as overrides.
Other EditorConfig save policies are separate work; reading indentation does not
silently enable whitespace cleanup, encoding conversion or line-ending changes.

Ctrl+/ uses catalog block delimiters when the language has no line prefix.
Each nonblank line is wrapped. Mixed selections keep already-wrapped lines;
all-wrapped selections unwrap. Partial block delimiters are left intact rather
than nested unsafely. Cursor/selection and Undo remain on the normal edit path.
General multi-line block-comment editing remains in the
[complete editor plan](../plans/editor-parity.md).

## Display preferences

The existing Editor preferences apply to new and open document views, including
linked splits and settings changes received from another shell. Reset removes
the scope's choices and restores live inheritance. They do not change source,
selection, history, the savepoint or dirty state.

- **Tab size** controls tab-stop width (1–32 columns), and the default indentation
  for files without a detected style. Every linked view of one table shares it.
  Cursor, selection and retained history use source-byte anchors across changes;
  syntax, diagnostics, search, inlays, ghost text and pointer hits reproject through
  the same width. Native parsing and canonical source revisions do not change.
  Back/Forward retains line and source-byte column, not an old display column.
- **Line numbers** hides only the number lane. Diagnostic and change lanes remain
  visible and actionable. A changed gutter retires pointer capture and reveals
  the same source caret in the new geometry.
- **Syntax colors** selects the pinned Chroma palette's source-token colors.
  Application chrome and selection keep the application theme. Light variants are
  used where available; source ink meets 4.5:1 contrast on the editor background.
  Theme/palette changes clear projected colors, not the current parser result.
- **Show whitespace** paints spaces as `·` and the first cell of each tab as `→`.
  **At edges** shows only leading/trailing whitespace; **Always** includes internal
  spaces and tabs. Expanded tab cells keep their source geometry. Markers never
  enter copy, search, save, snippet text, inlays or ghost suggestions. Per-line
  whitespace geometry is cached with the exact source line, not rebuilt per frame.

- **Word wrap** fits long source lines to the text viewport. Continuations keep
  their source line number and show a muted continuation mark. Up/Down and page
  keys move by visible rows; Home/End first use that row, then the source line.
  A soft boundary has two visual caret locations but never adds a source byte.
  Horizontal scrolling is absent while wrapped geometry is ready.

**Indent with** and **Indent size** supply inherited policy. Detect and zero
select source detection; project/file choices still take priority.

### Wrapped and folded rows

`sourceRows` is the one source-line/visible-row index for wrapping and folding.
Paint, scrollbars, pointer/drag, vertical motion, search/navigation, diagnostics,
inlay hit testing and popover anchors use it. The unprojected case is its identity
mapping. Each view owns width and collapsed choices; source/history stay shared.
A tab may span rows as display padding, but remains one source unit. Other
multi-cell graphemes remain atomic. Copy and edits contain neither hints nor
continuation/fold markers.

Fold/Unfold, Fold all/Unfold all and Fold level use the shared native action
registry, menu, searchable Editor actions and `editor.action` control. The gutter
chevron stays available with line numbers hidden. The collapsed source header
shows an ellipsis and hidden-line count. Expanding a parent retains nested choices.
Current native node ranges and lexically qualified bracket/comment/string spans
supply ranges; crossing/out-of-bounds ranges are rejected. No stale parse supplies
new positions, no language-name branch guesses a fold, and there is no advertised
LSP folding or indentation-only fallback.

Disjoint source changes shift retained folds; changes touching their lines open
them. Replacements and missing journal entries discard them. Navigation into hidden
source reveals it; typing at a collapsed boundary opens the affected block. Source
selection still spans hidden lines. Docked panes persist separate collapsed choices
through the existing private client view memory, qualified by exact source hash;
changed file bytes cannot inherit stale folds. Closing a view does not change peers.

Cold row metrics run in a cancellable source worker on the existing editor worker
pool. Exact buffer/revision/tab-width/generation checks reject late work. Main and
floating editor job drains deliver completions and shutdown joins both row and
syntax jobs. Source and ordinary unwrapped editing remain usable while cold work
is pending. Incremental changes up to 64 KiB of affected source lines remeasure only
those lines; larger changes take the owned worker. Width-only reflow reuses metrics.
Painting folded content prepares colors only for actually visible source lines,
not the whole hidden interval. Uniform wrapped rows use arithmetic runs rather
than one object per displayed row.

## Go to line

- Ctrl+G and the editor View menu open the shared text prompt with the lines
  this file has and the cursor's line pre-filled and selected, so a typed
  number replaces it. Enter jumps; Esc leaves the cursor and view untouched.
  `line:column` is accepted, and a column past the end of the line lands at its
  end. Rejected input (not a number, or a line beyond the file) keeps the
  prompt open and states the cause in the dialog's guidance row, which the
  next edit clears; nothing moves.
- The jump sets the cursor and marks the whole target line as a landing cue: a
  visible highlight that is not a selection, so typing inserts instead of
  replacing the line. An already-visible target does not move the viewport; an
  off-screen target lands with context above and below through the same reveal
  path as LSP navigation and search results.

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

## Indentation guides and minimap

The existing Editor page contains **Indent guides** and **Minimap**, both opt-in
presentation choices. They follow live application/workspace inheritance and reach
open, linked and new views. Neither writes source, changes selected bytes nor
adds an Undo record. Guides mark each effective indentation step in existing
leading whitespace. Tab stops, explicit file indentation and EditorConfig use the
same geometry. At a guide position the line replaces a whitespace marker; other
whitespace markers remain visible. Guides never overwrite source text or inlays.

Minimap widens the existing vertical scrollbar, not the editor document or its
state. Its sampled Braille image uses four visual-row samples per cell and the
first 32–64 columns of each sampled row, bounded to a 1 KiB source prefix.
Later wrapped content beyond that prefix shows `…`, not a misleading empty row. It is
a compact orientation aid, not a complete source or diagnostic overview. Wrapped
and folded rows come from the same projection as scrolling; hidden rows are not
sampled. Image work/storage is bounded by pane geometry. Source, tab width,
row/fold/inline geometry and terminal width profile invalidate the image; cursor,
scroll and theme changes do not resample it. Colors reproject through theme tokens.

The shared `ui.ScrollbarControl` paints the image, its viewport tint and one
track. Clicking jumps to the indicated area; dragging uses its normal pointer
capture and final release. There is no second viewport or minimap drag state.
The miniature hides when fewer than forty source columns would remain; it returns
when the pane grows. Ordinary scrollbars retain page-on-track-click behavior.
Source hit testing and horizontal/wrapped layout exclude the full reserved width.

Scrollbar offsets are visual rows. They pass through `setScrollRow`, never an
assignment to a logical source line. Canonical author Undo reveals its restored
caret after restoring private view state, like standalone Undo; it does not move
another shell or a sibling view's caret. Typing after scrolling the minimap returns
to the existing selected source, not to the miniature's clicked location.

## Multiple selections and carets

Alt+N selects the current word, then the next literal occurrence. Alt+F3 selects
all occurrences. Ctrl+Alt+Shift+Up/Down adds a caret on the adjacent source line.
These actions also appear in Editor actions, the editor context menu and exact-view
semantic controls. Alt-click adds or removes a caret. Alt-drag replaces the set
with a rectangle over visible rows. Shift is not part of this gesture: common
terminals use it to select terminal output instead of reporting mouse input.
Ctrl-click and Ctrl+Alt-click keep their definition/implementation behavior.

The header shows the caret count only when more than one exists. Escape first
keeps the primary caret/selection; a later Escape clears that selection before
closing the file. A normal click or explicit source navigation ends the extra
carets. Captured rectangle drags reuse the ordinary Host, scroll repeat, final
release and cancellation path. Empty rectangles retain insertion carets. Wrapped
rows, tab stops, inlays and complete grapheme boundaries use the same projection.

Each view holds immutable UTF-8 selection ranges with independent motion goals
and wrap affinity. Overlapping ranges and duplicate carets merge deterministically;
adjacent nonempty ranges remain separate. Source deltas transform every range.
Whole-source replacement discards stale carets. Linked views and peer shells do
not inherit a later selection choice. History retains the exact private set;
unresolved recovery stores and validates its byte offsets, not display columns.

Typing, pair wrapping, deletion, Enter, Tab, paste and native text/line commands
produce one original-coordinate edit vector and one Undo group. Line operations
visit each touched line once even when it holds several carets. Identical plans
share one edit while retaining their separate caret positions. Conflicting plans
are refused before any bytes change. Copy joins selected source pieces in source
order with the document terminator. Paste repeats the complete literal block at
every caret; line counts never implicitly distribute clipboard text.

Adding multiple carets retires a snippet interaction without undoing its accepted
source. Automatic suggestions stop while multiple carets exist; explicit completion
asks for one caret. Late replies and direct acceptance cannot edit only one member
of the set. The limit is 256 carets; Select All reports overflow without partially
changing selection. Rectangle endpoints stop at the same bound. Use Replace All
for larger literal changes. Ordinary editing does not require a language server.

Standalone grouped Undo/Redo and received canonical deltas apply one source vector,
not one buffer rebuild per caret. Exact vectors update only affected horizontal
width entries when line counts stay fixed. Paint caches selection geometry by
source revision, tab width and immutable selection identity.

## Edit transactions and history

- Ctrl+Z undoes; Ctrl+Y and Ctrl+Shift+Z redo (Super equivalents when delivered
  by the terminal). Navigation/focus changes separate typing. Adjacent plain
  typing within 500 ms is one logical group; Enter, indentation, pair operations,
  line moves/duplicate/delete, cut/paste, completion, formatting and replacement
  are atomic commands. Replace All is one transaction, not one step per match.
- Reaching the end of the requesting author's undo or redo history is an
  accepted no-op. It changes neither text, revision nor history and shows no
  error. An actual refusal states its cause without inventing another author.
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
- An unnamed connected document has a core-owned identity and no disk path.
  Its edits, undo history and shared views use the ordinary document owner.
  Automatic saves do not schedule or write it. Its first manual Save asks for a
  folder, file name and encoding in the shared Settings-style Form. The floating
  Save File window has one title and one action row. A successful create retains the document identity and
  joins the ordinary named-file save and autosave paths. An existing target needs
  a Cancel-default Replace / Compare / Cancel confirmation, exact native preimage
  checks and a retained Local History copy. Compare opens the existing main-area
  comparison tab only on request; closing it returns to the same name, folder and
  encoding. Targets that cannot meet that review's safety
  bounds are refused without changing the draft or file.
- Empty-rail double click creates one unnamed file in that pane. New Unnamed File
  in the palette and the empty-pane menu reach the same owner. The tab rail's `+`
  still creates a session; ordinary tab clicks and drags keep their semantics.

### File encoding

`textencoding` owns the strict byte/text boundary. Document text, editor offsets,
search matches, language-source leases and diffs use canonical UTF-8. Disk hashes,
preimages, receipts, Local History and Git mutations use exact encoded bytes.
Dirty state includes the saved encoding as well as text identity, without
exporting the file on each paint or keystroke.

Supported disk formats are UTF-8, UTF-8 BOM, UTF-16 LE and UTF-16 BE.
The compact BOM label stays readable in constrained Settings windows.
UTF-16 requires a BOM. No component guesses legacy codepages, replaces malformed
characters or changes line endings during conversion. UTF-32 is explicitly
refused, not mistaken for UTF-16. Streaming readers validate split codepoints and
surrogate pairs. An unreadable file does not abort search of other files.

The editor header shows the current encoding. It and **File Encoding** in the
palette or editor menu open the standard native menu:

- **Save text with encoding** preserves text and author undo, uses the normal
  serialized editor queue and the existing reviewed writer's retained preimage.
  The current reviewed-replacement limits apply, including project containment
  and the 2 MiB safety bound. It does not format or show a second diff.
- **Reload from disk** writes nothing. A Cancel-default confirmation protects
  unsaved text; the replacement is an ordinary undo group. Autosave cannot write
  the draft while that decision or reload is pending. Failed reads retain it.
  Reload detects the saved bytes; it is not a legacy-encoding override.

**New file encoding** is one application/workspace preference, defaulting to
UTF-8. It applies when the core creates an unnamed file. Existing files keep
their encoding, and Save File can override the new-file default.

Text tools use the same codec with their ordinary arguments and preserve an
existing file's encoding. Tool read/edit results identify non-default encodings;
their displayed diffs contain decoded text. Project replacement/formatting and
Git hunk staging re-encode at the mutation boundary. Managed execution baselines
and integration/undo retain the encoded file image, including one-sided
encoding changes. Git comparison transport uses byte fields, including JSON's
base64 representation, rather than lossy JSON strings.

Encoding selection for BOM-less UTF-16 or legacy codepages is not implemented.
These limits are not evidence of VS Code or JetBrains parity.

### Gutter and automatic saves

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
  bytes. **Format Document** (Shift+Alt+F, the editor menu and the palette)
  formats the current buffer without saving it; a connected editor sends its
  exact revision to the core formatter. **Format Project** formats saved files
  through the same catalog formatters and one transactional file owner. It
  skips open documents because their live text belongs to the editor, captures
  one Local History safety snapshot before writing and reports the changed and
  skipped counts. A project above the 200-file or 16 MiB review bound asks for
  individual formatting instead of applying a partial result. Toggle comment
  uses the language's first catalog line comment token.
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
   File-tree deletion verifies current disk bytes before checking saved/conflict
   state. A restored saved preimage clears its stale conflict. Missing descendant
   checkpoints do not veto deletion of their parent; their text, savepoint and
   undo history remain recoverable after deletion and restart. Existing files
   with unsaved work still stop deletion and the error names the file to open.
   Local History can restore a deleted saved file despite its removal conflict;
   unsaved text and active disk intents remain protected.
  Directory watches include their ancestors up to the workspace root because
  Windows reports a directory's rename to its parent, not to the watched contents.

## Format selection

**Format selection** (Alt+F8, editor context menu, Editor actions and
`editor.action` with `edit.formatSelection`) formats only nonempty selected
source ranges. It uses the existing document's synchronized language server and
its advertised standard `textDocument/rangeFormatting` capability. It does not
run the whole-file catalog formatter, start another producer or save the file.
Format Document and Format Project keep their existing catalog authority.

The request carries canonical source identity, private source-byte ranges and
effective indentation. The adapter converts exact bytes to UTF-16 positions;
returned edits use the strict shared LSP edit parser. The core rechecks source,
wire version, provider lifetime, settings and semantic context before returning
one bounded plan. Edits outside a requested range, overlap, invalid UTF-16/UTF-8,
invalid JSON or aggregate bounds fail the entire command. Select a complete block
if the server needs to format more text; Crude never silently widens the selection.
Empty/null results mean no changes; unsupported servers get an explicit notice.

The invoking editor remains responsive. Source, selection, file, tab, indentation
or workspace changes retire the request. A result arriving before its context
publication waits for that exact clock, not a timer or a speculative apply.
The native simultaneous-edit writer preserves private ranges and makes all ranges
one author Undo group. That mutation drains through normal document commands in
the same Update; it does not wait for a later tick to reach peer shells. Ordinary
save and recovery own persistence. Cancellation/shutdown uses joined client I/O.

Requests admit up to 256 ranges and use the established ten-second manual request
lifetime. Replacement and edit counts retain shared document/parser bounds.
Multiple selected ranges are requested against one immutable source, then applied
together; one failed response cannot leave partly formatted text. General
organize-imports/fix-on-save and EditorConfig whitespace cleanup are separate,
remaining workflows, not implied by range formatting.

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
- Managed executables acquire a store-owned exact-identity lease before client
  construction, outside the manager lock. Only that client's successful joined
  `Stop` releases it; failed or uncertain teardown keeps the tool pinned across
  restart and shutdown. The same store mutex gates lease acquisition and removal,
  so a stale assembly snapshot cannot race deletion into a new process.
  Successful launches record workspace use under their current-launch gate.
  See [tool-store lifetimes](tool-store.md#executable-lifetimes).
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
  transport's wrapped errors. Language settings offer **Retry** while failed or
  stopped and **Restart** / **Stop** while running; the Problems header counts
  the servers that stopped with an error and links there. Retry is
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
**Alt+;** references (including declarations). The mouse uses the same family:
holding **Ctrl** over a symbol underlines it in the accent color (a code link,
`tui/editor/code_link.go`); **Ctrl+click** goes to its definition and
**Ctrl+Alt+click** to its implementations. The click places the caret on the
symbol, and the editor only emits `NavigateAtMsg`; the shell asks the language
server and opens the result in the clicked view's pane. A click opens no
chooser for one destination, the shared chooser for several, and a status-line
notice ("No definition found.") for none. Ctrl+click on the declaration itself
shows its references. Definition settings gate the three
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
caret-anchored popover, sized to its rows. Each row shows its file name and
one-based line/column, with its folder in muted native List detail. The title
shows the result count. There is no Close-only footer or title-bar close button;
Escape and an outside click dismiss it.
Enter or a click opens the selected location directly, without a metadata page or a
second Open confirmation. Outside-project and unavailable locations say so in
the row. Listing never eagerly reads a private or dependency file. Empty
answers and failures use plain text; protocol evidence remains in the request
and tests, not a dump in the chooser.

Manual navigation and code-action requests share a two-minute total limit for
startup, synchronization and the answer. The manager waits on lifecycle
broadcasts rather than polling or retrying the RPC. Stopped, missing, failed
and changed-source cases terminate promptly. A result waits for both its
original context and exact connected producer in the shell replica, including
after **Restart Language Servers**.

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
Fresh and late automatic LSP completions cannot steal another producer's
candidate keys; a fresh explicit completion invocation intentionally retires the
candidate. Each candidate records its producer (`SetGhostTextFor`), and
type-through keeps it. Completion is the first producer: it offers its obvious
top suggestion and replaces or dismisses only its own candidate. A candidate may
sit at the caret of an active snippet field; accepting it edits that field.

Preparation rejects empty, invalid UTF-8, multiline, control/bidi-bearing or
over-4-KiB candidates instead of trimming, escaping or repairing source code.
Source lines and combined projection runs retain the existing inlay bounds.
Warm painting reuses retained source and projection geometry.

Apart from completion's own inline suggestion, this is an internal editor
prerequisite, **not an enabled prediction service**.
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

Native edits that match the current file server's advertised signature trigger
characters request parameter information (often `(` and `,`). While that
assistance is active, native argument typing, deletion, line breaks and
indentation replace it with one request for the newest source and caret. Arrow
keys and snippet-field Tab refresh the argument position without changing source.
One shell-local intent waits for the exact typed source and caret to be acknowledged,
then uses the same server/request/context boundary as explicit inspection.
The request carries the standard trigger kind, actual trigger character and
retrigger flag. An active retrigger preserves the exact bounded server result,
including original parameter offsets and markup, instead of reconstructing it
from display text. Retrigger-only characters apply only while help is active.
Input queued behind Undo triggers only when the original key actually executes.
An accepted suggestion that leaves the caret, or its selected first field,
directly after an advertised signature trigger requests it too, as after a
function suggestion. The selected default of a snippet field is not a user
selection and does not block the request.
Paste, API edits, peer publications and reopening a tab are not trigger input.
Unrelated edits, user selection, API caret changes, focus/lifetime/workspace
departure or a five-second acknowledgement deadline retire the intent without
querying older source. New native argument input replaces the old intent rather
than queueing every key. A ready-provider/source publication can lead the initial semantic-context
cursor; the same intent waits for that cursor rather than being dropped or
querying unobserved context. Automatic missing/unsupported/empty/failed answers
and timeouts stay quiet; they do not create loading toasts while typing.

The automatic WindowFrame/TextArea card is passive and source-anchored, with
native Unicode, tabs, inlays, scroll and nested-pane coordinates. It never
captures the next typed key. Completion suggestions and parameter information
coexist. The shared placement owner keeps their frames disjoint, preferring
space adjacent to the caret and using the next free interval when needed.
Old parameter text closes as soon as its source changes; only a current
acknowledged answer can replace it.
On short grids, the reader shrinks to the larger space above/below the caret and
retains native scrolling, rather than centering or covering the source caret.
Escape dismisses the suggestion list, then the parameter intent/card, before
closing the editor.
**Alt+I** explicitly requests the complete focusable reader. That reader and
symbol documentation also open at the caret instead of the screen center.

The native read-only WindowFrame/TextReader shows the selected signature,
active parameter and its documentation, and the other overload labels. Source,
provider and context identities stay in the retained result, not in the card.
The reader grows to fit, within the editor-help limits, and shows a scrollbar
when the text still overflows. It supports normal reading/selection/copy,
resize, Escape and outside-click dismissal.
It never inserts text, chooses an overload on the server or edits an argument.
Advertised signature characters and an accepted suggestion that opens an
argument list start automatic assistance. An accepted argument suggestion also
refreshes existing assistance. Supported empty answers end it, including after
the final snippet field leaves the call. Dynamic capability registration,
language-specific call parsing and full semantic-feature acceptance remain
outside this slice; see the
[editor parity plan](../plans/editor-parity.md).

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
read or a reconstructed `WorkspaceEdit`. The shared review shows relative file
paths, line numbers and the actual removed/added lines before any action.
Control characters, mixed line endings and missing final newlines remain visible.
File hashes, source/provider boundaries and action provenance stay in the
immutable retained evidence, not the ordinary dialog text. Cancel is initially
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
preview text and its audit evidence together are at most 64 KiB,
at most four pending previews are retained,
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

This is a bounded manual text-only rename workflow, not general refactoring or
dynamic registration.

### Manual code actions

The editor's **Code actions** context item and semantic `CodeActionsChord`
(**Alt+Enter** on legacy Unix terminals, **Ctrl+Enter** when modified Enter is
delivered or the host reserves Alt+Enter) request one explicit list for the saved,
acknowledged
canonical source and exact caret/selection. A strict editor query maps display
endpoints to UTF-8 source bytes, then the canonical coordinator maps them to
UTF-16. Split, ambiguous, reversed or out-of-source endpoints are refused, never
clamped or widened. The query does not mutate source, view state or history.

The compact caret-anchored native list shows action titles in plain text.
Available actions come first, with recommended actions first within that group.
If every action is unavailable, a single header explains the state instead of
repeating a warning on every row. Enter or a click on an available action
requests its changes directly, including lazy resolution when
the static provider supports it. There is no intermediate inspection page.
Unavailable rows remain visible and explain the reason when selected.

An edit action produces the shared **Review code action** read-only,
Cancel-default review, Apply/Release/Check-result state machine and selected Local
History safety used by rename. Ordinary edits may target several workspace files,
and a create-file operation is shown as a new file in the same review. The
transactional file owner creates and edits the complete set or refuses it; the
review remains the only mutation path. Successful source effects arrive through
canonical document topics, not a shell reload or private result. Other shells
retain their private cursors and drafts.

A command action executes one command that the initialized server advertised.
Crude advertises `workspace/executeCommand`, `workspace/applyEdit` and create-file
workspace-edit support. A server `workspace/applyEdit` during the command is
answered with `applied: false` and retained as original evidence, never applied
inside the transport. That evidence then enters the same immutable review. A
command whose result is a URL or whose `window/showDocument` names a browser
destination closes the list, shows its message and opens the page through the
existing bounded browser launcher. Informational results without an edit show a
notice instead of an empty review.

Accepted edits are bounded `WorkspaceEdit.changes` maps or ordered
`documentChanges`: ordinary `TextDocumentEdit` targets and `create` operations.
A versioned existing target must carry a nonnull int32 version exactly equal to
the original synchronized provider wire clock; a new file may carry version zero
or null. Rename/delete resource operations, annotations, notebooks, mixed
`changes`/`documentChanges` representations, edit-plus-command actions and no-op
edits remain visibly unsupported. Crude never drops an unsupported part and
applies the remainder. Resolved objects may add only `edit`; changing or removing
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
as positive results. They mean only **No code actions available here**, never
that the file or project is clean. Selection comparison during
Update is constant-time; source-byte conversion runs once per explicit request,
not once per tick or painted frame.

### Completion documentation and original insertion

Ctrl+Space and automatic suggestions share one canonical completion
invocation. Suggestions are low-profile, as in JetBrains IDEs and VS Code:

- The list is a **passive** caret-anchored WindowFrame without a title or key
  hints. The editor keeps keyboard focus and its caret. Automatic suggestions
  choose no row and read no documentation until the user asks; Ctrl+Space
  chooses the first row and shows its documentation.
- The list shows at most eight rows with clipped label and detail columns.
  Documentation takes space only for a chosen row that has detail,
  documentation or a status. Wide frames put it beside the list; narrow frames
  stack it below. Measurement is reused until content or constraints change.
- Ranking is a display projection of exact original rows. A row whose
  insertion only repeats the typed text is dropped; repeated rows with the same
  complete insertion appear once. Different imports or snippet fields remain
  distinct choices;
  rows that start with the typed text (exact case first) rank before other
  fuzzy matches; the server's sortText decides among equals. For typed
  suggestions, a row whose edit does not cover the identifier before the caret
  must still match that identifier (after `strings.To`, a keyword row is not
  offered).
- Inline text appears only for an obvious row: the best row that continues the
  typed text exactly, when the name of every other such row starts with its
  name (`print` before `println`, `ToUpper` before `ToUpperSpecial`). A real
  choice (`ToUpper`, `ToLower`), or the same name with different edits, shows
  only the list. Inline text must not guess an import source or overload.
- The obvious row of an automatic result is shown as faded inline text at the
  caret when its edit ends at the caret on the same line and its text continues
  the typed prefix. For a plain row, Tab inserts that text and Ctrl+Right one
  word. A row with snippet fields or other edits (an import) is only previewed:
  Tab accepts the row itself, so its fields and edits apply. Escape dismisses
  the inline text. A single plain suggestion opens no list.
- With no chosen row, Down chooses the first row, Tab accepts only the inline
  text, and every other key edits the source (Tab indents, Enter is a new line,
  Up moves the caret). Nothing is accepted that the user did not see chosen. After a choice, Up/Down, paging, Home/End, Enter/Tab
  acceptance and Ctrl+PgUp/PgDn documentation paging act on the list. A click
  focuses the list for pointer selection and copy.
- Typing that continues the same word keeps the list on screen as a read-only
  view of the previous result until the next result replaces it in place, so
  the list does not blink. It cannot be accepted from in that state. Other
  keys, a non-word character or Escape close it.

The passive list stays reachable through semantic control. Ctrl+Space on an
active numeric choice field still opens **Snippet choices** first.

Native typing intent is emitted after the final caret and edit group. The same
observer handles linked views and deferred history input; paste, accepted
completions, API edits and peer publications do not create typing intent.
The initialized server's capabilities are published for each open path through
the existing LSP snapshot. Completion and signature hints read that file's
capabilities, never a different shell's globally focused server status. Restart,
Stop, close and workspace replacement remove old capability observations.
Explicit Ctrl+Space and parameter-information actions do not depend on arrival
of that advisory snapshot. The existing asynchronous core request waits for the
exact source/server and reports its result; a late capability topic cannot
discard a user action. Automatic typing waits for advertised capabilities.
Typing during initialization keeps one bounded input intent. When capabilities
arrive, identifier input or an actually advertised character can request the
current source; unsupported punctuation is discarded without querying. Once an
intent observes a server instance, it cannot transfer to a replacement.
Automatic non-identifier triggers come from the server advertisement, not a
hardcoded dot. Requests carry Invoked, TriggerCharacter or incomplete-list
refresh context. Continued typing after an incomplete list sends the standard
refresh reason; a subsequent complete list ends that refresh mode.
One cancellable debounce waits for both the configured delay and canonical
acknowledgement. A timer firing first never discards the request. Source lifetime,
revision, caret, focus, feature generation, server instance, workspace changes and Escape retire
pending intent. Ghost-text candidates of other producers retain their keys;
completion's own inline text does not. An active snippet does not block
suggestions: typing in a field requests them like any other typing. In a field,
Tab accepts visible inline text, a chosen row or the sole visible supported
suggestion. With none of these, Tab moves to the next field. Accepting a plain suggestion inside the active field is an ordinary
edit of that field and keeps the snippet. A suggestion with its own fields
replaces the snippet.
The Code Intelligence **Suggestions** settings control automatic requests, delay
(200 ms by default; zero is immediate), and acceptance by Tab, Enter or both.
With automatic requests off, Ctrl+Space remains available. Other keys retain
normal indentation/newline behavior, and suggestions never write the file to disk.

A sole visible suggestion accepts Tab without Down first, including a fuzzy
match, a snippet and an import-bearing row. This selects the exact original ID
and uses the normal validated insertion, never the display label. Multiple
non-inline choices remain passive; Enter remains a newline until a row is chosen.
The Enter-only preference still leaves Tab to the editor. Stale rows cannot accept.

The core retains the original array or CompletionList item, original index and
opaque row ID, not a label-keyed reconstruction. Equal labels remain independent
rows. Null, empty, unsupported static capability, unsupported insertions and
provider failures remain distinct internally. Incomplete results ask the user
to keep typing; unsupported suggestions are described in plain words.
CompletionList defaults and dynamic registration are not advertised or accepted.
The core derives each row's typed prefix from its validated primary range.
The display uses the shared fzf matcher with the provider's `filterText` (or label)
and orders matches by `sortText` (or label). Filtering preserves original row IDs,
insertion ranges and resolve objects. Continued typing queries the current source,
including for incomplete results, instead of rebasing an old insertion.

Initial documentation retains string versus MarkupContent, plaintext versus
markdown, absence versus an explicitly empty value, and optional detail. The
reader shows the actual documentation or "No documentation available.", not
protocol format names or normal loading-completion states. Markdown,
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
context. Standard completion results do not report a source version; unknown
context inputs remain partial. These qualifications stay in the retained result,
not in the completion caption or documentation. The shell waits
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
choice callback. Suggestions work inside a field (see
[completion](#completion-documentation-and-original-insertion)); they take Tab
only while inline text or a chosen row is visible.

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
batches remain discoverable in Problems but cannot authorize
current paint, hover or navigation. Previously accepted same-batch ink can map
provisionally through unchanged rows without becoming current semantic evidence.
An identical later batch from the same producer and document lifetime preserves
that already-mapped ink when source has advanced past the batch. It does not
project the later batch's ranges. Changed or empty answers retire the old ink;
an exact source match replaces it through the normal projection.

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
   workers, at most 100,000 entries) counts files per language through the
   [catalog](code-intelligence-catalog.md) and custom languages. Hidden,
   dependency and build folders use the standard browser exclusions; symbolic
   links are not followed. Inherited worktree `.gitignore` rules apply before
   counting. Ignore inputs use bounded no-follow regular-file reads; links and
   special files supply no rules. Read/traversal errors fail discovery instead of
   claiming a complete plan. A larger project is marked limited, not complete.
2. **Resolution.** For each language, launch resolution picks the server. The
   program is looked up without starting it: an absolute path from settings,
   then the tool store, then the system path. Path folders inside the
   workspace are skipped, so a project cannot supply its own server program.
3. **Plan.** One `types.AssemblyPlan` per workspace: each language with its
   file count and an example file, server, program, source, status (ready, not
   installed, off, no known server), checked scope and a plain reason. The manager
   publishes it in its snapshot (`Assembly()` on the shell port) and the control API's
   diagnostics `lsp` view lists it.
4. **Activation.** A ready server whose catalog scope is `project` becomes a
   project producer at its project root. A server found in the tool store
   launches from there. A server that is not installed never launches: the
   status names the missing program, and editing stays usable.

The workspace starts the service when its analysis runtime starts. Settings
changes and **Start Language Servers** run it again; a newer run cancels an
older one and a stale result is dropped. Source create/delete/rename and ignore
changes immediately invalidate/cancel cached discovery, then debounce a fresh
scan (150 ms idle, at most one second hold). Root, settings and invalidation
generations fence timers and results. Watch recovery rescans even without active
producers. Close cancels timers and joins scans.

The retained core watches source changes, including files nobody opened. On
Windows it holds one root-recursive native watch and one parent-nonrecursive
directory-name watch filtered to the root name, with no descendant handles that
would block populated directory moves. Windows path identity is case-insensitive;
event/display spelling is preserved. Native `OVERLAPPED` and buffers remain
heap-owned and pinned through completion/cancellation and joined Close. Overflow,
read/admission failures and root loss fence project coverage. Root replacement
does not restore reliability automatically: the watcher must restart. Unrelated
sibling subtree activity is outside the native observation scope.
For a still-identical root, transient admission or backend failures rebuild the
native watcher with bounded retry delays. The old backend and event generation
retire before the full directory scan; only successful observation restores
coverage and replaces project producers. A directory that disappears during
admission cannot permanently silence diagnostics. Shutdown cancels retries and
joins the observer. Lost or replaced roots remain fenced.

Coverage is shown where the user looks for problems. Server rows show their
current state and declared scope as a capability, not proof of a complete
project check. **Project Languages** lists every language found, with the
installed server, its program and declared capability; installation alone does
not mean it is running. Each language includes an **Example file** so its
classification is inspectable. A missing installable tool offers **Set up tools**;
manual installation can be followed by **Check again**. The native setup owner
checks newly acquired sealed bytes at an independent inert root and joins the
exact process before publication, then admits usage and refreshes assembly.
Protocol initialization alone proves neither semantic features nor complete
project coverage. See [project setup](settings.md#project-tool-setup).

The language-server manager derives the active file's visible language status.
The editor header shows its live one-based cursor position and total buffer line
count, plus error and warning counts or that status label. The count follows the
focused file and includes the final empty line after a line terminator. The
Problems footer shows counts and marks restricted or stopped services as off,
and failures as errors. **Refresh** in the Problems header (and **Restart
language servers**) restarts every server and removes the results the stopped
servers reported for files no editor holds, because a new server reports only
files that still have problems. Automatic restarts and Stop keep those results
visible as out of date until a server replaces them.
Workspace health shows counts or the manager's status
message and routes its next action to the existing trust, settings or server
controls. No result is a clean-project claim. Diagnostic provenance remains in
the structured result, not in diagnostic readers or Problems rows.

Agent source positions resolve against the immutable source leased for the
actual request, not an independent disk read. Diagnostic and same-source
location quotes use those captured bytes. Destination files lacking a separately
qualified source remain labelled raw UTF-16 positions without guessed disk
quotes. Automatic post-edit probes obey the same session, role-tool and read-policy
admission as explicit calls; a later revocation drops queued notices instead of
delivering or replaying them on the next model step.
The agent `lsp` tool writes one summary line per file, then each problem as
one-based `line:column`, severity, message and source line; navigation writes
one-based `path:line:column` locations. A qualification that changes how to read
the answer (out of date, unconfirmed file version, unsaved editor text, a column
counted in UTF-16 units) is written on that line. Fact identities, context
hashes and unknown-input lists stay in the structured result: printing them
cost four lines per file that no agent acted on.
Multi-file diagnostic requests and automatic post-edit checks use one manager-owned
batch. It acquires all source views before collecting results and releases them
after all reads join, so its own opens and closes cannot invalidate sibling
answers. Duplicate paths share a read while keeping input order; missing files
have per-file failures. Canonical edits, disk replacement, cancellation, settings
and producer changes still pass through the same source and context checks.
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

Transient editor aids share one compact WindowFrame popover style: no title-bar
close control, dragging or manual resize. Rename input, navigation, code-action
selection, completion, documentation, parameter help, outline, inlay and snippet
choices anchor to the invoking source, reflow with the editor, and close on an
outside click. The click continues to its intended destination. An offscreen source
does not cause a centered fallback. Content appears immediately, without an empty
opening animation. Passive help does not take typing focus; explicit input and
choosers do. Escape follows the native selection/Back/Close ladder.
Rename reuses the bound TextField and validation body, not the modal text-prompt
chrome. The later immutable edit review remains a separate modal with explicit
Apply/Cancel actions; changing popover presentation does not weaken approval.

The editor context menu's **Show diagnostics** replaces the centered label stack
with a native WindowFrame/TextReader. It inspects the clicked source point
without consuming an existing selection; a keyboard-invoked menu uses the mounted
caret. Pointer hover and explicit details share the same reader builder and exact
fact-publication owner, not separate wrapping or source-coordinate models.

The source point projects through the editor's existing tab, grapheme, inlay and
scroll geometry plus the actual mounted pane offset. Readers fit below or above
that point, shrink to the available side, and re-anchor on the first resized
frame. An off-viewport source is unavailable, not clamped into a fictitious visible
anchor. Resizing retains the original TextArea, text and selection.

The card leads with the complete diagnostic message. A single problem uses the
severity in the frame title; overlapping problems have a severity label beside
each message, with errors first. Each message begins with the same colored severity
glyph as Problems. Gutter and overlapping underline severity use the same
highest-priority reduction, independent of server order: error, warning,
information, then hint. Reported source and rule code remain as compact
attribution after the message, following
[VS Code's marker hover](https://github.com/microsoft/vscode/blob/main/src/vs/editor/contrib/hover/browser/markerHoverParticipant.ts).
Source previews, position labels, generic coverage commentary and Crude's internal
identities do not occupy the reading surface. Source validation is unchanged.
Error-navigation feedback names the destination line without copying the full
problem into a notice. With no target, it describes this file only; a warning is
still readable but is not an error-navigation target.

The card fits short messages and expands for longer messages, up to 90 columns
and 18 rows and the available space beside its source. Both hover and explicit
inspection use the same sizing. If text still overflows, the native scrollbar
shows the reading position and supports dragging. TextArea and the scrollbar
share one ScrollModel; there is no second offset, wrapping or interaction owner.
Provider control characters are escaped literally, never interpreted as terminal
commands. The retained fact store owns diagnostic resource limits. The reader
does not add a second message-size limit or truncate accepted messages.

Explicit details focus the native reader. Hover opens passively, accepts wheel
browsing without taking typing focus, and an explicit body click enables keyboard
reading. Page keys, selection and copy use the shared TextArea. Escape and the
outside-click actions retire inspection without closing the
editor or consuming its private draft. Pointer departure retires passive hover.
While the pointer moves within the same set of overlapping diagnostics, the
reader remains anchored at that span's first column rather than following the
pointer. Crossing a diagnostic boundary changes the reader; if the start is
horizontally offscreen, the first visible pointer position is the anchor.
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
