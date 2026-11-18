package provider

import (
	"fmt"
	"strings"
	"testing"
)

func TestCustomPatchRequiresCompleteResponseItem(t *testing.T) {
	for _, terminal := range []string{
		`[DONE]`,
		`{"type":"response.completed","response":{"status":"completed","output":[]}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"custom_tool_call","call_id":"p","status":"in_progress","name":"apply_patch","input":"*** Begin Patch\n*** End Patch"}}`,
	} {
		d := (&OpenAIResponsesStandard{}).NewStreamDecoder()
		_, err := d.Decode(StreamEvent{Data: []byte(`{"type":"response.output_item.added","output_index":0,"item":{"type":"custom_tool_call","call_id":"p","status":"in_progress","name":"apply_patch","input":""}}`)})
		if err != nil {
			t.Fatal(err)
		}
		_, err = d.Decode(StreamEvent{Data: []byte(terminal)})
		if err == nil || !strings.Contains(err.Error(), "custom tool call") {
			t.Fatalf("incomplete patch accepted: %s (%v)", terminal, err)
		}
	}
}

func TestChatCustomPatchWithoutFinishReasonIsIncomplete(t *testing.T) {
	for _, call := range []string{
		fmt.Sprintf(`{"index":0,"type":"custom","id":"p","custom":{"name":"apply_patch","input":%q}}`, customPatch),
		`{"index":0,"type":"function","id":"p","function":{"name":"apply_patch","arguments":"{\"input\":\"*** Begin Patch\\n*** End Patch\"}"}}`,
	} {
		d := (&OpenAIStandard{}).NewStreamDecoder()
		_, err := d.Decode(StreamEvent{Data: []byte(fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[%s]}}]}`, call))})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Decode(StreamEvent{Data: []byte("[DONE]")}); err == nil {
			t.Fatalf("unfinished patch call accepted: %s", call)
		}
	}
}
