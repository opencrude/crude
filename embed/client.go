package embed

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"time"
)

const (
	defaultBaseURL    = "https://openrouter.ai/api/v1"
	defaultEmbedModel = "openai/text-embedding-3-small"
	maxRetries        = 5
)

// Client calls the OpenAI-compatible embeddings API.
type Client struct {
	APIKey  string
	Model   string
	BaseURL string
	http    *http.Client
}

// NewClient creates an embedding client. model defaults to text-embedding-3-small.
// baseURL overrides the default if non-empty.
func NewClient(apiKey, model, baseURL string) *Client {
	if model == "" {
		model = defaultEmbedModel
	}
	url := defaultBaseURL
	if baseURL != "" {
		url = baseURL
	}
	return &Client{
		APIKey:  apiKey,
		Model:   model,
		BaseURL: url,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Embed returns the embedding vector for a single text.
func (c *Client) Embed(text string) ([]float32, error) {
	vecs, err := c.EmbedBatch([]string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("embed: empty response")
	}
	return vecs[0], nil
}

// EmbedBatch returns embedding vectors for multiple texts, preserving order.
func (c *Client) EmbedBatch(texts []string) ([][]float32, error) {
	type embedReq struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	type embedData struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	}
	type embedResp struct {
		Data  []embedData `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	body, err := json.Marshal(embedReq{Model: c.Model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("embed: marshal: %w", err)
	}

	url := c.BaseURL + "/embeddings"

	var last error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest("POST", url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("embed: create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("HTTP-Referer", "http://localhost")
		req.Header.Set("X-Title", "Crude")

		resp, err := c.http.Do(req)
		if err != nil {
			last = err
			time.Sleep(backoff(attempt))
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			last = fmt.Errorf("embed: read response: %w", readErr)
			time.Sleep(backoff(attempt))
			continue
		}

		if resp.StatusCode != 200 {
			last = fmt.Errorf("embed: HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
			if isRetryable(resp.StatusCode) && attempt < maxRetries {
				time.Sleep(backoff(attempt))
				continue
			}
			return nil, last
		}

		var er embedResp
		if err := json.Unmarshal(respBody, &er); err != nil {
			return nil, fmt.Errorf("embed: unmarshal: %w", err)
		}
		if er.Error != nil {
			return nil, fmt.Errorf("embed: API error: %s", er.Error.Message)
		}

		// Sort by index to preserve order.
		sort.Slice(er.Data, func(i, j int) bool {
			return er.Data[i].Index < er.Data[j].Index
		})

		result := make([][]float32, len(er.Data))
		for i, d := range er.Data {
			result[i] = d.Embedding
		}
		return result, nil
	}

	return nil, fmt.Errorf("embed: retries exhausted: %w", last)
}

// CosineSim returns the cosine similarity between two vectors.
// Returns 0 if either vector is zero-length.
func CosineSim(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return float32(dot / denom)
}

func backoff(attempt int) time.Duration {
	// 2s, 4s, 8s, 16s, 32s
	return time.Duration(2<<(attempt-1)) * time.Second
}

func isRetryable(code int) bool {
	return code == 429 || code >= 500
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
