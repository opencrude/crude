package providersim

import (
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestMessageModelDetail(t *testing.T) {
	const sonnet, haiku = "claude-sonnet-4-6", "claude-haiku-4-5"
	s := New(t, WithCredentialModels(Messages, "a", Model{ID: sonnet, DisplayName: "Configured Sonnet", ContextWindow: 123456}),
		WithCredentialModels(Messages, "b", Model{ID: haiku}))
	getModel := func(path, key, beta string) *http.Response {
		t.Helper()
		r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.BaseURL(Messages)+"/models"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("x-api-key", key)
		r.Header.Set("anthropic-version", "2023-06-01")
		if beta != "" {
			r.Header.Set("anthropic-beta", beta)
		}
		return do(t, r)
	}
	for _, m := range []struct{ id, key string }{{sonnet, "a"}, {haiku, "b"}} {
		for _, beta := range []string{"", "compact-2026-09-04"} {
			schema := "ModelInfo"
			if beta != "" {
				schema = "BetaModelInfo"
			}
			row := messageJSON(t, getModel("/"+m.id, m.key, beta), 200, schema)
			list := messageJSON(t, getModel("", m.key, beta), 200, "")
			if row["id"] != m.id || !reflect.DeepEqual(list["data"], []any{row}) {
				t.Fatalf("detail does not match the authorized inventory: %v", row)
			}
			if m.id == sonnet && (row["display_name"] != "Configured Sonnet" || numField(t, row, "max_input_tokens") != 123456) {
				t.Fatalf("missing configured metadata: %v", row)
			}
			caps := row["capabilities"].(messageObject)
			if caps["context_management"].(messageObject)["compact_20260112"].(messageObject)["supported"] != true ||
				(beta == "" && caps["compaction"] != nil) ||
				(beta != "" && caps["compaction"].(messageObject)["summarize"].(messageObject)["supported"] != (m.id == sonnet)) {
				t.Fatalf("incorrect capabilities for %s with beta %q: %v", m.id, beta, caps)
			}
		}
	}
	messageErrorType(t, getModel("/"+sonnet, "bad", ""), 401, "authentication_error", "ErrorResponse")
	messageErrorType(t, getModel("/"+sonnet, "b", ""), 404, "not_found_error", "ErrorResponse")
}

func TestPublicResponsesExplicitTrigger(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			s := New(t)
			s.Script(Text("issued"), Text("not generated"), Text("continued"))
			saved := responseJSON(t, postResponses(t, s, Responses, "/responses", testInput("remember ticket 187")), 200, "Response")
			req := testInput([]any{responseObject{"role": "user", "content": "also remember blue"}, responseObject{"type": "compaction_trigger"}})
			req["previous_response_id"], req["stream"] = saved["id"], stream
			res := postResponses(t, s, Responses, "/responses", req)
			var response responseObject
			if stream {
				events := completeEvents(t, res, false)
				if eventNames(events) != "created in_progress output_item.added compaction.compacting output_item.done completed" {
					t.Fatal(eventNames(events))
				}
				response = lastResponse(events)
			} else {
				response = responseJSON(t, res, 200, "Response")
			}
			out := response["output"].([]any)
			if len(out) != 1 || out[0].(responseObject)["type"] != "compaction" || out[0].(responseObject)["encrypted_content"] == "" {
				t.Fatalf("expected one native compaction item: %v", out)
			}
			responseJSON(t, postResponses(t, s, Responses, "/responses", testInput(append(out, responseObject{"role": "user", "content": "next"}))), 200, "Response")
			last, _ := s.Last()
			if len(last.Effective.Messages) != 2 {
				t.Fatalf("lost or repeated history: %v", last.Effective)
			}
			summary := last.Effective.Messages[0].Parts[0].Text
			if !strings.Contains(summary, "ticket 187") || !strings.Contains(summary, "also remember blue") || strings.Contains(summary, "compaction_trigger") {
				t.Fatalf("wrong effective history after compaction: %s", summary)
			}
		})
	}
}

func TestPublicResponsesRejectsMisplacedTrigger(t *testing.T) {
	var reports []string
	s := newServer(t, func(f string, a ...any) { reports = append(reports, fmt.Sprintf(f, a...)) }, nil)
	s.Script(Text("still next"))
	req := testInput([]any{responseObject{"type": "compaction_trigger"}, responseObject{"role": "user", "content": "next"}})
	e := responseJSON(t, postResponses(t, s, Responses, "/responses", req), 400, "ErrorResponse")["error"].(responseObject)
	if len(reports) != 1 || !strings.Contains(reports[0], "/input/0") || e["param"] != "input" {
		t.Fatal(reports, e)
	}
	valid := responseJSON(t, postResponses(t, s, Responses, "/responses", testInput("hello")), 200, "Response")
	if !strings.Contains(string(jsonBytes(valid["output"])), "still next") {
		t.Fatal(valid)
	}
}

