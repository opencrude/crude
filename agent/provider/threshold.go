package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
)

const thresholdMethod = "threshold"
const thresholdBeta = "compact-2026-01-12"

// BoundaryTurn sends the already pending ordinary request under the paid attempt guard.
// Only its caller owns the ordinary output, publication and tool dispatch.
type BoundaryTurn func(context.Context, ClientOption) (BoundaryReply, error)

// BoundaryReply supplies the wire evidence without transferring ownership of an answer.
type BoundaryReply struct {
	Items           []Item
	Ordinary        bool
	Usage           TokenUsage
	CheckpointUsage *TokenUsage
}

// ThresholdReceipt identifies one accepted Responses output in its original input prefix.
// It permits a bounded export after ordinary output and tool effects are checkpointed.
type ThresholdReceipt struct {
	index         int
	scope, prefix string
	generation    uint64
	seal          json.RawMessage
}

// A configured edit is route evidence, not a second schedule. Split out only
// compaction. Keep unknown fields, unrelated edits and their relative order;
// the service, not this partition, validates its current option schema.
type thresholdEdits struct {
	declared           bool
	ordinary, boundary any
	minimum            int // A planning hint, never proof an edit will fire.
}

func (p *Provider) configuredThreshold(directive string) *thresholdEdits {
	v, ok := p.extraBody["context_management"]
	if !ok {
		return nil
	}
	wire := p.ProtocolID()
	if wire != "anthropic" && wire != "openai-responses" {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil
	}
	var outer map[string]any
	list := value
	kind := "compaction"
	if wire == "anthropic" {
		outer, _ = value.(map[string]any)
		list = outer["edits"]
		kind = "compact_20260112"
	}
	all, _ := list.([]any)
	var kept []any
	result := &thresholdEdits{boundary: value}
	found := false
	for _, v := range all {
		edit, _ := v.(map[string]any)
		if edit["type"] != kind {
			kept = append(kept, v)
			continue
		}
		found = true
		n, _ := edit["compact_threshold"].(json.Number)
		floor := int64(1000)
		if wire == "anthropic" {
			floor = 50_000
			trigger, _ := edit["trigger"].(map[string]any)
			n, _ = trigger["value"].(json.Number)
			if directive != "" {
				edit["pause_after_compaction"] = true
				instruction, _ := edit["instructions"].(string)
				if instruction != "" {
					instruction += "\n\n"
				}
				edit["instructions"] = instruction + directive
			}
		}
		count, _ := n.Int64()
		hint := int(max(floor, count))
		if result.minimum == 0 || hint < result.minimum {
			result.minimum = hint
		}
	}
	if !found {
		if outer == nil {
			return nil
		}
		if entries, ok := outer["edits"]; ok && entries != nil && all == nil {
			return nil
		}
		result.ordinary = value
		result.minimum = 50_000
		if directive != "" {
			boundary := maps.Clone(outer)
			boundary["edits"] = append(all, defaultThresholdEdit(directive))
			result.boundary = boundary
		}
		return result
	}
	result.declared = true
	if outer != nil {
		ordinary := maps.Clone(outer)
		if len(kept) > 0 {
			ordinary["edits"] = kept
		} else {
			delete(ordinary, "edits")
		}
		if len(ordinary) > 0 {
			result.ordinary = ordinary
		}
	} else if len(kept) > 0 {
		result.ordinary = kept
	}
	return result
}

