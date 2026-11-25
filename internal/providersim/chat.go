package providersim

import (
	"fmt"
	"iter"
	"net/http"
	"strconv"
)

const (
	chatRequestSchema    = "CreateChatCompletionRequest"
	chatResponseSchema   = "CreateChatCompletionResponse"
	chatStreamSchema     = "CreateChatCompletionStreamResponse"
	chatErrorSchema      = "ErrorResponse"
	chatModelsSchema     = "ListModelsResponse"
	chatSyntheticCreated = 1735689600
)

// ChatGateway is an opt-in extension to the public OpenAI Chat wire.
type ChatGateway string

// DeepSeek enables DeepSeek Chat's documented reasoning_content extension.
// It does not enable a separate suite of DeepSeek endpoints.
const DeepSeek ChatGateway = "deepseek"

// WithChatGateway enables a named Chat extension for exactly one model.
func WithChatGateway(model string, gateway ChatGateway) Option {
	return func(s *Server) {
		if gateway != DeepSeek {
			s.t.Fatalf("simulate Chat model %q: unknown gateway %q", model, gateway)
		}
		s.chatGateways[model] = gateway
	}
}

func init() { registerWire(Chat, chatHandler) }

func chatHandler(s *Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", s.completeChat)
	mux.HandleFunc("GET /v1/models", s.listChatModels)
	return mux
}

func (s *Server) completeChat(w http.ResponseWriter, r *http.Request) {
	req, err := receive(Chat, r)
	if err != nil {
		s.rejectChat(w, req, "", err)
		return
	}
	b, err := wireBundle(Chat)
	if err != nil {
		s.report("simulate Chat: %v", err)
		writeJSON(w, http.StatusInternalServerError, chatError("server_error", "", nil, "Chat schema is unavailable."))
		return
	}
	value, err := decodeJSON(req.Raw)
	if err != nil {
		s.rejectChat(w, req, "", err)
		return
	}
	obj, _ := value.(map[string]any)
	req.Model, _ = obj["model"].(string)
	gateway := s.chatGateways[req.Model]
	var param string
	if param, err = validateChat(b, value, obj, gateway); err != nil {
		s.rejectChat(w, req, param, err)
		return
	}
	includeUsage, blocks := decodeChat(&req, obj, gateway)
	// Synthetic default for the classic OpenAI Chat profile. Newer or gateway
	// models can have different thresholds. See the OpenAI prompt-caching guide.
	turn, ok := s.dispatch(&req, blocks, CacheProfile{Minimum: 1024, Granularity: 128})
	if !ok {
		writeJSON(w, http.StatusInternalServerError, chatError("server_error", "", nil, "No reply is scripted."))
		return
	}
	if turn.fault.mode == faultHTTP {
		if !s.awaitBody(r, turn) {
			return
		}
		status, body := chatFault(turn.fault)
		if hint := turn.fault.hint; hint.header != "" {
			w.Header().Set(hint.header, hint.value)
		}
		writeJSON(w, status, body)
		return
	}
	reply := newChatReply(req, turn, gateway)
	if req.Stream {
		s.sendStream(w, r, turn, chatStream, reply.events(includeUsage))
		return
	}
	if turn.fault.mode == faultStream || turn.fault.mode == faultCut {
		s.report("simulate Chat: use a streaming request for a stream fault")
		writeJSON(w, http.StatusInternalServerError, chatError("server_error", "", nil, "The scripted fault requires streaming."))
		return
	}
	if s.awaitBody(r, turn) {
		writeJSON(w, http.StatusOK, reply.completion())
	}
}

