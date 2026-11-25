package turnloop

import (
	"context"
	"encoding/json"
	"testing"

	"crude/agent/provider"
	"crude/internal/providersim"
)

// TestCompleteTextOneShot exercises the background-job completion helper:
// a full request/response round trip with no tools, no streaming callbacks,
// and no session state.
func TestCompleteTextOneShot(t *testing.T) {
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(r.Raw, &fields); err != nil {
			t.Error(err)
		}
		if _, present := fields["tools"]; present {
			t.Error("one-shot completion must not send tools")
		}
		if len(r.System) != 1 || len(r.Messages) != 1 || r.Messages[0].Role != "user" {
			t.Errorf("want system+user, got %+v", r.Conversation)
		}
		return providersim.Text(`{"memories":[]}`)
	})

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
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
