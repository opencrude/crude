package providersim

import (
	"maps"
	"strings"
	"testing"
)

func codexInput(items ...any) responseObject {
	return responseObject{"model": "gpt-5-codex", "input": items, "stream": true, "store": false, "parallel_tool_calls": true, "tool_choice": "auto", "include": []string{"reasoning.encrypted_content"}}
}

func TestCodexResponseStreamAndCompaction(t *testing.T) {
	s := New(t)
	s.Script(Text("ignored").WithTools(ToolCall{Name: "not_compaction"}), Text("ok").WithReasoning("summary", "", "cipher"))
	input := codexInput(responseObject{"type": "message", "role": "user", "content": []any{responseObject{"type": "input_text", "text": "carry this"}}}, responseObject{"type": "compaction_trigger"})
	input["client_metadata"] = responseObject{"version": "1"}
	input["request_country"] = "US"
	input["stream_options"] = responseObject{"reasoning_summary_delivery": "sequential_cutoff"}
	ev := completeEvents(t, postResponses(t, s, Codex, "/responses", input), true)
	if got := eventNames(ev); got != "created in_progress output_item.added compaction.compacting output_item.done completed" {
		t.Fatal(got)
	}
	out := lastResponse(ev)["output"].([]any)
	if len(out) != 1 || out[0].(responseObject)["type"] != "compaction" {
		t.Fatal(out)
	}
	req, _ := s.Last()
	if req.Wire != Codex || len(req.Messages) != 2 || len(req.Effective.Messages) != 1 || req.Path != "/backend-api/codex/responses" {
		t.Fatal(req)
	}
	body := codexInput(out[0], responseObject{"role": "user", "type": "message", "content": []any{responseObject{"type": "input_text", "text": "continue"}}})
	body["reasoning"] = responseObject{"summary": "auto"}
	ev = completeEvents(t, postResponses(t, s, Codex, "/responses", body), true)
	if !strings.Contains(eventNames(ev), "reasoning_summary_text.delta") || lastResponse(ev)["output"].([]any)[0].(responseObject)["encrypted_content"] != "cipher" {
		t.Fatal(ev)
	}
	req, _ = s.Last()
	if len(req.Effective.Messages) != 2 || !strings.Contains(req.Effective.Messages[0].Parts[0].Text, "carry this") || req.Effective.Messages[1].Parts[0].Text != "continue" {
		t.Fatal(req.Effective)
	}
}

func TestCodexRejectsNonHTTPClientShapes(t *testing.T) {
	s := newServer(t, func(string, ...any) {}, nil)
	base := codexInput(responseObject{"role": "user", "type": "message", "content": []any{responseObject{"type": "input_text", "text": "hello"}}})
	for key, bad := range map[string]any{"previous_response_id": "resp_1", "store": true, "stream": false, "input": "string shorthand", "instructions": responseObject{"text": "not text"}, "tool_choice": responseObject{"type": "function"}, "client_metadata": responseObject{"x": 1}, "stream_options": responseObject{"reasoning_summary_delivery": "unknown"}} {
		req := maps.Clone(base)
		req[key] = bad
		e := responseJSON(t, postResponses(t, s, Codex, "/responses", req), 400, "ErrorResponse")["error"].(responseObject)
		if e["param"] != key {
			t.Fatal(key, e)
		}
	}
	bad := codexInput(responseObject{"type": "compaction_trigger"}, base["input"].([]any)[0])
	responseJSON(t, postResponses(t, s, Codex, "/responses", bad), 400, "ErrorResponse")
	for _, path := range []string{"/responses/compact", "/responses/resp_1"} {
		res := get(t, s.BaseURL(Codex)+path)
		readResponse(t, res)
		if res.StatusCode != 404 {
			t.Fatal(res.Status)
		}
	}
}

func TestCodexStreamErrorCodes(t *testing.T) {
	s := New(t)
	for kind, code := range map[FaultKind]string{Overload: "server_is_overloaded", RateLimit: "rate_limit_exceeded", ContextOverflow: "context_length_exceeded", Auth: "invalid_api_key"} {
		s.Script(StreamError(1, kind).WithRetryHint(RetryAfterMilliseconds(730)))
		ev := completeEvents(t, postResponses(t, s, Codex, "/responses", codexInput(responseObject{"role": "user", "type": "message", "content": []any{responseObject{"type": "input_text", "text": "hi"}}})), true)
		if eventNames(ev) != "created failed" {
			t.Fatal(eventNames(ev))
		}
		e := lastResponse(ev)["error"].(responseObject)
		if e["code"] != code || e["headers"].(responseObject)["retry-after-ms"] != "730" {
			t.Fatal(e)
		}
	}
}

func TestCodexValidatesPrivateReplayInsteadOfPublicCreate(t *testing.T) {
	s := newServer(t, func(string, ...any) {}, []Option{WithCacheProfile(Codex, "gpt-5-codex", CacheProfile{Minimum: 1, Granularity: 1})})
	s.Script(Text("ok"), Text("again"))
	msg := responseObject{"type": "message", "role": "assistant", "content": []any{responseObject{"type": "output_text", "text": "previous answer"}}}
	thought := responseObject{"type": "reasoning", "summary": []any{}, "encrypted_content": "private"}
	req := codexInput(thought, msg)
	req["reasoning"] = responseObject{"effort": 127, "summary": "none"}
	req["include"] = []string{"private.item"}
	req["prompt_cache_options"] = responseObject{"ttl": "12h"}
	first := lastResponse(completeEvents(t, postResponses(t, s, Codex, "/responses", req), true))
	usage := first["usage"].(responseObject)
	if usage["input_tokens_details"].(responseObject)["cache_write_tokens"] != usage["input_tokens"] {
		t.Fatal(usage)
	}
	last, _ := s.Last()
	if last.Effective.Messages[0].Parts[0].Encrypted != "private" || last.Effective.Messages[1].Parts[0].Text != "previous answer" {
		t.Fatal(last.Effective)
	}
	second := lastResponse(completeEvents(t, postResponses(t, s, Codex, "/responses", req), true))
	usage = second["usage"].(responseObject)
	if usage["input_tokens_details"].(responseObject)["cached_tokens"] != usage["input_tokens"] {
		t.Fatal(usage)
	}
	msg["content"] = []any{responseObject{"type": "output_text", "text": false}}
	responseJSON(t, postResponses(t, s, Codex, "/responses", req), 400, "ErrorResponse")
	last, _ = s.Last()
	if !strings.Contains(last.Violation, "/input/1") {
		t.Fatal(last.Violation)
	}
}
