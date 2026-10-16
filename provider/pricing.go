package provider

import (
	"slices"
	"strconv"
	"strings"
	"sync"

	"crude/logger"
)

// ModelPrice holds per-token pricing for a single model, used for manual
// cost calculation via CalculateCost. Prices come from the public catalog or
// an explicit caller registration.
type ModelPrice struct {
	InputPricePerToken       float64
	OutputPricePerToken      float64
	InputCachedPricePerToken float64
}

// CalculateCost computes the cost of a request given token counts.
// cachedTokens are tokens that were served from a prompt cache (read at
// InputCachedPricePerToken). The remainder (promptTokens - cachedTokens)
// is charged at the full InputPricePerToken.
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
	cost := inputCost + outputCost
	logIf(func(lg *logger.Logger) {
		lg.Debug("PRICING", "calculate_cost", "prompt", promptTokens, "completion", completionTokens, "cached", cachedTokens, "cost", cost)
	})
	return cost
}

// ModelMeta is a complete public model card. Missing ModalitySupport keys are
// unknown, not false; ConflictingModalities retains contrary source evidence.
type ModelMeta struct {
	ContextWindow  int     `json:"context_window"`
	InputPrice     float64 `json:"input_price"`      // USD per token
	OutputPrice    float64 `json:"output_price"`     // USD per token
	CacheReadPrice float64 `json:"cache_read_price"` // USD per token
	// Modalities lists the input types beyond text the model accepts
	// (ModalityImage, ModalityPDF, ModalityVideo, ModalityAudio).
	Modalities            []string        `json:"modalities,omitempty"`
	SupportsTools         bool            `json:"supports_tools,omitempty"`
	MaxOutput             int             `json:"max_output,omitempty"`
	ModalitySupport       map[string]bool `json:"modality_support,omitempty"`
	Sources               []string        `json:"sources,omitempty"`
	ConflictingModalities []string        `json:"conflicting_modalities,omitempty"`
	PricingKnown          bool            `json:"pricing_known,omitempty"`
	ToolsKnown            bool            `json:"tools_known,omitempty"`
}

// Input modalities a model can accept beyond text, in canonical order.
const (
	ModalityImage = "image"
	ModalityPDF   = "pdf"
	ModalityVideo = "video"
	ModalityAudio = "audio"
)

// knownModalities is the canonical modality order for display and storage.
var knownModalities = []string{ModalityImage, ModalityPDF, ModalityVideo, ModalityAudio}

// KnownModalities returns the modality set the app models, in canonical order.
func KnownModalities() []string {
	return slices.Clone(knownModalities)
}

// CanonicalModalities filters a raw modality list down to the known set,
// preserving canonical order. Text is implicit and never listed.
func CanonicalModalities(modalities []string) []string {
	var out []string
	for _, known := range knownModalities {
		for _, m := range modalities {
			if strings.EqualFold(m, known) {
				out = append(out, known)
				break
			}
		}
	}
	return out
}

// UnionModalities merges two modality lists, preserving canonical order.
func UnionModalities(lists ...[]string) []string {
	var raw []string
	for _, list := range lists {
		raw = append(raw, list...)
	}
	return CanonicalModalities(raw)
}

// ToModelPrice converts ModelMeta to ModelPrice for cost calculation.
func (m ModelMeta) ToModelPrice() ModelPrice {
	return ModelPrice{
		InputPricePerToken:       m.InputPrice,
		OutputPricePerToken:      m.OutputPrice,
		InputCachedPricePerToken: m.CacheReadPrice,
	}
}

// FromModelPrice converts a ModelPrice back to ModelMeta, supplementing
// it with a context window.
func FromModelPrice(price ModelPrice, contextWindow int) ModelMeta {
	return ModelMeta{
		ContextWindow:  contextWindow,
		InputPrice:     price.InputPricePerToken,
		OutputPrice:    price.OutputPricePerToken,
		CacheReadPrice: price.InputCachedPricePerToken,
	}
}

// ---------------------------------------------------------------------------
// Dynamic pricing map, populated at runtime from models.dev (or OpenRouter
// API when available) and persisted to the config file.
// ---------------------------------------------------------------------------

var (
	dynamicPrices   = make(map[string]ModelPrice)
	modelCards      = make(map[string]ModelMeta)
	dynamicPricesMu sync.RWMutex
)

