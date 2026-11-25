package providersim

import (
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
)

func init() {
	registerWire(Responses, func(s *Server) http.Handler {
		state := newResponseState(s, Responses)
		s.responses = state
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/responses", state.create)
		mux.HandleFunc("POST /v1/responses/compact", state.create)
		mux.HandleFunc("GET /v1/responses/{id}", state.retrieve)
		mux.HandleFunc("GET /v1/models", state.listModels)
		return mux
	})
}

func (s *responseState) listModels(w http.ResponseWriter, r *http.Request) {
	req, err := receive(s.wire, r)
	if err != nil {
		s.reject(w, req, "", "", err)
		return
	}
	s.server.observe(req)
	models, ok := s.server.inventory(w, r, s.wire, "gpt-4o-mini")
	if !ok {
		return
	}
	list := make([]responseObject, 0, len(models))
	for _, model := range models {
		list = append(list, responseObject{"id": model.ID, "object": "model", "created": chatSyntheticCreated, "owned_by": "openai"})
	}
	writeJSON(w, 200, responseObject{"object": "list", "data": list})
}

func (s *responseState) retrieve(w http.ResponseWriter, r *http.Request) {
	req, err := receive(s.wire, r)
	if err != nil {
		s.reject(w, req, "", "", err)
		return
	}
	s.server.observe(req)
	if row, _ := s.lookup(r.PathValue("id")); row.body != nil {
		writeJSON(w, 200, row.body)
		return
	}
	writeJSON(w, 404, chatError("invalid_request_error", "", nil, "Response not found."))
}

// Synthetic error messages and HTTP fault selection; public error envelopes
// use ErrorResponse, distinct from both stream error forms.
func (s *responseState) reject(w http.ResponseWriter, req Request, field, code string, err error) {
	req.Violation = err.Error()
	s.server.observe(req)
	s.server.report("simulate %s %s: %s", req.Wire, req.Path, req.Violation)
	var param any
	if field != "" {
		param = field
	}
	writeJSON(w, 400, chatError("invalid_request_error", code, param, err.Error()))
}

