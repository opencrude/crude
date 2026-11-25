package providersim

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
)

const (
	ollamaChatRequest = "ChatRequest"
	ollamaShowRequest = "ShowRequest"
	ollamaNDJSON      = "application/x-ndjson"

	// Synthetic inventory and timestamps; no local model files are used.
	ollamaDefaultModel = "qwen3:latest"
	ollamaCreatedAt    = "2025-01-01T00:00:00Z"
	ollamaContextSize  = 32768
)

// The native routes and response types are pinned to Ollama 924cc286:
// https://raw.githubusercontent.com/ollama/ollama/924cc286cc592f49c481fa84e15fa60dbc75989e/server/routes.go
// https://raw.githubusercontent.com/ollama/ollama/924cc286cc592f49c481fa84e15fa60dbc75989e/api/types.go
func init() {
	registerWire(Ollama, func(s *Server) http.Handler {
		mux := http.NewServeMux()
		mux.HandleFunc("POST /api/chat", s.completeOllama)
		mux.HandleFunc("GET /api/tags", s.listOllama)
		mux.HandleFunc("POST /api/show", s.showOllama)
		return mux
	})
}

func (s *Server) completeOllama(w http.ResponseWriter, r *http.Request) {
	req, obj, ok := s.readOllama(w, r, ollamaChatRequest)
	if !ok {
		return
	}
	blocks := decodeOllama(&req, obj)
	// Synthetic cache thresholds are model-dependent. Count hits only when
	// the test provides a profile; the pinned API makes the cache read count optional.
	turn, ok := s.dispatch(&req, blocks, CacheProfile{})
	if !ok {
		writeJSON(w, http.StatusInternalServerError, ollamaError("No reply is scripted."))
		return
	}
	f := turn.fault
	if f.kind == Quota {
		// https://docs.ollama.com/api/errors.md documents rate limiting, not
		// an exhausted-credit response. The pinned server/cloud_proxy.go
		// forwards upstream errors without defining a quota code or message.
		const msg = "Ollama has no documented quota error. Use RateLimit for HTTP 429."
		s.report("simulate Ollama: %s", msg)
		writeJSON(w, http.StatusInternalServerError, ollamaError(msg))
		return
	}
	// Ollama commits a 200 stream only once it sends content. A failure
	// before the first line is an HTTP error even with stream:true.
	if f.mode == faultHTTP || (req.Stream && f.mode == faultStream && f.after == 0) {
		if s.awaitBody(r, turn) {
			status, message := ollamaFault(f)
			if hint := f.hint; hint.header != "" {
				w.Header().Set(hint.header, hint.value)
			}
			writeJSON(w, status, ollamaError(message))
		}
		return
	}
	if !req.Stream && (f.mode == faultStream || f.mode == faultCut) {
		s.report("simulate Ollama: use a streaming request for a stream fault")
		writeJSON(w, http.StatusInternalServerError, ollamaError("The scripted fault requires streaming."))
		return
	}
	message, err := newOllamaMessage(req, turn, obj["think"] == false)
	if err != nil {
		s.report("simulate Ollama %s: %v", req.Path, err)
		writeJSON(w, http.StatusInternalServerError, ollamaError("Invalid scripted tool arguments."))
		return
	}
	final := ollamaReply(req.Model, message, true)
	final.DoneReason = "stop"
	if turn.finish() == FinishLength {
		final.DoneReason = "length"
	}
	if !turn.omitUsage {
		s.addOllamaCounts(req, turn, &final)
	}
	if !req.Stream {
		if s.awaitBody(r, turn) {
			writeJSON(w, http.StatusOK, final)
		}
		return
	}
	final.Message = ollamaMessage{Role: "assistant"}
	s.sendStream(w, r, turn, streamCodec{
		contentType: ollamaNDJSON,
		write:       writeOllamaLine,
		encodeFault: func(f fault, _ int) frame {
			_, message := ollamaFault(f)
			return frame{data: jsonBytes(ollamaError(message))}
		},
		terminal: []frame{{data: jsonBytes(final)}},
	}, func(yield func(frame) bool) {
		sendOllamaMessage(req.Model, message, turn.mergedDeltas(), yield)
	})
}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	Thinking  string           `json:"thinking,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaToolCall struct {
	ID       string         `json:"id,omitempty"`
	Function ollamaFunction `json:"function"`
}

type ollamaFunction struct {
	Index     int             `json:"index"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ollamaCompletion struct {
	Model     string        `json:"model"`
	CreatedAt string        `json:"created_at"`
	Message   ollamaMessage `json:"message"`
	Done      bool          `json:"done"`
	// The pinned native runner's terminal reasons include stop and length.
	// Tool calls are identified by the message, not a separate finish reason.
	DoneReason string `json:"done_reason,omitempty"`
	Input      int    `json:"prompt_eval_count,omitempty"`
	Cached     *int   `json:"prompt_eval_cached_count,omitempty"`
	Output     int    `json:"eval_count,omitempty"`
}

