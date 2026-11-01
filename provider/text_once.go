package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	TextOnceMaxSystemBytes  = 16 << 10
	TextOnceMaxInputBytes   = 256 << 10
	TextOnceMaxOutputBytes  = 64 << 10
	TextOnceMaxOutputTokens = 4096
	onceEventBytes          = 32 << 10
	onceWireBytes           = 1 << 20
	onceEvents              = 2048
	onceDepth               = 24
	onceDuration            = 60 * time.Second
)

// TextOnceRequest is literal UTF-8 input; System is optional.
type TextOnceRequest struct {
	Model, System, Input            string
	MaxOutputTokens, MaxOutputBytes int
}

// TextOnceResult retains observations on error; only nil error makes Text usable.
// Nil Usage is unreported; unknown scalars are Go zeroes. UsageComplete requires
// BOTH original integer counters and a successful terminal, never inferred zero.
// Accepted is positive HTTP evidence, not billing or remote-compute join proof.
type TextOnceResult struct {
	Text          string
	Usage         *TokenUsage
	FinishReason  string
	Accepted      bool
	UsageComplete bool
}

// CompleteTextOnce sends one no-tools POST: no retry/redirect/fallback. Only
// built-in non-OAuth OpenAI Chat/Responses SSE text is supported. Client owns
// account/transport/timeout/progress policy. Limits above include raw ignored SSE
// fields and CRLF. Close/cancel joins the local body, not remote inference.
func (p *Provider) CompleteTextOnce(ctx context.Context, r TextOnceRequest) (result TextOnceResult, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("text once: %w", err)
		}
	}()
	if err = validateTextOnce(ctx, p, r); err != nil {
		return result, err
	}
	c, err := p.Client(WithMaxAttempts(1), WithResponseLimit(onceWireBytes))
	if err != nil {
		return result, err
	}
	if err = validateTextOnceExtras(p, c, r.Model); err != nil {
		return result, err
	}
	hc := *c.hc
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.hc = &hc
	source := c.headerSource
	c.headerSource = func(ctx context.Context) (http.Header, error) {
		if err := onceAuth(p); err != nil {
			return nil, err
		}
		h, err := source(ctx)
		for key := range h {
			if onceReplayHeader(key) {
				return nil, fmt.Errorf("replay header %q refused", key)
			}
		}
		return h, err
	}
	ctx, hardCancel := context.WithTimeout(ctx, onceDuration)
	defer hardCancel()
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	messages := make([]ChatMessage, 0, 2)
	if r.System != "" {
		messages = append(messages, NewTextMessage("system", r.System))
	}
	messages = append(messages, NewTextMessage("user", r.Input))
	sampling := p.Sampling()
	body, err := c.encodeChatRequest(&ChatRequest{
		Model: r.Model, Messages: messages, ToolChoice: "none", Stream: true,
		StreamOptions: &StreamOptions{IncludeUsage: true}, MaxOutputTokens: r.MaxOutputTokens,
		Temperature: sampling.Temperature, TopP: sampling.TopP,
	})
	if err != nil {
		return result, err
	}
	resp, err := c.do(ctx, http.MethodPost, c.chatURL(), body)
	if err != nil {
		return result, err
	}
	state := onceState{result: &result, limit: r.MaxOutputBytes}
	defer func() {
		err = errors.Join(err, resp.Body.Close())
		if ctx.Err() != nil {
			err = errors.Join(err, ctx.Err())
		}
		result.Text = state.text.String()
		result.UsageComplete = err == nil && state.complete && state.usageComplete
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, readStatusError(resp, onceEventBytes)
	}
	result.Accepted = true
	media, _, parseErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if parseErr != nil || media != "text/event-stream" {
		return result, fmt.Errorf("requires text/event-stream")
	}
	wire := &onceWire{inner: resp.Body}
	reader := NewSSEReader(wire)
	reader.SetMaxEventBytes(onceEventBytes)
	_, responses := c.proto.(*OpenAIResponsesStandard)
	for count := 0; count < onceEvents; count++ {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		event, readErr := reader.Next()
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				readErr = io.ErrUnexpectedEOF
			}
			return result, readErr
		}
		if !responses && (event.Event == "" || event.Event == "message") && bytes.Equal(event.Data, []byte("[DONE]")) {
			if result.FinishReason != "stop" {
				return result, fmt.Errorf("terminator without stop")
			}
			state.complete = true
			return result, nil
		}
		fields, decodeErr := onceJSON(event.Data)
		if decodeErr != nil {
			return result, decodeErr
		}
		if responses {
			err = state.responses(event.Event, fields)
		} else if event.Event != "" && event.Event != "message" {
			err = fmt.Errorf("unsupported event %q", event.Event)
		} else {
			err = state.chat(fields)
		}
		if err != nil || state.complete {
			return result, err
		}
	}
	return result, fmt.Errorf("more than %d data events", onceEvents)
}

