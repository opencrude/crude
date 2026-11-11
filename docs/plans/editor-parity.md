# Complete Editor Experience

> Status: ACTIVE IMPLEMENTATION. One target experience, not a phased release plan.
> The defect fixes listed below are implemented; the remaining contracts are
> work to do. A checked-in plan is not evidence that a feature exists.

## Outcome and working instructions

The user wants a dependable editor comparable to VS Code and JetBrains for
real editing tasks, not a list of advertised features or a collection of
language-specific hacks. They explicitly asked us to hunt beyond their examples,
finish the workflows, including general JetBrains-style commands and motions.
The latest request is to continue implementation, not stop at another handoff.

Treat the open requirements below as one acceptance contract. Work can be split
by owner, but do not call the editor complete until the integrated journeys pass.
Do not replace the shared document, completion, snippet, history, source geometry,
settings, control registry or language-service owners with parallel systems.

Before each change, reproduce the missing behavior through an actual input
entry point. Add an independent behavioral regression. Reuse existing tests for
linked views, canonical receipts, source identity, sparse edits, Unicode and
history. Update current references when a contract lands, and remove completed
items here. Record evidence and limits, not self-reported completion percentages.

Language syntax, template bodies, context rules and server knowledge belong in
catalog/query data. Go implements generic consumers. Never branch on a language
name to make one fixture pass. Never run native parsing, disk I/O, subprocesses
or network requests in Update or Paint.

## What this repair actually changes

| Defect | Owner and corrected outcome | Evidence |
|---|---|---|
| Accepting a function with Tab blocked suggestions inside its argument fields | `tui/viewmodel/editor_ghost.go` no longer treats all active snippets as a competing suggestion producer. Plain suggestions accepted inside a field preserve its mirrors and next Tab stop. Acceptance requests parameter information when it opens an argument list. | `TestSuggestionsWorkInsideAnAcceptedArgumentList`, `TestTabPrefersAChosenRowThenTheNextField`; old guard reproduced the failure. |
| Pasted CR line breaks appeared inside JSON lines | Shared `ui.PastedLineBreaks` decodes CRLF and CR to LF at shell paste ingress; the editor applies its document terminator. Escaped backslash-n inside strings is not a line break. Existing disk contents are not rewritten. | `TestPasteGivesEveryLineBreakTheDocumentTerminator`, `TestPasteOfCopiedJSONKeepsItsLines`, `TestTerminalPasteKeepsItsLinesInComposerAndEditor`. |
| CPU-derived admission stranded ordinary mixed-language projects | Core ceiling is the same 32-server bound permitted by settings; default workspace quota is six. An explicit lower quota remains effective. Waiting status distinguishes workspace quota from shared capacity. No claim of CPU/RSS containment. | `TestAnalysisProcessCeilingHonorsEveryWorkspaceQuota`, `TestDefaultLimitsRunOneServerForEachOpenLanguage`, `TestWaitingBehindOtherWorkspacesDoesNotBlameThisWorkspaceLimit`. |
| Problems hid a waiting explanation when no Settings action could help | `tui/viewmodel/problems_panel.go` preserves a non-ready status independently of its button. Previous results cannot turn a waiting/failed empty view into “No problems.” | `TestProblemsPreservesWaitingWithoutSettingsAction` checks the model and actual 80-column paint. |
| Comment toggle did nothing for block-only comment syntax | Catalog block delimiters support linewise toggle. A mixed selection keeps already wrapped lines, never nests them. A selection containing partial delimiters is left intact rather than generating broken comments. | `TestToggleCommentUsesBlockDelimitersWithoutALinePrefix`, `TestBlockCommentMixedSelectionNeverNestsAndUndoKeepsSource`. |
| Tab and indent commands imposed four spaces | `tui/editor/indentation.go` detects tabs or space steps from bounded source evidence, cached by shared buffer revision. Tab reaches the next detected stop; Enter, Backspace and selected-line operations use the same style. Ambiguous/new content uses the configured Tab size. | `TestIndentCommandsContinueDocumentStyle`, `TestTabUsesNextStopAndCachedStyleFollowsLinkedEdits`. |
| Splitting a padded bracket pair left the closing bracket over-indented | Enter consumes intervening whitespace in the same undo transaction and aligns the closing line with its opening line. | `TestEnterBetweenPaddedPairKeepsClosingIndentAndOneUndo`. |

