package providersim

import (
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
)

// Sources: the openai-completions bundle in schemas/manifest.json,
// https://api-docs.deepseek.com/api/create-completion.md and
// https://docs.mistral.ai/api/endpoint/fim (document hashes in the plan).
func init() {
	for wire, route := range map[Wire]string{
		FIMOpenAI: "POST /v1/completions", FIMDeepSeek: "POST /beta/completions", FIMCodestral: "POST /v1/fim/completions",
	} {
		registerWire(wire, func(s *Server) http.Handler {
			mux := http.NewServeMux()
			mux.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) { s.completeFIM(w, r, wire) })
			return mux
		})
	}
}

func (s *Server) completeFIM(w http.ResponseWriter, r *http.Request, wire Wire) {
	req, err := receive(wire, r)
	var includeUsage bool
	if err == nil {
		includeUsage, err = decodeFIM(&req)
	}
	if err != nil {
		req.Violation = err.Error()
		s.observe(req)
		s.report("simulate %s %s: %v", wire, req.Path, err)
		status, body := fimHTTPFault(wire, HTTPError(InvalidRequest).WithFaultMessage(err.Error()).fault)
		writeJSON(w, status, body)
		return
	}
	// Synthetic rendering for token and prefix accounting; neither side of a
	// FIM insertion is a chat message. Do not feed it through a chat template.
	turn, ok := s.dispatch(&req, []block{{"/prompt", req.Prompt}, {"/suffix", req.Suffix}}, CacheProfile{})
	fail := func(msg string) {
		_, body := fimHTTPFault(wire, HTTPError("").WithFaultMessage(msg).fault)
		writeJSON(w, http.StatusInternalServerError, body)
	}
	if !ok {
		fail("No reply is scripted.")
		return
	}
	if turn.fault.mode == faultHTTP {
		if s.awaitBody(r, turn) {
			status, body := fimHTTPFault(wire, turn.fault)
			if hint := turn.fault.hint; hint.header != "" {
				w.Header().Set(hint.header, hint.value)
			}
			writeJSON(w, status, body)
		}
		return
	}
	if err := validateFIMTurn(req, turn); err != nil {
		s.report("simulate %s: %v", wire, err)
		fail(err.Error())
		return
	}
	c := newFIMReply(req, turn)
	if !req.Stream {
		if s.awaitBody(r, turn) {
			body := c.body(c.choice(turn.Text, turn.finish()))
			if !turn.omitUsage {
				body["usage"] = c.usage
			}
			writeJSON(w, http.StatusOK, body)
		}
		return
	}
	s.sendStream(w, r, turn, streamCodec{
		contentType: "text/event-stream", write: writeSSE, terminal: []frame{{data: []byte("[DONE]")}},
		encodeFault:   func(f fault, _ int) frame { return c.faultFrame(f) },
		faultFinishes: wire != FIMOpenAI, // A native unsuccessful finish is still a completed stream.
	}, c.events(turn, includeUsage))
}

// This fixture serves single, literal prompt/suffix completions. Other
// provider features (batch prompts, logprobs, sampling, etc.) are out of
// scope. Reject unsupported parameters instead of silently ignoring them.
func decodeFIM(req *Request) (bool, error) {
	value, err := decodeJSON(req.Raw)
	if err != nil {
		return false, err
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return false, fmt.Errorf("/: expected an object")
	}
	if req.Wire == FIMOpenAI {
		b, err := wireBundle(FIMOpenAI)
		if err != nil {
			return false, err
		}
		if err := b.validate("CreateCompletionRequest", obj); err != nil {
			return false, err
		}
	}
	req.Model, ok = obj["model"].(string)
	_, supplied := obj["model"]
	if (!ok || req.Model == "") && (supplied || req.Wire != FIMCodestral) {
		return false, fmt.Errorf("/model: supply a model ID")
	}
	if req.Wire == FIMDeepSeek && req.Model != "deepseek-flash" && req.Model != "deepseek-v4-pro" {
		return false, fmt.Errorf("/model: use deepseek-flash or deepseek-v4-pro for DeepSeek FIM")
	}
	if req.Prompt, ok = obj["prompt"].(string); !ok {
		return false, fmt.Errorf("/prompt: supply a literal completion prompt")
	}
	for key, v := range obj {
		switch key {
		case "model", "prompt":
		case "suffix":
			if req.Suffix, ok = v.(string); !ok && v != nil {
				return false, fmt.Errorf("/suffix: expected a string")
			}
		case "stream":
			if req.Stream, ok = v.(bool); !ok && (v != nil || req.Wire == FIMCodestral) {
				return false, fmt.Errorf("/stream: expected a boolean")
			}
		case "max_tokens", "min_tokens":
			if key == "min_tokens" && req.Wire != FIMCodestral {
				return false, fmt.Errorf("/min_tokens: not a field of this completion wire")
			}
			if v == nil {
				continue
			}
			n, _ := v.(json.Number)
			if i, err := n.Int64(); err != nil || i < 0 {
				return false, fmt.Errorf("/%s: expected a non-negative integer", key)
			}
			if key == "min_tokens" {
				req.MinOutputLimit = n
			} else {
				req.OutputLimit = n
			}
		case "stream_options":
			if req.Wire == FIMCodestral {
				return false, fmt.Errorf("/stream_options: not a Codestral FIM field")
			}
		default:
			return false, fmt.Errorf("/%s: unsupported FIM fixture parameter", key)
		}
	}
	if options := obj["stream_options"]; options != nil {
		opts, ok := options.(map[string]any)
		if !ok || !req.Stream {
			return false, fmt.Errorf("/stream_options: supply an object with stream:true")
		}
		for key, v := range opts {
			if _, ok := v.(bool); key != "include_usage" || !ok {
				return false, fmt.Errorf("/stream_options/%s: only boolean include_usage is supported", key)
			}
		}
		return opts["include_usage"] == true, nil
	}
	return false, nil
}

