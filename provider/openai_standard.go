package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// OpenAIStandard implements Protocol for the OpenAI Chat Completions API
// format. This covers OpenAI, OpenRouter, Groq, Together, DeepSeek, Mistral,
// Ollama, and any other provider offering an OpenAI-compatible endpoint.
type OpenAIStandard struct{}

func (s *OpenAIStandard) Info() ProtocolInfo {
	return ProtocolInfo{
		ID:          "openai",
		Name:        "OpenAI Chat Completions",
		Description: "Chat Completions and compatible gateways.",
		Root:        "/v1",
		ChatPath:    "chat/completions",
		ModelsPath:  "models",
		Streaming:   FramingSSE,
		Options: ProtocolOptions{
			Store:        true,
			ServiceTiers: []string{"auto", "default", "flex", "priority", "fast"},
		},
	}
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
	if req.Temperature != nil {
		wire["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		wire["top_p"] = *req.TopP
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
		// Structured reasoning is the lossless continuation contract. Replaying
		// text aliases beside it gives gateways competing copies of one stream.
		if len(details) > 0 {
			wire["reasoning_details"] = mergeOpenAIReasoningDetails(details)
		} else if message.ReasoningContent != "" {
			wire["reasoning_content"] = message.ReasoningContent
		} else if message.Reasoning != "" {
			wire["reasoning"] = message.Reasoning
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
		choice := &resp.Choices[i]
		details := append([]json.RawMessage(nil), choice.Message.ReasoningDetails...)
		details = append(details, choice.Delta.ReasoningDetails...)
		if reasoning, ok := openAIVisibleReasoning(details); ok {
			choice.Message.ReasoningContent = reasoning
			choice.Message.Reasoning = reasoning
		}
		captureOpenAIReasoningDetails(&choice.Message, choice.Delta.ReasoningDetails)
		if choice.Message.ContentString() == "" && choice.Message.Refusal != "" {
			choice.Message.Content = TextContent(choice.Message.Refusal)
		}
	}
	return &resp, nil
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
	if reasoning, ok := openAIVisibleReasoning(choice.Delta.ReasoningDetails); ok {
		update.ReasoningDelta = reasoning
	} else {
		update.ReasoningDelta = choice.Delta.ReasoningContent
		if update.ReasoningDelta == "" {
			update.ReasoningDelta = choice.Delta.Reasoning
		}
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

// openAIVisibleReasoning extracts the displayable stream fragments from the
// structured OpenRouter reasoning_details contract. These fragments are the
// canonical stream: legacy delta.reasoning may be synthesized by a gateway and
// has been observed with a newline appended to every token.
func openAIVisibleReasoning(details []json.RawMessage) (string, bool) {
	fragments := openAIVisibleReasoningFragments(details)
	if len(fragments) == 0 {
		return "", false
	}
	var reasoning strings.Builder
	for _, fragment := range fragments {
		reasoning.WriteString(fragment)
	}
	return reasoning.String(), true
}

func openAIVisibleReasoningFragments(details []json.RawMessage) []string {
	var fragments []string
	for _, raw := range details {
		if len(raw) == 0 || !json.Valid(raw) {
			continue
		}
		var detail struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Summary string `json:"summary"`
		}
		if err := json.Unmarshal(raw, &detail); err != nil {
			continue
		}
		var fragment string
		switch detail.Type {
		case "reasoning.text":
			fragment = detail.Text
		case "reasoning.summary":
			fragment = detail.Summary
			if fragment == "" {
				fragment = detail.Text
			}
		default:
			continue
		}
		if fragment == "" {
			continue
		}
		fragments = append(fragments, fragment)
	}
	return fragments
}

// mergeOpenAIReasoningDetails implodes streaming fragments before replaying an
// assistant message. OpenRouter tags fragments from one logical detail with
// the same (type, index); sending those fragments back as separate array items
// makes providers interpret each token as a separate reasoning block.
func mergeOpenAIReasoningDetails(details []json.RawMessage) []json.RawMessage {
	if len(details) <= 1 {
		return details
	}
	merged := make([]json.RawMessage, 0, len(details))
	for index := 0; index < len(details); {
		key, mergeable := openAIReasoningDetailMergeKey(details[index])
		if !mergeable {
			merged = append(merged, bytes.Clone(details[index]))
			index++
			continue
		}
		end := index + 1
		for end < len(details) {
			nextKey, nextMergeable := openAIReasoningDetailMergeKey(details[end])
			if !nextMergeable || nextKey != key {
				break
			}
			end++
		}
		if end == index+1 {
			merged = append(merged, bytes.Clone(details[index]))
		} else {
			merged = append(merged, mergeOpenAIReasoningDetailRun(details[index:end]))
		}
		index = end
	}
	return merged
}

func openAIReasoningDetailMergeKey(raw json.RawMessage) (string, bool) {
	var detail map[string]json.RawMessage
	if err := json.Unmarshal(raw, &detail); err != nil {
		return "", false
	}
	indexRaw, ok := detail["index"]
	if !ok || string(indexRaw) == "null" {
		return "", false
	}
	var index any
	if err := json.Unmarshal(indexRaw, &index); err != nil || index == nil {
		return "", false
	}
	var detailType string
	if typeRaw := detail["type"]; len(typeRaw) > 0 {
		_ = json.Unmarshal(typeRaw, &detailType)
	}
	return fmt.Sprintf("%s\x00%T:%v", detailType, index, index), true
}

func mergeOpenAIReasoningDetailRun(run []json.RawMessage) json.RawMessage {
	merged := make(map[string]json.RawMessage)
	textParts := map[string][]string{"text": nil, "summary": nil}
	textSeen := make(map[string]bool, len(textParts))
	for _, raw := range run {
		var detail map[string]json.RawMessage
		if err := json.Unmarshal(raw, &detail); err != nil {
			continue
		}
		for key, value := range detail {
			if _, isText := textParts[key]; isText {
				var fragment string
				if err := json.Unmarshal(value, &fragment); err == nil {
					textParts[key] = append(textParts[key], fragment)
					textSeen[key] = true
					continue
				}
			}
			if string(value) != "null" {
				merged[key] = bytes.Clone(value)
			}
		}
	}
	for key, parts := range textParts {
		if !textSeen[key] {
			continue
		}
		encoded, _ := json.Marshal(strings.Join(parts, ""))
		merged[key] = encoded
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return bytes.Clone(run[0])
	}
	return encoded
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

// ModelsURL implements ModelLister.
func (s *OpenAIStandard) ModelsURL(base *url.URL, cursor string) *url.URL {
	return joinPath(base, "models")
}

// DecodeModelsPage implements ModelLister for the OpenAI {"data":[...]}
// envelope.
func (s *OpenAIStandard) DecodeModelsPage(body []byte) (ModelPage, error) {
	var result struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return ModelPage{}, fmt.Errorf("unmarshal models: %w", err)
	}

	models := make([]ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, ModelInfo{
			ID:   m.ID,
			Name: m.Name,
		})
	}
	return ModelPage{Models: models}, nil
}

// RequestExtras implements RequestExtras with the standard OpenAI reasoning
// effort format. Chat Completions does not store requests by default; an
// explicit Store setting opts the conversation into server-side storage.
func (s *OpenAIStandard) RequestExtras(modelID, thinkingEffort string, settings ProtocolSettings) map[string]any {
	extras := map[string]any{}
	if settings.Store != nil && *settings.Store && !settings.ZeroDataRetention {
		extras["store"] = true
	}
	if settings.ServiceTier != "" {
		extras["service_tier"] = settings.ServiceTier
	}
	if thinkingEffort != "" && !(thinkingEffort == "none" && !usesOpenAICompletionTokenLimit(modelID)) {
		extras["reasoning_effort"] = thinkingEffort
	}
	if len(extras) == 0 {
		return nil
	}
	return extras
}

// ContextWindow implements ProtocolMetadata.
func (s *OpenAIStandard) ContextWindow(modelID string) int {
	return LookupContextWindow(modelID)
}
