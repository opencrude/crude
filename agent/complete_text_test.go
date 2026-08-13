package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"crude/provider"
)

// TestCompleteTextOneShot exercises the background-job completion helper:
// a full request/response round trip with no tools, no streaming callbacks,
// and no session state.
func TestCompleteTextOneShot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if _, hasTools := request["tools"]; hasTools {
			t.Error("one-shot completion must not send tools")
		}
		msgs, _ := request["messages"].([]any)
		if len(msgs) != 2 {
			t.Errorf("messages = %d, want system+user", len(msgs))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w,
			"data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"memories\\\":[]}\"},\"finish_reason\":\"stop\"}]}\n\n"+
				"data: [DONE]\n\n")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	out, err := CompleteText(context.Background(), p, p.Model,
		"You are the memory curator.", "NEW TURNS:\n--- turn 1 ---\nUSER: hi")
	if err != nil {
		t.Fatalf("CompleteText: %v", err)
	}
	if out != `{"memories":[]}` {
		t.Fatalf("content = %q", out)
	}
}