// SetModelPrice stores a price entry for a model ID (case-insensitive).
func SetModelPrice(modelID string, price ModelPrice) {
	dynamicPricesMu.Lock()
	dynamicPrices[strings.ToLower(modelID)] = price
	dynamicPricesMu.Unlock()
}

// SetModelMeta stores both pricing and context window from a ModelMeta value.
func SetModelMeta(modelID string, meta ModelMeta) {
	dynamicPricesMu.Lock()
	dynamicPrices[strings.ToLower(modelID)] = meta.ToModelPrice()
	modelCards[strings.ToLower(modelID)] = cloneModelMeta(meta)
	dynamicPricesMu.Unlock()
	if meta.ContextWindow > 0 {
		SetModelContext(modelID, meta.ContextWindow)
	}
	logIf(func(lg *logger.Logger) {
		lg.Debug("PRICING", "set_meta", "model", modelID, "ctx", meta.ContextWindow, "input_price", meta.InputPrice)
	})
}

// LoadModelMetas bulk-loads a map of model metadata (e.g. from config).
// Existing entries for the same model IDs are overwritten.
func LoadModelMetas(metas map[string]ModelMeta) {
	for id, meta := range metas {
		SetModelMeta(id, meta)
	}
	// Populate capabilities from the same data source.
	LoadModelCaps(metas)
	logIf(func(lg *logger.Logger) { lg.Info("PRICING", "load_metas", "count", len(metas)) })
}

// GetAllModelMetas returns independent complete model cards, not a lossy
// reconstruction from runtime pricing/context caches or user overrides.
func GetAllModelMetas() map[string]ModelMeta {
	if catalog := activeModelCatalog.Load(); catalog != nil {
		return catalog.Models()
	}
	dynamicPricesMu.RLock()
	defer dynamicPricesMu.RUnlock()

	result := make(map[string]ModelMeta, len(modelCards))
	for id, meta := range modelCards {
		result[id] = cloneModelMeta(meta)
	}
	return result
}

func cloneModelMeta(meta ModelMeta) ModelMeta {
	meta.Modalities = slices.Clone(meta.Modalities)
	meta.Sources = slices.Clone(meta.Sources)
	meta.ConflictingModalities = slices.Clone(meta.ConflictingModalities)
	if meta.ModalitySupport != nil {
		values := make(map[string]bool, len(meta.ModalitySupport))
		for key, value := range meta.ModalitySupport {
			values[key] = value
		}
		meta.ModalitySupport = values
	}
	return meta
}

// ModelCard is public-source metadata, never an agent's local overrides.
func ModelCard(modelID string) (ModelMeta, bool) {
	if catalog := activeModelCatalog.Load(); catalog != nil {
		return catalog.Lookup(modelID)
	}
	dynamicPricesMu.RLock()
	defer dynamicPricesMu.RUnlock()
	if meta := LookupModelMeta(modelID, modelCards); meta != nil {
		return cloneModelMeta(*meta), true
	}
	return ModelMeta{}, false
}

// ---------------------------------------------------------------------------
// Known free-model suffixes.
// ---------------------------------------------------------------------------

var knownFreePrefixes = []string{
	":free",
	"-free",
	"/free",
}

// stripProviderPrefix removes the leading "provider/" prefix from a model ID.
// Example: "openai/gpt-4o" → "gpt-4o", "gpt-4o" → "gpt-4o".
func stripProviderPrefix(modelID string) string {
	if idx := strings.Index(modelID, "/"); idx > 0 && idx < len(modelID)-1 {
		return modelID[idx+1:]
	}
	return modelID
}

// StripProviderPrefix is the exported version of stripProviderPrefix.
func StripProviderPrefix(modelID string) string {
	return stripProviderPrefix(modelID)
}

// LookupModelMeta looks up a model ID (which may have a provider/ prefix) in
// a models.dev result map and returns the ModelMeta if found.  Returns nil
// when the model is not present.
func LookupModelMeta(modelID string, metas map[string]ModelMeta) *ModelMeta {
	lower := strings.ToLower(modelID)
	if meta, ok := metas[lower]; ok {
		return &meta
	}
	if stripped := stripProviderPrefix(lower); stripped != lower {
		if meta, ok := metas[stripped]; ok {
			return &meta
		}
	}
	return nil
}

