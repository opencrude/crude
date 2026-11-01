package session

import "crude/provider"

// OutputTokenLimit keeps the provider hard bound and the captured role ceiling
// with their binding owner. It is a maximum, not an enable-time spending default.
func (t EditorPredictionCompleter) OutputTokenLimit() int {
	if t.role.ModelMaxOutput > 0 {
		return min(t.role.ModelMaxOutput, provider.TextOnceMaxOutputTokens)
	}
	return provider.TextOnceMaxOutputTokens
}
