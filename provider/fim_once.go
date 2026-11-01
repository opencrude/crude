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
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// FIMDialect is an explicitly selected wire shape, not endpoint discovery.
type FIMDialect string

const (
	FIMOpenAI    FIMDialect = "openai-completions"
	FIMDeepSeek  FIMDialect = "deepseek-fim"
	FIMCodestral FIMDialect = "codestral-fim"

	fimInputBytes = 256 << 10
	fimTextBytes  = 64 << 10
	fimEventBytes = 32 << 10
	fimTotalBytes = 1 << 20
	fimEventCount = 2048
	fimDuration   = 60 * time.Second
)

// EndpointPath is mandatory and mount-relative: "completions" and "/completions"
// both append to Client's resolved API base using joinPath. A bare origin gets
// the configured OpenAI protocol's /v1 root; an existing base mount is preserved.
// No path is inferred from Dialect. Percent escapes (including benign ones),
// empty/dot segments, URLs, query, fragment and backslashes are refused.
type FIMOnceRequest struct {
	Dialect                         FIMDialect
	EndpointPath, Model             string
	Prefix, Suffix                  string
	MaxOutputTokens, MaxOutputBytes int
}

// FIMOnceResult retains observations on error. Accepted is positive HTTP
// admission only, not billing/no-effect/remote-compute evidence. Nil Usage is
// unreported; partial Usage has unknown fields as Go zeroes. UsageComplete
// requires explicit counts for BOTH fields and a successful terminal stream.
type FIMOnceResult struct {
	Text          string
	Usage         *TokenUsage
	UsageComplete bool
	FinishReason  string
	Accepted      bool
}

// CompleteFIMOnce sends one POST: no retry, redirect, discovery or chat fallback.
// Only built-in OpenAI-compatible non-OAuth auth and SSE are supported. Account
// headers, transport policy, timeout and progress watchdog are retained, with a
// hard 60-second ceiling. Success requires stop/length AND [DONE]. Closing joins
// this local reader, not remote inference. Caller owns privacy/budget/generations.
// Wire: api-docs.deepseek.com/api/create-completion, docs.mistral.ai/api/endpoint/fim,
// official OpenAI completion schema. Codestral uses delta.content; others text.
func (p *Provider) CompleteFIMOnce(ctx context.Context, req FIMOnceRequest) (result FIMOnceResult, err error) {
	if err = p.validateFIM(req); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	c, err := p.Client(WithMaxAttempts(1), WithResponseLimit(fimTotalBytes))
	if err != nil {
		return result, err
	}
	hc := *c.hc
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.hc = &hc
	ctx, hardCancel := context.WithTimeout(ctx, fimDuration)
	defer hardCancel()
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	wire := map[string]any{
		"model": req.Model, "prompt": req.Prefix, "suffix": req.Suffix,
		"max_tokens": req.MaxOutputTokens, "stream": true,
	}
	if req.Dialect != FIMCodestral {
		wire["stream_options"] = map[string]bool{"include_usage": true}
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return result, err
	}
	resp, err := c.do(ctx, http.MethodPost, joinPath(c.base, req.EndpointPath), body)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, readStatusError(resp, fimEventBytes)
	}
	result.Accepted = true
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "text/event-stream" {
		return result, fmt.Errorf("FIM requires text/event-stream")
	}
	limited := &io.LimitedReader{R: resp.Body, N: fimTotalBytes + 1}
	reader := NewSSEReader(limited)
	reader.SetMaxEventBytes(fimEventBytes)
	var inputKnown, outputKnown bool
	for count := 0; count < fimEventCount; count++ {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		event, readErr := reader.Next()
		if limited.N <= 0 {
			return result, fmt.Errorf("%w: FIM total bytes", ErrResponseTooLarge)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				readErr = io.ErrUnexpectedEOF
			}
			return result, readErr
		}
		if event.Event != "" && event.Event != "message" {
			return result, fmt.Errorf("unexpected FIM event %q", event.Event)
		}
		if bytes.Equal(event.Data, []byte("[DONE]")) {
			if result.FinishReason != "stop" && result.FinishReason != "length" {
				return result, fmt.Errorf("FIM terminator without successful finish")
			}
			if err = ctx.Err(); err != nil {
				return result, err
			}
			result.UsageComplete = inputKnown && outputKnown
			return result, nil
		}
		if err = consumeFIMEvent(event.Data, req, &result, &inputKnown, &outputKnown); err != nil {
			return result, err
		}
	}
	return result, fmt.Errorf("%w: FIM event count", ErrResponseTooLarge)
}