func validateTextOnce(ctx context.Context, p *Provider, r TextOnceRequest) error {
	if ctx == nil {
		return fmt.Errorf("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("provider is nil")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	switch p.Protocol().(type) {
	case *OpenAIStandard, *OpenAIResponsesStandard:
	default:
		return fmt.Errorf("unsupported protocol %q", p.ProtocolID())
	}
	if err := onceAuth(p); err != nil {
		return err
	}
	for key := range p.headers {
		if onceReplayHeader(key) {
			return fmt.Errorf("replay header %q refused", key)
		}
	}
	if onceReplayHeader(p.authHeader) {
		return fmt.Errorf("replay auth header refused")
	}
	if r.MaxOutputTokens < 1 || r.MaxOutputTokens > TextOnceMaxOutputTokens ||
		r.MaxOutputBytes < 1 || r.MaxOutputBytes > TextOnceMaxOutputBytes {
		return fmt.Errorf("invalid output limits")
	}
	if len(r.Model) > 1024 || strings.TrimSpace(r.Model) == "" || !utf8.ValidString(r.Model) || strings.ContainsAny(r.Model, "\x00\r\n") {
		return fmt.Errorf("invalid model")
	}
	if len(r.System) > TextOnceMaxSystemBytes || len(r.Input) > TextOnceMaxInputBytes || strings.TrimSpace(r.Input) == "" {
		return fmt.Errorf("empty or oversized prompt")
	}
	if !utf8.ValidString(r.System) || !utf8.ValidString(r.Input) || strings.ContainsRune(r.System, 0) || strings.ContainsRune(r.Input, 0) {
		return fmt.Errorf("prompt must be UTF-8 text without NUL")
	}
	return nil
}

func onceReplayHeader(key string) bool {
	key = strings.ToLower(key)
	return key == "idempotency-key" || key == "x-idempotency-key"
}

func onceAuth(p *Provider) error {
	p.authMu.RLock()
	binding, oauth := p.binding, p.oauth != nil
	p.authMu.RUnlock()
	if binding != nil {
		account, err := binding.snapshot()
		if err != nil {
			return err
		}
		oauth = account.OAuth != nil
	}
	if oauth || p.authType == AuthChatGPT {
		return fmt.Errorf("OAuth is unsupported")
	}
	return nil
}

func validateTextOnceExtras(p *Provider, c *Client, model string) error {
	if extras, ok := c.proto.(RequestExtras); ok && model != p.Model {
		var settings ProtocolSettings
		if p.protocolSettings != nil {
			settings = *p.protocolSettings
		}
		if !reflect.DeepEqual(extras.RequestExtras(model, p.thinkingEffort, settings), extras.RequestExtras(p.Model, p.thinkingEffort, settings)) {
			return fmt.Errorf("model-dependent settings require matching provider model")
		}
	}
	for key, value := range c.requestExtras {
		switch key {
		case "store":
			if _, ok := value.(bool); ok {
				continue
			}
		case "service_tier", "reasoning_effort":
			if _, ok := value.(string); ok {
				continue
			}
		case "truncation":
			if value == "auto" || value == "disabled" {
				continue
			}
		case "include":
			if reflect.DeepEqual(value, []string{"reasoning.encrypted_content"}) {
				continue
			}
		case "reasoning":
			if fields, ok := value.(map[string]any); ok {
				valid := true
				for field, setting := range fields {
					_, isString := setting.(string)
					valid = valid && (field == "effort" || field == "summary") && isString
				}
				if valid {
					continue
				}
			}
		}
		return fmt.Errorf("unsupported request extra %q", key)
	}
	return nil
}

type onceWire struct {
	inner              io.Reader
	total, event, line int
	cr, reset          bool
	runeBytes          [utf8.UTFMax]byte
	runeLen            int
}

func (w *onceWire) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	p = p[:min(len(p), onceWireBytes-w.total+1)]
	n, err := w.inner.Read(p)
	for i, b := range p[:n] {
		w.total++
		if w.reset && (!w.cr || b != '\n') {
			w.event, w.reset = 0, false
		}
		w.event++
		if w.total > onceWireBytes || w.event > onceEventBytes {
			return i, fmt.Errorf("%w: text once raw wire/event limit", ErrResponseTooLarge)
		}
		w.runeBytes[w.runeLen] = b
		w.runeLen++
		if utf8.FullRune(w.runeBytes[:w.runeLen]) {
			r, size := utf8.DecodeRune(w.runeBytes[:w.runeLen])
			if r == utf8.RuneError && size == 1 {
				return i, fmt.Errorf("invalid wire UTF-8")
			}
			w.runeLen = 0
		}
		if w.cr && b == '\n' {
			w.cr = false
			if w.reset {
				w.event, w.reset = 0, false
			}
			continue
		}
		w.cr = b == '\r'
		if b == '\r' || b == '\n' {
			if w.line == 0 {
				if w.cr {
					w.reset = true
				} else {
					w.event = 0
				}
			}
			w.line = 0
		} else {
			w.line++
		}
	}
	if errors.Is(err, io.EOF) && (w.runeLen != 0 || (w.event != 0 && !w.reset)) {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func onceJSON(raw []byte) (map[string]any, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON UTF-8")
	}
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
		} else if inString && raw[i] == '\\' {
			i++
			if i >= len(raw) {
				return nil, fmt.Errorf("truncated escape")
			}
			if raw[i] != 'u' {
				continue
			}
			if i+4 >= len(raw) {
				return nil, fmt.Errorf("truncated Unicode escape")
			}
			u, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
			if err != nil {
				return nil, err
			}
			i += 4
			if u >= 0xdc00 && u <= 0xdfff {
				return nil, fmt.Errorf("unpaired low surrogate")
			}
			if u >= 0xd800 && u <= 0xdbff {
				if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
					return nil, fmt.Errorf("unpaired high surrogate")
				}
				low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
				if err != nil || low < 0xdc00 || low > 0xdfff {
					return nil, fmt.Errorf("invalid surrogate pair")
				}
				i += 6
			}
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	value, err := onceValue(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); !errors.Is(err, io.EOF) {
		return nil, errOnceContent
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errOnceContent
	}
	return object, nil
}

