package providersim

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
)

func init() {
	registerWire(Codex, func(s *Server) http.Handler {
		state := newResponseState(s, Codex)
		mux := http.NewServeMux()
		mux.HandleFunc("POST /backend-api/codex/responses", state.create)
		mux.HandleFunc("GET /backend-api/codex/models", s.listCodexModels)
		return mux
	})
}

// Route and client_version query:
// https://github.com/openai/codex/blob/ebd0765/codex-rs/codex-api/src/endpoint/models.rs.
// ModelsResponse and ModelInfo (including required Rust deserialize fields):
// https://github.com/openai/codex/blob/ebd0765/codex-rs/protocol/src/openai_models.rs.
// Capability defaults are synthetic, not claims about any production model.
func (s *Server) listCodexModels(w http.ResponseWriter, r *http.Request) {
	req, err := receive(Codex, r)
	if err != nil {
		req.Violation = err.Error()
		s.observe(req)
		s.report("simulate Codex %s: %v", req.Path, err)
		writeJSON(w, 400, chatError("invalid_request_error", "", nil, "Invalid model request."))
		return
	}
	s.observe(req)
	models, ok := s.inventory(w, r, Codex, "gpt-5-codex")
	if !ok {
		return
	}
	rows := make([]responseObject, 0, len(models))
	for i, m := range models {
		row := responseObject{
			"slug": m.ID, "display_name": cmp.Or(m.DisplayName, m.ID), "supported_reasoning_levels": []any{},
			"shell_type": "disabled", "visibility": "list", "supported_in_api": true, "priority": i,
			"support_verbosity": false, "truncation_policy": responseObject{"mode": "tokens", "limit": 4096},
			"experimental_supported_tools": []string{},
		}
		if m.ContextWindow != 0 {
			row["context_window"] = m.ContextWindow
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, responseObject{"models": rows})
}

// openai/codex@ebd0765 common.rs::ResponsesApiRequest, Reasoning,
// StreamOptions, PromptCacheOptions and TextControls. Each field validator
// is resolved once. Validate input items with the generated ResponseItem, not
// public Responses: Codex replays output_text without a public message ID.
// Synthetic transport policy: require model, input, stream:true, store:false;
// reject unknown top-level fields. The Rust serializer is not a backend spec.
// Observed, not pinned: the backend has answered a request without top-level
// instructions with 400 "Instructions are required", and earlier Codex clients
// sent them, so the field is accepted as optional text.
const codexFields = `{
 "model":{"type":"string","minLength":1},"stream":{"const":true},"store":{"const":false},
 "instructions":{"type":"string"},
 "input":{"type":"array"},"tools":{"type":"array"},"include":{"type":"array","items":{"type":"string"}},
 "service_tier":{"type":"string"},"tool_choice":{"type":"string"},"parallel_tool_calls":{"type":"boolean"},
 "reasoning":{"type":["object","null"],"properties":{
   "effort":{"anyOf":[{"type":"string"},{"type":"integer","minimum":0}]},
   "summary":{"enum":["auto","concise","detailed","none"]},"context":{"enum":["auto","current_turn","all_turns"]}}},
 "stream_options":{"type":"object","required":["reasoning_summary_delivery"],"properties":{"reasoning_summary_delivery":{"const":"sequential_cutoff"}}},
 "prompt_cache_key":{"type":"string"},"prompt_cache_options":{"type":"object","required":["ttl"],"properties":{"ttl":{"enum":["30m","12h"]}}},
 "text":{"type":"object","properties":{"verbosity":{"enum":["low","medium","high"]},"format":{"type":"object","required":["type","strict","schema","name"],"properties":{"type":{"const":"json_schema"},"strict":{"type":"boolean"},"name":{"type":"string"}}}}},
 "client_metadata":{"type":"object","additionalProperties":{"type":"string"}},
 "access_programs":{"type":"object","required":["cyber"],"properties":{"cyber":{"enum":["standard","daybreak_blue","daybreak_red"]}}},
 "request_country":{"type":["string","null"],"pattern":"^[A-Z]{2}$"}
}`

var codexFieldSchemas = sync.OnceValues(func() (map[string]*jsonschema.Resolved, error) {
	var defs map[string]*jsonschema.Schema
	if err := json.Unmarshal([]byte(codexFields), &defs); err != nil {
		return nil, err
	}
	out := make(map[string]*jsonschema.Resolved, len(defs))
	for k, def := range defs {
		r, err := def.Resolve(nil)
		if err != nil {
			return nil, err
		}
		out[k] = r
	}
	return out, nil
})

func validateCodex(b *bundle, obj responseObject) (string, error) {
	fields, err := codexFieldSchemas()
	if err != nil {
		return "", err
	}
	for _, key := range []string{"model", "stream", "store", "input"} {
		if _, ok := obj[key]; !ok {
			return key, fmt.Errorf("/%s: missing Codex HTTP field", key)
		}
	}
	for k, v := range obj {
		schema := fields[k]
		if schema == nil {
			return k, fmt.Errorf("%s: not a Codex HTTP request field", jsonPointer("", k))
		}
		value, _, err := schemaNumbers(v)
		if err == nil {
			err = schema.Validate(value)
		}
		if err != nil {
			return k, fmt.Errorf("%s: %w", jsonPointer("", k), err)
		}
	}
	items := jsonField[[]any](obj, "input")
	for i, v := range items {
		if err := b.validate("ResponseItem", v); err != nil {
			return "input", fmt.Errorf("/input/%d: %w", i, err)
		}
		it, _ := v.(responseObject)
		if it["type"] == "compaction_trigger" && len(it) != 1 {
			return "input", fmt.Errorf("/input/%d: a Codex compaction_trigger has no other fields", i)
		}
	}
	return "", nil
}
