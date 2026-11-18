package session

import (
	"unicode/utf8"

	"crude/types/reasoning"
)

// inferredReasoningKind is an off-UI derivation over the owning session's
// immutable visible traces and provider-reported usage. It applies only to
// this agent/account/provider/model connection; it never guesses from a model
// name, prompt text, or a token counter for the current incomplete request.
func (s *Session) inferredReasoningKind(agentID, providerID, accountID, modelID string) reasoning.Kind {
	if s == nil || agentID == "" || providerID == "" || accountID == "" || modelID == "" {
		return reasoning.Unknown
	}
	visible := make(map[uint64]int)
	s.msgsMu.Lock()
	for i, msg := range s.msgs {
		if i >= len(s.displayMetas) {
			break
		}
		meta := s.displayMetas[i]
		if meta.ProviderID != providerID || meta.AccountID != accountID || meta.ModelID != modelID || meta.TurnGeneration == 0 {
			continue
		}
		if msg.ReasoningDisplay.Kind == reasoning.Raw {
			s.msgsMu.Unlock()
			return reasoning.Unknown // explicit raw wire evidence overrides ratios
		}
		if msg.ReasoningDisplay.Kind != reasoning.Unknown && msg.ReasoningDisplay.Kind != reasoning.Provisional && msg.ReasoningDisplay.Kind != reasoning.InferredSummary && msg.ReasoningDisplay.Kind != reasoning.InferredRaw {
			continue
		}
		visible[meta.TurnGeneration] += utf8.RuneCountInString(msg.ReasoningContent)
	}
	s.msgsMu.Unlock()
	if len(visible) == 0 {
		return reasoning.Unknown
	}
	type observed struct{ tokens, calls int }
	usage := make(map[uint64]observed)
	s.usageMu.RLock()
	for _, record := range s.usageRecords {
		if record.AgentID != agentID || record.ProviderID != providerID || record.AccountID != accountID || record.ModelID != modelID || record.RequestKind != "normal" || !record.ReasoningKnown || record.Partial || record.ReasoningTokens <= 0 {
			continue
		}
		group := usage[record.TurnGeneration]
		group.tokens += record.ReasoningTokens
		group.calls++
		usage[record.TurnGeneration] = group
	}
	s.usageMu.RUnlock()
	confirmed, rawConfirmed := 0, 0
	exceptional := false
	for generation, runes := range visible {
		group := usage[generation]
		if group.calls == 0 {
			continue
		}
		if !reasoning.CondensedByUsage(runes, group.tokens, true) {
			if runes >= 12 && group.tokens >= 128 && group.tokens <= 2*((runes+3)/4) {
				rawConfirmed++
				continue
			}
			return reasoning.Unknown // insufficient evidence fails closed
		}
		confirmed++
		if group.tokens >= 16*((runes+3)/4) {
			exceptional = true
		}
	}
	if confirmed > 0 && rawConfirmed > 0 {
		return reasoning.Unknown // contradictory evidence fails closed
	}
	if rawConfirmed >= 2 {
		return reasoning.InferredRaw
	}
	if confirmed >= 2 || confirmed == 1 && exceptional {
		return reasoning.InferredSummary
	}
	return reasoning.Unknown
}
