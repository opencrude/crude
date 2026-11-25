package provider

import (
	"fmt"
	"testing"
)

func TestOpenAIUsagePresenceIsNotInferredFromDetailsObject(t *testing.T) {
	for _, test := range []struct {
		name, usage            string
		read, write, reasoning bool
	}{
		{"absent", `{"prompt_tokens":100,"completion_tokens":10}`, false, false, false},
		{"read-only", `{"prompt_tokens":100,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":20}}`, true, false, false},
		{"explicit-zero", `{"prompt_tokens":100,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}`, true, true, true},
		{"unrelated-details", `{"prompt_tokens":100,"completion_tokens":10,"prompt_tokens_details":{"audio_tokens":0},"completion_tokens_details":{"audio_tokens":0}}`, false, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			update, err := (&OpenAIStandard{}).NewStreamDecoder().Decode(StreamEvent{Data: []byte(`{"choices":[],"usage":` + test.usage + `}`)})
			if err != nil {
				t.Fatal(err)
			}
			if update.Usage == nil || update.Usage.CacheReadKnown != test.read || update.Usage.CacheWriteKnown != test.write || update.Usage.ReasoningKnown != test.reasoning {
				t.Fatalf("reported-field presence was changed: %+v", update.Usage)
			}
		})
	}
}

func TestResponsesUsageRetainsReportedReasoning(t *testing.T) {
	update, err := (&OpenAIResponsesStandard{}).NewStreamDecoder().Decode(StreamEvent{
		Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"response","status":"completed","output":[],"usage":{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"cached_tokens":20},"output_tokens_details":{"reasoning_tokens":3}}}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if update.Usage == nil || !update.Usage.ReasoningKnown || update.Usage.ReasoningTokens != 3 || !update.Usage.ReasoningInOutput || update.Usage.CacheWriteKnown {
		t.Fatalf("Responses usage dropped reasoning or invented cache writes: %+v", update.Usage)
	}
}

func TestAnthropicUsageAddsCacheToInputOnce(t *testing.T) {
	decoder := (&AnthropicStandard{}).NewStreamDecoder()
	start, err := decoder.Decode(StreamEvent{Data: []byte(`{"type":"message_start","message":{"usage":{"input_tokens":70,"output_tokens":1,"cache_read_input_tokens":20,"cache_creation_input_tokens":10}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	final, err := decoder.Decode(StreamEvent{Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10}}`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, usage := range []*TokenUsage{start.Usage, final.Usage} {
		if usage == nil || usage.PromptTokens != 100 || usage.PromptTokensCached != 20 || usage.PromptTokensWritten != 10 || !usage.CacheReadKnown || !usage.CacheWriteKnown || usage.ReasoningKnown {
			t.Fatalf("Anthropic cache normalization is wrong: %+v", usage)
		}
	}
	if final.Usage.CompletionTokens != 10 {
		t.Fatalf("Anthropic cumulative output added instead of replaced: %+v", final.Usage)
	}
	missing, err := (&AnthropicStandard{}).NewStreamDecoder().Decode(StreamEvent{Data: []byte(`{"type":"message_start","message":{"usage":{"input_tokens":70,"output_tokens":1}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if missing.Usage.CacheReadKnown || missing.Usage.CacheWriteKnown {
		t.Fatalf("missing Anthropic cache counts invented: %+v", missing.Usage)
	}
}

func TestOllamaUsageDoesNotInventCacheOrReasoningCounts(t *testing.T) {
	for _, wire := range []string{
		`{"done":true,"message":{"role":"assistant","content":"ok","thinking":"reasoning text"},"prompt_eval_count":100,"eval_count":10,"eval_duration":500000000}`,
		`{"done":true,"message":{"role":"assistant","content":"ok"},"prompt_eval_tokens":100,"eval_tokens":10,"eval_duration":500000000}`,
	} {
		update, err := (&OllamaStandard{}).NewStreamDecoder().Decode(StreamEvent{Data: []byte(wire)})
		if err != nil {
			t.Fatal(err)
		}
		u := update.Usage
		if u == nil || u.PromptTokens != 100 || u.CompletionTokens != 10 || u.CacheReadKnown || u.CacheWriteKnown || u.ReasoningKnown || u.EvalDuration != 500000000 {
			t.Fatalf("Ollama token counts/timing misreported: %+v", u)
		}
	}
}

// A gateway may omit optional usage fields. In each codec a missing count,
// a reported miss and a reported hit must survive both response paths.
func TestOptionalCacheReadsSurviveWireProjection(t *testing.T) {
	for _, tc := range []struct {
		proto                    Protocol
		key, body, event, stream string
	}{
		{&OllamaStandard{}, "prompt_eval_cached_count", `{"done":true,"message":{"role":"assistant","content":"ok"},"prompt_eval_count":100,"eval_count":1%s}`, "", "%s"},
		{&GeminiStandard{}, "cachedContentTokenCount", `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100%s}}`, "", "%s"},
		{&GeminiInteractionsStandard{}, "total_cached_tokens", `{"id":"i","status":"completed","steps":[],"usage":{"total_input_tokens":100%s}}`, "interaction.completed", `{"event_type":"interaction.completed","interaction":%s}`},
	} {
		t.Run(tc.proto.Info().ID, func(t *testing.T) {
			for _, n := range []int{-1, 0, 75} {
				field := ""
				if n >= 0 {
					field = fmt.Sprintf(",%q:%d", tc.key, n)
				}
				body := fmt.Sprintf(tc.body, field)
				check := func(u TokenUsage) {
					t.Helper()
					if u.PromptTokens != 100 || u.CacheReadKnown != (n >= 0) || u.PromptTokensCached != max(n, 0) {
						t.Fatalf("%s count %d: %+v", tc.key, n, u)
					}
				}
				response, err := tc.proto.DecodeResponse([]byte(body))
				if err != nil {
					t.Fatal(err)
				}
				check(response.Usage.Normalized())
				update, err := tc.proto.NewStreamDecoder().Decode(StreamEvent{Event: tc.event, Data: []byte(fmt.Sprintf(tc.stream, body))})
				if err != nil || update.Usage == nil {
					t.Fatalf("stream usage: %v, %v", update, err)
				}
				check(*update.Usage)
				if tc.proto.Info().ID == "ollama" {
					resp, err := tc.proto.DecodeResponse([]byte(`{"done":false,"message":{"content":"a"}}` + "\n" + body))
					if err != nil {
						t.Fatal(err)
					}
					check(resp.Usage.Normalized())
				}
			}
		})
	}
}

func TestAnthropicCompactionUsageRequiresEveryIterationToReportCache(t *testing.T) {
	wire := &AnthropicStandard{}
	_, got, err := wire.compactResult([]byte(`{
		"type":"message","role":"assistant","stop_reason":"compaction",
		"content":[{"type":"compaction","content":"summary","signature":"abc"}],
		"usage":{"input_tokens":999,"output_tokens":999,"speed":"fast","iterations":[
			{"type":"compaction","input_tokens":10,"output_tokens":2,"cache_read_input_tokens":100,"cache_creation_input_tokens":5},
			{"type":"compaction","input_tokens":20,"output_tokens":3,"cache_read_input_tokens":0}
		]}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.PromptTokens != 135 || got.CompletionTokens != 5 || got.PromptTokensCached != 100 ||
		!got.CacheReadKnown || got.CacheWriteKnown || got.ServiceTier != "fast" {
		t.Fatalf("incomplete writes or outer summary distorted usage: %+v", got)
	}
}