func onceValue(d *json.Decoder, depth int) (any, error) {
	if depth > onceDepth {
		return nil, fmt.Errorf("JSON nesting limit")
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, errOnceContent
			}
			if _, exists := object[name]; exists {
				return nil, fmt.Errorf("duplicate field %q", name)
			}
			object[name], err = onceValue(d, depth+1)
			if err != nil {
				return nil, err
			}
		}
		_, err := d.Token()
		return object, err
	case json.Delim('['):
		var array []any
		for d.More() {
			value, err := onceValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		_, err := d.Token()
		return array, err
	case json.Delim('}'), json.Delim(']'):
		return nil, errOnceContent
	default:
		return token, nil
	}
}

var errOnceContent = errors.New("unsupported or ambiguous provider text wire")

type onceState struct {
	result                  *TextOnceResult
	text                    strings.Builder
	limit                   int
	usageComplete, complete bool
	textSeen, textDone      bool
	responseID, itemID      string
}

func (s *onceState) append(text string) error {
	remaining := s.limit - s.text.Len()
	if len(text) > remaining {
		prefix := text[:remaining]
		for !utf8.ValidString(prefix) {
			prefix = prefix[:len(prefix)-1]
		}
		s.text.WriteString(prefix)
		return fmt.Errorf("output exceeds %d bytes", s.limit)
	}
	s.text.WriteString(text)
	return nil
}

func onceKeys(m map[string]any, allowed string) error {
	for key := range m {
		if strings.Contains(key, "|") || !strings.Contains("|"+allowed+"|", "|"+key+"|") {
			return errOnceContent
		}
	}
	return nil
}

func onceInteger(value any) (int, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("expected integer token counter/index")
	}
	n, err := strconv.Atoi(string(number))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid integer token counter/index")
	}
	return n, nil
}

func onceZero(m map[string]any, keys ...string) error {
	for _, key := range keys {
		if value, exists := m[key]; exists {
			n, err := onceInteger(value)
			if err != nil || n != 0 {
				return errOnceContent
			}
		}
	}
	return nil
}

