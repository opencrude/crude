package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"crude/logger"
)

// modelsDevURL is the single endpoint for models.dev — a comprehensive
// open-source database of AI model specs, pricing, and capabilities.
// There is no per-model query API; the full ~6MB JSON must be downloaded.
const modelsDevURL = "https://models.dev/api.json"

// mdProvider mirrors the per-provider section of the models.dev JSON.
type mdProvider struct {
	Models map[string]mdModel `json:"models"`
}

// mdModel mirrors a single model entry in models.dev.
type mdModel struct {
	Cost  mdCost  `json:"cost,omitempty"`
	Limit mdLimit `json:"limit,omitempty"`
}

// mdCost mirrors the pricing sub-section of a model entry.
// Prices in models.dev are in USD per million tokens.
type mdCost struct {
	Input     float64 `json:"input,omitempty"`
	Output    float64 `json:"output,omitempty"`
	CacheRead float64 `json:"cache_read,omitempty"`
}

// mdLimit mirrors the limits sub-section of a model entry.
type mdLimit struct {
	Context int `json:"context"`
}

// FetchModelsDevAll downloads the full models.dev dataset and returns a flat
// map of lowercased model ID → ModelMeta (context window + per-token pricing).
//
// When the same model ID appears under multiple providers, the cheapest input
// price wins and the largest context window is retained.
//
// Prices from models.dev are per million tokens; they are converted to
// per-token values before being returned.
func FetchModelsDevAll() (map[string]ModelMeta, error) {
	logIf(func(lg *logger.Logger) { lg.Info("PROVIDER", "models_dev_fetch", "url", modelsDevURL) })

	client := &http.Client{Timeout: 30 * time.Second}
	start := time.Now()
	resp, err := client.Get(modelsDevURL)
	if err != nil {
		return nil, fmt.Errorf("fetch models.dev: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		logIf(func(lg *logger.Logger) { lg.Warn("PROVIDER", "models_dev_http_error", "status", resp.StatusCode) })
		return nil, fmt.Errorf("models.dev returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read models.dev: %w", err)
	}
	readDur := time.Since(start).Milliseconds()
	logIf(func(lg *logger.Logger) { lg.Debug("PROVIDER", "models_dev_downloaded", "bytes", len(body), "latency_ms", readDur) })

	var data map[string]mdProvider
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("parse models.dev: %w", err)
	}

	result := make(map[string]ModelMeta)

	for _, prov := range data {
		for modelID, entry := range prov.Models {
			lower := strings.ToLower(modelID)

			// Convert per-million-token → per-token.
			inPerToken := entry.Cost.Input / 1e6
			outPerToken := entry.Cost.Output / 1e6
			cachePerToken := entry.Cost.CacheRead / 1e6
			ctx := entry.Limit.Context

			if existing, ok := result[lower]; ok {
				// Cheapest input price wins.
				if existing.InputPrice == 0 || (inPerToken > 0 && inPerToken < existing.InputPrice) {
					existing.InputPrice = inPerToken
					existing.OutputPrice = outPerToken
					existing.CacheReadPrice = cachePerToken
				}
				// Largest context window wins.
				if ctx > existing.ContextWindow {
					existing.ContextWindow = ctx
				}
				result[lower] = existing
			} else if inPerToken > 0 || ctx > 0 {
				result[lower] = ModelMeta{
					ContextWindow:  ctx,
					InputPrice:     inPerToken,
					OutputPrice:    outPerToken,
					CacheReadPrice: cachePerToken,
				}
			}
		}
	}

	logIf(func(lg *logger.Logger) { lg.Info("PROVIDER", "models_dev_ok", "count", len(result), "latency_ms", time.Since(start).Milliseconds()) })

	return result, nil
}

// FetchModelsDevContext downloads the full models.dev dataset and returns a
// flat map of lowercased model ID → context window (tokens). This is a
// backwards-compatible wrapper around FetchModelsDevAll kept for callers
// that only need context.
func FetchModelsDevContext(priorityProviders ...string) (map[string]int, error) {
	all, err := FetchModelsDevAll()
	if err != nil {
		return nil, err
	}
	result := make(map[string]int, len(all))
	for id, meta := range all {
		if meta.ContextWindow > 0 {
			result[id] = meta.ContextWindow
		}
	}
	return result, nil
}