func newOllamaMessage(req Request, turn Turn, thinkingOff bool) (ollamaMessage, error) {
	// Synthetic projection: Ollama has no separate refusal, reasoning
	// signature, or encrypted reasoning on this wire.
	message := ollamaMessage{Role: "assistant", Content: turn.Text + turn.Refusal}
	if !thinkingOff {
		message.Thinking = turn.Reasoning.Text
	}
	for i, call := range turn.Tools {
		raw := json.RawMessage(call.Arguments)
		if len(raw) == 0 {
			raw = json.RawMessage(`{}`)
		}
		value, err := decodeJSON(raw)
		if _, object := value.(map[string]any); err != nil || !object {
			return ollamaMessage{}, fmt.Errorf("/message/tool_calls/%d/function/arguments: expected a JSON object", i)
		}
		id := call.ID
		if id == "" {
			// Synthetic ID; use the real wire's call_ prefix.
			id = fmt.Sprintf("call_sim_%d_%d", req.Index, i)
		}
		message.ToolCalls = append(message.ToolCalls, ollamaToolCall{
			ID: id, Function: ollamaFunction{Index: i, Name: call.Name, Arguments: raw},
		})
	}
	return message, nil
}

func ollamaReply(model string, message ollamaMessage, done bool) ollamaCompletion {
	return ollamaCompletion{Model: model, CreatedAt: ollamaCreatedAt, Message: message, Done: done}
}

func (s *Server) addOllamaCounts(req Request, turn Turn, final *ollamaCompletion) {
	// Synthetic token counts. prompt_eval_count is the entire prompt, not
	// the uncached portion. No native cache-write or reasoning-only metric.
	if usage := turn.Usage; usage != nil {
		final.Input, final.Output = usage.Input, usage.Output
		if !usage.OmitCacheRead {
			final.Cached = &usage.Cached
		}
		return
	}
	final.Input = req.Prefix.InputTokens
	if s.cacheProfiles[prefixKey{wire: Ollama, model: req.Model}].Granularity > 0 {
		final.Cached = &req.Prefix.CachedTokens
	}
	m := final.Message
	final.Output = estimateTokens(m.Content) + estimateTokens(m.Thinking)
	for _, call := range m.ToolCalls {
		final.Output += estimateTokens(call.Function.Name) + estimateTokens(string(call.Function.Arguments))
	}
}

func sendOllamaMessage(model string, message ollamaMessage, deltas []string, yield func(frame) bool) {
	emit := func(msg ollamaMessage) bool {
		msg.Role = "assistant"
		return yield(frame{data: jsonBytes(ollamaReply(model, msg, false))})
	}
	// Synthetic boundaries: one line for each populated message field, with
	// each call's arguments kept as a full object rather than a string delta.
	if message.Thinking != "" && !emit(ollamaMessage{Thinking: message.Thinking}) {
		return
	}
	if message.Content != "" || len(deltas) > 0 {
		for _, chunk := range textChunks(deltas, message.Content) {
			if !emit(ollamaMessage{Content: chunk}) {
				return
			}
		}
	}
	if len(message.ToolCalls) != 0 {
		emit(ollamaMessage{ToolCalls: message.ToolCalls})
	}
}

