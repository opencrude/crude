package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

// OpenAIStandard implements APIStandard for the OpenAI Chat Completions API format.
// This covers OpenAI, OpenRouter, Groq, Together, DeepSeek, Mistral, Ollama, and
// any other provider offering an OpenAI-compatible endpoint.
type OpenAIStandard struct{}

func (s *OpenAIStandard) ID() string { return "openai" }

func (s *OpenAIStandard) ChatEndpoint(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/chat/completions"
}

func (s *OpenAIStandard) ModelsEndpoint(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/models"
}

func (s *OpenAIStandard) Auth(apiKey string) (string, string) {
	if apiKey == "" {
		return "", ""
	}
	return "Authorization", "Bearer " + apiKey
}

func (s *OpenAIStandard) EncodeRequest(req *ChatRequest) ([]byte, error) {
	return json.Marshal(req)
}

func (s *OpenAIStandard) DecodeResponse(body []byte) (*ChatResponse, error) {
	var resp ChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &resp, nil
}

func (s *OpenAIStandard) DecodeStreamChunk(data string) (*ChatResponse, error) {
	if data == "[DONE]" {
		return nil, nil
	}
	var chunk ChatResponse
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return nil, fmt.Errorf("unmarshal stream chunk: %w", err)
	}
	return &chunk, nil
}

func (s *OpenAIStandard) DecodeModels(body []byte) ([]ModelInfo, error) {
	var result struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("unmarshal models: %w", err)
	}

	models := make([]ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, ModelInfo{
			ID:   m.ID,
			Name: m.Name,
		})
	}
	return models, nil
}

func (s *OpenAIStandard) ExtraBody(modelID, thinkingEffort string) map[string]any {
	if thinkingEffort == "" {
		return nil
	}
	// Standard OpenAI reasoning effort format
	return map[string]any{
		"reasoning_effort": thinkingEffort,
	}
}

func (s *OpenAIStandard) ContextWindow(modelID string) int {
	return LookupContextWindow(modelID)
}

func (s *OpenAIStandard) UsageFromResponse(resp *ChatResponse) (int, int, int) {
	return UsageFromResponse(resp)
}