func (p *Provider) validateFIM(req FIMOnceRequest) error {
	if p == nil {
		return fmt.Errorf("FIM provider is nil")
	}
	switch req.Dialect {
	case FIMOpenAI, FIMDeepSeek, FIMCodestral:
	default:
		return fmt.Errorf("unsupported explicit FIM dialect")
	}
	path := req.EndpointPath
	if len(path) == 0 || len(path) > 4096 || strings.ContainsAny(path, "\\%?#:@") ||
		strings.HasPrefix(path, "//") || strings.TrimSpace(path) != path || !utf8.ValidString(path) {
		return fmt.Errorf("invalid explicit FIM endpoint path")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("invalid FIM path segment")
		}
	}
	for _, r := range path {
		if r <= ' ' || r == 127 {
			return fmt.Errorf("invalid FIM path control")
		}
	}
	if strings.TrimSpace(req.Model) == "" || len(req.Model) > 4096 || !fimLiteral(req.Model) {
		return fmt.Errorf("FIM requires a literal model")
	}
	if len(req.Prefix) > fimInputBytes || len(req.Suffix) > fimInputBytes-len(req.Prefix) ||
		len(req.Prefix)+len(req.Suffix) == 0 || !fimLiteral(req.Prefix) || !fimLiteral(req.Suffix) {
		return fmt.Errorf("FIM input must be bounded literal UTF-8")
	}
	if req.MaxOutputTokens <= 0 || req.MaxOutputTokens > 4096 ||
		req.MaxOutputBytes <= 0 || req.MaxOutputBytes > fimTextBytes {
		return fmt.Errorf("invalid FIM output bounds")
	}
	if p.StandardName != "" && !isKnownProtocolID(p.StandardName) {
		return fmt.Errorf("unknown FIM account protocol")
	}
	if _, ok := p.Protocol().(*OpenAIStandard); !ok {
		return fmt.Errorf("FIM requires built-in OpenAI-compatible account wire")
	}
	base, err := url.Parse(strings.TrimSpace(p.BaseURL))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" ||
		base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" ||
		strings.ContainsAny(base.Path, "\\%") || base.RawPath != "" {
		return fmt.Errorf("invalid FIM account base URL")
	}
	for _, segment := range strings.Split(base.Path, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("invalid FIM base traversal")
		}
	}
	switch p.authType {
	case "", AuthProtocol, AuthBearer, AuthCustomHeader, AuthNone:
	default:
		return fmt.Errorf("unsupported FIM authentication")
	}
	p.authMu.RLock()
	binding, oauth := p.binding, p.oauth
	p.authMu.RUnlock()
	if binding != nil {
		account, snapshotErr := binding.snapshot()
		if snapshotErr != nil {
			return snapshotErr
		}
		oauth = account.OAuth
	}
	if oauth != nil {
		return fmt.Errorf("FIM does not support OAuth accounts or refresh")
	}
	if len(p.extraBody) != 0 || p.protocolSettings != nil || p.thinkingEffort != "" ||
		p.sampling.Temperature != nil || p.sampling.TopP != nil || len(p.requestExtras()) != 0 {
		return fmt.Errorf("FIM refuses configured request extras and chat overrides")
	}
	for key := range p.headers {
		// Idempotency keys authorize net/http replay on stale pooled connections.
		if strings.EqualFold(key, "Idempotency-Key") || strings.EqualFold(key, "X-Idempotency-Key") {
			return fmt.Errorf("FIM refuses transport replay headers")
		}
	}
	return nil
}

func fimLiteral(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if (r < 32 && r != '\n' && r != '\r' && r != '\t') || r == 127 {
			return false
		}
	}
	return true
}

var errFIMFormat = errors.New("malformed or unsupported FIM response fields")

