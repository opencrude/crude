package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
)

const (
	nativeWindowItem     = "compaction_window"
	compactBeta          = "compact-2026-09-04"
	maxNativeWindowBytes = 256 << 10 // Never replay a proxy's enormous retained raw tail.
)

// A wire returning a compaction item is not necessarily able to continue from
// it. The complete returned window is one indivisible unit for verification.
type compactWire interface {
	compactBody(req *ChatRequest, directive string, extras map[string]any) (map[string]any, error)
	compactResult(body []byte) (nativeWindow, TokenUsage, error)
}
type nativeWindow struct {
	raw  json.RawMessage
	text string
}

var errIgnoredCompaction = errors.New("the provider answered normally instead of compacting")
var errNoWindow = errors.New("the provider did not return a usable compaction item")
var errUnusableCheckpoint = errors.New("the provider returned a checkpoint that Crude cannot use")

// errCheckpointOutputLimit is a checkpoint read-back that stopped at its output
// allowance. The native operation itself worked, so this is not a refusal.
var errCheckpointOutputLimit = errors.New("the provider checkpoint reached its output limit")

const nativeOptionsReason = "Native compaction cannot keep this account's request options."

// nativeStandard is the item format a wire replays: the Responses family
// shares one, so a window is stamped by format rather than by route.
func nativeStandard(proto Protocol) string {
	if proto.Info().Options.NativeCompaction == "responses/compact" {
		return "openai-responses"
	}
	return proto.Info().ID
}

// compactBody sends the window exactly as the conversation renders it, so
// the system message and history keep their cached prefix. OpenAI documents
// that instructions are omitted on this call; the portable handoff is
// requested from the returned window instead.
func (s *OpenAIResponsesStandard) compactBody(req *ChatRequest, _ string, extras map[string]any) (map[string]any, error) {
	plan := PlanResponseContinuation(req)
	input, err := encodeResponsesInput(req.Messages, plan.Start)
	if err != nil {
		return nil, err
	}
	wire := map[string]any{"model": req.Model, "input": input}
	if plan.PreviousResponseID != "" {
		wire["previous_response_id"] = plan.PreviousResponseID
	}
	for _, k := range []string{"provider", "service_tier", "prompt_cache_key", "prompt_cache_retention", "prompt_cache_options"} {
		if v, ok := extras[k]; ok {
			wire[k] = v
		}
	}
	return wire, nil
}
func (s *OpenAIResponsesStandard) compactResult(body []byte) (nativeWindow, TokenUsage, error) {
	return responsesWindow(body, "response.compaction")
}

// The Responses create API also accepts an explicit final compaction trigger.
// Some gateways mount create but not /compact. Never send both real histories.
// The request is the wire's own create request, so a backend that only
// streams (the ChatGPT Codex backend) still receives a valid one.
type triggerWire struct{ enc Protocol }

func (s *triggerWire) compactBody(req *ChatRequest, directive string, extras map[string]any) (map[string]any, error) {
	b, err := s.enc.EncodeRequest(req)
	if err != nil {
		return nil, err
	}
	wire, err := decodeJSONObject(b)
	if err != nil {
		return nil, err
	}
	// A trigger requests a compaction item, not a text reply. Let the
	// service size that item as it does for /compact. A small probe or
	// read-back allowance can be invalid here even when both calls work.
	delete(wire, "max_output_tokens")
	mergeExtras(wire, extras, nil)
	input, _ := wire["input"].([]any)
	wire["input"] = append(input, map[string]any{"type": "compaction_trigger"})
	wire["store"] = false
	// A backend that requires top-level instructions gets the directive
	// when a synthetic check has no system message of its own.
	if v, ok := wire["instructions"].(string); ok && v == "" {
		wire["instructions"] = directive
	}
	return wire, nil
}