These fixes do **not** establish all-language parity, installed-server acceptance
for the tester's 4.1 binary, repaired historical JSON files, general auto-dedent,
or a built-in template library. Configurable tab width and wrapping/folding have
since landed through the geometry owner described below.

## Inventory: existing is not missing

The source already has canonical shared documents, dirty-byte identity, undo,
linked splits, search/replace, auto-pairs, line operations, snippet tab stops and
choices, completion documentation/resolve, signature information, hover,
definition/type/implementation/references navigation, document outline, inlay
hints, code actions, reviewed rename, manual formatting and format-on-manual-save.
Extend these owners rather than building replacements.

The initial audit found no integrated folding or wrap projection; these now
have one shared row owner. EditorConfig indentation and explicit file choices
now use the source-table policy. Remaining gaps include multi-cursor editing,
indent guides, semantic token projection, linked editing and CodeLens.
Some LSP method constants exist in the vendored transport; a constant is not a
working workspace/shell workflow. AI literal ghost projection and budget libraries
exist, but a shipped automatic inference service does not follow from them.

## Everyday commands and motion

The latest user request prioritizes implementation over another handoff. Ctrl+D
means Duplicate line/selection. Tab accepts one visible supported suggestion
without choosing it first; neither example limits this work.

Implemented and verified: one typed native action executor for
line/text operations, Ctrl+D, join, line-above/below, case/sort/reverse/transpose,
line selection, paragraph/name-part motions and selection expansion/shrink.
The editor menu, searchable **Editor actions** and exact-view semantic commands
share it. Deferred actions retain recoverable identity behind shared Undo.

A sole visible supported completion row accepts Tab through its original-object
path, including fuzzy matches, snippets and imports. Enter and the acceptance
preference keep their distinct behavior. Current native node ranges and
lexically qualified bracket endpoints support structural selection, bracket
navigation and matched-delimiter paint. Canonical results cross the generated
wire; private results stay in the existing syntax worker.