func (s *responseState) create(w http.ResponseWriter, r *http.Request) {
	req, err := receive(s.wire, r)
	if err != nil {
		s.reject(w, req, "", "", err)
		return
	}
	value, err := decodeJSON(req.Raw)
	if err != nil {
		s.reject(w, req, "", "", err)
		return
	}
	obj, ok := value.(responseObject)
	if !ok {
		s.reject(w, req, "", "", fmt.Errorf("/: expected an object"))
		return
	}
	b, err := wireBundle(s.wire)
	if err != nil {
		s.server.report("simulate Responses: %v", err)
		writeJSON(w, 500, chatError("server_error", "", nil, "Responses schema unavailable."))
		return
	}
	compact := req.Path == "/v1/responses/compact"
	component := "CreateResponse"
	if compact {
		component = "CompactResponseMethodPublicBody"
	}
	var field string
	if s.wire == Codex {
		field, err = validateCodex(b, obj)
	} else {
		field, err = validateResponse(b, component, obj)
	}
	if err != nil {
		s.reject(w, req, field, "", err)
		return
	}
	items := decodeResponse(&req, obj)
	// https://developers.openai.com/api/docs/guides/compaction#request-compaction-with-an-explicit-trigger
	// The marker is a final input on response creation, never transcript text.
	trigger := false
	for i, item := range items {
		if item["type"] == "compaction_trigger" {
			if compact || i != len(items)-1 {
				s.reject(w, req, "input", "", fmt.Errorf("/input/%d: supply a compaction_trigger only as the last item of a Responses create request", i))
				return
			}
			trigger = true
		}
	}
	if _, hasPrevious := obj["previous_response_id"].(string); hasPrevious {
		row, known := s.lookup(req.PreviousResponseID)
		if row.body == nil {
			const field, code = "previous_response_id", "previous_response_not_found"
			err := fmt.Errorf("/previous_response_id: previous response %q not found", req.PreviousResponseID)
			if known {
				// The provider expired a real stored ID. A well-formed client
				// can encounter this state; no script or prefix is consumed.
				s.server.observe(req)
				writeJSON(w, 400, chatError("invalid_request_error", code, field, err.Error()))
			} else {
				s.reject(w, req, field, code, err)
			}
			return
		}
		items = slices.Concat(row.window, items)
	}
	window, err := s.resolve(items)
	if err != nil {
		s.reject(w, req, "input", "invalid_encrypted_content", err)
		return
	}
	req.Effective = projectResponse(obj, window.effective)
	// Synthetic classic implicit cache profile. The private Codex service does
	// not specify thresholds; configure a profile explicitly to simulate hits.
	profile := CacheProfile{}
	if s.wire == Responses {
		profile = CacheProfile{Minimum: 1024, Granularity: 128}
	}
	turn, ok := s.server.dispatch(&req, responseBlocks(obj, window.rendered), profile)
	if !ok {
		writeJSON(w, 500, chatError("server_error", "", nil, "No reply is scripted."))
		return
	}
	if turn.fault.mode == faultHTTP {
		if !s.server.awaitBody(r, turn) {
			return
		}
		status, payload := responseHTTPFault(turn.fault)
		if h := turn.fault.hint; h.header != "" {
			w.Header().Set(h.header, h.value)
		}
		writeJSON(w, status, payload)
		return
	}
	if !req.Stream && (turn.fault.mode == faultStream || turn.fault.mode == faultCut) {
		s.server.report("simulate %s: use a streaming request for a stream fault", s.wire)
		writeJSON(w, 500, chatError("server_error", "", nil, "The scripted fault requires streaming."))
		return
	}
	if !req.Stream && !s.server.awaitBody(r, turn) {
		return
	}
	if compact && turn.omitUsage || turn.Usage != nil && turn.Usage.OmitCacheRead {
		// CompactResource requires usage; ResponseUsage requires both
		// cached_tokens and cache_write_tokens when usage is present.
		msg := "Response usage requires input_tokens_details.cached_tokens."
		if compact && turn.omitUsage {
			msg = "A compaction response requires usage."
		}
		s.server.report("simulate %s: %s", s.wire, msg)
		writeJSON(w, 500, chatError("server_error", "", nil, msg))
		return
	}
	if compact {
		writeJSON(w, 200, responseObject{"id": fmt.Sprintf("cmpresp_sim_%d", req.Index), "created_at": chatSyntheticCreated, "usage": responseUsage(req, turn, true), "object": "response.compaction", "output": s.compact(req.Index, window.originals)})
		return
	}
	output := make([]responseObject, 0)
	if trigger || responseThreshold(obj, req.Prefix.InputTokens) {
		output = append(output, s.seal(fmt.Sprintf("cmp_sim_%d", req.Index), window.originals))
	}
	if !trigger {
		output = append(output, responseItems(req, obj, turn)...)
	}
	body := responseEnvelope(req, obj)
	if turn.omitUsage {
		delete(body, "usage")
	}
	final := maps.Clone(body)
	final["status"] = "completed"
	final["completed_at"] = chatSyntheticCreated
	if !turn.omitUsage {
		final["usage"] = responseUsage(req, turn, trigger)
	}
	final["output"] = output
	if f := turn.finish(); f == FinishFiltered || f == FinishLength {
		reason := "max_output_tokens"
		if f == FinishFiltered {
			reason = "content_filter"
		}
		final["status"] = "incomplete"
		final["incomplete_details"] = responseObject{"reason": reason}
	}
	save := func() {
		if s.wire == Responses && (req.Store == nil || *req.Store) {
			s.save(body["id"].(string), final, slices.Concat(window.rendered, output))
		}
	}
	if !req.Stream {
		save()
		writeJSON(w, 200, final)
		return
	}
	deltas := turn.textDeltas
	if s.wire == Codex {
		deltas = turn.mergedDeltas()
	}
	events := responseFrames(body, output, deltas, turn.toolDeltas)
	terminal := responseFrame(len(events), "response."+final["status"].(string), responseObject{"response": final})
	codec := streamCodec{contentType: "text/event-stream", terminal: []frame{terminal}, write: func(w io.Writer, f frame) error {
		if f.event == terminal.event {
			save()
		}
		return writeSSE(w, f)
	}, encodeFault: func(f fault, n int) frame { return responseStreamFault(s.wire, body, f, n) }}
	s.server.sendStream(w, r, turn, codec, slices.Values(events))
}

func responseHTTPFault(f fault) (int, responseObject) {
	status, body := chatFault(f)
	if f.kind == ContextOverflow {
		body["error"].(map[string]any)["param"] = "input"
	}
	return status, body
}

