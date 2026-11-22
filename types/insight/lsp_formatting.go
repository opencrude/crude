package insight

import "crude/workspace/document"

// Formatting spans are UTF-8 offsets in the exact canonical source. Wire
// positions are derived on the core, never from terminal columns.
type LSPFormattingSpan struct{ Start, End int }
type LSPFormattingOptions struct {
	TabSize      int
	InsertSpaces bool
}
type LSPFormattingRequest struct {
	Source  Source
	Ranges  []LSPFormattingSpan
	Options LSPFormattingOptions
}
type LSPFormattingResponse struct {
	WireVersion int
	JSON        []byte
	Unsupported bool
}
type LSPFormattingResult struct {
	Boundary    Boundary
	WireVersion int
	Edits       []document.Edit
	Unsupported bool
}