Connected/history/recovery/source-fencing checks, native captures at both sizes,
ordinary and focused race coverage, build/lint/docs/site and the unchanged allocation
gates pass. See [checkpoint evidence](../issues/2026-11-11-editor-polish.md#everyday-commands-and-sole-suggestions-2026-11-11).
These changes do not complete the remaining sections below.

## Source geometry and truthful preferences

**Owners:** `tui/editor/coords.go`, `tui/editor/source_geometry.go`,
`tui/editor/inline_projection.go`, `tui/editor/buffer.go`,
`tui/viewmodel/editor_save.go`, `types/types.go`, existing preferences plumbing.

**Remaining gap:** Minimap has a stored field with no implemented view. Word wrap
now uses the shared row projection below.
Line numbers, Syntax colors and Show whitespace now reach current, linked and
new editor views and live peer settings. Colors reproject without reparsing;
whitespace stays out of source and copy. Autosave and format-on-save also have
consumers. Tab size now supplies shared display geometry and the fallback indent
width. Source-byte history/recovery and navigation anchors keep the same source
through width changes. Explicit file choices, EditorConfig and inherited
indentation now share one source-table policy. The header menu identifies the
current source; compact validated width prompts, conversion, Reset and Reload use
native controls. First file-open input uses prepared project policy. Private pane
memory restores sparse choices without overwriting newer selections. Connected
peer/profile/control and native 160×40/80×24 journeys pass; indentation is still
separate from display width. Detailed evidence belongs in the checkpoint issue.

**Implementation contract:**

- Resolve explicit file choices, applicable EditorConfig, workspace choices,
  application choices, detected indentation and defaults in that order. Keep
  indentation width, tab display width and insert-tabs policy distinct. Show
  `Spaces: 2` or `Tabs: 4` in the existing editor status, with a compact menu to
  change the current file's policy and an explicit conversion action.
- Replace the fixed display-width assumptions together, not only insertTab.
  Source-to-cell mapping must cover cursor, selection, pointer, syntax, search,
  diagnostics, inlays, ghost text, saved view state and history. Linked views
  of one table must not interpret a Position using different widths.
- Preserve source byte anchors when policy changes. Undo/Redo and retained
  navigation history need geometry-neutral anchors; do not reinterpret old
  visual columns using a new width. Never mutate source merely to paint a tab.
- Settings changes apply to open views, new views, restored tabs and previews
  that are meant to inherit them. Explicit preview overrides remain explicit.
  Line numbers can hide without hiding diagnostic or folding actions.
- Syntax colors reuse theme tokens/styles and invalidate color projection only.
  Whitespace markers are display-only, never copied or saved. Boundary mode
  shows leading/trailing whitespace, not every internal space.

**Acceptance:** change 4 to 2 to 8 with tabs, emoji and combining marks in two
linked views; caret and selected source bytes stay fixed. Type, click, search,
accept a completion, undo/redo, save, reopen and switch workspaces. Verify every
visible setting actually changes its stated behavior and reset restores inheritance.

## Folding and wrapped lines: one row projection

**Owners:** `tui/editor/render.go`, `tui/editor/chrome.go`,
`tui/editor/pointer.go`, `tui/editor/movement.go`, `syntax/`, `types/syntax`,
`workspace/insight.go`, and the established LSP request/transport owners.

Implemented: the source-line/visible-row index combines wrapping and folding.
Paint, scrollbars, pointer/drag, vertical and page motion, search/navigation,
diagnostic and inlay anchors use it. Source bytes, Undo and LSP coordinates stay
unchanged. Native source ranges and lexically qualified delimiters/comments/strings
supply folds without per-language Go branches. There is no advertised LSP folding
or indentation-only fallback. Alt+-/Alt+=, gutter toggles, Fold/Unfold all and Fold
levels 1–9 share the native registry, menu, picker and semantic executor.

Nested choices and source-qualified private pane memory survive reopening;
disjoint edits shift folds and touched ranges reopen. A peer's fold choices stay
private. Cold/large source geometry runs in an owned cancellable worker; width-only
reflow reuses metrics and folded syntax prepares visible lines only. Current
references and measured costs are in [Editor](../reference/editor.md#wrapped-and-folded-rows)
and [performance baselines](../reference/performance-baselines.md#wrapped-and-folded-source-rows-2026-11-11).

The native 160×40/80×24 journey verifies fold/unfold, gutter click, Go to line,
Ctrl+D and one Undo with seven ready boundaries and joined cleanup. Connected
profile return, peer edits/Undo, source/geometry regressions, ordinary inventory
and affected race checks pass. Windows/macOS component builds are not native UI
acceptance. Final allocation-gate evidence belongs in the checkpoint issue, not
an all-editor-parity claim.

## Context-aware completion without a model

**Owners:** existing LSP transport, workspace completion, `snippet/`,
`tui/viewmodel/lsp_completion.go`, and `tui/editor/lsp_edits.go`.

**User clarification (2026-11-11):** HTML scaffolding, C loops and Lisp functions
were examples of useful assistance, not a requirement to build a custom template
library. No local model is wanted now. A possible custom-trained model is later
work, not permission to install an inference service in this repair.

Complete the existing LSP path first. Servers supply language-specific symbols,
types, edits, imports and snippets where supported. Crude must honor the actual
advertised triggers, request context, incomplete-list refresh and insertion
contract rather than special-casing familiar languages or constructs.

- Retain one passive list, inline preview, selection/acceptance path, numeric
  snippet owner and canonical Undo transaction. A snippet returned by a server
  is an LSP result, not a new local-template feature.
- Preserve original server row identity and source/context validity for resolve
  and additional edits. Do not manufacture semantics or silently widen ranges.
- For markup expansion, evaluate an established implementation such as Emmet
  instead of maintaining an imitation. Integrate through the existing assistance
  path only after checking context, dependency cost and real editing behavior.
  This is a remaining evaluation, not shipped support.
- Context-sensitive transformations need real syntax/type information from
  tooling; a parser alone cannot infer types or the user's intended program.
- Ordinary editing remains usable with no server. Missing language knowledge is
  not filled with fabricated results or a hidden AI request.

**Acceptance:** real server suggestions, snippet fields, imports, documentation,
continued argument editing and one-step Undo; advertised non-dot triggers;
incomplete replies; cancellation, restart, two shells, exact UTF-16 edits and
native narrow-screen behavior. The three example expansions are not standalone
pass/fail criteria for a bundled library.

## Completion and signature continuity

**Owners:** existing completion and signature coordinator files in
`tui/viewmodel/`, `workspace/lsp_completion.go`, `lspclient/`.

- Keep parameter help anchored while typing arguments, changing overloads and
  moving between fields. Refresh on provider-advertised trigger/retrigger input
  and current argument position; do not require a new opening parenthesis.
  Completion and parameter help coexist without covering each other or taking
  editor focus. Escape dismisses the top transient aid before closing the file.
- The continuation repair now refreshes help during native argument typing,
  deletion, Enter, indentation, caret keys, snippet-field navigation and accepted
  argument suggestions. Completion no longer closes passive parameter help; one
  placement owner keeps both frames disjoint. Old parameter text is removed on
  source change, and a fresh answer waits for the exact acknowledgement.
  Final focused ordinary/race and installed-gopls native captures pass at 160×40
  and 80×24. Both captures verify all ten interaction boundaries and joined
  cleanup. Advertised triggers, signature context, incomplete-list refresh and
  initialization/publication ordering are connected through the existing LSP
  path. Wire-level, per-file isolation, connected regressions, the full ordinary
  suite and all discovered race groups pass. The race umbrella command reached
  its outer time limit; the last two groups passed separately with unchanged
  coverage/deadlines. Build, lint, documentation and all allocation gates pass.
  The original Windows GC finding remains open. See the
  [continuation evidence](../issues/2026-11-11-editor-polish.md#lsp-continuation-2026-11-11).
- Static trigger characters and incomplete-list request context are implemented.
  Commit-character acceptance and indentation-adjusting insertion modes remain
  unadvertised. Add them only with complete native input, transaction and Undo
  coverage. Do not guess or widen server edit ranges.
- Keep ordinary editing usable while a server is unavailable. Preserve exact
  server-row identity for additional edits, resolve and imported symbols.
- Support nested snippets deliberately if added: inner completion must have a
  defined return-to-outer-field behavior, not two uncoordinated Tab owners.

**Acceptance:** accept function, type first argument, choose a member, type comma,
select second argument and overload, then leave the call. Test nested calls,
selection replacement, Undo, delayed receipts, stale server replies and a narrow
screen. Assert the actual displayed signature, not only that intent was queued.

## Structural editing and multiple selections

**Owners:** `tui/editor/editops.go`, `tui/editor/movement.go`, source geometry,
`syntax/`, catalog data, existing document transaction and author undo owners.

- Matching-delimiter paint and navigation are implemented through exact syntax
  results. Finish Smart Enter and closing-delimiter dedent: exclude strings/comments and use
  current syntax/context rules. A stale parser must not rewrite indentation based
  on guessed brackets. Continue comment leaders and language indentation through
  data-driven rules; do not hard-code colon/newline behavior per language.
- Extend the implemented deterministic expand/shrink selection stack with
  negotiated semantic selection ranges. Current native structure and lexical
  bracket ranges already participate; no stale result may supply a source range.
- Add Select next occurrence, Select all occurrences, add caret above/below and
  rectangular selection. Use source-byte ranges and one simultaneous edit vector
  in the canonical transaction. Merge overlapping ranges deterministically.
- Multi-cursor paste distributes equal-count lines only when the policy is explicit;
  otherwise every caret receives the literal block. Completion, snippets and
  conflicts must have a defined multi-caret policy. Do not partly apply a command.
- General block-comment toggle must eventually handle existing multi-line comments
  and delimiter-containing content using syntax context. The repair is linewise,
  and deliberately refuses unsafe partial delimiters.

**Acceptance:** escaped strings, comment brackets, malformed code, adjacent and
nested delimiters, CRLF, wide graphemes, tabs, empty rectangular ranges,
overlapping cursors, one-step Undo, peer edits, cancellation and render/hit agreement.

## Navigation, semantic presentation and project editing

**Owners:** existing workspace LSP families, `third_party/powernap/pkg/lsp/`,
`lspclient/`, `types/`, `shellport/`, native editor-help windows and review owner.

| Missing/partial workflow | Complete expected result and implementation constraint |
|---|---|
| Workspace symbols and breadcrumbs | Search project symbols from the existing palette/native list; selecting one goes to exact source and participates in Back/Forward. Document outline already exists. Reuse it for breadcrumb hierarchy, not another symbol cache. |
| Document highlights | Current symbol reads/writes are distinguished with restrained theme tokens; async answers are fenced by exact source and caret invocation. They never become editable selections. |
| Semantic tokens | Negotiate full/range/delta support, validate legends and bounds, recover a rejected delta via the protocol's full refresh, combine with lexical fallback by explicit precedence. Theme changes reproject colors without requerying source. |
| Linked editing | Paired tag-name edits use negotiated ranges, one local transaction and source-qualified invalidation. Never keep mirrored editing after a peer touches its range. |
| Call/type hierarchy | Expand incoming/outgoing calls or supertypes/subtypes in the shared bounded tree/list. Lazy requests, cancellation and exact location navigation; no unrelated popup framework. |
| CodeLens | Only actual provider-supported actions appear. Resolve on demand; edits go through review and command effects through existing authorization. Never print protocol identifiers as user labels. |
| Formatting selection and save actions | One formatter selection authority; support range formatting where offered. Organize imports/fix-on-save are explicit opt-ins, serialized with format/save against one source revision. No duplicate file writer or surprise multi-file save. |
| Whitespace cleanup | EditorConfig indentation is connected. Trim trailing whitespace/final newline remain explicit save policies to implement, visible in review and reversible. Never silently rewrite historical mixed line endings on open. |
| Pull diagnostics and dynamic features | Add only supported negotiated workflows; retain previous result IDs/cancellation semantics where specified. A missing provider, queue or failed analysis must not be labelled a clean project. |

**Acceptance for every row:** supported/unsupported/empty/error/cancelled cases,
source and workspace switch during request, two shells, dirty documents, narrow
native UI, actual command effect and Undo/recovery. Add protocol codecs and semantic
controls in the same change, not as an unconnected backend endpoint.

## Resource recovery and optional AI

The larger process ceiling fixes a default starvation bug, not scheduling fairness
or memory containment. Active editor work must not be indefinitely blocked behind
background project producers. Extend the **existing governor** with explicit
interactive priority and fair background scheduling; do not add a second pool.
Release only positively stopped processes, retain uncertain teardown evidence,
and show a recovery action that changes the binding constraint. Closing files
may not release a project-scoped producer: name that distinction rather than
promising it always frees a slot. Test mixed projects, many workspaces, retained
old generations, low quotas, crashes, restart and thread/memory hints.

AI suggestions remain separate from deterministic completion. Reuse existing
prediction budget, source snapshot, cancellation and ghost projection owners.
Require the established workspace consent and positive budget before transmission;
local models still need an explicit resource/configuration path. Label next-edit
previews, require explicit acceptance, and use review for broader modifications.
No model may claim semantic correctness from a plausible generated pattern.
Measure acceptance, latency, unwanted edits and resource costs against a fixed
corpus before claiming it beats another IDE.

## Integrated acceptance and handoff requirements

Run one developer journey with a small multi-language web/native/script project:
open files, paste JSON from a browser, use supported language-server constructs, accept
function/member suggestions inside their fields, fold nested blocks, search and
navigate through folds, edit two splits, use multiple carets, format, rename with
review, undo, save, restart and resume. Repeat while a server starts/fails and while
another shell edits the same document. Editing remains usable without a server or model.

Every implementation report must list changed owners, behavioral regressions,
real provider/platform evidence, screenshots inspected at 160×40 and 80×24,
latency/allocation measurements and remaining unsupported behavior. Run the
[validation gates](../guides/validation.md). Never turn a timeout, skipped native
fixture, protocol constant or screenshot command exit into a success claim.

## Design references

These are behavior references, not permission to acquire Marketplace content:

- [VS Code snippets](https://code.visualstudio.com/docs/editing/userdefinedsnippets):
  prefix/body/description, scope, placeholder navigation and indentation-aware insertion.
- [JetBrains live templates](https://www.jetbrains.com/help/idea/using-live-templates.html):
  abbreviation + Tab, fields, contextual and surround templates.
- [LSP specification](https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/):
  negotiate each feature; preserve source and provider boundaries.
- Current Crude contracts: [Editor](../reference/editor.md),
  [native syntax](../reference/native-syntax.md),
  [interaction matrix](../reference/interaction-matrix.md),
  [file workflows](editor-file-workflows.md),
  [autonomous code intelligence](autonomous-code-intelligence.md).
