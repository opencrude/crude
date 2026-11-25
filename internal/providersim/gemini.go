package providersim

import (
	"encoding/base64"
	"fmt"
	"iter"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

const (
	geminiRequestSchema = "GenerateContentRequest"
	geminiReplySchema   = "GenerateContentResponse"
	googleErrorSchema   = "ErrorResponse"
	googleAPIVersion    = "/v1beta"
	googleDefaultModel  = "gemini-3.1-pro-preview"
)

func init() { registerWire(Gemini, geminiHandler) }

func geminiHandler(s *Server) http.Handler {
	state := &geminiWire{server: s}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+googleAPIVersion+"/models/{operation}", state.generate)
	mux.HandleFunc("GET "+googleAPIVersion+"/models", state.models)
	mux.HandleFunc("GET "+googleAPIVersion+"/models/{model}", state.models)
	return mux
}

type geminiWire struct {
	server     *Server
	mu         sync.RWMutex
	signatures map[geminiCallKey]map[string]bool
}

type geminiCallKey struct{ model, history, call string }

func (g *geminiWire) generate(w http.ResponseWriter, r *http.Request) {
	model, method, _ := strings.Cut(r.PathValue("operation"), ":")
	if model == "" || (method != "generateContent" && method != "streamGenerateContent") {
		writeGeminiError(w, http.StatusNotFound, "NOT_FOUND", "Model operation not found.")
		return
	}
	req, err := receive(Gemini, r)
	req.Model, req.Stream = model, method == "streamGenerateContent"
	if err != nil {
		g.reject(w, req, err)
		return
	}
	b, err := wireBundle(Gemini)
	if err != nil {
		g.internalError(w, err)
		return
	}
	obj, err := googleRequestObject(req.Raw)
	if err == nil {
		err = validateGemini(b, obj)
	}
	if err == nil && req.Stream && r.URL.Query().Get("alt") != "sse" {
		err = fmt.Errorf("/alt: use alt=sse for streaming")
	}
	if err == nil {
		err = g.validateSignatures(req.Model, googleArray(obj["contents"]))
	}
	if err != nil {
		g.reject(w, req, err)
		return
	}
	blocks := decodeGemini(&req, obj)
	turn, ok := g.server.dispatch(&req, blocks, googleCacheProfile(req.Model))
	if !ok {
		writeGeminiError(w, 500, "INTERNAL", "No reply is scripted.")
		return
	}
	if turn.fault.mode == faultHTTP {
		if turn.gate.await(r.Context(), g.server.done) {
			status, body := geminiFault(turn.fault.kind)
			applyGeminiRetry(w, body, status, turn.fault.hint)
			writeJSON(w, status, body)
		}
		return
	}
	if !req.Stream && (turn.fault.mode == faultCut || turn.fault.mode == faultStream) {
		g.internalError(w, fmt.Errorf("the scripted fault requires streaming"))
		return
	}
	reply, err := newGeminiReply(req, turn)
	if err != nil {
		g.internalError(w, err)
		return
	}
	save := func() { g.rememberSignature(req.Model, googleArray(obj["contents"]), reply.parts) }
	if req.Stream {
		g.server.sendStream(w, r, turn, geminiStream, reply.events(save))
	} else if turn.gate.await(r.Context(), g.server.done) {
		save()
		writeJSON(w, http.StatusOK, reply.envelope(reply.parts, true))
	}
}

func (g *geminiWire) reject(w http.ResponseWriter, req Request, err error) {
	req.Violation = err.Error()
	g.server.observe(req)
	g.server.report("simulate Gemini %s: %s", req.Path, req.Violation)
	writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", req.Violation)
}

func (g *geminiWire) internalError(w http.ResponseWriter, err error) {
	g.server.report("simulate Gemini: %v", err)
	writeGeminiError(w, http.StatusInternalServerError, "INTERNAL", "Cannot produce the scripted reply.")
}