func TestPublicResponsesExpireIssuedID(t *testing.T) {
	var reports []string
	s := newServer(t, func(f string, a ...any) { reports = append(reports, fmt.Sprintf(f, a...)) }, nil)
	s.Script(Text("first"), Text("full replay"))
	body := testInput("original")
	body["store"] = true
	first := responseJSON(t, postResponses(t, s, Responses, "/responses", body), 200, "Response")
	id := first["id"].(string)
	if !s.ExpireResponse(id) || s.ExpireResponse(id) || s.ExpireResponse("resp_fabricated") {
		t.Fatal("expiry must act only on a currently stored ID")
	}
	responseJSON(t, get(t, s.BaseURL(Responses)+"/responses/"+id), 404, "ErrorResponse")
	next := testInput("next")
	for _, ref := range []string{id, id, "resp_fabricated"} {
		next["previous_response_id"] = ref
		fault := responseJSON(t, postResponses(t, s, Responses, "/responses", next), 400, "ErrorResponse")["error"].(responseObject)
		last, _ := s.Last()
		if last.PreviousResponseID != ref || fault["param"] != "previous_response_id" || fault["code"] != "previous_response_not_found" {
			t.Fatal(last, fault)
		}
		if ref == id && (len(reports) != 0 || last.Violation != "") {
			t.Fatal("provider-side expiry failed the test", reports)
		}
	}
	if len(reports) != 1 || !strings.Contains(reports[0], "resp_fabricated") {
		t.Fatalf("a fabricated ID must fail a strict test: %v", reports)
	}
	rebase := testInput([]any{responseObject{"role": "user", "content": "original"}, first["output"].([]any)[0], responseObject{"role": "user", "content": "next"}})
	answer := responseJSON(t, postResponses(t, s, Responses, "/responses", rebase), 200, "Response")
	if !strings.Contains(string(jsonBytes(answer["output"])), "full replay") {
		t.Fatalf("the stale ref consumed a turn: %v", answer)
	}
}

// At codex@ebd0765, core/src/compact_remote_v2_attempt.rs appends a
// CompactionTrigger and compact_remote_v2.rs uses stream_responses_http.
// Public OpenAPI is not evidence for mounting its standalone route on Codex.
func TestCodexStandaloneNotMounted(t *testing.T) {
	s := New(t)
	res := postResponses(t, s, Codex, "/responses/compact", responseObject{"model": "gpt-5-codex", "instructions": "Compact", "input": []any{}})
	readResponse(t, res)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected Codex compact route: %s", res.Status)
	}
}

func TestResponsesCustomToolAndFunction(t *testing.T) {
	parts := []string{"*** Begin Patch\n", "", "*** Add File: notes.txt\n+λ\n", "*** End Patch"}
	s := New(t)
	s.Script(Turn{}.WithToolArgumentDeltas("call_custom", "apply_patch", parts...).WithToolArgumentDeltas("call_fn", "other", `{`, `}`), Text("ok"))
	req := testInput("Add notes, then check them.")
	req["stream"] = true
	req["tools"] = []any{responseObject{"type": "custom", "name": "apply_patch", "description": "Apply a patch", "format": responseObject{"type": "text"}},
		responseObject{"type": "function", "name": "other", "parameters": responseObject{"type": "object"}, "strict": false}}
	events := completeEvents(t, postResponses(t, s, Responses, "/responses", req), false)
	output := lastResponse(events)["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("output: %v", output)
	}
	want := strings.Join(parts, "")
	item := output[0].(responseObject)
	if item["type"] != "custom_tool_call" || item["name"] != "apply_patch" || item["input"] != want || item["call_id"] != "call_custom" || output[1].(responseObject)["type"] != "function_call" {
		t.Fatal(output)
	}
	var fragments, function []string
	var inputDone bool
	for _, e := range events {
		switch e["type"] {
		case "response.custom_tool_call_input.delta":
			if numField(t, e, "output_index") != 0 || e["item_id"] != item["id"] {
				t.Fatal(e)
			}
			fragments = append(fragments, e["delta"].(string))
		case "response.custom_tool_call_input.done":
			inputDone = e["input"] == want && e["item_id"] == item["id"]
		case "response.function_call_arguments.delta":
			function = append(function, e["delta"].(string))
		}
	}
	if !inputDone || !slices.Equal(fragments, parts) || !slices.Equal(function, []string{"{", "}"}) {
		t.Fatalf("wrong input fragments or done: %v %q %q", inputDone, fragments, function)
	}
	replay := testInput(append(output, responseObject{"type": "custom_tool_call_output", "call_id": "call_custom", "output": "applied"},
		responseObject{"type": "function_call_output", "call_id": "call_fn", "output": "checked"}))
	responseJSON(t, postResponses(t, s, Responses, "/responses", replay), 200, "Response")
	last, _ := s.Last()
	if p := last.Effective.Messages[0].Parts[0]; p.Call.ID != "call_custom" || p.Call.Arguments != want || last.Effective.Messages[2].Parts[0].Text != "applied" {
		t.Fatal(last.Effective.Messages)
	}
}