func consumeFIMEvent(data []byte, req FIMOnceRequest, result *FIMOnceResult, inputKnown, outputKnown *bool) error {
	if err := validateFIMJSON(data); err != nil {
		return err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return errFIMFormat
	}
	if raw := obj["usage"]; raw != nil && string(raw) != "null" {
		var usage map[string]json.RawMessage
		if err := json.Unmarshal(raw, &usage); err != nil || usage == nil {
			return errFIMFormat
		}
		for _, field := range []struct {
			key   string
			known *bool
			input bool
		}{{"prompt_tokens", inputKnown, true}, {"completion_tokens", outputKnown, false}} {
			if value, present := usage[field.key]; present {
				n, err := strconv.Atoi(string(value))
				if err != nil || n < 0 {
					return fmt.Errorf("invalid FIM %s", field.key)
				}
				if result.Usage == nil {
					result.Usage = &TokenUsage{}
				}
				if field.input {
					result.Usage.PromptTokens = n
				} else {
					result.Usage.CompletionTokens = n
				}
				*field.known = true
			}
		}
	}
	if err := fimKeys(obj, "id", "object", "created", "model", "system_fingerprint", "choices", "usage"); err != nil {
		return err
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(obj["choices"], &choices); err != nil || choices == nil || len(choices) > 1 {
		return errFIMFormat
	}
	if len(choices) == 0 {
		if obj["usage"] == nil || string(obj["usage"]) == "null" {
			return errFIMFormat
		}
		return nil
	}
	choice := choices[0]
	var index int
	if raw, ok := choice["index"]; !ok || json.Unmarshal(raw, &index) != nil || string(raw) == "null" || index != 0 {
		return errFIMFormat
	}
	var text string
	if req.Dialect == FIMCodestral {
		if err := fimKeys(choice, "index", "delta", "finish_reason", "logprobs", "prompt_logprobs"); err != nil {
			return err
		}
		var delta map[string]json.RawMessage
		if json.Unmarshal(choice["delta"], &delta) != nil || delta == nil {
			return errFIMFormat
		}
		if err := fimKeys(delta, "role", "content", "tool_calls"); err != nil {
			return err
		}
		if raw := delta["tool_calls"]; raw != nil && string(raw) != "null" {
			return errFIMFormat
		}
		if raw := delta["role"]; raw != nil && string(raw) != "null" && string(raw) != `"assistant"` {
			return errFIMFormat
		}
		if raw := delta["content"]; raw != nil && string(raw) != "null" {
			if json.Unmarshal(raw, &text) != nil {
				return errFIMFormat
			}
		}
	} else {
		if err := fimKeys(choice, "index", "text", "finish_reason", "logprobs"); err != nil {
			return err
		}
		raw := choice["text"]
		if raw == nil || string(raw) == "null" || json.Unmarshal(raw, &text) != nil {
			return errFIMFormat
		}
	}
	for _, key := range []string{"logprobs", "prompt_logprobs"} {
		if raw := choice[key]; raw != nil && string(raw) != "null" {
			return errFIMFormat
		}
	}
	if !fimLiteral(text) {
		return errFIMFormat
	}
	if result.FinishReason != "" {
		return errFIMFormat
	}
	if len(text) > req.MaxOutputBytes-len(result.Text) {
		n := req.MaxOutputBytes - len(result.Text)
		for n > 0 && !utf8.RuneStart(text[n]) {
			n--
		}
		result.Text += text[:n]
		return fmt.Errorf("%w: FIM output bytes", ErrResponseTooLarge)
	}
	result.Text += text
	raw, present := choice["finish_reason"]
	if !present {
		return errFIMFormat
	}
	if string(raw) != "null" {
		if json.Unmarshal(raw, &result.FinishReason) != nil || result.FinishReason == "" {
			return errFIMFormat
		}
		if result.FinishReason != "stop" && result.FinishReason != "length" {
			return fmt.Errorf("unsuccessful FIM finish %q", result.FinishReason)
		}
	}
	return nil
}

func fimKeys(obj map[string]json.RawMessage, allowed ...string) error {
	for key := range obj {
		found := false
		for _, candidate := range allowed {
			found = found || key == candidate
		}
		if !found {
			return fmt.Errorf("unexpected FIM field %q", key)
		}
	}
	return nil
}

// Bounded FIM JSON only: reject duplicate keys and Unicode repair before decode.
func validateFIMJSON(data []byte) error {
	if !utf8.Valid(data) || !json.Valid(data) {
		return fmt.Errorf("invalid FIM JSON/UTF-8")
	}
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if data[i] != 'u' {
			continue
		}
		code, _ := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return fmt.Errorf("unpaired FIM JSON surrogate")
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return fmt.Errorf("unpaired FIM JSON surrogate")
			}
			low, _ := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if low < 0xdc00 || low > 0xdfff {
				return fmt.Errorf("unpaired FIM JSON surrogate")
			}
			i += 6
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return fimJSONValue(decoder, 0)
}

func fimJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 16 {
		return fmt.Errorf("FIM JSON nesting exceeds limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	keys := map[string]bool{}
	for decoder.More() {
		if delim == '{' {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || keys[key] {
				return fmt.Errorf("duplicate FIM JSON key")
			}
			keys[key] = true
		}
		if err := fimJSONValue(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
