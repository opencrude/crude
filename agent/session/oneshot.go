package session

import (
	"context"
	"fmt"

	"crude/agent/provider"
	"crude/agent/turnloop"
)

// TextCompleter is one role's provider and model, resolved on the caller's
// goroutine so the request itself can run in the background without reading
// the live configuration maps.
type TextCompleter struct {
	Model     string
	provider  *provider.Provider
	maxOutput int
}

// ErrandCompleter resolves the Errand role (small, narrowly scoped tasks) for a
// one-shot completion. A disabled role is reported, never silently replaced.
func (c *Config) ErrandCompleter() (TextCompleter, error) {
	if c == nil {
		return TextCompleter{}, fmt.Errorf("model configuration is unavailable")
	}
	category := c.CategoryConfig(CategoryErrand)
	if category.Enabled != nil && !*category.Enabled {
		return TextCompleter{}, fmt.Errorf("the Errand agent is disabled in settings")
	}
	p, err := c.providerForCategory(category)
	if err != nil {
		return TextCompleter{}, err
	}
	return TextCompleter{Model: category.Model, provider: p, maxOutput: category.ModelMaxOutput}, nil
}

// Complete streams one completion; emit receives each token as it arrives.
func (t TextCompleter) Complete(ctx context.Context, system, user string, emit func(string)) (string, error) {
	if t.provider == nil {
		return "", fmt.Errorf("model is not configured")
	}
	return turnloop.CompleteTextStreaming(ctx, t.provider, t.Model, system, user, t.maxOutput, emit)
}