// Synthetic model inventory and token limits, using the shape in
// https://aistudio.google.com/api-reference/models.md?generate-content=true.
func (g *geminiWire) models(w http.ResponseWriter, r *http.Request) {
	req, err := receive(Gemini, r)
	req.Model = r.PathValue("model")
	if err != nil {
		g.reject(w, req, err)
		return
	}
	g.server.observe(req)
	models, configured := g.server.models[Gemini]
	if !configured {
		models = []string{googleDefaultModel}
	}
	list := make([]googleObject, 0, len(models))
	for _, model := range models {
		model = strings.TrimPrefix(model, "models/")
		entry := googleObject{
			"name": "models/" + model, "baseModelId": model, "version": "001", "displayName": model,
			"inputTokenLimit": 1048576, "outputTokenLimit": 65536,
			"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
		}
		if req.Model == model {
			writeJSON(w, http.StatusOK, entry)
			return
		}
		list = append(list, entry)
	}
	if req.Model != "" {
		writeGeminiError(w, http.StatusNotFound, "NOT_FOUND", "Model not found.")
		return
	}
	writeJSON(w, http.StatusOK, googleObject{"models": list})
}

func geminiError(status int, code, message string) googleObject {
	return googleObject{"error": googleObject{"code": status, "status": code, "message": message}}
}

func writeGeminiError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, geminiError(status, code, message))
}

// Synthetic messages and context/auth mapping; HTTP codes and canonical status
// strings use the Google HTTP error envelope (https://google.aip.dev/193).
// The context-overflow wording is the observed service message, for example
// https://discuss.google.dev/t/token-limit-exceeded-error-400-invalid-argument/352656;
// its token counts are synthetic.
const googleOverflowMessage = "The input token count (1048577) exceeds the maximum number of tokens allowed (1048576)."

func geminiFault(kind FaultKind) (int, googleObject) {
	switch kind {
	case RateLimit:
		return 429, geminiError(429, "RESOURCE_EXHAUSTED", "Rate limit exceeded.")
	case ContextOverflow:
		return 400, geminiError(400, "INVALID_ARGUMENT", googleOverflowMessage)
	case Overload:
		return 503, geminiError(503, "UNAVAILABLE", "The model is overloaded.")
	case Auth:
		return 403, geminiError(403, "PERMISSION_DENIED", "The API key lacks permission.")
	default:
		return 500, geminiError(500, "INTERNAL", "The request failed.")
	}
}

