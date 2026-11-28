# Glamour source tracing

Pinned from charm.land/glamour/v2 v2.0.0 (MIT; upstream LICENSE retained).
Only the ANSI renderer, style data and their internal dependencies are used.

Crude adds an optional SourceTrace hook at the existing token/code render
boundary. Goldmark owns Markdown parsing and container indentation; Glamour,
Chroma and Lip Gloss continue to own appearance, highlighting and wrapping.
The hook annotates a private in-process projection. These annotations are
consumed before native row painting and never reach a terminal. Default output
has no annotations. Margin/indent writers suspend source attribution while
writing synthetic spaces. Upstream tests and fixtures are retained.

The local delta adds `SourceTrace` to ANSI options; code-node extraction is
shared with the chat-wide copy command. Literal code no longer passes through
Markdown escape/entity rewriting. Inline code uses Goldmark's CommonMark text
normalization. Already-rendered link/table children keep their original ranges
instead of being interpreted a second time. Parsed block/cell boundaries retain
semantic separators. No parser, highlighter, table engine or wrap algorithm is
reimplemented here.

Mapped chat tables additionally keep parsed cells in source order for copy
and pass RTL header order to Lip Gloss as a column permutation. A neutral
header takes the enclosing list or quote's direction, then LTR. Goldmark's
containers pick a direction once from their first strong prose character,
skipping tables and code, and inherit when neutral. Glamour measures marker
and rail widths at layout, reserves each item's marker width on continuation
rows, and annotates the outside prefix on every row of the container. Crude
places those prefixes at inline-start, independently from the contained
paragraph direction. It resolves marker text in its own bidi run, as in
[CSS Lists 3](https://drafts.csswg.org/css-lists-3/#marker-pseudo).

Heading decoration also stays outside the source paragraph. The pinned Lip
Gloss layout hooks mark final cell boxes and their wrapped text, before it
is joined with adjacent columns. Thematic breaks use the actual block width
as non-source decoration. Mapped padding writes one styled run of spaces
instead of formatting each column separately. Unmapped rendering retains
upstream output.

Inferred file-reference AutoLink nodes carry the private
`crude-source-reference` attribute. They use the normal link painter with their
literal source spelling, including inside tables, without a duplicate footer.
Authored web links and their table footers keep the upstream behavior.

Code blocks call chroma's `quick.Highlight` steps directly, with the lexer
lookup memoized by fence language (`codeLexer`). Chroma's `lexers.Get` globs
every file pattern twice for a name it does not know, such as the empty
language of an unlabeled fence, on each render. Analysis, fallback,
formatter, style and output are unchanged.

Upstream golden outputs for `code_block` and issues 107, 239, 257, 48 and 79
are calibrated to Crude's existing Chroma/Lip Gloss/xansi dependency graph.
Before updating these imported baselines, all 54 outputs were compared byte for
byte with **unmodified** Glamour v2.0.0 on that same graph using a Go source
overlay: there was no local output delta. The differences are upstream
formatter reset sequences and the shared width of the definition-list arrow,
not new wrap rules or relaxed assertions. The upstream issue corpus is included.

Run the fork's ANSI tests with the root module's dependency graph. Running
`go test ./...` inside this directory selects the older upstream graph, which
is not the graph used to calibrate those outputs. An isolated copy of the root
`go.mod`/`go.sum`, supplied through `go test -mod=mod -modfile=...`, lets the tests
load their golden helper without changing product dependencies or fixtures.
Native Windows validation uses this product graph; no goldens are regenerated.
