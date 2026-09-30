package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestContextWindowErrorsRequireCanonicalCapacityEvidence(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{"local", &ContextWindowError{InputTokens: 497687, ContextWindow: 256000}, true},
		{"wrapped code", fmt.Errorf("request: %w", &StatusError{StatusCode: http.StatusBadRequest, Code: "context_length_exceeded"}), true},
		{"DeepSeek phrasing", &StatusError{StatusCode: http.StatusBadRequest, Message: "The input is longer than the model's context length"}, true},
		{"streamed", &APIError{Code: "context_window_exceeded"}, true},
		{"authorization", &StatusError{StatusCode: http.StatusUnauthorized, Message: "prompt is too long"}, false},
		{"payload size", &StatusError{StatusCode: http.StatusRequestEntityTooLarge, Message: "request entity too large"}, false},
		{"bad schema", &StatusError{StatusCode: http.StatusBadRequest, Message: "invalid arguments"}, false},
		{"untyped text", errors.New("context_length_exceeded"), false},
		{"cancellation", context.Canceled, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IsContextWindowExceeded(test.err); got != test.want {
				t.Fatalf("context classification=%t want %t", got, test.want)
			}
		})
	}
}
