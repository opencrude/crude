// Package lspnav passively normalizes bounded, original standard-LSP navigation
// JSON. Targets are data, not permission to open a URI or trust a provider.
// This package performs no I/O and has no source-text or capability knowledge.
package lspnav

// Kind identifies the standard navigation response union.
type Kind string

const (
	Definition     Kind = "definition"
	TypeDefinition Kind = "type-definition"
	Implementation Kind = "implementation"
	References     Kind = "references"
)

const (
	MaxRawBytes = 1 << 20
	MaxTargets  = 1024
	MaxURIBytes = 4096
)

// Position is a nonnegative, at-most-int32 LSP logical position. Character is a
// UTF-16 code-unit offset, not a byte offset, rune index or display column.
// Validating an endpoint against actual target text belongs to the caller.
type Position struct {
	Line, Character int
}

type Range struct {
	Start, End Position
}

// Target preserves the decoded URI verbatim, without resolution, normalization
// or authorization. A Location has Selection == Range, nil Origin and Link false.
// A LocationLink retains its target range, selection and optional origin.
type Target struct {
	URI       string
	Range     Range
	Selection Range
	Origin    *Range
	Link      bool
}

// Answer distinguishes explicit null, an observed empty list, and target data.
// Unsupported capability is a producer-layer outcome, never a parser answer.
type Answer string

const (
	TargetsAnswer Answer = "targets"
	NullAnswer    Answer = "null"
	EmptyAnswer   Answer = "empty"
)

type Result struct {
	Answer  Answer
	Targets []Target
}
