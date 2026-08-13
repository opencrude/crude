package provider

import (
	"bytes"
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

func (s *OpenAIStandard) Headers(apiKey string) map[string]string {
	header, value := s.Auth(apiKey)
	if header == "" {
		return nil
	}
	return map[string]string{header: value}
}

func (s *OpenAIStandard) EncodeRequest(req *ChatRequest) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	messages, err := encodeOpenAIChatMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	wire := map[string]any{
		"model":    req.Model,
		"messages": messages,
		"stream":   req.Stream,
	}
	if len(req.Tools) > 0 {
		tools := make([]openAIChatToolParam, 0, len(req.Tools))
		for _, tool := range req.Tools {
			toolType := tool.Type
			if toolType == "" {
				toolType = "function"
			}
			tools = append(tools, openAIChatToolParam{
				Type: toolType, Function: tool.Function,
			})
		}
		wire["tools"] = tools
	}
	if req.ToolChoice != nil {
		wire["tool_choice"] = req.ToolChoice
	}
	if req.StreamOptions != nil {
		wire["stream_options"] = req.StreamOptions
	}
	switch {
	case req.MaxCompletionTokens > 0:
		wire["max_completion_tokens"] = req.MaxCompletionTokens
	case req.MaxTokens > 0:
		wire["max_tokens"] = req.MaxTokens
	case req.MaxOutputTokens > 0:
		if usesOpenAICompletionTokenLimit(req.Model) {
			wire["max_completion_tokens"] = req.MaxOutputTokens
		} else {
			// Compatible providers have the broadest support for max_tokens.
			wire["max_tokens"] = req.MaxOutputTokens
		}
	}
	return json.Marshal(wire)
}

func usesOpenAICompletionTokenLimit(model string) bool {
	model = strings.ToLower(StripProviderPrefix(model))
	return strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "o4") ||
		strings.HasPrefix(model, "gpt-5")
}

type openAIChatToolCallParam struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type openAIChatToolParam struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

func encodeOpenAIChatMessages(messages []ChatMessage) ([]any, error) {
	result := make([]any, 0, len(messages))
	for _, message := range messages {
		wire := map[string]any{
			"role":    message.Role,
			"content": message.Content,
		}
		if message.ReasoningContent != "" {
			wire["reasoning_content"] = message.ReasoningContent
		}
		if message.Reasoning != "" {
			wire["reasoning"] = message.Reasoning
		}
		if message.Refusal != "" {
			wire["refusal"] = message.Refusal
		}
		if message.ToolCallID != "" {
			wire["tool_call_id"] = message.ToolCallID
		}
		if len(message.ToolCalls) > 0 {
			calls := make([]openAIChatToolCallParam, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				callType := call.Type
				if callType == "" {
					callType = "function"
				}
				calls = append(calls, openAIChatToolCallParam{
					ID: call.ID, Type: callType, Function: call.Function,
				})
			}
			wire["tool_calls"] = calls
		}

		details := make([]json.RawMessage, 0, len(message.ReasoningDetails)+len(message.ProviderItems))
		for _, raw := range message.ReasoningDetails {
			if !json.Valid(raw) {
				return nil, fmt.Errorf("invalid OpenAI-compatible reasoning detail")
			}
			details = append(details, bytes.Clone(raw))
		}
		for _, item := range message.ProviderItems {
			if item.Standard != "openai" || len(item.Data) == 0 {
				continue
			}
			if !json.Valid(item.Data) {
				return nil, fmt.Errorf("invalid preserved OpenAI-compatible reasoning detail")
			}
			details = append(details, bytes.Clone(item.Data))
		}
		if len(details) > 0 {
			wire["reasoning_details"] = details
		}
		result = append(result, wire)
	}
	return result, nil
}

