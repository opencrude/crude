package turnloop

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"crude/agent/provider"
)

func TestRecoveryDiagnosticHidesCredentialsButKeepsTheStatus(t *testing.T) {
	const key = "sk-live-0123456789abcdefSECRET"
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"auth code", fmt.Errorf("send: %w", &provider.StatusError{StatusCode: 401, APICode: "invalid_api_key", Message: "invalid_api_key: Incorrect API key provided: " + key}),
			"API 401 (invalid_api_key). " + hiddenDiagnostic},
		{"bearer echo", &provider.StatusError{StatusCode: 400, Message: "bad header Authorization: Bearer " + key},
			"API 400. " + hiddenDiagnostic},
		{"free-form code", &provider.StatusError{StatusCode: 403, APICode: key, Message: "api_key=" + key},
			"API 403. " + hiddenDiagnostic},
		{"lowercase credential in code", &provider.StatusError{StatusCode: 403, APICode: strings.ToLower(key), Message: "api_key=" + strings.ToLower(key)},
			"API 403. " + hiddenDiagnostic},
		{"not a status", errors.New("dial: password=" + key), hiddenDiagnostic},
		{"ordinary", &provider.StatusError{StatusCode: 503, Message: "overloaded"}, "API 503: overloaded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := recoveryDiagnostic(tc.err)
			if got != tc.want || strings.Contains(got, key) {
				t.Fatalf("diagnostic = %q, want %q", got, tc.want)
			}
		})
	}
}