func (s *onceState) usage(m map[string]any, responses bool) error {
	value, exists := m["usage"]
	if !exists {
		return nil
	}
	s.usageComplete = false
	if value == nil {
		return nil
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return errOnceContent
	}
	u := &TokenUsage{}
	s.result.Usage = u
	input, output, inDetails, outDetails := "prompt_tokens", "completion_tokens", "prompt_tokens_details", "completion_tokens_details"
	if responses {
		input, output, inDetails, outDetails = "input_tokens", "output_tokens", "input_tokens_details", "output_tokens_details"
	}
	var inputKnown, outputKnown bool
	var scalarErr error
	for _, field := range []struct {
		key   string
		dst   *int
		known *bool
	}{{input, &u.PromptTokens, &inputKnown}, {output, &u.CompletionTokens, &outputKnown}} {
		if v, exists := fields[field.key]; exists {
			n, err := onceInteger(v)
			if err != nil {
				scalarErr = errors.Join(scalarErr, err)
			} else {
				*field.dst, *field.known = n, true
			}
		}
	}
	if scalarErr != nil {
		return scalarErr
	}
	if err := onceKeys(fields, input+"|"+output+"|"+inDetails+"|"+outDetails+"|total_tokens|prompt_cache_hit_tokens"); err != nil {
		return err
	}
	// Normalize details only AFTER original scalar-presence validation.
	wire := make(map[string]any, len(fields))
	for key, value := range fields {
		switch key {
		case input:
			key = "prompt_tokens"
		case output:
			key = "completion_tokens"
		case inDetails:
			key = "prompt_tokens_details"
		case outDetails:
			key = "completion_tokens_details"
		}
		wire[key] = value
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return err
	}
	var accounting ChatUsage
	if err := json.Unmarshal(raw, &accounting); err != nil {
		return err
	}
	usage := accounting.normalized()
	if usage.PromptTokensCached < 0 || usage.PromptTokensWritten < 0 || usage.ReasoningTokens < 0 {
		return fmt.Errorf("negative accounting detail")
	}
	if value, exists := fields["total_tokens"]; exists {
		if _, err := onceInteger(value); err != nil {
			return err
		}
	}
	usage.ServiceTier, _ = m["service_tier"].(string)
	*s.result.Usage = usage
	s.usageComplete = inputKnown && outputKnown
	return nil
}

func (s *onceState) chat(m map[string]any) error {
	if err := s.usage(m, false); err != nil {
		return err
	}
	if m["error"] != nil {
		return errOnceContent
	}
	if err := onceKeys(m, "id|object|created|model|system_fingerprint|service_tier|choices|usage|error"); err != nil {
		return err
	}
	choices, ok := m["choices"].([]any)
	if !ok || len(choices) > 1 {
		return errOnceContent
	}
	if len(choices) == 0 {
		if _, exists := m["usage"]; !exists {
			return errOnceContent
		}
		return nil
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return errOnceContent
	}
	if s.result.FinishReason != "" {
		return errOnceContent
	}
	if finish, exists := choice["finish_reason"]; exists && finish != nil {
		reason, ok := finish.(string)
		if !ok {
			return errOnceContent
		}
		s.result.FinishReason = reason
	}
	if err := onceKeys(choice, "index|delta|finish_reason|logprobs"); err != nil {
		return err
	}
	if err := onceZero(choice, "index"); err != nil {
		return err
	}
	delta, ok := choice["delta"].(map[string]any)
	if !ok {
		return errOnceContent
	}
	if err := onceKeys(delta, "role|content"); err != nil {
		return err
	}
	if role, exists := delta["role"]; exists && role != "assistant" {
		return errOnceContent
	}
	if choice["logprobs"] != nil {
		return errOnceContent
	}
	if s.result.FinishReason != "" && s.result.FinishReason != "stop" {
		return errOnceContent
	}
	if content, exists := delta["content"]; exists && content != nil {
		text, ok := content.(string)
		if !ok {
			return errOnceContent
		}
		return s.append(text)
	}
	return nil
}

func (s *onceState) identity(m map[string]any, key string, stored *string) error {
	if value, exists := m[key]; exists {
		id, ok := value.(string)
		if !ok || id == "" || (*stored != "" && *stored != id) {
			return errOnceContent
		}
		*stored = id
	}
	return nil
}

func (s *onceState) snapshot(text string) error {
	if s.textSeen {
		if s.text.String() != text {
			return errOnceContent
		}
		return nil
	}
	s.textSeen = true
	return s.append(text)
}

func oncePart(value any) (string, error) {
	part, ok := value.(map[string]any)
	if !ok || part["type"] != "output_text" {
		return "", errOnceContent
	}
	if err := onceKeys(part, "type|text|annotations|logprobs"); err != nil {
		return "", err
	}
	text, ok := part["text"].(string)
	if !ok {
		return "", errOnceContent
	}
	return text, nil
}

