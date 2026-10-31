package types

// LSPRenameEdit retains original provider JSON until the canonical coordinator
// validates its text-only shape. Generated protocol optional versions must not
// turn explicit null or missing identity into a fabricated version zero.
type LSPRenameEdit struct {
	JSON        []byte
	WireVersion int
	Boundary    InsightBoundary // authoritative workspace coordinator only
}
