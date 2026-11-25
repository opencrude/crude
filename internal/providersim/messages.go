package providersim

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Messages wire: anthropics/anthropic-sdk-python@f1edcd7d751a4cd6d72cf8b6454930d8aab2c97c.
// The 2026-11-25 documentation pins (SHA-256) are in messages_test.go.
func init() {
	registerWire(Messages, func(s *Server) http.Handler {
		state := newMessageState(s)
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/messages", state.create)
		mux.HandleFunc("GET /v1/models", state.listModels)
		return mux
	})
}

// HTTP codes and envelopes: https://platform.claude.com/docs/en/api/errors.md.
// Synthetic IDs, human-readable fault messages and default retry delay.
func messageError(w http.ResponseWriter, kind, msg, code string) messageObject {
	id := w.Header().Get("request-id")
	if id == "" {
		id = "req_sim_" + rand.Text()
		w.Header().Set("request-id", id)
	}
	e := messageObject{"type": kind, "message": msg}
	if code != "" {
		e["details"] = messageObject{"error_code": code}
	}
	return messageObject{"type": "error", "error": e, "request_id": id}
}
func messageFault(w http.ResponseWriter, f fault) (int, messageObject) {
	envelope := func(status int, kind, msg string) (int, messageObject) {
		return status, messageError(w, kind, f.text(msg), "")
	}
	// https://platform.claude.com/docs/en/api/errors.md#http-errors
	// The 502/408 pairing with api_error/timeout_error is synthetic.
	switch f.kind {
	case ModelNotFound:
		return envelope(404, "not_found_error", "Model not found.")
	case Quota:
		// https://platform.claude.com/docs/en/api/errors#error-codes:
		// a monthly spend-cap rejection has no retry-after header.
		return 429, messageError(w, "rate_limit_error", f.text("The account has reached its spend limit."), "enforced_spend_limit_reached")
	case BadGateway:
		return envelope(502, "api_error", "Bad gateway.")
	case InvalidRequest:
		return envelope(400, "invalid_request_error", "Invalid request.")
	case RequestTimeout:
		return envelope(408, "timeout_error", requestTimedOut)
	case RateLimit:
		return envelope(429, "rate_limit_error", "Rate limit reached.")
	case Overload:
		return envelope(529, "overloaded_error", "Overloaded")
	case Auth:
		return envelope(401, "authentication_error", "Invalid API key.")
	case ContextOverflow:
		return envelope(400, "invalid_request_error", "prompt is too long: input exceeds the model context window")
	default:
		return envelope(500, "api_error", "Internal server error.")
	}
}
func (s *messageState) reject(w http.ResponseWriter, req Request, err error) {
	req.Violation = err.Error()
	s.server.observe(req)
	s.server.report("simulate %s %s: %s", req.Wire, req.Path, req.Violation)
	msg, code := req.Violation, ""
	var v messageViolation
	if errors.As(err, &v) {
		msg = strings.ReplaceAll(strings.TrimPrefix(v.path, "/"), "/", ".") + ": " + v.message
		code = v.code
	}
	if errors.As(err, new(*http.MaxBytesError)) {
		writeJSON(w, 413, messageError(w, "request_too_large", "Request exceeds the Messages API size limit.", ""))
		return
	}
	writeJSON(w, 400, messageError(w, "invalid_request_error", msg, code))
}