// Some gateways identify just the rejected parameter, not the edit.
func thresholdRefused(err error) bool {
	if compactRejection(err) {
		return true
	}
	var s *StatusError
	if !errors.As(err, &s) || (s.StatusCode != 400 && s.StatusCode != 422) {
		return false
	}
	field := strings.ToLower(s.Param + " " + s.Message)
	if !strings.Contains(field, "context_management") && !strings.Contains(field, thresholdBeta) {
		return false
	}
	return strings.Contains(field, "unknown") || strings.Contains(field, "unsupported") || strings.Contains(field, "not support") || strings.Contains(field, "unrecognized")
}
func defaultThresholdEdit(directive string) map[string]any {
	return map[string]any{"type": "compact_20260112", "trigger": map[string]any{"type": "input_tokens", "value": 50_000}, "pause_after_compaction": true, "instructions": directive}
}
func (x *compactExchange) boundaryOption() ClientOption {
	return func(c *Client) {
		*c = *withoutRedirects(c)
		c.dial.MaxAttempts = 1
		c.boundary = true
		c.requestExtras = maps.Clone(c.requestExtras)
		if c.requestExtras == nil {
			c.requestExtras = map[string]any{}
		}
		if c.proto.Info().ID == "anthropic" {
			*c = *withBetaHeader(c, thresholdBeta)
		}
		if edits := x.provider.configuredThreshold(x.request.Directive); edits != nil {
			c.requestExtras["context_management"] = edits.boundary
		} else {
			c.requestExtras["context_management"] = map[string]any{"edits": []any{defaultThresholdEdit(x.request.Directive)}}
		}

	}
}

func lastSeal(items []Item) int {
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Type == "compaction" {
			return i
		}
	}
	return -1
}

// Only the latest seal and its ordered suffix replace the original input.
// Project first: a model, account or prefix change must not export opaque data.
func (t *ThresholdReceipt) window(p *Provider, msgs []ChatMessage) (nativeWindow, error) {
	if t == nil || t.scope != p.compactionScope(p.Model) || t.index < 0 || t.index >= len(msgs) {
		return nativeWindow{}, errNoWindow
	}
	projected := FilterReplayMessages(msgs, p)
	m := projected[t.index]
	i := lastSeal(m.ProviderItems)
	if m.ReplayPrefixHash != t.prefix || i < 0 || !bytes.Equal(m.ProviderItems[i].Data, t.seal) {
		return nativeWindow{}, errNoWindow
	}
	// Keep the provider's original output order, including configuration updates.
	// Encoding only subsequent messages excludes any canonical reconstruction of
	// output that the seal itself already covered.
	var items []any
	size := 0
	for _, it := range m.ProviderItems[i:] {
		size += len(it.Data)
		if size > maxNativeWindowBytes {
			return nativeWindow{}, errNoWindow
		}
		items = append(items, it.Data)
	}
	for _, m := range projected[t.index+1:] {
		size += len(m.Content)
		if size > maxNativeWindowBytes {
			return nativeWindow{}, errNoWindow
		}
	}
	tail, err := encodeResponsesInput(projected[t.index:], 1)
	if err != nil {
		return nativeWindow{}, err
	}
	items = append(items, tail...)
	raw, err := json.Marshal(items)
	if err != nil || len(raw) > maxNativeWindowBytes {
		return nativeWindow{}, errNoWindow
	}
	return nativeWindow{raw: raw}, nil
}

