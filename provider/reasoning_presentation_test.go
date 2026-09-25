package provider

import (
	"crude/types/reasoning"
	"testing"
)

func TestReasoningPresentationUsesWireContract(t *testing.T) {
	for _, tc := range []struct {
		name    string
		decoder StreamDecoder
		wire    string
		kind    reasoning.Kind
		text    string
	}{
		{"responses-summary", (&OpenAIResponsesStandard{}).NewStreamDecoder(), `{"type":"response.reasoning_summary_text.delta","delta":"Summary"}`, reasoning.Summary, "Summary"},
		{"responses-raw", (&OpenAIResponsesStandard{}).NewStreamDecoder(), `{"type":"response.reasoning_text.delta","delta":"Trace"}`, reasoning.Raw, "Trace"},
		{"router-summary", (&OpenAIStandard{}).NewStreamDecoder(), `{"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.summary","summary":"Summary"}]}}]}`, reasoning.Summary, "Summary"},
		{"router-raw", (&OpenAIStandard{}).NewStreamDecoder(), `{"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.text","text":"Trace"}]}}]}`, reasoning.Raw, "Trace"},
		{"flattened-unknown", (&OpenAIStandard{}).NewStreamDecoder(), `{"choices":[{"delta":{"reasoning_content":"Visible"}}]}`, reasoning.Unknown, "Visible"},
		{"encrypted-hidden", (&OpenAIStandard{}).NewStreamDecoder(), `{"choices":[{"delta":{"reasoning":"ciphertext","reasoning_details":[{"type":"reasoning.encrypted","data":"ciphertext"}]}}]}`, reasoning.Unknown, ""},
		{"anthropic-summary", (&AnthropicStandard{}).NewStreamDecoder(), `{"type":"content_block_start","index":2,"content_block":{"type":"thinking","thinking":"Summary"}}`, reasoning.Summary, "Summary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.decoder.Decode(StreamEvent{Data: []byte(tc.wire)})
			if err != nil || got.ReasoningText() != tc.text {
				t.Fatalf("update=%+v err=%v", got, err)
			}
			if tc.text != "" && (len(got.Reasoning) != 1 || got.Reasoning[0].Kind != tc.kind) {
				t.Fatalf("lost kind: %+v", got)
			}
		})
	}
}

func TestResponsesSummaryBoundariesAndDoneAreLossless(t *testing.T) {
	d := (&OpenAIResponsesStandard{}).NewStreamDecoder()
	var display reasoning.Display
	var text string
	for _, wire := range []string{
		`{"type":"response.reasoning_summary_text.delta","summary_index":0,"delta":"First"}`,
		`{"type":"response.reasoning_summary_text.done","summary_index":0,"text":"First"}`,
		`{"type":"response.reasoning_summary_text.delta","summary_index":1,"delta":"Second"}`,
		`{"type":"response.reasoning_summary_text.done","summary_index":1,"text":"Second summary"}`,
		`{"type":"response.completed","response":{"status":"completed","output":[{"type":"reasoning","summary":[{"text":"First"},{"text":"Second summary"}]}]}}`,
	} {
		u, err := d.Decode(StreamEvent{Data: []byte(wire)})
		if err != nil {
			t.Fatal(err)
		}
		for _, delta := range u.Reasoning {
			text = display.Append(text, delta)
		}
	}
	if text != "First\n\nSecond summary" || text[display.Start:] != "Second summary" || display.Kind != reasoning.Summary {
		t.Fatalf("text=%q display=%+v", text, display)
	}
}

func TestAnthropicAdaptiveDisplayAndEffort(t *testing.T) {
	s := &AnthropicStandard{}
	for _, model := range []string{"claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8", "claude-opus-5-5", "claude-sonnet-5", "claude-fable-5-1", "claude-mythos-preview", "anthropic.claude-sonnet-4-6-v1"} {
		extra := s.RequestExtras(model, "high", ProtocolSettings{})
		thinking, _ := extra["thinking"].(map[string]any)
		if thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
			t.Fatalf("%s: %+v", model, extra)
		}
	}
	if s.RequestExtras("claude-haiku-4-5", "high", ProtocolSettings{}) != nil {
		t.Fatal("sent unsupported adaptive config")
	}
	if thinking := s.RequestExtras("claude-sonnet-5", "none", ProtocolSettings{})["thinking"].(map[string]any); thinking["display"] != nil {
		t.Fatal("disabled thinking sent display")
	}
	if extra := s.RequestExtras("claude-opus-4-7", "", ProtocolSettings{}); extra["thinking"] == nil || extra["output_config"] != nil {
		t.Fatalf("default-on summary config=%+v", extra)
	}
}

func TestMeasuredGatewayKindOnlyAnnotatesFlattenedReasoning(t *testing.T) {
	decoder := (&OpenAIStandard{}).NewStreamDecoder()
	for _, tc := range []struct {
		name, wire string
		hint, want reasoning.Kind
	}{
		{"first unlabelled request", `{"choices":[{"delta":{"reasoning_content":"**A short summary**"}}]}`, reasoning.Unknown, reasoning.Provisional},
		{"measured connection", `{"choices":[{"delta":{"reasoning_content":"**A short summary**"}}]}`, reasoning.InferredSummary, reasoning.InferredSummary},
		{"explicit raw", `{"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.text","text":"private raw trace"}]}}]}`, reasoning.InferredSummary, reasoning.Raw},
		{"explicit summary", `{"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.summary","summary":"public summary"}]}}]}`, reasoning.Unknown, reasoning.Summary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			update, err := decoder.Decode(StreamEvent{Data: []byte(tc.wire)})
			if err != nil {
				t.Fatal(err)
			}
			annotateFlattenedReasoning(&update, tc.hint)
			if len(update.Reasoning) != 1 || update.Reasoning[0].Kind != tc.want {
				t.Fatalf("explicit wire type overridden: %+v", update.Reasoning)
			}
		})
	}
}
