package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crude/internal/providersim"
)

func continuationFixture(t *testing.T, endpoint string) (*Provider, ChatRequest) {
	t.Helper()
	p := FromPreset("openai-chat", "key", endpoint)
	if err := p.SetProtocolID("openai-responses"); err != nil {
		t.Fatal(err)
	}
	p.Model = "continuation-model"
	// Learning is intentionally retained across clients and test repetitions.
	// Give this fixture its own connection identity instead of clearing that state.
	p.AccountID = t.TempDir()
	req := ChatRequest{Model: p.Model, ResponseContinuationScope: p.ResponseContinuationScope(p.Model),
		Messages: []ChatMessage{NewTextMessage("system", strings.Repeat("stable policy\n", 8000)), NewTextMessage("user", "inspect")}}
	message := ChatMessage{Role: "assistant", Content: TextContent(""), ToolCalls: []ToolCall{{ID: "patch", Type: "custom", Function: FunctionCall{Name: "apply_patch", Arguments: "patch"}}}}
	BindResponseReference(&message, &ResponseReference{ID: "response-1", Scope: req.ResponseContinuationScope, InputHash: MessagePrefixHash(req.Messages)})
	req.Messages = append(req.Messages, message, ChatMessage{Role: "tool", ToolCallID: "patch", Content: TextContent("written")})
	return p, req
}

func inlineMediaContinuationFixture(t *testing.T, endpoint string) (*Provider, ChatRequest) {
	t.Helper()
	p, req := continuationFixture(t, endpoint)
	req.Messages[1] = NewMultiPartMessage("user", []ContentPart{
		{Type: "text", Text: "inspect"},
		{Type: "image_url", ImageURL: &ImageURL{URL: testPNGDataURL()}},
	})
	BindResponseReference(&req.Messages[2], &ResponseReference{
		ID: "response-1", Scope: req.ResponseContinuationScope,
		InputHash: MessagePrefixHash(req.Messages[:2]),
	})
	req.MediaReplayRecovery = p.MediaReplayRecoveryEnabled()
	return p, req
}

func testPNGDataURL() string {
	header := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0, 'I', 'H', 'D', 'R'}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(header)
}