// compactResult keeps only the compaction item: the trigger's other output
// is a reply to the old window, not part of the next one.
func (s *triggerWire) compactResult(body []byte) (nativeWindow, TokenUsage, error) {
	w, u, err := responsesWindow(body, "response")
	if err != nil {
		return w, u, err
	}
	var items []json.RawMessage
	if json.Unmarshal(w.raw, &items) != nil {
		return nativeWindow{}, u, errNoWindow
	}
	var kept []json.RawMessage
	for _, raw := range items {
		var item struct{ Type string }
		if json.Unmarshal(raw, &item) == nil && item.Type == "compaction" {
			kept = append(kept, raw)
		}
	}
	if len(kept) != 1 {
		return nativeWindow{}, u, errNoWindow
	}
	w.raw, _ = json.Marshal(kept)
	return w, u, nil
}
func responsesWindow(body []byte, object string) (nativeWindow, TokenUsage, error) {
	var v struct {
		Status      string
		Object      string
		Output      json.RawMessage
		Usage       *openAIResponsesUsage `json:"usage"`
		ServiceTier string                `json:"service_tier"`
	}
	var u TokenUsage
	if json.Unmarshal(body, &v) != nil {
		return nativeWindow{}, u, errNoWindow
	}
	if w := v.Usage; w != nil {
		u = (&ChatUsage{PromptTokens: w.InputTokens, CompletionTokens: w.OutputTokens, PromptTokensDetails: w.InputTokensDetails, CompletionTokensDetails: w.OutputTokensDetails}).Normalized()
		u.ServiceTier = v.ServiceTier
	}
	if v.Object != object || object == "response" && v.Status != "completed" || len(v.Output) > maxNativeWindowBytes {
		return nativeWindow{}, u, errNoWindow
	}
	var raws []json.RawMessage
	if json.Unmarshal(v.Output, &raws) != nil {
		return nativeWindow{}, u, errNoWindow
	}
	// A window may retain the instructions it was given ahead of the summary.
	// The next request supplies the current system message itself.
	for len(raws) > 0 {
		var lead struct{ Type, Role string }
		if json.Unmarshal(raws[0], &lead) != nil || lead.Type != "message" && lead.Type != "" || lead.Role != "system" && lead.Role != "developer" {
			break
		}
		raws = raws[1:]
	}
	items := make([]struct {
		Type      string
		Encrypted string `json:"encrypted_content"`
		Role      string
	}, len(raws))
	for i, raw := range raws {
		if json.Unmarshal(raw, &items[i]) != nil {
			return nativeWindow{}, u, errNoWindow
		}
	}
	found := false
	for _, i := range items {
		switch i.Type {
		case "compaction", "reasoning", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output":
		case "message", "":
			if i.Role != "assistant" && i.Role != "user" {
				return nativeWindow{}, u, errNoWindow
			}
		default:
			return nativeWindow{}, u, errNoWindow
		}
		if i.Type == "compaction" {
			if strings.TrimSpace(i.Encrypted) == "" {
				return nativeWindow{}, u, errNoWindow
			}
			found = true
		}
		if i.Role == "system" || i.Role == "developer" {
			return nativeWindow{}, u, errNoWindow
		}
	}
	if !found {
		return nativeWindow{}, u, errNoWindow
	}
	raw, err := json.Marshal(raws)
	if err != nil {
		return nativeWindow{}, u, errNoWindow
	}
	return nativeWindow{raw: raw}, u, nil
}
func (s *AnthropicStandard) compactBody(req *ChatRequest, directive string, extras map[string]any) (map[string]any, error) {
	body, err := s.EncodeRequest(req)
	if err != nil {
		return nil, err
	}
	wire, err := decodeJSONObject(body)
	if err != nil {
		return nil, err
	}
	mergeExtras(wire, extras, nil)
	wire["compaction"] = map[string]any{"type": "summarize", "instructions": directive}
	return wire, nil
}
func (s *AnthropicStandard) compactResult(body []byte) (nativeWindow, TokenUsage, error) {
	var r struct {
		Type, Role string
		Stop       string `json:"stop_reason"`
		Content    []json.RawMessage
		Usage      json.RawMessage
	}
	var u TokenUsage
	if json.Unmarshal(body, &r) != nil {
		return nativeWindow{}, u, errNoWindow
	}
	var met struct {
		anthropicUsage
		Iterations []anthropicUsage `json:"iterations"`
	}
	if len(r.Usage) > 0 && json.Unmarshal(r.Usage, &met) == nil {
		if len(met.Iterations) == 0 {
			u = met.tokenUsage()
		} else {
			for _, it := range met.Iterations {
				u = u.Add(it.tokenUsage())
			}
			// Iterations share this request's service tier. Their token counts,
			// not the outer totals, account for the compaction work.
			if tier := met.servedTier(); tier != "" {
				u.ServiceTier = tier
			}
		}
	}
	if r.Type != "message" || r.Role != "assistant" || r.Stop != "compaction" || len(r.Content) != 1 || len(r.Content[0]) > maxNativeWindowBytes {
		return nativeWindow{}, u, errNoWindow
	}
	var b struct{ Type, Content, Signature string }
	if json.Unmarshal(r.Content[0], &b) != nil || b.Type != "compaction" || strings.TrimSpace(b.Content) == "" || strings.TrimSpace(b.Signature) == "" {
		return nativeWindow{}, u, errNoWindow
	}
	return nativeWindow{raw: r.Content[0], text: b.Content}, u, nil
}

