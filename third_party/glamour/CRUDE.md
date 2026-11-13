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

Upstream golden outputs for `code_block` and issues 107, 239, 257, 48 and 79
are calibrated to Crude's existing Chroma/Lip Gloss/xansi dependency graph.
Before updating these imported baselines, all 54 outputs were compared byte for
byte with **unmodified** Glamour v2.0.0 on that same graph using a Go source
overlay: there was no local output delta. The differences are upstream
formatter reset sequences and the shared width of the definition-list arrow,
not new wrap rules or relaxed assertions. The upstream issue corpus is included.
