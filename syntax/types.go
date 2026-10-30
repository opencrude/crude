// Package syntax owns bounded, presentation-neutral native syntax analysis.
// Its results are structural, not type-checked semantic facts.
package syntax

import (
	"errors"
	"time"

	"crude/document"
	syntaxdata "crude/types/syntax"
)

const (
	MaxDocumentBytes = document.MaxBytes
	MaxCaptures      = 1 << 20
	MaxProblems      = 1024
	PreparationLimit = 5 * time.Second
)

var (
	ErrUnsupported = errors.New("syntax language is not bundled")
	ErrClosed      = errors.New("syntax session is closed")
	ErrRevision    = errors.New("syntax revision is not newer than the accepted source")
	ErrLimit       = errors.New("syntax analysis limit exceeded")
	ErrInvalidText = errors.New("syntax source is not valid UTF-8")
)

// The engine's public vocabulary is the neutral wire contract. Aliases here
// keep one data identity; importing that contract never imports this C runtime.
type LanguageID = syntaxdata.LanguageID
type Kind = syntaxdata.Kind
type Span = syntaxdata.Span
type Problem = syntaxdata.Problem
type Identity = syntaxdata.Identity
type Result = syntaxdata.Result

const (
	Go         = syntaxdata.Go
	Python     = syntaxdata.Python
	JavaScript = syntaxdata.JavaScript
	TypeScript = syntaxdata.TypeScript
	TSX        = syntaxdata.TSX
)

const (
	Plain       = syntaxdata.Plain
	Keyword     = syntaxdata.Keyword
	Type        = syntaxdata.Type
	Function    = syntaxdata.Function
	Builtin     = syntaxdata.Builtin
	Property    = syntaxdata.Property
	Variable    = syntaxdata.Variable
	String      = syntaxdata.String
	Number      = syntaxdata.Number
	Comment     = syntaxdata.Comment
	Operator    = syntaxdata.Operator
	Punctuation = syntaxdata.Punctuation
	Constant    = syntaxdata.Constant
	Tag         = syntaxdata.Tag
	Attribute   = syntaxdata.Attribute
)
