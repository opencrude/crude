package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"crude/logger"
)

// ModelInfo is a minimal model descriptor returned by the Models API.
type ModelInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextWindow int    `json:"context_window"`
}

// FetchModels retrieves the model list from a provider's /models endpoint.
// No pricing or context metadata is fetched here — that happens in the
// session layer via HandleFetchModels (which also consults models.dev).
func FetchModels(p *Provider) ([]ModelInfo, error) {
	if p == nil {
		return nil, fmt.Errorf("provider is nil")
	}

	endpoint := p.ModelsEndpoint()
	if endpoint == "" {
		return nil, fmt.Errorf("model listing not supported by %s standard", p.StandardID())
	}

	logIf(func(lg *logger.Logger) { lg.Info("PROVIDER", "fetch_models", "provider", p.Name, "endpoint", endpoint) })

	client := &http.Client{Timeout: 15 * time.Second}

	start := time.Now()
	const maxModelsResponseBytes = 16 << 20
	var models []ModelInfo
	afterID := ""
	seenCursors := make(map[string]struct{})
	for {
		requestEndpoint := endpoint
		if p.StandardID() == "anthropic" {
			parsed, err := url.Parse(endpoint)
			if err != nil {
				return nil, fmt.Errorf("parse models endpoint: %w", err)
			}
			query := parsed.Query()
			query.Set("limit", "1000")
			if afterID != "" {
				query.Set("after_id", afterID)
			}
			parsed.RawQuery = query.Encode()
			requestEndpoint = parsed.String()
		}
		req, err := http.NewRequest(http.MethodGet, requestEndpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		for header, value := range p.Headers() {
			req.Header.Set(header, value)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch models: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxModelsResponseBytes+1))
		closeErr := resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("failed to read response: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close models response: %w", closeErr)
		}
		if resp.StatusCode != http.StatusOK {
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				logIf(func(lg *logger.Logger) {
					lg.Error("PROVIDER", "fetch_models_auth_fail", "provider", p.Name, "status", resp.StatusCode)
				})
				if p.APIKey == "" {
					return nil, fmt.Errorf("%s requires an API key to list models — enter one in Settings → Providers", p.Name)
				}
				return nil, fmt.Errorf("invalid API key for %s", p.Name)
			}
			if resp.StatusCode == http.StatusNotFound {
				logIf(func(lg *logger.Logger) { lg.Warn("PROVIDER", "fetch_models_not_supported", "provider", p.Name) })
				return nil, fmt.Errorf("models endpoint not supported by %s", p.Name)
			}
			return nil, fmt.Errorf("%s returned %d", p.Name, resp.StatusCode)
		}
		if len(body) > maxModelsResponseBytes {
			return nil, fmt.Errorf("models response exceeds %d bytes", maxModelsResponseBytes)
		}
		page, err := p.DecodeModels(body)
		if err != nil {
			return nil, fmt.Errorf("parse models: %w", err)
		}
		models = append(models, page...)
		if p.StandardID() != "anthropic" {
			break
		}
		var pagination struct {
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := json.Unmarshal(body, &pagination); err != nil {
			return nil, fmt.Errorf("parse Anthropic model pagination: %w", err)
		}
		if !pagination.HasMore {
			break
		}
		if pagination.LastID == "" {
			return nil, fmt.Errorf("anthropic models response has_more without last_id")
		}
		if _, exists := seenCursors[pagination.LastID]; exists {
			return nil, fmt.Errorf("anthropic models pagination repeated cursor %q", pagination.LastID)
		}
		seenCursors[pagination.LastID] = struct{}{}
		afterID = pagination.LastID
	}

	sort.Slice(models, func(i, j int) bool {
		return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID)
	})

	logIf(func(lg *logger.Logger) {
		lg.Info("PROVIDER", "fetch_models_ok", "provider", p.Name, "count", len(models), "latency_ms", time.Since(start).Milliseconds())
	})

	return models, nil
}
