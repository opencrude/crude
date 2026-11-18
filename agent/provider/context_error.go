package provider

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var outputCeilingEvidence = regexp.MustCompile(`(?i)\b(max_tokens|max_completion_tokens|max_output_tokens)\s*\(current value:\s*(\d+)\)\s*must be between\s*0\s*and\s*(\d+)`)

// OutputCeilingFromError accepts only a provider-reported limit for the exact
// output token parameter/value we sent. Unrelated invalid requests cannot
// change the learned capacity of this connection.
func OutputCeilingFromError(err error, requested int) (int, bool) {
	var status *StatusError
	if requested < 1 || !errors.As(err, &status) ||
		(status.StatusCode != http.StatusBadRequest && status.StatusCode != http.StatusUnprocessableEntity) {
		return 0, false
	}
	match := outputCeilingEvidence.FindStringSubmatch(status.Message)
	if len(match) != 4 {
		return 0, false
	}
	actual, err1 := strconv.Atoi(match[2])
	ceiling, err2 := strconv.Atoi(match[3])
	if err1 != nil || err2 != nil || actual != requested || ceiling <= 0 || ceiling >= requested {
		return 0, false
	}
	return ceiling, true
}

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
