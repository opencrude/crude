package types

// LSPSemanticPosition is a zero-based UTF-16 position in synchronized source.
type LSPSemanticPosition struct{ Line, Character int }

// End.Character == -1 asks the core to resolve the end of that source line.
type LSPSemanticRange struct{ Start, End LSPSemanticPosition }

type LSPSemanticSupport struct {
	Types, Modifiers   []string
	Full, Delta, Range bool
}

// LSPSemanticResponse is detached raw provider evidence. The workspace, not the
// transport, attaches authoritative source, context and process identity.
type LSPSemanticResponse struct {
	Boundary    InsightBoundary
	WireVersion int
	JSON        []byte
	Support     LSPSemanticSupport
	Unsupported bool
}

// LSPSemanticSpan holds a source-relative byte range and legend indexes.
type LSPSemanticSpan struct {
	Start, End, Type int
	Modifiers        uint32
}

// LSPSemanticSet is read-only evidence tied to a private editor view and exact
// canonical source. Indexes refer to Types/Modifiers, never provider code.
type LSPSemanticSet struct {
	Boundary                InsightBoundary
	WireVersion             int
	ViewID                  string
	Sequence                uint64
	Range                   LSPSemanticRange
	Full, Unsupported, Null bool
	Types, Modifiers        []string
	Spans                   []LSPSemanticSpan
}