func (s *onceState) message(value any, placeholder bool) error {
	item, ok := value.(map[string]any)
	if !ok || item["type"] != "message" || item["role"] != "assistant" {
		return errOnceContent
	}
	if err := onceKeys(item, "id|type|role|status|content"); err != nil {
		return err
	}
	if err := s.identity(item, "id", &s.itemID); err != nil {
		return err
	}
	if status, exists := item["status"]; exists && status != "completed" && (!placeholder || status != "in_progress") {
		return errOnceContent
	}
	content, ok := item["content"].([]any)
	if !ok || len(content) > 1 || (!placeholder && len(content) != 1) {
		return errOnceContent
	}
	if len(content) == 0 {
		return nil
	}
	text, err := oncePart(content[0])
	if err != nil {
		return err
	}
	if placeholder {
		if text != "" {
			return errOnceContent
		}
		return nil
	}
	return s.snapshot(text)
}

func (s *onceState) responses(event string, m map[string]any) error {
	typ, ok := m["type"].(string)
	if !ok || (event != "" && event != typ) {
		return errOnceContent
	}
	if response, ok := m["response"].(map[string]any); ok {
		if err := s.usage(response, true); err != nil {
			return err
		}
		if err := onceKeys(response, "id|object|created_at|completed_at|status|error|incomplete_details|output|usage|service_tier|background|instructions|max_output_tokens|max_tool_calls|model|parallel_tool_calls|previous_response_id|reasoning|store|temperature|text|tool_choice|tools|top_logprobs|top_p|truncation|metadata|user|safety_identifier|prompt_cache_key|prompt_cache_retention|prompt|conversation|context_management"); err != nil {
			return err
		}
		if err := s.identity(response, "id", &s.responseID); err != nil {
			return err
		}
	}
	allowed := "type|sequence_number"
	switch typ {
	case "response.created", "response.queued", "response.in_progress", "response.completed", "response.incomplete", "response.failed", "response.cancelled":
		allowed += "|response"
	case "response.output_item.added", "response.output_item.done":
		allowed += "|output_index|item"
	case "response.content_part.added", "response.content_part.done":
		allowed += "|output_index|content_index|item_id|part"
	case "response.output_text.delta":
		allowed += "|output_index|content_index|item_id|delta|logprobs"
	case "response.output_text.done":
		allowed += "|output_index|content_index|item_id|text|logprobs"
	case "error":
		allowed += "|error|code|message|param"
	}
	if err := onceKeys(m, allowed); err != nil {
		return err
	}
	if err := onceZero(m, "output_index", "content_index"); err != nil {
		return err
	}
	if err := s.identity(m, "item_id", &s.itemID); err != nil {
		return err
	}
	switch typ {
	case "response.created", "response.queued", "response.in_progress":
		response, ok := m["response"].(map[string]any)
		if !ok || response["error"] != nil {
			return errOnceContent
		}
		output, ok := response["output"].([]any)
		if !ok || len(output) != 0 {
			return errOnceContent
		}
	case "response.output_item.added", "response.output_item.done":
		if err := s.message(m["item"], typ == "response.output_item.added"); err != nil {
			return err
		}
		s.textDone = s.textDone || typ == "response.output_item.done"
	case "response.content_part.added", "response.content_part.done":
		text, err := oncePart(m["part"])
		if err != nil {
			return err
		}
		if typ == "response.content_part.added" {
			if text != "" {
				return errOnceContent
			}
		} else if err := s.snapshot(text); err != nil {
			return err
		}
		s.textDone = s.textDone || typ == "response.content_part.done"
	case "response.output_text.delta":
		text, ok := m["delta"].(string)
		if !ok || s.textDone {
			return errOnceContent
		}
		s.textSeen = true
		return s.append(text)
	case "response.output_text.done":
		text, ok := m["text"].(string)
		if !ok {
			return errOnceContent
		}
		if err := s.snapshot(text); err != nil {
			return err
		}
		s.textDone = true
	case "response.completed", "response.incomplete", "response.failed", "response.cancelled":
		response, ok := m["response"].(map[string]any)
		if !ok {
			return errOnceContent
		}
		if typ != "response.completed" || response["status"] != "completed" || response["error"] != nil || response["incomplete_details"] != nil {
			s.result.FinishReason = strings.TrimPrefix(typ, "response.")
			return errOnceContent
		}
		s.result.FinishReason = "stop"
		output, ok := response["output"].([]any)
		if !ok || len(output) > 1 {
			return errOnceContent
		}
		if len(output) == 0 {
			if s.textSeen {
				return errOnceContent
			}
		} else if err := s.message(output[0], false); err != nil {
			return err
		}
		s.complete = true
	case "error":
		return errOnceContent
	default:
		return errOnceContent
	}
	return nil
}
