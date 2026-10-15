package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestControlApprovalLinesNeverShowSecretValues(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"name": "EXA_API_KEY", "secret": "exa-live-value",
		"connection": map[string]any{"id": "docs", "headers": map[string]string{"X-Custom": "header-value"}, "environment": map[string]string{"PLAIN": "env-value"}},
	})
	lines := controlArgumentLines(raw)
	for _, leaked := range []string{"exa-live-value", "header-value", "env-value"} {
		if strings.Contains(lines, leaked) {
			t.Fatalf("approval prompt shows %q:\n%s", leaked, lines)
		}
	}
	if !strings.Contains(lines, "name: EXA_API_KEY") || !strings.Contains(lines, "connection.id: docs") {
		t.Fatalf("approval prompt lost non-secret context:\n%s", lines)
	}
}