func (s *Server) listChatModels(w http.ResponseWriter, r *http.Request) {
	req, err := receive(Chat, r)
	if err != nil {
		s.rejectChat(w, req, "", err)
		return
	}
	s.observe(req)
	models, ok := s.inventory(w, r, Chat, "gpt-4o-mini")
	if !ok {
		return
	}
	list := make([]map[string]any, 0, len(models))
	for _, model := range models {
		// Timestamp and owner are synthetic fixture data.
		list = append(list, map[string]any{
			"id": model.ID, "object": "model", "created": chatSyntheticCreated, "owned_by": "openai",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": list})
}

func (s *Server) rejectChat(w http.ResponseWriter, req Request, param string, err error) {
	req.Violation = err.Error()
	s.observe(req)
	s.report("simulate Chat %s: %s", req.Path, req.Violation)
	var field any
	if param != "" {
		field = param
	}
	// The provider shape and type follow the pinned API; message text is synthetic.
	writeJSON(w, http.StatusBadRequest, chatError("invalid_request_error", "", field, "Invalid Chat request."))
}

func chatError(kind, code string, param any, text string) map[string]any {
	var codeValue any
	if code != "" {
		codeValue = code
	}
	return map[string]any{"error": map[string]any{
		"message": text, "type": kind, "param": param, "code": codeValue,
	}}
}

// Messages are synthetic. The overload status, type, and code are in the
// pinned spec's InferenceServiceUnavailable response; the context and auth
// details represent common OpenAI errors, not guarantees about deployments.
// https://developers.openai.com/api/docs/guides/error-codes documents
// insufficient_quota and invalid_request_error. The pinned Codex parser accepts
// model_not_found and insufficient_quota: https://github.com/openai/codex/blob/ebd0765/codex-rs/codex-api/src/error.rs.
// The 502 and 408 use synthetic gateway envelopes (RFC 9110 §§15.6.3, 15.5.9).
func chatFault(f fault) (int, map[string]any) {
	envelope := func(kind, code string, param any, text string) map[string]any {
		return chatError(kind, code, param, f.text(text))
	}
	switch f.kind {
	case ModelNotFound:
		return 404, envelope("invalid_request_error", "model_not_found", nil, "Model not found.")
	case Quota:
		return 429, envelope("insufficient_quota", "insufficient_quota", nil, "The account has reached its quota.")
	case BadGateway:
		return 502, envelope("server_error", "server_error", nil, "Bad gateway.")
	case InvalidRequest:
		return 400, envelope("invalid_request_error", "", nil, "Invalid request.")
	case RequestTimeout:
		return 408, envelope("server_error", "request_timeout", nil, requestTimedOut)
	case RateLimit:
		return http.StatusTooManyRequests, envelope("rate_limit_error", "rate_limit_exceeded", nil, "Rate limit exceeded.")
	case ContextOverflow:
		return http.StatusBadRequest, envelope("invalid_request_error", "context_length_exceeded", "messages", "The input exceeds the context limit.")
	case Overload:
		return http.StatusServiceUnavailable, envelope("service_unavailable_error", "server_is_overloaded", nil, "The model is overloaded.")
	case Auth:
		return http.StatusUnauthorized, envelope("invalid_request_error", "invalid_api_key", nil, "The API key is invalid.")
	default:
		return http.StatusInternalServerError, envelope("server_error", "", nil, "The simulated request failed.")
	}
}

var chatStream = streamCodec{
	contentType: "text/event-stream",
	write:       writeSSE,
	terminal:    []frame{{data: []byte("[DONE]")}},
	encodeFault: func(f fault, _ int) frame {
		// The pinned POST /chat/completions 200 description says: “a data frame
		// may contain a JSON object with an `error` field instead of a completion
		// chunk.” Reusing ErrorResponse and the injected event count are synthetic.
		_, body := chatFault(f)
		return frame{data: jsonBytes(body)}
	},
}

type chatReply struct {
	id      string
	model   string
	turn    Turn
	usage   Usage
	gateway ChatGateway
}

func newChatReply(req Request, turn Turn, gateway ChatGateway) chatReply {
	usage := Usage{Input: req.Prefix.InputTokens, Cached: req.Prefix.CachedTokens}
	// Synthetic output estimate uses the same byte rule as the input. Parts
	// not emitted by the selected wire do not count toward output usage.
	usage.Output = estimateTokens(turn.Text) + estimateTokens(turn.Refusal)
	if gateway == DeepSeek {
		usage.Reasoning = estimateTokens(turn.Reasoning.Text)
		usage.Output += usage.Reasoning
	}
	for _, call := range turn.Tools {
		usage.Output += estimateTokens(call.Name) + estimateTokens(call.Arguments)
	}
	if turn.Usage != nil {
		usage = *turn.Usage
	}
	return chatReply{id: "chatcmpl-sim-" + strconv.Itoa(req.Index), model: req.Model, turn: turn, usage: usage, gateway: gateway}
}

func (c chatReply) envelope(object string, choices []map[string]any) map[string]any {
	// The ID and creation time are deterministic synthetic fixture values.
	return map[string]any{
		"id": c.id, "model": c.model, "created": chatSyntheticCreated, "object": object, "choices": choices,
	}
}

func (c chatReply) completion() map[string]any {
	var content any = c.turn.Text
	if c.turn.Text == "" && (len(c.turn.Tools) != 0 || c.turn.Refusal != "") {
		content = nil
	}
	message := map[string]any{"role": "assistant", "content": content}
	if c.turn.Refusal != "" {
		message["refusal"] = c.turn.Refusal
	}
	// No OpenAI Chat schema exposes reasoning text, signatures or ciphertext.
	// DeepSeek documents reasoning_content beside message.content and
	// delta.content: https://api-docs.deepseek.com/guides/thinking_mode.md
	if c.gateway == DeepSeek && c.turn.Reasoning.Text != "" {
		message["reasoning_content"] = c.turn.Reasoning.Text
	}
	if len(c.turn.Tools) != 0 {
		calls := make([]map[string]any, len(c.turn.Tools))
		for i, call := range c.turn.Tools {
			calls[i] = chatFunctionCall(c.callID(i, call), call.Name, call.Arguments)
		}
		message["tool_calls"] = calls
	}
	choice := map[string]any{"index": 0, "message": message, "finish_reason": c.turn.finish(), "logprobs": nil}
	body := c.envelope("chat.completion", []map[string]any{choice})
	if !c.turn.omitUsage {
		body["usage"] = chatUsage(c.usage)
	}
	return body
}

func (c chatReply) events(includeUsage bool) iter.Seq[frame] {
	// Synthetic gateway behavior: omit even a requested stream usage chunk.
	includeUsage = includeUsage && !c.turn.omitUsage
	return func(yield func(frame) bool) {
		chunk := func(choices []map[string]any, usage any) bool {
			body := c.envelope("chat.completion.chunk", choices)
			if includeUsage {
				body["usage"] = usage
			}
			return yield(frame{data: jsonBytes(body)})
		}
		delta := func(fields map[string]any, reason any) bool {
			return chunk([]map[string]any{{"index": 0, "delta": fields, "finish_reason": reason}}, nil)
		}
		if !delta(map[string]any{"role": "assistant"}, nil) {
			return
		}
		if c.gateway == DeepSeek && c.turn.Reasoning.Text != "" && !delta(map[string]any{"reasoning_content": c.turn.Reasoning.Text}, nil) {
			return
		}
		if c.turn.Text != "" || len(c.turn.textDeltas) > 0 {
			for _, part := range textChunks(c.turn.textDeltas, c.turn.Text) {
				if !delta(map[string]any{"content": part}, nil) {
					return
				}
			}
		}
		if c.turn.Refusal != "" && !delta(map[string]any{"refusal": c.turn.Refusal}, nil) {
			return
		}
		for i, call := range c.turn.Tools {
			start := chatFunctionCall(c.callID(i, call), call.Name, "")
			start["index"] = i
			if !delta(map[string]any{"tool_calls": []map[string]any{start}}, nil) {
				return
			}
			// ChatCompletionMessageToolCallChunk.function.arguments (OpenAI pin).
			if call.Arguments == "" && len(c.turn.toolDeltas[i]) == 0 {
				continue
			}
			for _, part := range textChunks(c.turn.toolDeltas[i], call.Arguments) {
				args := map[string]any{"index": i, "function": map[string]any{"arguments": part}}
				if !delta(map[string]any{"tool_calls": []map[string]any{args}}, nil) {
					return
				}
			}
		}
		if !delta(map[string]any{}, c.turn.finish()) {
			return
		}
		if includeUsage {
			chunk([]map[string]any{}, chatUsage(c.usage))
		}
	}
}

func chatFunctionCall(id, name, arguments string) map[string]any {
	return map[string]any{
		"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": arguments},
	}
}

func (c chatReply) callID(index int, call ToolCall) string {
	if call.ID != "" {
		return call.ID
	}
	return fmt.Sprintf("call_sim_%s_%d", c.id, index)
}

func chatUsage(usage Usage) map[string]any {
	prompt := map[string]int{}
	if !usage.OmitCacheRead {
		prompt["cached_tokens"] = usage.Cached
	}
	if usage.CacheWrite != 0 {
		prompt["cache_write_tokens"] = usage.CacheWrite
	}
	body := map[string]any{
		"prompt_tokens": usage.Input, "completion_tokens": usage.Output,
		"total_tokens": usage.Input + usage.Output,
	}
	if len(prompt) > 0 {
		body["prompt_tokens_details"] = prompt
	}
	if usage.Reasoning != 0 {
		body["completion_tokens_details"] = map[string]int{"reasoning_tokens": usage.Reasoning}
	}
	return body
}
