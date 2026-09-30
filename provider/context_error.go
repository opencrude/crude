package provider

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type ContextWindowError struct {
	InputTokens   int
	ContextWindow int
}

func (err *ContextWindowError) Error() string {
	return fmt.Sprintf("estimated input exceeds the model context window (%d/%d tokens)", err.InputTokens, err.ContextWindow)
}

func IsContextWindowExceeded(err error) bool {
	var local *ContextWindowError
	if errors.As(err, &local) {
		return true
	}
	var status *StatusError
	var streamed *APIError
	var code, message string
	switch {
	case errors.As(err, &status):
		if status.StatusCode != http.StatusBadRequest && status.StatusCode != http.StatusRequestEntityTooLarge && status.StatusCode != http.StatusUnprocessableEntity {
			return false
		}
		code, message = status.Code, status.Message
	case errors.As(err, &streamed):
		code, message = streamed.Code+" "+streamed.Type, streamed.Message
	default:
		return false
	}
	text := strings.ToLower(code + " " + message)
	for _, marker := range []string{"context_length_exceeded", "context_window_exceeded", "maximum context length", "exceeds the context window", "exceeds model context", "input is longer than the model's context length", "prompt is too long"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
