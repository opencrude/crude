package provider

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
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

	client := &http.Client{Timeout: 15 * time.Second}

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	header, value := p.Auth()
	req.Header.Set(header, value)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return nil, fmt.Errorf("invalid API key for %s", p.Name)
		}
		if resp.StatusCode == 404 {
			return nil, fmt.Errorf("models endpoint not supported by %s", p.Name)
		}
		return nil, fmt.Errorf("%s returned %d", p.Name, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response")
	}

	models, err := p.standard.DecodeModels(body)
	if err != nil {
		return nil, fmt.Errorf("parse models: %w", err)
	}

	sort.Slice(models, func(i, j int) bool {
		return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID)
	})

	return models, nil
}