// LookupPrice finds the best-known price for a model.  It first tries an
// exact match against the dynamic price map, then tries stripping any
// "provider/" prefix to match against the base model name, then falls back
// to a fuzzy substring match against all known keys.  Models whose IDs
// contain a known free suffix always return zero pricing.
func LookupPrice(model string) ModelPrice {
	price, _, _ := LookupPriceKnown(model)
	return price
}

// LookupPriceKnown distinguishes an explicitly free model from absent pricing.
func LookupPriceKnown(model string) (ModelPrice, bool, string) {
	if catalog := activeModelCatalog.Load(); catalog != nil {
		if meta, ok := catalog.Lookup(model); ok && meta.PricingKnown {
			return meta.ToModelPrice(), true, "catalog"
		}
	}
	lower := strings.ToLower(model)

	// Exact match.
	dynamicPricesMu.RLock()
	if p, ok := dynamicPrices[lower]; ok {
		dynamicPricesMu.RUnlock()
		logIf(func(lg *logger.Logger) { lg.Debug("PRICING", "lookup", "model", model, "match", "exact") })
		return p, true, "catalog"
	}
	dynamicPricesMu.RUnlock()

	// Try without the provider prefix (e.g. "openai/gpt-4o" → "gpt-4o").
	if stripped := stripProviderPrefix(lower); stripped != lower {
		dynamicPricesMu.RLock()
		if p, ok := dynamicPrices[stripped]; ok {
			dynamicPricesMu.RUnlock()
			logIf(func(lg *logger.Logger) {
				lg.Debug("PRICING", "lookup", "model", model, "match", "stripped", "matched", stripped)
			})
			return p, true, "catalog"
		}
		dynamicPricesMu.RUnlock()
	}

	// Fuzzy fallback: if no exact match, check whether any key in
	// dynamicPrices contains the model ID as a substring (case-insensitive).
	// Free variants are checked first: a fuzzy match like "longcat-2.0" for
	// "meituan/LongCat-2.0:free" would otherwise price a free model.
	dynamicPricesMu.RLock()
	for _, prefix := range knownFreePrefixes {
		if strings.Contains(lower, prefix) {
			dynamicPricesMu.RUnlock()
			logIf(func(lg *logger.Logger) { lg.Debug("PRICING", "lookup", "model", model, "match", "free_suffix") })
			return ModelPrice{0, 0, 0}, true, "free-model"
		}
	}
	for key, price := range dynamicPrices {
		if strings.Contains(key, lower) || strings.Contains(lower, key) {
			dynamicPricesMu.RUnlock()
			logIf(func(lg *logger.Logger) {
				lg.Debug("PRICING", "lookup", "model", model, "match", "fuzzy", "matched", key)
			})
			return price, true, "catalog-fuzzy"
		}
	}
	dynamicPricesMu.RUnlock()

	logIf(func(lg *logger.Logger) { lg.Debug("PRICING", "lookup", "model", model, "match", "zero") })
	return ModelPrice{0, 0, 0}, false, ""
}

// ParseModelPricing parses pricing from the OpenRouter Models API format
// (map[string]string where values are already in USD per token).  This is
// kept for future OpenRouter-specific integrations; the primary pricing
// source is now models.dev.
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
		InputPricePerToken:  promptPrice, // already per token
		OutputPricePerToken: completionPrice,
	})
}

// premiumServiceTier reports whether a processing tier bills above the
// standard catalog rate (OpenAI Fast mode, formerly priority, and faster tiers).
func premiumServiceTier(tier string) bool {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "priority", "fast", "ultrafast":
		return true
	}
	return false
}

// RequestPriceKnown prices one request made through p. The tier the provider
// reports having served wins over the requested one, so a fast request that
// was downgraded is priced at the standard rate. Crude has no tier-specific
// prices, so a premium-tier request reports its cost as unknown rather than
// silently understating it at the standard rate.
func (p *Provider) RequestPriceKnown(model, servedTier string) (ModelPrice, bool, string) {
	price, known, source := LookupPriceKnown(model)
	tier := servedTier
	if tier == "" && p != nil && p.protocolSettings != nil {
		tier = p.protocolSettings.ServiceTier
	}
	if known && premiumServiceTier(tier) {
		return price, false, "catalog standard rate; " + strings.ToLower(tier) + " tier not priced"
	}
	return price, known, source
}
