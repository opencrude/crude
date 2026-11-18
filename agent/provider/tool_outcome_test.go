package provider

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"crude/types/toolresult"
)

func TestZeroToolOutcomeDoesNotChangeCanonicalMessageBytes(t *testing.T) {
	message := ChatMessage{Role: "tool", Content: TextContent("evidence"), ToolCallID: "call"}
	plain, err := json.Marshal(message)
	if err != nil || strings.Contains(string(plain), "tool_outcome") {
		t.Fatalf("zero presentation outcome changed existing immutable message identity: %s %v", plain, err)
	}
	message.ToolOutcome = &toolresult.Outcome{State: toolresult.Denied, CallID: "exact-call"}
	encoded, err := json.Marshal(message)
	if err != nil || !strings.Contains(string(encoded), `"state":"denied"`) {
		t.Fatalf("explicit outcome disappeared: %s %v", encoded, err)
	}
	var restored ChatMessage
	if json.Unmarshal(encoded, &restored) != nil || !reflect.DeepEqual(restored.ToolOutcome, message.ToolOutcome) {
		t.Fatal("outcome did not survive canonical persistence")
	}
}