func (s *OpenAIStandard) DecodeResponse(body []byte) (*ChatResponse, error) {
	var resp ChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if resp.Error != nil {
		return nil, openAIError(resp.Error.Type, resp.Error.Code, resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI response contains no choices")
	}
	for i := range resp.Choices {
		captureOpenAIReasoningDetails(&resp.Choices[i].Message, resp.Choices[i].Delta.ReasoningDetails)
		if resp.Choices[i].Message.ContentString() == "" && resp.Choices[i].Message.Refusal != "" {
			resp.Choices[i].Message.Content = TextContent(resp.Choices[i].Message.Refusal)
		}
	}
	return &resp, nil
}

func (s *OpenAIStandard) DecodeStreamChunk(data string) (*ChatResponse, error) {
	update, err := s.NewStreamDecoder().Decode(StreamEvent{Data: []byte(data)})
	if err != nil {
		return nil, err
	}
	return streamUpdateToChatResponse(update), nil
}

func (s *OpenAIStandard) NewStreamDecoder() StreamDecoder {
	return &openAIChatStreamDecoder{}
}

type openAIChatStreamDecoder struct{}

func (d *openAIChatStreamDecoder) Decode(event StreamEvent) (StreamUpdate, error) {
	data := strings.TrimSpace(string(event.Data))
	if data == "" {
		return StreamUpdate{}, nil
	}
	if data == "[DONE]" {
		return StreamUpdate{Done: true}, nil
	}

	var chunk ChatResponse
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return StreamUpdate{}, fmt.Errorf("decode OpenAI chat stream event: %w", err)
	}
	if chunk.Error != nil {
		return StreamUpdate{}, openAIError(chunk.Error.Type, chunk.Error.Code, chunk.Error.Message)
	}

	var update StreamUpdate
	if chunk.Usage != nil {
		usage := TokenUsage{
			PromptTokens:     chunk.Usage.PromptTokens,
			CompletionTokens: chunk.Usage.CompletionTokens,
		}
		if chunk.Usage.PromptTokensDetails != nil {
			usage.PromptTokensCached = chunk.Usage.PromptTokensDetails.CachedTokens
			usage.PromptTokensWritten = chunk.Usage.PromptTokensDetails.CacheWriteTokens
		}
		update.Usage = &usage
	}
	if len(chunk.Choices) == 0 {
		return update, nil
	}

	choice := chunk.Choices[0]
	update.ContentDelta = choice.Delta.Content
	if update.ContentDelta == "" {
		update.ContentDelta = choice.Delta.Refusal
	}
	update.ReasoningDelta = choice.Delta.ReasoningContent
	if update.ReasoningDelta == "" {
		update.ReasoningDelta = choice.Delta.Reasoning
	}
	update.FinishReason = choice.FinishReason
	update.ProviderItems = openAIReasoningDetailItems(choice.Delta.ReasoningDetails, nil)
	for _, tc := range choice.Delta.ToolCalls {
		update.ToolCalls = append(update.ToolCalls, ToolCallDelta{
			Index:     tc.Index,
			ID:        tc.ID,
			Type:      tc.Type,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return update, nil
}

func captureOpenAIReasoningDetails(message *ChatMessage, additional []json.RawMessage) {
	details := append(message.ReasoningDetails, additional...)
	message.ReasoningDetails = nil
	message.ProviderItems = append(message.ProviderItems, openAIReasoningDetailItems(details, nil)...)
}

func openAIReasoningDetailItems(
	details []json.RawMessage,
	seen map[string]struct{},
) []ProviderItem {
	var items []ProviderItem
	for _, raw := range details {
		if len(raw) == 0 || !json.Valid(raw) {
			continue
		}
		key := string(raw)
		if seen != nil {
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
		}
		var descriptor struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(raw, &descriptor)
		itemType := descriptor.Type
		if itemType == "" {
			itemType = "reasoning_detail"
		}
		items = append(items, ProviderItem{
			Standard: "openai",
			Type:     itemType,
			Data:     bytes.Clone(raw),
		})
	}
	return items
}

func openAIError(typ string, code any, message string) error {
	codeString := ""
	if code != nil {
		codeString = fmt.Sprint(code)
	}
	return &APIError{Type: typ, Code: codeString, Message: message}
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
	if thinkingEffort == "none" && !usesOpenAICompletionTokenLimit(modelID) {
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
