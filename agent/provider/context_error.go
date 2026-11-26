package provider

import "fmt"

// ContextWindowError is Crude's own refusal to send a request whose estimated
// input already exceeds the model's context window.
type ContextWindowError struct {
	InputTokens   int
	ContextWindow int
}

func (err *ContextWindowError) Error() string {
	return fmt.Sprintf("estimated input exceeds the model context window (%d/%d tokens)", err.InputTokens, err.ContextWindow)
}
