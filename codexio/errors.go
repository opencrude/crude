package codexio

import "fmt"

// ParseError retains the upstream hunk line number and diagnostic text.
type ParseError struct {
	Line    int
	Message string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("invalid patch hunk at line %d, %s", e.Line, e.Message)
}

func hunkError(line int, message string) error {
	return &ParseError{Line: line, Message: message}
}
