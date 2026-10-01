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

func TestOutputCeilingRequiresExactProviderEvidence(t *testing.T) {
	for _, test := range []struct {
		name      string
		err       error
		requested int
		want      int
	}{
		{"deepseek rejection", &StatusError{StatusCode: 400, Message: `invalid_request_error: {"code":400,"reason":"INVALID_REQUEST_BODY","message":"max_tokens (current value: 550463) must be between 0 and 393216 "}`}, 550463, 393216},
		{"different output parameter", &StatusError{StatusCode: 422, Message: "max_completion_tokens (current value: 550463) must be between 0 and 393216"}, 550463, 393216},
		{"wrong requested value", &StatusError{StatusCode: 400, Message: "max_tokens (current value: 123) must be between 0 and 393216"}, 550463, 0},
		{"not smaller", &StatusError{StatusCode: 400, Message: "max_tokens (current value: 550463) must be between 0 and 999999"}, 550463, 0},
		{"input rejection", &StatusError{StatusCode: 400, Message: "prompt is too long"}, 550463, 0},
		{"untrusted text", errors.New("max_tokens (current value: 550463) must be between 0 and 393216"), 550463, 0},
		{"auth failure", &StatusError{StatusCode: 401, Message: "max_tokens (current value: 550463) must be between 0 and 393216"}, 550463, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			limit, ok := OutputCeilingFromError(fmt.Errorf("extract intent: %w", test.err), test.requested)
			if limit != test.want || ok != (test.want != 0) {
				t.Fatalf("limit=%d accepted=%v, want %d", limit, ok, test.want)
			}
		})
	}
}