func TestResponsesContinuationSendsOnlyDeltaWithoutChangingHistory(t *testing.T) {
	_, req := continuationFixture(t, "https://api.openai.com/v1")
	before, err := json.Marshal(req.Messages)
	if err != nil {
		t.Fatal(err)
	}
	body, err := (&OpenAIResponsesStandard{}).EncodeRequest(&req)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Previous string `json:"previous_response_id"`
		Input    []struct {
			Type, Output string
			CallID       string `json:"call_id"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Previous != "response-1" || len(wire.Input) != 1 || wire.Input[0].Type != "custom_tool_call_output" || wire.Input[0].CallID != "patch" || wire.Input[0].Output != "written" {
		t.Fatalf("incorrect delta: previous=%q items=%d bytes=%d", wire.Previous, len(wire.Input), len(body))
	}
	full := req
	full.ResponseContinuationScope = ""
	fullBody, err := (&OpenAIResponsesStandard{}).EncodeRequest(&full)
	if err != nil {
		t.Fatal(err)
	}
	if len(body)*100 >= len(fullBody) {
		t.Fatalf("upload reduction below 99%%: full=%d delta=%d", len(fullBody), len(body))
	}
	after, err := json.Marshal(req.Messages)
	if err != nil || string(before) != string(after) {
		t.Fatal("delta encoding mutated canonical history")
	}
}

func TestResponsesContinuationMediaReplayLearningIsAdaptive(t *testing.T) {
	_, req := inlineMediaContinuationFixture(t, "https://media-replay-learning.invalid/v1")
	if plan := PlanResponseContinuation(&req); plan.PreviousResponseID != "response-1" || plan.Reason != "delta" {
		t.Fatalf("unproven connection did not use documented continuation: %+v", plan)
	}
	if err := RecordResponseMediaReplayLimitation(req.ResponseContinuationScope); err != nil {
		t.Fatal(err)
	}
	if plan := PlanResponseContinuation(&req); plan.PreviousResponseID != "" || plan.Start != 0 || plan.Reason != "media_replay_limitation_learned" {
		t.Fatalf("learned media limitation was ignored: %+v", plan)
	}
	_, neighbour := inlineMediaContinuationFixture(t, "https://media-replay-learning.invalid/v1")
	if plan := PlanResponseContinuation(&neighbour); plan.PreviousResponseID != "response-1" || plan.Reason != "delta" {
		t.Fatalf("a different account inherited the learned media limitation: %+v", plan)
	}
	req.MediaReplayRecovery = false
	if plan := PlanResponseContinuation(&req); plan.PreviousResponseID != "response-1" || plan.Reason != "delta" {
		t.Fatalf("disabled safeguard still changed continuation: %+v", plan)
	}
}

func TestResponsesContinuationResetsOnExactContextOrConnectionChange(t *testing.T) {
	for _, change := range []string{"system", "output", "model", "account", "endpoint", "partial_after_changed_output", "compaction", "attachment"} {
		t.Run(change, func(t *testing.T) {
			p, req := continuationFixture(t, "https://api.openai.com/v1")
			switch change {
			case "system":
				req.Messages[0].Content = TextContent("new policy")
			case "output":
				req.Messages[2].ToolCalls[0].Function.Arguments = "changed patch"
			case "model":
				req.Model = "another-model"
				req.ResponseContinuationScope = p.ResponseContinuationScope(req.Model)
			case "account":
				p.AccountID = "account-b"
				req.ResponseContinuationScope = p.ResponseContinuationScope(req.Model)
			case "endpoint":
				p.BaseURL = "https://other.invalid/v1"
				req.ResponseContinuationScope = p.ResponseContinuationScope(req.Model)
			case "partial_after_changed_output":
				// Output without an ID never hides a changed anchor.
				req.Messages[2].ToolCalls[0].Function.Arguments = "changed patch"
				req.Messages = append(req.Messages, NewTextMessage("assistant", "unfinished"))
			case "compaction":
				req.Messages = req.Messages[1:]
			case "attachment":
				req.Messages[1] = NewMultiPartMessage("user", []ContentPart{{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,YQ=="}}})
				BindResponseReference(&req.Messages[2], &ResponseReference{ID: "response-1", Scope: req.ResponseContinuationScope, InputHash: MessagePrefixHash(req.Messages[:2])})
				req.Messages[1] = NewMultiPartMessage("user", []ContentPart{{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,Yg=="}}})
			}
			if previous, start := responseContinuation(&req); previous != "" || start != 0 {
				t.Fatalf("unsafe %s reused %s", change, previous)
			}
		})
	}
}

// A response cut by a stream interruption or a stop has no stored ID. Its
// accepted text and the continuation request follow the latest completed
// response in the delta; they never force a whole-history upload.
func TestResponsesContinuationKeepsAnchorAfterPartialOutput(t *testing.T) {
	_, req := continuationFixture(t, "https://api.openai.com/v1")
	req.Messages = append(req.Messages,
		NewTextMessage("assistant", "partial answer before the cut"),
		NewTextMessage("user", "Continue from the partial answer."))
	if plan := PlanResponseContinuation(&req); plan.PreviousResponseID != "response-1" || plan.Start != 3 || plan.Reason != "delta" {
		t.Fatalf("partial output dropped the completed anchor: %+v", plan)
	}
	body, err := (&OpenAIResponsesStandard{}).EncodeRequest(&req)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Previous string `json:"previous_response_id"`
		Input    []struct {
			Type, Role, CallID string
			Content            json.RawMessage
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Previous != "response-1" || len(wire.Input) != 3 || wire.Input[0].Type != "custom_tool_call_output" ||
		wire.Input[1].Role != "assistant" || wire.Input[2].Role != "user" ||
		strings.Count(string(body), "partial answer before the cut") != 1 || strings.Contains(string(body), "stable policy") {
		t.Fatalf("delta after the cut = %s", body)
	}
	// Output without any stored ID cannot anchor a delta.
	_, cut := continuationFixture(t, "https://api.openai.com/v1")
	cut.Messages = append(cut.Messages[:2:2], NewTextMessage("assistant", "partial answer before the cut"))
	if plan := PlanResponseContinuation(&cut); plan.PreviousResponseID != "" || plan.Reason != "response_id_unavailable" {
		t.Fatalf("a cut response became an anchor: %+v", plan)
	}
}

func TestResponseContinuationRespectsStorageAndProtocol(t *testing.T) {
	p, _ := continuationFixture(t, "https://api.openai.com/v1")
	off, on := false, true
	for _, settings := range []*ProtocolSettings{{Store: &off}, {ZeroDataRetention: true, Store: &on}, {Truncation: &on}} {
		p.SetProtocolSettings(settings)
		if p.ResponseContinuationScope(p.Model) != "" {
			t.Fatal("retention/truncation policy was bypassed")
		}
	}
	p.SetProtocolSettings(&ProtocolSettings{ZeroDataRetention: true, Store: &on})
	p.SetExtraBody(map[string]any{"store": true})
	client, err := p.Client()
	if err != nil {
		t.Fatal(err)
	}
	body, err := client.encodeChatRequest(&ChatRequest{Model: p.Model})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Store bool `json:"store"`
	}
	if err := json.Unmarshal(body, &wire); err != nil || wire.Store {
		t.Fatal("generic extra body overrode zero data retention")
	}
	p.SetExtraBody(nil)
	p.SetProtocolSettings(nil)
	for _, protocol := range []string{"openai", "codex", "anthropic"} {
		if err := p.SetProtocolID(protocol); err != nil {
			t.Fatal(err)
		}
		if p.ResponseContinuationScope(p.Model) != "" {
			t.Fatal("unsupported protocol got a stored-response reference")
		}
	}
}

func TestResponsesContinuationOnlyAdoptsCompletedStoredResponses(t *testing.T) {
	for _, state := range []string{`"status":"completed","store":true`, `"status":"completed","store":false`, `"status":"incomplete","store":true`} {
		payload := fmt.Sprintf(`{"id":"r",%s,"output":[]}`, state)
		response, err := (&OpenAIResponsesStandard{}).DecodeResponse([]byte(payload))
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Contains(state, `"completed"`) && strings.Contains(state, `"store":true`)
		if (response.ContinuationID != "") != want {
			t.Fatalf("invalid completion anchor: %s", payload)
		}
	}
}

func TestResponsesContinuationTracksOfficialStreamingLifecycle(t *testing.T) {
	sim := providersim.New(t)
	sim.Script(providersim.Text("done"))
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, sim.BaseURL(providersim.Responses)+"/responses", strings.NewReader(`{"model":"m","stream":true,"store":true,"input":"question"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := NewSSEReader(resp.Body)
	decoder := (&OpenAIResponsesStandard{}).NewStreamDecoder()
	var created string
	for i := 0; ; i++ {
		event, err := reader.Next()
		if err != nil {
			t.Fatalf("no terminal lifecycle at frame %d: %v", i, err)
		}
		if i < 2 {
			want := []string{"response.created", "response.in_progress"}[i]
			if event.Event != want {
				t.Fatalf("frame %d: %q want %q", i, event.Event, want)
			}
			var v struct{ Response struct{ ID string } }
			if err := json.Unmarshal(event.Data, &v); err != nil || v.Response.ID == "" || (i == 1 && created != v.Response.ID) {
				t.Fatalf("lifecycle ID %q after %q: %v", v.Response.ID, created, err)
			}
			created = v.Response.ID
		}
		update, err := decoder.Decode(event)
		if err != nil {
			t.Fatal(err)
		}
		if event.Event == "response.completed" {
			if !update.Done || update.ContinuationID != created || update.ContinuationReason != "response_id_retained" {
				t.Fatalf("terminal event did not retain its ID %q: %+v", created, update)
			}
			break
		}
		if update.ContinuationID != "" || update.Done {
			t.Fatalf("nonterminal lifecycle event was not passive: %+v", update)
		}
	}
}

func TestResponsesContinuationUsesCreatedIDWhenGatewayOmitsTerminalDuplicate(t *testing.T) {
	decoder := (&OpenAIResponsesStandard{}).NewStreamDecoder()
	if _, err := decoder.Decode(StreamEvent{Data: []byte(`{"type":"response.created","response":{"id":"resp-created","status":"in_progress","store":true,"output":[]}}`)}); err != nil {
		t.Fatal(err)
	}
	completed, err := decoder.Decode(StreamEvent{Data: []byte(`{"type":"response.completed","response":{"status":"completed","output":[]}}`)})
	if err != nil || completed.ContinuationID != "resp-created" {
		t.Fatalf("creation-event authority was lost: %+v err=%v", completed, err)
	}

	decoder = (&OpenAIResponsesStandard{}).NewStreamDecoder()
	if _, err := decoder.Decode(StreamEvent{Data: []byte(`{"type":"response.created","response":{"id":"first","status":"in_progress","store":true,"output":[]}}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := decoder.Decode(StreamEvent{Data: []byte(`{"type":"response.completed","response":{"id":"different","status":"completed","store":true,"output":[]}}`)}); err == nil {
		t.Fatal("conflicting lifecycle response IDs were accepted")
	}
}

func TestResponsesContinuationReportsWhyNoIDCanBeRetained(t *testing.T) {
	for name, wire := range map[string]string{
		"missing id": `{"type":"response.completed","response":{"status":"completed","store":true,"output":[]}}`,
		"not stored": `{"type":"response.completed","response":{"id":"response","status":"completed","store":false,"output":[]}}`,
		"incomplete": `{"type":"response.incomplete","response":{"id":"response","status":"incomplete","store":true,"output":[]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			update, err := (&OpenAIResponsesStandard{}).NewStreamDecoder().Decode(StreamEvent{Data: []byte(wire)})
			if err != nil || update.ContinuationID != "" || update.ContinuationReason == "" {
				t.Fatalf("silent continuation fallback: %+v err=%v", update, err)
			}
		})
	}
}

// Start from an actual stored patch call. The tool result is authoritative
// local history; neither an unavailable ID nor a gateway that fails to replay
// a stored image may drop it or execute the patch a second time.
func storedToolContinuation(t *testing.T, sim *providersim.Server, media bool) (*Provider, ChatRequest) {
	t.Helper()
	fixture := continuationFixture
	if media {
		fixture = inlineMediaContinuationFixture
	}
	p, req := fixture(t, sim.BaseURL(providersim.Responses))
	client, err := p.Client(WithMaxAttempts(1))
	if err != nil {
		t.Fatal(err)
	}
	sim.Script(providersim.CallTools(providersim.ToolCall{ID: "patch", Name: "apply_patch", Arguments: "patch"}))
	first := req
	first.Messages = req.Messages[:2]
	first.Tools = []ToolDef{{Type: "custom", Function: FunctionDef{Name: "apply_patch"}, Format: &ToolFormat{Type: "grammar", Syntax: "lark", Definition: `start: "patch"`}}}
	result, err := client.Complete(t.Context(), &first)
	if err != nil || len(result.Choices) != 1 || result.Choices[0].Message.ResponseReference == nil {
		t.Fatalf("could not store tool reply: %+v %v", result, err)
	}
	msg := result.Choices[0].Message
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "patch" || msg.ToolCalls[0].Type != "custom" {
		t.Fatalf("stored the wrong tool: %+v", msg.ToolCalls)
	}
	req.Messages[2] = msg
	return p, req
}

const rejectedImageReplay = "Invalid 'input[1].content[1].image_url'. Expected a base64-encoded data URL with an image MIME type, but got an invalid base64-encoded value."

func recoverContinuation(t *testing.T, streaming, media bool) {
	t.Helper()
	sim := providersim.New(t)
	p, req := storedToolContinuation(t, sim, media)
	previous := req.Messages[2].ResponseReference.ID
	if media {
		sim.Script(providersim.HTTPError(providersim.InvalidRequest).WithFaultMessage(rejectedImageReplay))
	} else if !sim.ExpireResponse(previous) {
		t.Fatal("could not expire the stored tool reply")
	}
	sim.Script(providersim.Text("done"))
	client, err := p.Client(WithMaxAttempts(1))
	if err != nil {
		t.Fatal(err)
	}
	var content strings.Builder
	var id string
	if streaming {
		stream, err := client.Stream(t.Context(), &req)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		for {
			u, err := stream.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			content.WriteString(u.ContentDelta)
			if u.ContinuationID != "" {
				id = u.ContinuationID
			}
		}
	} else {
		response, err := client.Complete(t.Context(), &req)
		if err != nil || len(response.Choices) != 1 || response.Choices[0].Message.ResponseReference == nil {
			t.Fatalf("recovery: %+v %v", response, err)
		}
		msg := response.Choices[0].Message
		content.WriteString(msg.ContentString())
		id = msg.ResponseReference.ID
	}
	if id == "" || id == previous || content.String() != "done" {
		t.Fatalf("recovered text=%q id=%q", content.String(), id)
	}
	calls := sim.Requests()
	if len(calls) != 3 || req.ResponseContinuationScope == "" {
		t.Fatalf("recovery did not stay bounded and request-owned: %d calls including the seed", len(calls))
	}
	for _, r := range calls {
		if r.Method != "POST" || r.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer key" {
			t.Error("wrong continuation route or credential")
		}
	}
	first := calls[1]
	if first.PreviousResponseID != previous || len(first.Messages) != 1 || len(first.Messages[0].Parts) != 1 || first.Messages[0].Parts[0].Kind != providersim.PartToolResult || first.Messages[0].Parts[0].CallID != "patch" || first.Messages[0].Parts[0].Text != "written" {
		t.Error("initial request was not the exact tool-result delta")
	}
	retry := calls[2]
	var wire struct {
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(retry.Raw, &wire); err != nil || retry.PreviousResponseID != "" || len(wire.Input) != 4 || retry.Stream != streaming {
		t.Fatalf("recovery omitted full authoritative context: %d items, previous=%q err=%v", len(wire.Input), retry.PreviousResponseID, err)
	}
	// Check the server, not just the spelling of an ID invented by the client.
	lookup, err := http.NewRequestWithContext(t.Context(), http.MethodGet, sim.BaseURL(providersim.Responses)+"/responses/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := http.DefaultClient.Do(lookup)
	if err != nil {
		t.Fatal(err)
	}
	defer stored.Body.Close()
	if stored.StatusCode != http.StatusOK {
		t.Fatalf("recovered ID cannot be retrieved: HTTP %d", stored.StatusCode)
	}
	if media {
		var user struct {
			Content []struct {
				Type     string
				ImageURL string `json:"image_url"`
			}
		}
		if err := json.Unmarshal(wire.Input[1], &user); err != nil || len(user.Content) != 2 || user.Content[1].Type != "input_image" || user.Content[1].ImageURL != testPNGDataURL() {
			t.Fatalf("authoritative retry lost image: %#v (%v)", user.Content, err)
		}
		if plan := PlanResponseContinuation(&req); plan.Reason != "media_replay_limitation_learned" || plan.PreviousResponseID != "" {
			t.Fatalf("connection limitation was not learned: %+v", plan)
		}
	}
}

func TestResponsesContinuationRecoversMissingReferenceOnce(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint(streaming), func(t *testing.T) { recoverContinuation(t, streaming, false) })
	}
}

func TestResponsesContinuationRecoversInlineMediaReplayAndLearns(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint(streaming), func(t *testing.T) { recoverContinuation(t, streaming, true) })
	}
}

func TestResponsesContinuationMediaReplayRecoveryCanBeDisabled(t *testing.T) {
	sim := providersim.New(t)
	p, req := storedToolContinuation(t, sim, true)
	sim.Script(providersim.HTTPError(providersim.InvalidRequest).WithFaultMessage(rejectedImageReplay))
	off := false
	p.SetProtocolSettings(&ProtocolSettings{MediaReplayRecovery: &off})
	client, err := p.Client(WithMaxAttempts(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(t.Context(), &req); err == nil {
		t.Fatal("disabled recovery accepted the gateway error")
	}
	if requests := sim.Requests(); len(requests) != 2 || requests[1].PreviousResponseID != req.Messages[2].ResponseReference.ID {
		t.Fatalf("disabled recovery changed or repeated its initial delta (%d calls incl. seed)", len(requests))
	}
}

func TestContinuationMediaReplayRecoveryRequiresExactEvidence(t *testing.T) {
	_, req := inlineMediaContinuationFixture(t, "https://media-replay-evidence.invalid/v1")
	plan := planResponseContinuation(&req, false)
	exact := &StatusError{
		StatusCode: http.StatusBadRequest,
		Message:    "Invalid 'input[1].content[1].image_url'. Expected a base64-encoded data URL, but got an invalid base64-encoded value.",
	}
	for name, failure := range map[string]error{
		"exact":          exact,
		"wrong status":   &StatusError{StatusCode: http.StatusTooManyRequests, Message: exact.Message},
		"unrelated":      &StatusError{StatusCode: http.StatusBadRequest, Message: "invalid model"},
		"unstructured":   &StatusError{StatusCode: http.StatusBadRequest, Message: "base64 failed"},
		"wrong API type": &APIError{Type: "permission_error", Message: exact.Message},
	} {
		want := name == "exact"
		if got := continuationMediaReplayRejected(&req, plan, failure); got != want {
			t.Errorf("%s recovery evidence=%v want=%v", name, got, want)
		}
	}

	remote := req
	remote.Messages = append([]ChatMessage(nil), req.Messages...)
	remote.Messages[1] = NewMultiPartMessage("user", []ContentPart{{
		Type: "image_url", ImageURL: &ImageURL{URL: "https://example.com/image.png"},
	}})
	BindResponseReference(&remote.Messages[2], &ResponseReference{
		ID: "response-1", Scope: remote.ResponseContinuationScope,
		InputHash: MessagePrefixHash(remote.Messages[:2]),
	})
	if continuationMediaReplayRejected(&remote, planResponseContinuation(&remote, false), exact) {
		t.Fatal("remote image URL triggered inline media recovery")
	}

	invalid := req
	invalid.Messages = append([]ChatMessage(nil), req.Messages...)
	invalid.Messages[1] = NewMultiPartMessage("user", []ContentPart{{
		Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,not-base64"},
	}})
	BindResponseReference(&invalid.Messages[2], &ResponseReference{
		ID: "response-1", Scope: invalid.ResponseContinuationScope,
		InputHash: MessagePrefixHash(invalid.Messages[:2]),
	})
	if continuationMediaReplayRejected(&invalid, planResponseContinuation(&invalid, false), exact) {
		t.Fatal("malformed local image triggered provider replay recovery")
	}
}

func TestMediaReplayRecoveryNeverReplaysAcceptedOutput(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"Invalid 'input[1].content[1].image_url'. Expected a base64-encoded data URL, but got an invalid base64-encoded value.\"}}\n\n")
	}))
	defer server.Close()

	p, req := inlineMediaContinuationFixture(t, server.URL)
	client, err := p.Client(WithMaxAttempts(1))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := client.Stream(t.Context(), &req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	first, err := stream.Next()
	if err != nil || first.ContentDelta != "partial" {
		t.Fatalf("first update=%+v err=%v", first, err)
	}
	if _, err := stream.Next(); err == nil {
		t.Fatal("media replay error after output was hidden")
	}
	if requests != 1 {
		t.Fatalf("accepted output was replayed: requests=%d", requests)
	}
}