func (x *compactExchange) threshold(ctx context.Context, attempt attemptKey, cache *supportCache, flight *supportFlight, fail func(error, bool) (NativeCheckpointResult, error), record func(string, error) error) (NativeCheckpointResult, error) {
	r := x.request
	if r.Boundary == nil {
		return NativeCheckpointResult{Fallback: "No model request is pending for native compaction."}, nil
	}
	minimum := 50_000
	if edits := x.provider.configuredThreshold(""); edits != nil {
		minimum = edits.minimum
	}
	if r.Explicit && r.InputTokens < minimum {
		return NativeCheckpointResult{Fallback: "The conversation is below the provider threshold."}, nil
	}
	if x.provider.ProtocolID() == "anthropic" {
		for _, m := range FilterReplayMessages(r.Messages, x.provider) {
			if nativeItem(m, "anthropic") != nil {
				return NativeCheckpointResult{Fallback: "This conversation uses a signed provider checkpoint."}, nil
			}
		}
	}
	if err := cache.guardAttempt(ctx, attempt, flight, r.Explicit, true); err != nil {
		return NativeCheckpointResult{}, err
	}
	x.stage("Compacting with the provider")
	reply, err := r.Boundary(ctx, x.boundaryOption())
	i := lastSeal(reply.Items)
	// Messages may report separate compaction and message iterations. Responses
	// has one aggregate: let the ordinary turn own it whenever it produced output.
	cost := reply.CheckpointUsage
	if cost == nil && !reply.Ordinary {
		cost = &reply.Usage
	}
	usable := false
	if cost != nil {
		defer func() { x.report("native_compaction", *cost, usable) }()
	}

	if err != nil {
		if reply.Ordinary {
			_, _ = fail(err, false)
			return NativeCheckpointResult{Turn: true}, nil
		}
		if confirmedNativeRejection(err) {
			if e := cache.releaseRejected(attempt, flight); e != nil {
				return NativeCheckpointResult{}, e
			}
		}
		if RejectedAccount(err) == nil && thresholdRefused(err) {
			if e := record(supportUnavailable, err); e != nil {
				return NativeCheckpointResult{}, e
			}
			return NativeCheckpointResult{Fallback: "The provider rejected threshold compaction.", Changed: true}, nil
		}
		return fail(err, false)
	}
	ordinary := func(settle bool) (NativeCheckpointResult, error) {
		result := NativeCheckpointResult{Turn: true}
		if settle || r.Explicit {
			result.Receipt = &ThresholdReceipt{generation: flight.generation, scope: attempt.scope, index: len(r.Messages)}
		}
		return result, nil
	}
	if i < 0 {
		if reply.Ordinary {
			return ordinary(false)
		}
		return fail(errNoWindow, false)
	}
	it := reply.Items[i]
	if x.provider.ProtocolID() == "openai-responses" {
		var block struct {
			Encrypted string `json:"encrypted_content"`
		}
		valid := len(it.Data) <= maxNativeWindowBytes && json.Unmarshal(it.Data, &block) == nil && strings.TrimSpace(block.Encrypted) != ""
		usable = valid
		if !valid {
			if reply.Ordinary {
				return ordinary(true)
			}
			return fail(errNoWindow, false)
		}
		// Preserve the ordinary reply before any paid export. A seal-only response
		// has no ordinary work; read its exact window directly instead.
		if reply.Ordinary {
			return NativeCheckpointResult{Turn: true, Receipt: &ThresholdReceipt{index: len(r.Messages), scope: attempt.scope, prefix: it.PrefixHash, generation: flight.generation, seal: bytes.Clone(it.Data)}}, nil
		}
		var suffix []json.RawMessage
		for _, item := range reply.Items[i:] {
			suffix = append(suffix, item.Data)
		}
		raw, _ := json.Marshal(suffix)
		if len(raw) > maxNativeWindowBytes {
			return fail(errNoWindow, false)
		}
		return x.export(ctx, nativeWindow{raw: raw}, false, fail, record)
	}
	if reply.Ordinary {
		return ordinary(true)
	}
	var block struct{ Content, Signature string }
	if len(reply.Items) != 1 || len(it.Data) > maxNativeWindowBytes || json.Unmarshal(it.Data, &block) != nil || strings.TrimSpace(block.Content) == "" {
		if x.provider.CompactionMethod() == CompactionProviderOnly {
			if err := record(supportCooling, errNoWindow); err != nil {
				return NativeCheckpointResult{}, err
			}
			return NativeCheckpointResult{}, newNativeFailure(errNoWindow)
		}
		return fail(errNoWindow, false)
	}
	usable = true
	if block.Signature != "" {
		return x.export(ctx, nativeWindow{raw: it.Data, text: block.Content}, false, fail, record)
	}
	if err := r.ValidatePortable(block.Content); err != nil {
		usable = false
		return fail(errUnusableCheckpoint, false)
	}
	if err := record(supportVerified, nil); err != nil {
		return NativeCheckpointResult{}, err
	}
	msg := NewTextMessage("user", block.Content)
	msg.InternalID = NewMessageID()
	msg.Origin, msg.Visibility = OriginCheckpoint, VisibilityContextOnly
	return NativeCheckpointResult{Message: msg}, nil
}