func nativeItem(m ChatMessage, wire string) *Item {
	if !m.nativeReplay || m.Origin != OriginCheckpoint {
		return nil
	}
	for i := range m.ProviderItems {
		item := &m.ProviderItems[i]
		if item.Type == nativeWindowItem && item.Standard == wire {
			return item
		}
	}
	return nil
}
func nativeMessage(p *Provider, model string, w nativeWindow, portable string, system ChatMessage) ChatMessage {
	msg := NewTextMessage("user", portable)
	msg.Origin, msg.Visibility = OriginCheckpoint, VisibilityContextOnly
	msg.InternalID = NewMessageID()
	msg.ReplayScope = p.ReplayScope()
	msg.ReplayPrefixHash = MessagePrefixHash([]ChatMessage{system})
	msg.ReplaySourceModel = model
	msg.ProviderItems = []Item{{Standard: nativeStandard(p.Protocol()), Type: nativeWindowItem, Data: bytes.Clone(w.raw), ReplayScope: p.compactionScope(model), SourceModel: model, PrefixHash: msg.ReplayPrefixHash}}
	return msg
}
func compactRejection(err error) bool {
	var s *StatusError
	if !errors.As(err, &s) {
		return false
	}
	m := strings.ToLower(s.Message + " " + s.Param + " " + s.APICode)
	return (s.StatusCode == 400 || s.StatusCode == 404 || s.StatusCode == 422) && strings.Contains(m, "compact") && (strings.Contains(m, "not support") || strings.Contains(m, "unsupported") || strings.Contains(m, "unknown") || strings.Contains(m, "not found") || strings.Contains(m, "not permitted"))
}

// nativeFailure is a user-facing sentence. Its cause is kept only when the
// result is uncertain, so run recovery retries an outage like any request but
// does not repeat the route's definite answer.
type nativeFailure struct {
	text  string
	cause error
}

func (e *nativeFailure) Error() string { return e.text }
func (e *nativeFailure) Unwrap() error { return e.cause }

func newNativeFailure(err error) *nativeFailure {
	var s *StatusError
	switch {
	case errors.As(err, &s) && definiteRefusal(s.StatusCode) && s.StatusCode != http.StatusTooManyRequests:
		return &nativeFailure{text: fmt.Sprintf("The provider rejected native compaction (HTTP %d).", s.StatusCode)}
	case errors.As(err, &s):
		return &nativeFailure{text: fmt.Sprintf("The provider did not confirm native compaction (HTTP %d).", s.StatusCode), cause: err}
	case errors.Is(err, errUnusableCheckpoint):
		return &nativeFailure{text: "The provider returned a checkpoint that Crude cannot use."}
	case errors.Is(err, errCheckpointOutputLimit):
		return &nativeFailure{text: "The provider checkpoint reached its output limit."}
	case errors.Is(err, errIgnoredCompaction), errors.Is(err, errNoWindow):
		return &nativeFailure{text: "The provider did not return a native checkpoint."}
	default:
		return &nativeFailure{text: "The provider did not confirm native compaction.", cause: err}
	}
}