func validateFIMTurn(req Request, t Turn) error {
	if !req.Stream && t.fault.mode != faultNone {
		return fmt.Errorf("the scripted fault requires streaming")
	}
	if len(t.Tools) != 0 || t.Reasoning != (Thought{}) || t.Refusal != "" {
		return fmt.Errorf("use a text turn for a FIM completion")
	}
	switch t.finish() {
	case FinishLength, FinishStop:
	case FinishFiltered:
		if req.Wire == FIMCodestral {
			return fmt.Errorf("codestral FIM has no content_filter finish reason")
		}
	default:
		return fmt.Errorf("unsupported FIM finish reason %q", t.finish())
	}
	if t.omitUsage && (req.Wire == FIMCodestral && !req.Stream || req.Wire == FIMDeepSeek && req.Stream) {
		return fmt.Errorf("this FIM reply requires usage")
	}
	if u := t.Usage; u != nil {
		if u.Cached < 0 || u.Input < u.Cached || u.Output < 0 || u.CacheWrite != 0 || u.Reasoning != 0 {
			return fmt.Errorf("supply FIM input, output and cache-read counts only; cache reads must fit in the input")
		}
		if u.OmitCacheRead && req.Wire == FIMDeepSeek {
			return fmt.Errorf("deepseek FIM usage requires prompt_cache_hit_tokens")
		}
	}
	if t.fault.mode == faultStream && req.Wire != FIMOpenAI {
		// Neither endpoint documents arbitrary HTTP-style errors in SSE.
		// Both document an unsuccessful finish reason for inference failure.
		if t.fault.kind != Overload || t.fault.message != nil {
			return fmt.Errorf("this FIM stream supports Overload without custom wording; use HTTPError for a detailed fault")
		}
		if t.fault.after > max(1, len(t.textDeltas)) {
			return fmt.Errorf("place the FIM stream fault before the finish choice")
		}
	}
	return nil
}

type fimReply struct {
	wire      Wire
	id, model string
	stream    bool
	usage     map[string]any
}

func newFIMReply(req Request, t Turn) fimReply {
	model := req.Model
	if model == "" && req.Wire == FIMCodestral {
		model = "codestral-2404" // FIMCompletionRequest.model default.
	}
	u := Usage{Input: req.Prefix.InputTokens, Cached: req.Prefix.CachedTokens, Output: estimateTokens(t.Text)}
	if t.Usage != nil {
		u = *t.Usage
	}
	counts := map[string]any{"prompt_tokens": u.Input, "completion_tokens": u.Output, "total_tokens": u.Input + u.Output}
	if !u.OmitCacheRead {
		counts["prompt_tokens_details"] = map[string]int{"cached_tokens": u.Cached}
	}
	if req.Wire == FIMDeepSeek {
		counts["prompt_cache_hit_tokens"], counts["prompt_cache_miss_tokens"] = u.Cached, u.Input-u.Cached
	}
	// Synthetic ID and token counts; no live account or tokenizer.
	return fimReply{wire: req.Wire, id: fmt.Sprintf("cmpl-sim-%d", req.Index), model: model, stream: req.Stream, usage: counts}
}

func (c fimReply) choice(text string, finish any) map[string]any {
	o := map[string]any{"index": 0, "finish_reason": finish}
	if c.wire != FIMCodestral {
		o["text"], o["logprobs"] = text, nil
	} else if c.stream {
		o["delta"] = map[string]string{"content": text}
	} else {
		o["message"] = map[string]string{"role": "assistant", "content": text}
	}
	return o
}

func (c fimReply) body(choice map[string]any) map[string]any {
	choices := []map[string]any{}
	if choice != nil {
		choices = append(choices, choice)
	}
	object := "text_completion"
	if c.wire == FIMCodestral {
		object = "chat.completion"
		if c.stream {
			object += ".chunk"
		}
	}
	return map[string]any{"id": c.id, "created": chatSyntheticCreated, "model": c.model, "object": object, "choices": choices}
}

func (c fimReply) events(t Turn, includeUsage bool) iter.Seq[frame] {
	return func(yield func(frame) bool) {
		emit := func(body map[string]any) bool { return yield(frame{data: jsonBytes(body)}) }
		for _, text := range textChunks(t.textDeltas, t.Text) {
			body := c.body(c.choice(text, nil))
			if includeUsage && !t.omitUsage {
				body["usage"] = nil
			}
			if !emit(body) {
				return
			}
		}
		end := c.body(c.choice("", t.finish()))
		// DeepSeek always attaches usage to the finish choice. Mistral allows
		// usage on CompletionChunk; selecting the last chunk is synthetic.
		// OpenAI sends the requested extra usage-only chunk before [DONE].
		// Suppressing it with WithoutUsage is synthetic gateway behavior.
		if c.wire != FIMOpenAI && !t.omitUsage {
			end["usage"] = c.usage
		} else if includeUsage && !t.omitUsage {
			end["usage"] = nil
		}
		if emit(end) && c.wire == FIMOpenAI && includeUsage && !t.omitUsage {
			body := c.body(nil)
			body["usage"] = c.usage
			emit(body)
		}
	}
}
