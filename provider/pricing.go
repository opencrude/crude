package provider

import (
	"strconv"
	"strings"
	"sync"
)

// ModelPrice represents the cost per token for a model.
type ModelPrice struct {
	InputPricePerToken  float64 // cost per input token in USD
	OutputPricePerToken float64 // cost per output token in USD
	InputCachedPricePerToken float64 // cost per cached input token (0 = same as input)
}

// CalculateCost computes the USD cost for a given usage set.
func (p ModelPrice) CalculateCost(promptTokens, completionTokens, cachedTokens int) float64 {
	newPromptTokens := promptTokens - cachedTokens
	if newPromptTokens < 0 {
		newPromptTokens = 0
	}
	cachedPrice := p.InputCachedPricePerToken
	if cachedPrice == 0 {
		cachedPrice = p.InputPricePerToken
	}
	inputCost := float64(newPromptTokens)*p.InputPricePerToken + float64(cachedTokens)*cachedPrice
	outputCost := float64(completionTokens) * p.OutputPricePerToken
	return inputCost + outputCost
}

// dynamicPrices stores prices fetched from provider APIs, keyed by model ID.
var (
	dynamicPrices   = make(map[string]ModelPrice)
	dynamicPricesMu sync.RWMutex
)

// SetModelPrice stores a dynamically fetched price for a model.
func SetModelPrice(modelID string, price ModelPrice) {
	dynamicPricesMu.Lock()
	dynamicPrices[strings.ToLower(modelID)] = price
	dynamicPricesMu.Unlock()
}

// modelPrices maps model ID substrings to their hardcoded fallback pricing.
// Prices are per 1M tokens, converted to per-token.
// Sources: OpenRouter pricing pages, provider docs (accurate as of 2025).
var modelPrices = []struct {
	pattern string
	price   ModelPrice
}{
	// OpenAI
	{"gpt-4o", ModelPrice{2.50 / 1e6, 10.00 / 1e6, 1.25 / 1e6}},
	{"gpt-4o-mini", ModelPrice{0.15 / 1e6, 0.60 / 1e6, 0.075 / 1e6}},
	{"gpt-4-turbo", ModelPrice{10.00 / 1e6, 30.00 / 1e6, 0}},
	{"gpt-4", ModelPrice{30.00 / 1e6, 60.00 / 1e6, 0}},
	{"gpt-3.5-turbo", ModelPrice{0.50 / 1e6, 1.50 / 1e6, 0}},
	{"o1", ModelPrice{15.00 / 1e6, 60.00 / 1e6, 7.50 / 1e6}},
	{"o3-mini", ModelPrice{1.10 / 1e6, 4.40 / 1e6, 0.55 / 1e6}},

	// Anthropic
	{"claude-3.5-sonnet", ModelPrice{3.00 / 1e6, 15.00 / 1e6, 0.30 / 1e6}},
	{"claude-3-opus", ModelPrice{15.00 / 1e6, 75.00 / 1e6, 1.50 / 1e6}},
	{"claude-3-haiku", ModelPrice{0.25 / 1e6, 1.25 / 1e6, 0.03 / 1e6}},
	{"claude-3-sonnet", ModelPrice{3.00 / 1e6, 15.00 / 1e6, 0.30 / 1e6}},
	{"claude-sonnet-4", ModelPrice{3.00 / 1e6, 15.00 / 1e6, 0.30 / 1e6}},

	// Google
	{"gemini-2.0-flash", ModelPrice{0.10 / 1e6, 0.40 / 1e6, 0.025 / 1e6}},
	{"gemini-2.0-pro", ModelPrice{2.00 / 1e6, 8.00 / 1e6, 0.50 / 1e6}},
	{"gemini-1.5-flash", ModelPrice{0.075 / 1e6, 0.30 / 1e6, 0.01875 / 1e6}},
	{"gemini-1.5-pro", ModelPrice{3.50 / 1e6, 10.50 / 1e6, 0.875 / 1e6}},
	{"gemini-2.5-pro", ModelPrice{1.25 / 1e6, 10.00 / 1e6, 0.3125 / 1e6}},

	// DeepSeek
	{"deepseek-chat", ModelPrice{0.27 / 1e6, 1.10 / 1e6, 0.07 / 1e6}},
	{"deepseek-reasoner", ModelPrice{0.55 / 1e6, 2.19 / 1e6, 0.14 / 1e6}},
	{"deepseek-v3", ModelPrice{0.27 / 1e6, 1.10 / 1e6, 0.07 / 1e6}},

	// Mistral
	{"mistral-large", ModelPrice{2.00 / 1e6, 6.00 / 1e6, 0}},
	{"mistral-small", ModelPrice{0.20 / 1e6, 0.60 / 1e6, 0}},
	{"codestral", ModelPrice{1.00 / 1e6, 3.00 / 1e6, 0}},

	// Meta / Llama (via OpenRouter / Together)
	{"llama-3.3-70b", ModelPrice{0.59 / 1e6, 0.79 / 1e6, 0}},
	{"llama-3.1-8b", ModelPrice{0.05 / 1e6, 0.05 / 1e6, 0}},
	{"llama-3.2-3b", ModelPrice{0.03 / 1e6, 0.06 / 1e6, 0}},
	{"llama-3.2-1b", ModelPrice{0.02 / 1e6, 0.04 / 1e6, 0}},

	// Qwen
	{"qwen-2.5-72b", ModelPrice{0.90 / 1e6, 0.90 / 1e6, 0}},
	{"qwen-2.5-coder-32b", ModelPrice{0.90 / 1e6, 0.90 / 1e6, 0}},
	{"qwen-max", ModelPrice{2.00 / 1e6, 6.00 / 1e6, 0}},

	// Groq
	{"groq", ModelPrice{0, 0, 0}},

	// Default fallback for models not in the table
	{"", ModelPrice{1.00 / 1e6, 3.00 / 1e6, 0}},
}

// LookupPrice returns the ModelPrice for a given model name.
// Priority: 1) dynamically fetched price, 2) hardcoded table, 3) fallback default.
func LookupPrice(model string) ModelPrice {
	lower := strings.ToLower(model)

	dynamicPricesMu.RLock()
	if p, ok := dynamicPrices[lower]; ok {
		dynamicPricesMu.RUnlock()
		return p
	}
	dynamicPricesMu.RUnlock()

	for _, entry := range modelPrices {
		if entry.pattern == "" {
			return entry.price
		}
		if strings.Contains(lower, entry.pattern) {
			return entry.price
		}
	}
	return ModelPrice{1.00 / 1e6, 3.00 / 1e6, 0}
}

// ParseModelPricing parses OpenRouter-style pricing from a map[string]string
// (values are USD per 1M tokens as strings) and stores it via SetModelPrice.
func ParseModelPricing(modelID string, pricing map[string]string) {
	if pricing == nil {
		return
	}
	promptStr, hasPrompt := pricing["prompt"]
	completionStr, hasCompletion := pricing["completion"]
	if !hasPrompt || !hasCompletion {
		return
	}
	promptPrice, err := strconv.ParseFloat(promptStr, 64)
	if err != nil {
		return
	}
	completionPrice, err := strconv.ParseFloat(completionStr, 64)
	if err != nil {
		return
	}
	SetModelPrice(modelID, ModelPrice{
		InputPricePerToken:  promptPrice / 1e6,
		OutputPricePerToken: completionPrice / 1e6,
	})
}
