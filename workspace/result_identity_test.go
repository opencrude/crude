package workspace

import (
	"testing"
	"time"
)

func TestReusedProviderIDDoesNotMergeIndependentToolOperations(t *testing.T) {
	c := NewConversation()
	for _, id := range []string{"operation-1", "operation-2"} {
		c.UpdateToolCall("provider-reused-id", id, "edit_file", "same.go")
		c.UpdateToolResult("provider-reused-id", id, "edit_file", "Error: identical failure", true, 0, 0, 0)
	}
	for i := range 8 {
		c.AdvanceToolLifecycles(time.Now().Add(time.Duration(i+1) * time.Hour))
	}
	messages := c.Messages()
	if len(messages) != 2 || messages[0].Content != "Error: identical failure" || messages[1].Content != messages[0].Content ||
		messages[0].InternalID == messages[1].InternalID {
		t.Fatalf("a reused provider ID erased an independent result: %+v", messages)
	}
	c.UpdateToolResult("provider-reused-id", "operation-1", "edit_file", "First operation's late evidence", true, 0, 0, 0)
	messages = c.Messages()
	if messages[0].Content != "First operation's late evidence" || messages[1].Content != "Error: identical failure" {
		t.Fatal("late evidence was routed by the reused provider ID")
	}
}
