package agent

import "crude/provider"

func contextSafetyReserve(window int) int {
	return min(4096, max(256, window/20))
}

func (a *Agent) outputTokenLimit(window, requested int) int {
	limit := maxOutputTokensForContext(window)
	if requested > 0 && (limit == 0 || requested < limit) {
		limit = requested
	}
	configured := a.MaxOutputTokens
	if configured == 0 {
		if metadata, ok := provider.ModelCard(a.Model); ok {
			configured = metadata.MaxOutput
		}
	}
	if configured > 0 && (limit == 0 || configured < limit) {
		limit = configured
	}
	return limit
}

// Compaction output can include reasoning, which does not enter the checkpoint.
// The generation ceiling follows the selected model and provider configuration,
// independently of the amount of text needed in the final checkpoint.
func (a *Agent) compactionOutputLimit(window, requested int) int {
	limit := window
	if a.MaxOutputTokens > 0 && (limit <= 0 || a.MaxOutputTokens < limit) {
		limit = a.MaxOutputTokens
	}
	if metadata, ok := provider.ModelCard(a.Model); ok && metadata.MaxOutput > 0 && (limit <= 0 || metadata.MaxOutput < limit) {
		limit = metadata.MaxOutput
	}
	if requested > 0 && (limit <= 0 || requested < limit) {
		limit = requested
	}
	return limit
}

func (a *Agent) contextThreshold(window int) int {
	threshold := compactionThreshold(window, a.ContextCompactionPercent)
	if threshold == 0 {
		return 0
	}
	return min(threshold, max(1, window-a.outputTokenLimit(window, 0)-contextSafetyReserve(window)))
}