func TestContinuationUnavailableRequiresStructuredReferenceEvidence(t *testing.T) {
	for _, err := range []error{
		&StatusError{StatusCode: http.StatusNotFound, Param: "previous_response_id"},
		&StatusError{StatusCode: http.StatusBadRequest, APICode: "previous_response_not_found"},
		&APIError{Code: "previous_response_not_found"},
		&APIError{Param: "previous_response_id"},
	} {
		if !continuationUnavailable(err) {
			t.Errorf("structured rejected reference was not recoverable: %+v", err)
		}
	}
	for _, err := range []error{
		&StatusError{StatusCode: http.StatusNotFound, Message: "previous response not found"},
		&StatusError{StatusCode: http.StatusBadRequest, Param: "model"},
		&APIError{Message: "previous_response_id unavailable"},
	} {
		if continuationUnavailable(err) {
			t.Errorf("unstructured or unrelated error triggered recovery: %+v", err)
		}
	}
}

func TestResponsesContinuationNeverReplaysAcceptedOutputOrCancellation(t *testing.T) {
	for _, scenario := range []string{"partial", "cancelled", "unrelated-error", "repeated-reference-error"} {
		t.Run(scenario, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if scenario == "partial" {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\ndata: {\"type\":\"error\",\"error\":{\"param\":\"previous_response_id\",\"message\":\"expired\"}}\n\n")
				} else {
					w.WriteHeader(http.StatusBadRequest)
					param := "previous_response_id"
					if scenario == "unrelated-error" {
						param = "model"
					}
					_, _ = fmt.Fprintf(w, `{"error":{"param":%q,"message":"not usable"}}`, param)
				}
			}))
			defer server.Close()
			p, req := continuationFixture(t, server.URL)
			client, err := p.Client(WithMaxAttempts(1))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			stream, err := client.Stream(ctx, &req)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			for err == nil {
				_, err = stream.Next()
			}
			if err == io.EOF {
				t.Fatal("invalid request was accepted")
			}
			want := 1
			if scenario == "cancelled" {
				want = 0
			}
			if scenario == "repeated-reference-error" {
				want = 2
			}
			if requests != want {
				t.Fatalf("unsafe replay: requests=%d want=%d", requests, want)
			}
		})
	}
}

// The provider already has the screenshot. Changing this request's callable
// tools must neither upload that screenshot again nor retain a revoked tool.
func TestResponsesToolChangeKeepsStoredImageHistory(t *testing.T) {
	_, req := inlineMediaContinuationFixture(t, "https://example.test/v1")
	req.Tools = []ToolDef{{Type: "function", Function: FunctionDef{Name: "new_tool", Parameters: json.RawMessage(`{"type":"object"}`)}}}
	body, err := (&OpenAIResponsesStandard{}).EncodeRequest(&req)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Previous string                  `json:"previous_response_id"`
		Input    []json.RawMessage       `json:"input"`
		Tools    []struct{ Name string } `json:"tools"`
	}
	if err = json.Unmarshal(body, &wire); err != nil || wire.Previous != "response-1" || len(wire.Input) != 1 || strings.Contains(string(body), "base64,") || len(wire.Tools) != 1 || wire.Tools[0].Name != "new_tool" {
		t.Fatalf("tool change resent history or lost current selection: previous=%q input=%d tools=%v err=%v", wire.Previous, len(wire.Input), wire.Tools, err)
	}
}
