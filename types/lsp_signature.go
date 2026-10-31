package types

// LSPSignatureHelp is detached parameter information for an exact synchronized
// source. It is provider evidence, not proof of complete project context.
type LSPSignatureHelp struct {
	Boundary        InsightBoundary // filled only by the authoritative workspace coordinator
	Signatures      []LSPSignature
	ActiveSignature int
}

type LSPSignature struct {
	Label           string
	Documentation   string
	Parameters      []LSPSignatureParameter
	ActiveParameter int // -1 when the signature has no parameters
}

// Label is the provider's text, or the exact substring selected by its UTF-16
// offsets. Offsets are never terminal columns, and are not exported for editing.
type LSPSignatureParameter struct {
	Label         string
	Documentation string
}