func responseStreamFault(wire Wire, base responseObject, f fault, n int) frame {
	_, body := responseHTTPFault(f)
	detail := body["error"].(responseObject)
	// Synthetic fault selection: use top-level error for codes outside the
	// public ResponseErrorCode enum, and server_error for public overload.
	if wire == Responses {
		switch f.kind {
		case Auth, ContextOverflow, ModelNotFound, Quota, InvalidRequest, RequestTimeout:
			return responseFrame(n, "error", responseObject{"code": detail["code"], "message": detail["message"], "param": detail["param"]})
		}
	}
	code := detail["code"]
	// Codex accepts any optional string code on response.failed:
	// https://github.com/openai/codex/blob/ebd0765/codex-rs/codex-api/src/sse/responses_error.rs.
	// Use the generic request type as its synthetic code for a generic 400.
	if wire == Codex && f.kind == InvalidRequest {
		code = detail["type"]
	}
	if wire == Responses && f.kind == Overload {
		code = "server_error"
	}
	e := responseObject{"code": code, "message": detail["message"]}
	// Codex sse/responses_error.rs recognizes nested headers on response.failed.
	if hint := f.hint; wire == Codex && hint.header != "" {
		e["headers"] = responseObject{hint.header: hint.value}
	}
	failed := maps.Clone(base)
	failed["status"] = "failed"
	failed["error"] = e
	return responseFrame(n, "response.failed", responseObject{"response": failed})
}

func responseUsage(req Request, t Turn, compact bool) responseObject {
	u := Usage{Input: req.Prefix.InputTokens, Cached: req.Prefix.CachedTokens, CacheWrite: req.Prefix.CacheWriteTokens}
	if !compact {
		u.Reasoning = estimateTokens(t.Reasoning.Text)
		u.Output = u.Reasoning + estimateTokens(t.Text) + estimateTokens(t.Refusal)
		for _, call := range t.Tools {
			u.Output += estimateTokens(call.Name) + estimateTokens(call.Arguments)
		}
	}
	if t.Usage != nil {
		u = *t.Usage
	}
	return responseObject{
		"input_tokens": u.Input, "output_tokens": u.Output, "total_tokens": u.Input + u.Output,
		"input_tokens_details":  responseObject{"cached_tokens": u.Cached, "cache_write_tokens": u.CacheWrite},
		"output_tokens_details": responseObject{"reasoning_tokens": u.Reasoning},
	}
}

func responseEnvelope(req Request, obj responseObject) responseObject {
	// Synthetic IDs, timestamp and default sampling parameters. Required fields
	// and nullability come from the bundled Response, not from a client parser.
	body := responseObject{
		"object": "response", "id": fmt.Sprintf("resp_sim_%d", req.Index), "status": "in_progress", "created_at": chatSyntheticCreated,
		"access_programs": nil, "usage": nil, "error": nil, "incomplete_details": nil, "output": []any{},
		"model": req.Model, "instructions": obj["instructions"], "tools": []any{}, "parallel_tool_calls": true,
		"metadata": responseObject{}, "tool_choice": "auto", "temperature": 1, "top_p": 1,
	}
	for _, key := range []string{"tools", "parallel_tool_calls", "metadata", "tool_choice", "temperature", "top_p", "previous_response_id", "max_output_tokens", "text", "reasoning"} {
		// Codex has its own private request controls (e.g. numeric effort).
		// Do not copy them into the public response envelope.
		if req.Wire == Responses && obj[key] != nil {
			body[key] = obj[key]
		}
	}
	return body
}

func responseItems(req Request, obj responseObject, t Turn) []responseObject {
	index := req.Index
	// Codex ContentItem has no refusal variant (protocol/models.rs). Synthetic
	// projection: return refusal text as model output, not a foreign content type.
	if req.Wire == Codex && t.Refusal != "" {
		t.Text += t.Refusal
		t.Refusal = ""
	}

	var out []responseObject
	status := "completed"
	if t.finish() == FinishLength || t.finish() == FinishFiltered {
		status = "incomplete"
	}
	if th := t.Reasoning; th.Text != "" || th.Encrypted != "" {
		item := responseObject{"type": "reasoning", "status": status, "id": fmt.Sprintf("rs_sim_%d", index), "summary": []any{}}
		if th.Encrypted != "" {
			item["encrypted_content"] = th.Encrypted
		}
		if th.Text != "" {
			cfg := jsonField[responseObject](obj, "reasoning")
			key, typ := "content", "reasoning_text"
			if (cfg["summary"] != nil && cfg["summary"] != "none") || cfg["generate_summary"] != nil {
				key, typ = "summary", "summary_text"
			}
			item[key] = []any{responseObject{"type": typ, "text": th.Text}}
		}
		out = append(out, item)
	}
	if t.Text != "" || len(t.textDeltas) > 0 || t.Refusal != "" || (len(out) == 0 && len(t.Tools) == 0) {
		parts := []any{}
		if t.Text != "" || len(t.textDeltas) > 0 || t.Refusal == "" {
			parts = append(parts, responseObject{"type": "output_text", "annotations": []any{}, "logprobs": []any{}, "text": t.Text})
		}
		if t.Refusal != "" {
			parts = append(parts, responseObject{"type": "refusal", "refusal": t.Refusal})
		}
		out = append(out, responseObject{"type": "message", "id": fmt.Sprintf("msg_sim_%d", index), "role": "assistant", "status": status, "content": parts})
	}
	for i, call := range t.Tools {
		id := call.ID
		if id == "" {
			id = fmt.Sprintf("call_sim_%d_%d", index, i)
		}
		// CustomToolCall (public OpenAPI) carries free-form input, not
		// function arguments. A custom input can also happen to be JSON:
		// dispatch on the declaration, never the name or input syntax.
		if slices.ContainsFunc(jsonField[[]any](obj, "tools"), func(raw any) bool {
			tool, _ := raw.(responseObject)
			return tool["type"] == "custom" && tool["name"] == call.Name
		}) {
			out = append(out, responseObject{"type": "custom_tool_call", "id": fmt.Sprintf("ctc_sim_%d_%d", index, i), "call_id": id, "name": call.Name, "input": call.Arguments})
			continue
		}
		out = append(out, responseObject{"type": "function_call", "status": status, "id": fmt.Sprintf("fc_sim_%d_%d", index, i), "call_id": id, "name": call.Name, "arguments": call.Arguments})
	}
	return out
}