func writeOllamaLine(w io.Writer, f frame) error {
	if _, err := w.Write(f.data); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func ollamaError(message string) map[string]string { return map[string]string{"error": message} }

func ollamaFault(f fault) (int, string) {
	// https://docs.ollama.com/api/errors.md lists 400, 404, 429 and 502.
	// Wording and the timeout pairing are synthetic.
	switch f.kind {
	case ModelNotFound:
		return 404, f.text("model not found")
	case BadGateway:
		return 502, f.text("cloud model unavailable")
	case InvalidRequest:
		return 400, f.text("invalid request")
	case RequestTimeout:
		return 408, f.text(requestTimedOut)
	case ContextOverflow:
		// llama_server.go maps the runner's context overflow to 413.
		return http.StatusRequestEntityTooLarge, f.text("the input length exceeds the context length")
	case Overload:
		// routes.go maps the scheduler's ErrMaxQueue (sched.go) to 503.
		return http.StatusServiceUnavailable, f.text("server busy, please try again.  maximum pending requests exceeded")
	case Auth:
		// cloud_proxy.go emits this native 401 envelope (signin_url is optional).
		return http.StatusUnauthorized, f.text("unauthorized")
	case RateLimit:
		// Synthetic cloud rejection: the native cloud proxy preserves upstream
		// status; the local queue uses 503 instead of rate limiting.
		return http.StatusTooManyRequests, f.text("rate limit exceeded")
	default:
		return http.StatusInternalServerError, f.text("The request failed.")
	}
}

func (s *Server) listOllama(w http.ResponseWriter, r *http.Request) {
	req, err := receive(Ollama, r)
	if err != nil {
		s.rejectOllama(w, req, err)
		return
	}
	s.observe(req)
	models, ok := s.inventory(w, r, Ollama, ollamaDefaultModel)
	if !ok {
		return
	}
	rows := make([]map[string]any, 0, len(models))
	for _, m := range models {
		id := m.ID
		// Synthetic metadata, including the digest of the fixture name rather
		// than a model file.
		rows = append(rows, map[string]any{
			"model": id, "name": id, "modified_at": ollamaCreatedAt,
			"digest": fmt.Sprintf("%x", sha256.Sum256([]byte(id))), "size": 0,
			"details": ollamaModelDetails(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": rows})
}

func (s *Server) showOllama(w http.ResponseWriter, r *http.Request) {
	req, _, ok := s.readOllama(w, r, ollamaShowRequest)
	if !ok {
		return
	}
	s.observe(req)
	models, ok := s.inventory(w, r, Ollama, ollamaDefaultModel)
	if !ok {
		return
	}
	if !slices.ContainsFunc(models, func(m Model) bool { return m.ID == req.Model }) {
		writeJSON(w, http.StatusNotFound, ollamaError(fmt.Sprintf("model '%s' not found", req.Model)))
		return
	}
	// Synthetic capabilities and GGUF metadata for every configured test model.
	writeJSON(w, http.StatusOK, map[string]any{
		"model_info": map[string]any{
			"general.architecture": "qwen3", "qwen3.context_length": ollamaContextSize,
		},
		"details": ollamaModelDetails(), "modified_at": ollamaCreatedAt,
		"capabilities": []string{"completion", "tools", "thinking"},
		"thinking":     map[string]any{"values": []bool{false, true}, "default": true},
	})
}

func ollamaModelDetails() map[string]any {
	return map[string]any{"format": "gguf", "family": "qwen3", "families": []string{"qwen3"}}
}
