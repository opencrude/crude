# Native Syntax Analysis

> Status: Current initial syntax integration. Selected native grammars are
> scheduled from canonical documents and pushed to connected editors.
> General assembly, semantic producers and full workflow/device acceptance
> remain open; Chroma still covers other lexical consumers.

## Ownership and boundary

`syntax/` is a presentation-neutral library. A Session owns one native parser,
compiled highlight query and accepted tree/source revision. One worker must
serialize Parse and Close; callers never run native work on Update, Paint or
a workspace actor. Close is explicit and idempotent. This is analysis state,
not another canonical text, undo or save owner.

Parse accepts positive monotonically increasing revisions and UTF-8 source
within the shared document size limit. It copies source, edits a clone of the
old tree using exact byte/point coordinates, and commits only after successful
parse, query and context checks. Cancellation, stale revision, bad encoding,
limit failure and close cannot publish a partial result or replace the old
accepted base. Cancelled native parse state is reset before reuse.

Results have no C handles. Source spans partition the complete source,
including plain gaps, and use exclusive UTF-8 byte ranges. They contain
neither LSP columns nor terminal geometry. The consumer must fence document,
owner and revision before publication and perform grapheme/tab projection.
Grammar/runtime identity and the composed query digest identify syntax
provenance, not semantic project context or compiler correctness.

The immutable data identities are defined in `types/syntax`, without a native
dependency. The engine exposes that same vocabulary; neutral wire clients can
consume results without linking a parser. `make test-lsp` guards pure-Go
contract/transport builds as well as the separate nested LSP lifetime suite.

## Canonical scheduling and connected editors

`workspace.DocumentService` retains text/history/save authority and owns one
`InsightService` for derived views. Opening/committing/renaming/releasing a
document offers only its identity, path and revision. The worker obtains
immutable accepted bytes off-thread. Savepoint, focus, viewport and theme
changes do not reparse source. No source export or native work runs in the
document mutation path.

Each workspace has a conflating identity-token queue, one worker and at most
one reusable parser session. Two native preparation slots are shared across
workspaces in the core process. This is initial native admission, not a full
core-wide process/memory governor. Results are bounded to 65,536 spans per
document and a 16 MiB workspace projection budget; native heap/RSS and parser
retention need independent resource acceptance. Last-view release cancels and
reclaims source analysis; shutdown cancels waiting work and joins the worker.

Publication checks document identity, exact source revision/path, open-view
entry token, cancellation and service lifetime under the canonical lock.
`types.DocumentInsight` has an independent version plus random service
generation so core restart cannot make old and new versions comparable.
Pending, current, unsupported, failed and limited are explicit; a nil result
never means no compiler errors. No native tree crosses the boundary.

`coreprotocol.TopicInsight` is independent from the acknowledged document
journal. A slow insight subscriber cannot hold up source edits, receipts or
other clients. Reconnect sends a complete latest derived snapshot, never a
text mutation. `shellport/remote` shares the exact open/attachment/workspace
token with its document subscription. Update rejects stale tokens and caches
only the newest pre-mount result; source may overtake syntax or vice versa.

Connected clean source uses only the canonical native result. Unacknowledged
private edits and read-only/standalone editor surfaces prepare syntax off-UI.
Private parsing never publishes canonical findings. Exact provisional ink may
remain after an edit receipt while shared analysis catches up; that display is
still labelled pending, never canonical semantic evidence. The same
grapheme/tab/theme projector handles all byte spans, and mapped decorations
preserve unchanged cells during replacement. Future canonical results cannot
cancel a different private draft or paint on its text.

The existing header/footer and status details expose Syntax shared, draft,
pending, lexical, limited or unavailable. These are syntax-source states,
not project-check counts. LSP remains its existing producer path and is not
yet routed through the complete planned Insight fact service.

## Selected grammar data

Only explicit grammar/scanner modules are linked:

| ID | Source |
|---|---|
| Go | tree-sitter-go v0.25.0 |
| Python | tree-sitter-python v0.25.0 |
| JavaScript/JSX | tree-sitter-javascript v0.23.1 |
| TypeScript/TSX | tree-sitter-typescript commit 75b3874edb2dc714fb1fd77a32013d0f8699989f |

Highlight query data in `syntax/queries/` derives from those MIT sources.
TypeScript/TSX compose the JavaScript and TypeScript queries. The Go generic
identifier pattern precedes specific function patterns so later-pattern
precedence does not erase function roles. Nested captures override enclosing
captures; an embedded expression may mask its string ancestor with plain ink.
Text predicates run through the binding. Local-scope predicates are not
assumed true: those patterns are omitted until a locals owner exists.

This is not scope/type analysis, semantic tokens, cross-language injections,
automatic provider acquisition or general language coverage. Unsupported
languages return an explicit unavailable result; the product retains existing
lexical support. Bundled grammar selection is separately reviewed native code,
not model-authored semantic behavior or executable data downloaded implicitly.
Parse errors/missing nodes remain syntax findings, not compiler diagnostics.
Problem output is bounded and reports truncation.

## Bounded native lifetime

Each call has a five-second maximum preparation context, chunked native input,
a query match budget, capture/output limits and cancellation callbacks.
Highlight derivation is currently whole-document even when tree reuse is
incremental. No claim of constant-time edits or keystroke latency follows from
a parser benchmark. Native heap/RSS is separate from Go allocation accounting.

The pinned replacement in `third_party/go-tree-sitter/` fixes callback lifetime
defects in the official v0.25.0 Go binding, with the upstream C runtime unchanged.
Parser progress handles are released after all encoding variants; query
options occupy C-owned memory for the cursor lifetime; re-exec/Close releases
their Go handle and storage. Logger replacement/close releases its old handle,
and custom input buffers are freed. The regression observes actual callback
payload collection and invokes query callbacks after GC, not a proxy count.
See `third_party/go-tree-sitter/CRUDE.md`.

Do not remove the module replacement or substitute a runtime/grammar ABI
without the corpus, native-owner and platform gates. Go's race detector alone
does not prove C memory safety. The binding is not a scanner security sandbox.

## Verification and release

`make test-syntax` runs the library corpus and the separate replacement-module
suite, which ordinary root `go test ./...` does not include. CI includes both
Linux and native Windows binding checks; configuration is not a claim that a
hosted or native Windows run has passed.

The corpus checks independently expected colours and TypeScript arrow AST,
long offscreen context changes and undo, CRLF/Unicode, random malformed edits
against fresh parsing, cancellation/revision/input ownership, real Go/TSX
sources and fuzz seeds. Allocation benchmarks are gated separately from typing
and native RSS. See [performance baselines](performance-baselines.md).

Release archives must include the embedded query notice from
`syntax/queries/NOTICE` as well as the dependency licenses. Those query assets
are part of the original module and would be excluded by go-licenses'
original-module ignore rule. Archive tests verify exact notice bytes and reject
missing notices on both package formats.

Focused three-client wire/App journeys cover shared results, slow analysis ACK,
pending edits, author-specific undo/save and lifecycle fencing. Linux native
captures at 160x40 and 80x24 exercise tree-open, actual typing and undo for
Go/Python with Syntax shared visible. The first capture targeted the tree filter
instead of an editor and is not accepted as editor evidence. All capture windows
and owned private cores were closed. These are not VM, remote or human calibration
passes, and screenshots do not establish a 16 ms latency tail.

The remaining product work and user-journey gates belong to
[autonomous intelligence](../plans/autonomous-code-intelligence.md) and
[workflow acceptance](../plans/ide-workflow-acceptance.md); the initial syntax
integration does not close those larger gates.