// ErrCheckpointReplayNeedsCompaction prevents a large cold upload behind a replaced checkpoint.
var ErrCheckpointReplayNeedsCompaction = errors.New("the provider rejected its checkpoint. Compact the conversation before continuing")

func (p *Provider) rejectedCheckpoint(msg ChatMessage, item Item) bool {
	cache := currentSupport()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.attempts[attemptKey{item.ReplayScope, "replay:" + msg.InternalID, p.AccountID}] != 0
}

// PortableReplayTooLarge reports whether replacing a refused artifact would replay
// a large suffix cold. Discovery expiry and transient outages never change a prefix.
func (p *Provider) PortableReplayTooLarge(msgs, projected []ChatMessage) bool {
	for i, m := range msgs[:min(2, len(msgs))] {
		if m.Origin != OriginCheckpoint {
			continue
		}
		for _, item := range m.ProviderItems {
			if item.Type == nativeWindowItem && item.ReplayScope == p.compactionScope(p.Model) && p.rejectedCheckpoint(m, item) {
				for _, m := range projected[i+1:] {
					if m.Role == "assistant" && m.ReplayScope == p.ReplayScope() && m.ReplayPrefixHash != "" {
						return false
					}
				}
				return EstimateMessagesTotal(msgs[i+1:], p.Model) > 8192
			}
		}
	}
	return false
}

// RecoverNativeReplay handles only an explicit, pre-output invalid-artifact
// rejection. The caller retries the portable checkpoint, never raw history.
func (p *Provider) RecoverNativeReplay(messages []ChatMessage, cause error) ([]ChatMessage, bool) {
	var status *StatusError
	if !errors.As(cause, &status) || (status.StatusCode != 400 && status.StatusCode != 422) {
		return nil, false
	}
	m := strings.ToLower(status.Param + " " + status.Message)
	namesItem := strings.Contains(m, "compaction") || strings.Contains(m, "encrypted_content")
	rejected := strings.Contains(m, "invalid") || strings.Contains(m, "unsupported") || strings.Contains(m, "not support") || strings.Contains(m, "signature")
	if status.APICode != "invalid_encrypted_content" && (!namesItem || !rejected) {
		return nil, false
	}
	for i, msg := range messages {
		item := nativeItem(msg, nativeStandard(p.Protocol()))
		if item == nil || msg.ContentString() == "" {
			continue
		}
		// Fence this late rejection against account removal with the same lock
		// order as account replacement (account, then evidence).
		p.authMu.RLock()
		binding := p.binding
		p.authMu.RUnlock()
		if binding != nil {
			binding.manager.mu.RLock()
			defer binding.manager.mu.RUnlock()
			if binding.manager.accounts[binding.id] != binding.slot {
				return nil, false
			}
		}
		out := append([]ChatMessage(nil), messages...)
		out[i].ProviderItems = nil
		out[i].nativeReplay = false
		// Do not create evidence after a concurrent forget or overwrite a new probe.
		c := currentSupport()
		// This exact checkpoint is bad. Do not discard other sessions' warm
		// prefixes when the route is cooling down, forgotten, or reverified.
		k := attemptKey{item.ReplayScope, "replay:" + msg.InternalID, p.AccountID}
		c.mu.Lock()
		if c.attempts == nil {
			c.attempts = make(map[attemptKey]int64)
		}
		c.attempts[k] = math.MaxInt64
		if c.db != nil {
			_, _ = c.db.Exec(`INSERT OR REPLACE INTO compaction_attempts(scope,source,account,expires) VALUES(?,?,?,?)`, k.scope, k.source, k.account, int64(math.MaxInt64))
		}
		c.mu.Unlock()
		if f, owner := c.begin(context.Background(), item.ReplayScope, p.AccountID); owner {
			if prior := c.fact(item.ReplayScope); prior.usable() {
				_ = c.record(item.ReplayScope, f, p, p.Model, supportCooling, prior.method)
			}
			c.end(item.ReplayScope, f)
		}
		return out, true
	}
	return nil, false
}
