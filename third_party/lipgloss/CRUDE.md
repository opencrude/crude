# Lip Gloss chat layout hooks

Pinned from `charm.land/lipgloss/v2` v2.0.3 (MIT; upstream license and tests
retained). Both new hooks are optional. `table.CellRenderFunc` exposes the
sized style of each cell at the existing render call. `Style.RenderContent`
can mark the wrapped content before generating alignment spaces, padding,
margins and borders. Crude uses the hooks only for its private mapped Markdown
projection. Lip Gloss still owns column negotiation, style, wrapping and
joining. Existing callers take the original unannotated render path.

`WrapWriter` feeds the same byte stream to the upstream ANSI parser. It
forwards contiguous text to the underlying writer, splitting only at newlines
where it must reset and restore the pen. It does not allocate a slice and
call the writer once per byte of every nested Markdown block.