func (s *messageState) create(w http.ResponseWriter, r *http.Request) {
	// Public first-party Messages limit is documented as 128 MB (synthetic:
	// binary megabytes). Reject declared oversize requests before reading them;
	// the byte limit also applies to chunked transfers.
	const limit = 128 << 20
	w.Header().Set("request-id", "req_sim_"+rand.Text())
	var tooLarge bool
	if r.ContentLength > limit {
		tooLarge = true
		r.Body = http.NoBody
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
	req, err := receive(Messages, r)
	if tooLarge {
		err = &http.MaxBytesError{Limit: limit}
	}
	if err != nil {
		s.reject(w, req, err)
		return
	}
	val, err := decodeJSON(req.Raw)
	if err != nil {
		s.reject(w, req, err)
		return
	}
	obj, ok := val.(messageObject)
	if !ok {
		s.reject(w, req, invalidMessage("/", "Expected an object"))
		return
	}
	b, err := wireBundle(Messages)
	if err != nil {
		s.server.report("simulate Messages schema: %v", err)
		writeJSON(w, 500, messageError(w, "api_error", "Messages schema unavailable.", ""))
		return
	}
	cfg, err := messageParams(&req, obj, b)
	if err != nil {
		s.reject(w, req, err)
		return
	}
	prompt, err := s.decode(&req, obj, cfg)
	if err != nil {
		s.reject(w, req, err)
		return
	}
	c := prompt.cachePlan(req.Model)
	fallback := CacheProfile{Minimum: messageModelProfile(req.Model).minimum, Granularity: 1}
	turn, ok := s.server.dispatchWithPrefix(&req, prompt.blocks, fallback, func(p *Prefix, profile CacheProfile) { c.account(p, profile, s.cache, time.Now()) })
	if !ok {
		writeJSON(w, 500, messageError(w, "api_error", "No reply is scripted.", ""))
		return
	}
	if !req.Stream || turn.fault.mode == faultHTTP {
		if !s.server.awaitBody(r, turn) {
			return
		}
	}
	if turn.fault.mode == faultHTTP {
		status, payload := messageFault(w, turn.fault)
		if h := turn.fault.hint; h.header != "" && turn.fault.kind != Quota {
			w.Header().Set(h.header, h.value)
		} else if turn.fault.kind == RateLimit {
			w.Header().Set("retry-after", "1")
		}
		writeJSON(w, status, payload)
		return
	}
	if !req.Stream && (turn.fault.mode == faultStream || turn.fault.mode == faultCut) {
		s.server.report("simulate Messages: use a streaming request for a stream fault")
		writeJSON(w, 500, messageError(w, "api_error", "The scripted fault requires streaming.", ""))
		return
	}
	body, err := s.reply(req, turn, cfg, c.hourWrites)
	if err != nil {
		s.server.report("simulate Messages turn: %v", err)
		writeJSON(w, 500, messageError(w, "api_error", err.Error(), ""))
		return
	}
	if !req.Stream {
		s.publishCache(c)
		writeJSON(w, 200, body)
		return
	}
	// Synthetic ping immediately after message_start. Cache writes become
	// available at message_start, not when a gated request was dispatched.
	codec := streamCodec{contentType: "text/event-stream", terminal: []frame{messageFrame("message_stop", messageObject{})}, write: func(w io.Writer, f frame) error {
		if f.event == "message_start" {
			s.publishCache(c)
		}
		return writeSSE(w, f)
	}, encodeFault: func(f fault, _ int) frame {
		_, payload := messageFault(w, f)
		return frame{event: "error", data: jsonBytes(payload)}
	}}
	s.server.sendStream(w, r, turn, codec, slices.Values(messageFrames(body, turn)))
}
func messageStop(t Turn) string {
	switch t.finish() {
	case FinishLength:
		return "max_tokens"
	case FinishFiltered:
		return "refusal"
	case FinishToolCalls:
		return "tool_use"
	}
	if t.Refusal != "" {
		return "refusal"
	}
	return "end_turn"
}

func (s *messageState) content(index int, t Turn) ([]messageObject, error) {
	out := make([]messageObject, 0)
	th := t.Reasoning
	if th.Text != "" || th.Signature != "" {
		v := messageObject{"type": "thinking", "thinking": th.Text}
		if th.Signature != "" {
			v["signature"] = th.Signature
		}
		out = append(out, s.issue(v, th.Signature == ""))
	}
	if th.Encrypted != "" {
		out = append(out, s.issue(messageObject{"type": "redacted_thinking", "data": th.Encrypted}, false))
	}
	for i, text := range []string{t.Text, t.Refusal} {
		if text != "" || i == 0 && len(t.textDeltas) > 0 {
			out = append(out, messageObject{"type": "text", "text": text})
		}
	}
	for i, call := range t.Tools {
		args, err := decodeJSON([]byte(call.Arguments))
		_, ok := args.(messageObject)
		if err != nil || !ok {
			return nil, fmt.Errorf("tool %q input must be a JSON object", call.Name)
		}
		id := call.ID
		if id == "" {
			id = fmt.Sprintf("toolu_sim_%d_%d", index, i)
		}
		out = append(out, messageObject{"type": "tool_use", "id": id, "name": call.Name, "input": json.RawMessage(call.Arguments)})
	}
	return out, nil
}

// Synthetic counts everywhere except an explicit Turn.Usage. The wire's
// input_tokens excludes both cache writes and cache reads. Compaction-only
// usage appears in iterations, not in the top-level message usage.
func messageCounts(u Usage, hour int) messageObject {
	o := messageObject{"input_tokens": u.Input - u.CacheWrite - u.Cached, "cache_creation_input_tokens": u.CacheWrite, "output_tokens": u.Output, "cache_creation": messageObject{"ephemeral_1h_input_tokens": hour, "ephemeral_5m_input_tokens": u.CacheWrite - hour}}
	if !u.OmitCacheRead {
		o["cache_read_input_tokens"] = u.Cached
	}
	return o
}
func messageIteration(kind string, u Usage, hour int) messageObject {
	o := messageCounts(u, hour)
	o["type"] = kind
	return o
}
func (s *messageState) reply(req Request, t Turn, cfg compactConfig, hour int) (messageObject, error) {
	if t.omitUsage {
		return nil, errors.New("a Messages reply requires usage")
	}
	if t.Usage != nil && t.Usage.OmitCacheRead && (cfg.demand || cfg.threshold != 0 || hasMessageBeta(req.Header, thresholdBeta)) {
		return nil, errors.New("iteration usage requires cache_read_input_tokens")
	}
	stop := messageStop(t)
	compact := cfg.demand || cfg.threshold != 0 && req.Prefix.InputTokens >= cfg.threshold
	only := compact && (cfg.demand || cfg.pause)
	u := Usage{Input: req.Prefix.InputTokens, Cached: req.Prefix.CachedTokens, CacheWrite: req.Prefix.CacheWriteTokens, Output: estimateTokens(t.Text) + estimateTokens(t.Refusal) + estimateTokens(t.Reasoning.Text) + estimateTokens(t.Reasoning.Encrypted)}
	for _, call := range t.Tools {
		u.Output += estimateTokens(call.Name) + estimateTokens(call.Arguments)
	}
	if t.Usage != nil {
		u = *t.Usage
		hour = 0
	}
	if u.Cached < 0 || u.CacheWrite < 0 || u.Input-u.Cached < u.CacheWrite || u.Output < 0 {
		return nil, fmt.Errorf("usage must be non-negative; cached reads and writes must fit within total input")
	}
	out := make([]messageObject, 0)
	iterations := make([]messageObject, 0)
	if compact {
		// Synthetic threshold summary for a combined summary + ordinary reply;
		// Text scripts the summary itself on a compaction-only request.
		summary := t.Text
		if !only {
			summary = fmt.Sprintf("Summary of %d messages.", len(req.Effective.Messages))
		}
		compUsage := u
		if t.Usage == nil {
			compUsage.Output = estimateTokens(summary)
		}
		iterations = append(iterations, messageIteration("compaction", compUsage, hour))
		if !cfg.demand || stop == "end_turn" && summary != "" {
			out = append(out, s.issue(messageObject{"type": "compaction", "content": summary}, cfg.demand))
			if only {
				stop = "compaction"
			}
		}
		if only {
			u = Usage{}
			hour = 0
		} else if t.Usage == nil { // Synthetic fresh message iteration: system, tools and summary replace original dialogue.
			u.Input = estimateTokens(summary)
			for _, p := range req.System {
				u.Input += estimateTokens(string(p.Raw))
			}
			for _, tool := range req.Tools {
				u.Input += estimateTokens(string(tool.Raw))
			}
			u.Cached = 0
			u.CacheWrite = 0
			hour = 0
		}
	}
	if !only {
		if req.OutputLimit == "0" {
			stop = "max_tokens"
			u.Output = 0
		} else {
			content, err := s.content(req.Index, t)
			if err != nil {
				return nil, err
			}
			out = append(out, content...)
		}
		if cfg.threshold != 0 || hasMessageBeta(req.Header, thresholdBeta) {
			iterations = append(iterations, messageIteration("message", u, hour))
		}
	}
	usage := messageCounts(u, hour)
	if len(iterations) > 0 {
		usage["iterations"] = iterations
	}
	return messageObject{"id": fmt.Sprintf("msg_sim_%d", req.Index), "type": "message", "role": "assistant", "model": req.Model, "content": out, "stop_reason": stop, "stop_sequence": nil, "usage": usage}, nil
}

// https://platform.claude.com/docs/en/build-with-claude/streaming.md:
// the SSE name matches type; synthetic chunking uses one delta per field.
// On-demand summaries arrive whole; threshold summaries use compaction_delta.
func messageFrame(event string, v messageObject) frame {
	v["type"] = event
	return frame{event: event, data: jsonBytes(v)}
}
func messageFrames(body messageObject, turn Turn) []frame {
	start := maps.Clone(body)
	usage := body["usage"].(messageObject)
	initial := maps.Clone(usage)
	initial["output_tokens"] = 0
	delete(initial, "iterations")
	start["content"] = []any{}
	start["stop_reason"] = nil
	start["usage"] = initial
	out := []frame{messageFrame("message_start", messageObject{"message": start}), messageFrame("ping", messageObject{})}
	tool, textDeltas := 0, turn.textDeltas
	for i, v := range body["content"].([]messageObject) {
		first := maps.Clone(v)
		var deltas []messageObject
		add := func(typ, field string, val any) { deltas = append(deltas, messageObject{"type": typ, field: val}) }
		switch v["type"] {
		case "text":
			first["text"] = ""
			for _, part := range textChunks(textDeltas, v["text"].(string)) {
				add("text_delta", "text", part)
			}
			textDeltas = nil // A subsequent text block is the separate refusal.
		case "thinking":
			first["thinking"] = ""
			first["signature"] = ""
			add("thinking_delta", "thinking", v["thinking"])
			add("signature_delta", "signature", v["signature"])
		case "tool_use":
			first["input"] = messageObject{}
			for _, part := range textChunks(turn.toolDeltas[tool], string(v["input"].(json.RawMessage))) {
				add("input_json_delta", "partial_json", part)
			}
			tool++
		case "compaction":
			if v["signature"] == nil {
				first["content"] = nil
				add("compaction_delta", "content", v["content"])
			}
		}
		out = append(out, messageFrame("content_block_start", messageObject{"index": i, "content_block": first}))
		for _, d := range deltas {
			out = append(out, messageFrame("content_block_delta", messageObject{"index": i, "delta": d}))
		}
		out = append(out, messageFrame("content_block_stop", messageObject{"index": i}))
	}
	final := maps.Clone(usage)
	delete(final, "cache_creation")
	return append(out, messageFrame("message_delta", messageObject{"delta": messageObject{"stop_reason": body["stop_reason"], "stop_sequence": nil}, "usage": final}))
}