func applyGeminiRetry(w http.ResponseWriter, body googleObject, status int, hint RetryHint) {
	if hint.header == "" {
		return
	}
	// A relative hint on HTTP 429 becomes google.rpc.RetryInfo. Absolute
	// dates cannot be represented without a reference time, so keep the RFC
	// Retry-After header. Date and non-429 hints are synthetic test support.
	if n, err := strconv.ParseInt(hint.value, 10, 64); status == 429 && err == nil && n >= 0 {
		delay := hint.value + "s"
		if hint.header == "retry-after-ms" {
			if n%1000 == 0 {
				delay = fmt.Sprintf("%ds", n/1000)
			} else {
				delay = fmt.Sprintf("%d.%03ds", n/1000, n%1000)
			}
		}
		googleObjectAt(body, "error")["details"] = []googleObject{{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": delay}}
		return
	}
	w.Header().Set(hint.header, hint.value)
}

var geminiStream = streamCodec{
	contentType: "text/event-stream",
	write:       writeSSE,
	fault: func(kind FaultKind, _ int) frame {
		// Synthetic: neither the REST discovery document nor the SDK pin
		// establishes an in-band error frame after an HTTP 200 response.
		_, body := geminiFault(kind)
		return frame{data: jsonBytes(body)}
	},
}

// Synthetic implicit-cache profile: reuse the official minima for named
// models; the one-token step, no TTL, and immediate hits are not a prediction
// of provider billing. Other models have no default (WithCacheProfile opts in).
// https://aistudio.google.com/docs/caching.md and ?generate-content=true.
func googleCacheProfile(model string) CacheProfile {
	switch model {
	case "gemini-2.5-flash", "gemini-2.5-pro":
		return CacheProfile{Minimum: 2048, Granularity: 1}
	case "gemini-3.1-pro-preview", "gemini-3.6-flash", "gemini-3.8-flash", "gemini-4-carbon", "gemini-4-argon", "gemini-4-hydrogen", "gemini-4-neon":
		return CacheProfile{Minimum: 4096, Granularity: 1}
	default:
		return CacheProfile{}
	}
}

func googleUsage(req Request, turn Turn) Usage {
	if turn.Usage != nil {
		return *turn.Usage
	}
	// Synthetic estimate: use the same byte rule as input. Opaque signatures
	// are not charged as generated tokens in this fixture.
	u := Usage{Input: req.Prefix.InputTokens, Cached: req.Prefix.CachedTokens, Reasoning: estimateTokens(turn.Reasoning.Text)}
	u.Output = estimateTokens(turn.Text) + estimateTokens(turn.Refusal) + u.Reasoning
	for _, call := range turn.Tools {
		u.Output += estimateTokens(call.Name) + estimateTokens(call.Arguments)
	}
	return u
}

type geminiReply struct {
	model, id, reason string
	parts             []any
	usage             Usage
}

func newGeminiReply(req Request, t Turn) (geminiReply, error) {
	id := fmt.Sprintf("sim-%d", req.Index) // Synthetic response ID.
	out := geminiReply{model: req.Model, id: id, reason: "STOP", usage: googleUsage(req, t), parts: []any{}}
	switch t.finish() {
	case FinishLength:
		out.reason = "MAX_TOKENS"
	case FinishFiltered:
		out.reason = "SAFETY"
	}
	text := t.Text + t.Refusal // Gemini has no distinct refusal content field.
	if t.Reasoning.Text != "" {
		out.parts = append(out.parts, googleObject{"text": t.Reasoning.Text, "thought": true})
	}
	if text != "" {
		out.parts = append(out.parts, googleObject{"text": text})
	}
	firstCall := len(out.parts)
	for _, call := range t.Tools {
		args, err := googleArguments(call)
		if err != nil {
			return geminiReply{}, fmt.Errorf("function %q arguments: %w", call.Name, err)
		}
		fn := googleObject{"name": call.Name, "args": args}
		if call.ID != "" {
			fn["id"] = call.ID
		}
		out.parts = append(out.parts, googleObject{"functionCall": fn})
	}
	if sig := geminiReplySignature(req, t, id); sig != "" {
		out.attachSignature(sig, firstCall)
	}
	return out, nil
}

func geminiReplySignature(req Request, t Turn, id string) string {
	if t.Reasoning.Signature != "" {
		return t.Reasoning.Signature
	}
	if geminiRequiresSignatures(req.Model) && (len(t.Tools) != 0 || t.Reasoning.Text != "") {
		return base64.StdEncoding.EncodeToString([]byte(id)) // Synthetic signature.
	}
	return ""
}

func (c *geminiReply) attachSignature(signature string, firstCall int) {
	if len(c.parts) == 0 {
		c.parts = append(c.parts, googleObject{"text": ""})
	}
	i := len(c.parts) - 1
	if strings.HasPrefix(c.model, "gemini-2.5-") {
		i = 0
	} else if firstCall < len(c.parts) {
		i = firstCall
	}
	c.parts[i].(googleObject)["thoughtSignature"] = signature
}

func (c geminiReply) envelope(parts []any, final bool) googleObject {
	candidate := googleObject{"index": 0}
	if parts != nil {
		candidate["content"] = googleObject{"role": "model", "parts": parts}
	}
	out := googleObject{"candidates": []googleObject{candidate}, "modelVersion": c.model, "responseId": c.id}
	if final {
		candidate["finishReason"] = c.reason
		out["usageMetadata"] = googleObject{
			"promptTokenCount": c.usage.Input, "cachedContentTokenCount": c.usage.Cached,
			"candidatesTokenCount": c.usage.Output - c.usage.Reasoning, "thoughtsTokenCount": c.usage.Reasoning,
			"totalTokenCount": c.usage.Input + c.usage.Output,
		}
	}
	return out
}

func (c geminiReply) events(save func()) iter.Seq[frame] {
	return func(yield func(frame) bool) {
		// Synthetic chunk boundaries: each part is atomic, followed by final
		// candidate/usage metadata. SSE has no [DONE] sentinel on this wire.
		for _, part := range c.parts {
			if !yield(frame{data: jsonBytes(c.envelope([]any{part}, false))}) {
				return
			}
		}
		save()
		yield(frame{data: jsonBytes(c.envelope(nil, true))})
	}
}

func geminiRequiresSignatures(model string) bool {
	return strings.HasPrefix(model, "gemini-3") || strings.HasPrefix(model, "gemini-4")
}

func firstGeminiCall(content googleObject) (googleObject, int) {
	for i, value := range googleArray(content["parts"]) {
		part, _ := value.(googleObject)
		if googleObjectAt(part, "functionCall") != nil {
			return part, i
		}
	}
	return nil, -1
}

func (g *geminiWire) rememberSignature(model string, history, parts []any) {
	if !geminiRequiresSignatures(model) {
		return
	}
	part, _ := firstGeminiCall(googleObject{"parts": parts})
	if part == nil {
		return
	}
	key := geminiCallKey{model, string(jsonBytes(history)), string(jsonBytes(part["functionCall"]))}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.signatures == nil {
		g.signatures = make(map[geminiCallKey]map[string]bool)
	}
	known := g.signatures[key]
	if known == nil {
		known = make(map[string]bool)
		g.signatures[key] = known
	}
	known[googleString(part, "thoughtSignature")] = true
}

// Only function-call steps in the current turn require validation on Gemini
// 3 and 4. Old turns and ordinary response text do not have this requirement.
// https://aistudio.google.com/docs/thought-signatures?generate-content=true
func (g *geminiWire) validateSignatures(model string, history []any) error {
	if !geminiRequiresSignatures(model) {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	for i := len(history) - 1; i >= 0; i-- {
		content, _ := history[i].(googleObject)
		role := googleString(content, "role")
		if (role == "user" || role == "") && geminiStartsTurn(content) {
			break
		}
		if role != "model" {
			continue
		}
		part, at := firstGeminiCall(content)
		if part == nil {
			continue
		}
		sig := googleString(part, "thoughtSignature")
		path := indexedGooglePath(indexedGooglePath("/contents", i)+"/parts", at) + "/thoughtSignature"
		if sig == "" {
			return fmt.Errorf("%s: function call %s is missing a thought_signature", path, googleString(googleObjectAt(part, "functionCall"), "name"))
		}
		if sig == "context_engineering_is_the_way_to_go" || sig == "skip_thought_signature_validator" {
			continue // Documented migration placeholders for externally generated calls.
		}
		// Synthetic replay verification for locally issued calls. The simulator
		// cannot authenticate a signature from a real provider or another test.
		key := geminiCallKey{model, string(jsonBytes(history[:i])), string(jsonBytes(part["functionCall"]))}
		if known := g.signatures[key]; len(known) != 0 && !known[sig] {
			return fmt.Errorf("%s: invalid thought signature", path)
		}
	}
	return nil
}

func geminiStartsTurn(content googleObject) bool {
	for _, value := range googleArray(content["parts"]) {
		part, _ := value.(googleObject)
		if part["text"] != nil || part["fileData"] != nil || part["inlineData"] != nil {
			return true
		}
	}
	return false
}
