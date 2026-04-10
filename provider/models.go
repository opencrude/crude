package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type ModelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type modelsResponse struct {
	Data []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"data"`
}

func FetchModels(p *Provider) ([]ModelInfo, error) {
	if p == nil {
		return nil, fmt.Errorf("provider is nil")
	}

	client := &http.Client{Timeout: 15 * time.Second}

	req, err := http.NewRequest("GET", p.ModelsEndpoint(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if p.APIKey != "" {
		req.Header.Set(p.AuthHeader, p.AuthValue())
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return nil, fmt.Errorf("Invalid API key for %s", p.Name)
		}
		if resp.StatusCode == 404 {
			return nil, fmt.Errorf("Models endpoint not supported by %s", p.Name)
		}
		return nil, fmt.Errorf("%s returned %d (see logs)", p.Name, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Failed to read response")
	}

	var result modelsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("Failed to parse models. Ensure %s is compatible", p.Name)
	}

	models := make([]ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, ModelInfo{ID: m.ID, Name: m.Name})
	}

	sort.Slice(models, func(i, j int) bool {
		return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID)
	})

	return models, nil
}
