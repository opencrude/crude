// Package syntax defines immutable source-syntax data without loading a parser.
// Wire clients and other neutral contracts must not depend on native runtimes.
package syntax

type LanguageID string

const (
	Go         LanguageID = "go"
	Python     LanguageID = "python"
	JavaScript LanguageID = "javascript"
	TypeScript LanguageID = "typescript"
	TSX        LanguageID = "tsx"
)

// Kind is independent of terminal themes, lexers and semantic providers.
type Kind uint8

const (
	Plain Kind = iota
	Keyword
	Type
	Function
	Builtin
	Property
	Variable
	String
	Number
	Comment
	Operator
	Punctuation
	Constant
	Tag
	Attribute
)

// Span is an exclusive UTF-8 source byte range, never a display or LSP column.
// A complete result partitions the source, including Plain gaps.
type Span struct {
	Start, End uint32
	Kind       Kind
}

type Problem struct {
	Start, End uint32
	Missing    bool
}

// Identity fences parser and query changes separately from source revisions.
type Identity struct {
	Language    LanguageID
	Grammar     string
	QueryDigest [32]byte
	Runtime     string
}

// Result contains no native handles. Its slices belong to the recipient.
// Syntax problems are not compiler diagnostics or project coverage.
type Result struct {
	Revision          uint64
	Identity          Identity
	Spans             []Span
	Problems          []Problem
	ProblemsTruncated bool
	HasError          bool
}
