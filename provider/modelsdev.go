package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
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
	Name  string   `json:"name,omitempty"`
	Limit *mdLimit `json:"limit,omitempty"`
}

// mdLimit mirrors the limit sub-section of a model entry.
type mdLimit struct {
	Context int `json:"context"`
}

// FetchModelsDevContext downloads the full models.dev dataset and returns a
// flat map of lowercased model ID → context window (tokens). When the same
// model ID appears under multiple providers (e.g. upstream + mirror/wrapper),
// entries from the specified priorityProviders (provider IDs such as
// "opencode", "opencode-go") always take priority. Among equal-tier
// providers the largest context window wins.
func FetchModelsDevContext(priorityProviders ...string) (map[string]int, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(modelsDevURL)
	if err != nil {
		return nil, fmt.Errorf("fetch models.dev: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("models.dev returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read models.dev: %w", err)
	}

	var data map[string]mdProvider
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("parse models.dev: %w", err)
	}

	// Build a flat map keyed by lowercased model ID.
	result := make(map[string]int)
	isPriorityEntry := make(map[string]bool)

	// Build a fast lookup for priority provider IDs.
	prioritySet := make(map[string]struct{}, len(priorityProviders))
	for _, pp := range priorityProviders {
		prioritySet[pp] = struct{}{}
	}

	for provID, prov := range data {
		for modelID, entry := range prov.Models {
			if entry.Limit == nil || entry.Limit.Context <= 0 {
				continue
			}

			lower := strings.ToLower(modelID)
			_, isPriority := prioritySet[provID]

			if existing, ok := result[lower]; !ok {
				// First time seeing this model — accept.
				result[lower] = entry.Limit.Context
				isPriorityEntry[lower] = isPriority
			} else if isPriority {
				// Priority entries always replace non-priority entries.
				result[lower] = entry.Limit.Context
				isPriorityEntry[lower] = true
			} else if entry.Limit.Context > existing && !isPriorityEntry[lower] {
				// Larger context wins among same-tier (non-priority) providers.
				result[lower] = entry.Limit.Context
			}
		}
	}

	return result, nil
}