// Typed event names are from openai-python@aff223d ResponseStreamEvent. The
// synthetic chunking uses one delta per nonempty part; indexes address the
// same item and part in the terminal Response and every intermediate event.
func responseFrame(n int, name string, fields responseObject) frame {
	fields["type"] = name
	fields["sequence_number"] = n
	return frame{event: name, data: jsonBytes(fields)}
}

func responseFrames(base responseObject, output []responseObject, textDeltas []string, toolDeltas map[int][]string) []frame {
	var frames []frame
	emit := func(name string, fields responseObject) {
		frames = append(frames, responseFrame(len(frames), "response."+name, fields))
	}
	emit("created", responseObject{"response": base})
	emit("in_progress", responseObject{"response": base})
	tool := 0
	for i, item := range output {
		fields := func() responseObject { return responseObject{"output_index": i, "item_id": item["id"]} }
		start := maps.Clone(item)
		if _, ok := item["status"]; ok {
			start["status"] = "in_progress"
		}
		for _, key := range []string{"content", "summary"} {
			if _, ok := item[key]; ok {
				start[key] = []any{}
			}
		}
		delete(start, "encrypted_content")
		switch item["type"] {
		case "function_call":
			start["arguments"] = ""
		case "custom_tool_call":
			start["input"] = ""
		case "compaction":
			start["encrypted_content"] = ""
		}
		emit("output_item.added", responseObject{"output_index": i, "item": start})
		switch item["type"] {
		case "compaction":
			emit("compaction.compacting", fields())
		case "function_call", "custom_tool_call":
			key, event := "arguments", "function_call_arguments"
			if item["type"] == "custom_tool_call" {
				key, event = "input", "custom_tool_call_input"
			}
			for _, part := range textChunks(toolDeltas[tool], item[key].(string)) {
				f := fields()
				f["delta"] = part
				emit(event+".delta", f)
			}
			tool++
			f := fields()
			f[key] = item[key]
			if key == "arguments" {
				f["name"] = item["name"]
			}
			emit(event+".done", f)
		default:
			for _, key := range []string{"summary", "content"} {
				for j, raw := range jsonField[[]any](item, key) {
					p := raw.(responseObject)
					typ := p["type"].(string)
					partEvent, indexKey, textKey, deltaEvent := "content_part", "content_index", "text", typ
					if typ == "summary_text" {
						partEvent, indexKey, deltaEvent = "reasoning_summary_part", "summary_index", "reasoning_summary_text"
					}
					if typ == "refusal" {
						textKey = "refusal"
					}
					partFields := func() responseObject { f := fields(); f[indexKey] = j; return f }
					f := partFields()
					empty := maps.Clone(p)
					empty[textKey] = ""
					f["part"] = empty
					emit(partEvent+".added", f)
					emitText := func(stage, k, text string) {
						f := partFields()
						f[k] = text
						if typ == "output_text" {
							f["logprobs"] = []any{}
						}
						emit(deltaEvent+"."+stage, f)
					}
					text := p[textKey].(string)
					var deltas []string
					if typ == "output_text" {
						deltas = textDeltas
					}
					for _, part := range textChunks(deltas, text) {
						emitText("delta", "delta", part)
					}
					emitText("done", textKey, text)
					f = partFields()
					f["part"] = p
					emit(partEvent+".done", f)
				}
			}
		}
		emit("output_item.done", responseObject{"output_index": i, "item": item})
	}
	return frames
}
