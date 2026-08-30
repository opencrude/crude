package provider

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// OpenAIResponsesStandard implements the native OpenAI Responses API. Model
// listing uses the same envelope as Chat Completions, so the OpenAI protocol
// is embedded and its ModelLister implementation is promoted.
type OpenAIResponsesStandard struct {
	OpenAIStandard
}

func (s *OpenAIResponsesStandard) Info() ProtocolInfo {
	return ProtocolInfo{
		ID:          "openai-responses",
		Name:        "OpenAI Responses",
		Description: "Native Responses API with reasoning-item continuity.",
		Root:        "/v1",
		ChatPath:    "responses",
		ModelsPath:  "models",
		Streaming:   FramingSSE,
		Options: ProtocolOptions{
			Store:              true,
			StoreDefault:       true,
			ZeroDataRetention:  true,
			ReasoningSummaries: true,
			Truncation:         true,
			ServiceTiers:       []string{"auto", "default", "flex", "priority", "fast"},
		},
	}
}

func (s *OpenAIResponsesStandard) EncodeRequest(req *ChatRequest) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	input, err := encodeResponsesInput(req.Messages)
	if err != nil {
		return nil, err
	}
	wire := map[string]any{
		"model":  req.Model,
		"input":  input,
		"stream": req.Stream,
	}
	if len(req.Tools) > 0 {
		wire["tools"] = encodeResponsesTools(req.Tools)
	}
	if req.ToolChoice != nil {
		wire["tool_choice"] = encodeResponsesToolChoice(req.ToolChoice)
	}
	maxOutput := req.MaxOutputTokens
	if maxOutput == 0 {
		maxOutput = req.MaxCompletionTokens
	}
	if maxOutput == 0 {
		maxOutput = req.MaxTokens
	}
	if maxOutput > 0 {
		wire["max_output_tokens"] = maxOutput
	}
	if req.Temperature != nil {
		wire["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		wire["top_p"] = *req.TopP
	}
	return json.Marshal(wire)
}

func encodeResponsesInput(messages []ChatMessage) ([]any, error) {
	input := make([]any, 0, len(messages))
	for _, msg := range messages {
		preservedMessage := false
		preservedCalls := make(map[string]bool)
		if msg.Role == "assistant" {
			for _, item := range msg.ProviderItems {
				if item.Standard != "openai-responses" || len(item.Data) == 0 {
					continue
				}
				if !json.Valid(item.Data) {
					return nil, fmt.Errorf("invalid preserved OpenAI response item")
				}
				var descriptor struct {
					Type   string `json:"type"`
					CallID string `json:"call_id"`
				}
				if err := json.Unmarshal(item.Data, &descriptor); err != nil {
					return nil, fmt.Errorf("decode preserved OpenAI response item: %w", err)
				}
				switch descriptor.Type {
				case "message":
					preservedMessage = true
				case "function_call":
					preservedCalls[descriptor.CallID] = true
				}
				input = append(input, json.RawMessage(bytes.Clone(item.Data)))
			}
		}

		switch msg.Role {
		case "tool":
			output := any(msg.ContentString())
			if msg.Content.HasImage() {
				content, err := encodeResponsesContent(msg)
				if err != nil {
					return nil, err
				}
				output = content
			}
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": msg.ToolCallID,
				"output":  output,
			})
			continue
		case "user", "system", "developer", "assistant":
			if (msg.ContentString() != "" || msg.Content.HasImage()) &&
				(msg.Role != "assistant" || !preservedMessage) {
				content, err := encodeResponsesContent(msg)
				if err != nil {
					return nil, err
				}
				input = append(input, map[string]any{
					"role":    msg.Role,
					"content": content,
				})
			}
		default:
			if msg.ContentString() != "" {
				input = append(input, map[string]any{
					"role":    "user",
					"content": []any{map[string]any{"type": "input_text", "text": msg.ContentString()}},
				})
			}
		}

		if msg.Role == "assistant" {
			for _, call := range msg.ToolCalls {
				if preservedCalls[call.ID] {
					continue
				}
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   call.ID,
					"name":      call.Function.Name,
					"arguments": call.Function.Arguments,
				})
			}
		}
	}
	return input, nil
}

func encodeResponsesContent(msg ChatMessage) ([]any, error) {
	parts := msg.Content.Parts()
	content := make([]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text":
			// Reconstructed conversation messages use the Responses
			// EasyInputMessage shape. output_text is reserved for raw output
			// message items returned by the API.
			content = append(content, map[string]any{"type": "input_text", "text": part.Text})
		case "image_url":
			if msg.Role == "assistant" {
				continue
			}
			if part.ImageURL == nil || part.ImageURL.URL == "" {
				return nil, fmt.Errorf("OpenAI image content is missing a URL")
			}
			if err := validateOpenAIImageURL(part.ImageURL.URL); err != nil {
				return nil, err
			}
			image := map[string]any{
				"type":      "input_image",
				"image_url": part.ImageURL.URL,
			}
			if part.ImageURL.Detail != "" {
				image["detail"] = part.ImageURL.Detail
			}
			content = append(content, image)
		}
	}
	return content, nil
}

func validateOpenAIImageURL(value string) error {
	if !strings.HasPrefix(value, "data:") {
		parsed, err := url.ParseRequestURI(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("OpenAI image URL must be an absolute HTTP(S) URL")
		}
		return nil
	}
	metadata, data, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !ok {
		return fmt.Errorf("OpenAI image data URL is missing a comma")
	}
	mediaType, encoding, hasEncoding := strings.Cut(metadata, ";")
	if !hasEncoding || encoding != "base64" {
		return fmt.Errorf("OpenAI image data URL must use base64 encoding")
	}
	switch mediaType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		return fmt.Errorf("OpenAI image MIME type %q is not supported", mediaType)
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		if _, rawErr := base64.RawStdEncoding.DecodeString(data); rawErr != nil {
			return fmt.Errorf("OpenAI image data is not valid base64: %w", err)
		}
	}
	return nil
}

func encodeResponsesTools(tools []ToolDef) []any {
	result := make([]any, 0, len(tools))
	for _, tool := range tools {
		result = append(result, map[string]any{
			"type":        "function",
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
			"parameters":  tool.Function.Parameters,
		})
	}
	return result
}

func encodeResponsesToolChoice(choice any) any {
	raw, err := json.Marshal(choice)
	if err != nil {
		return choice
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return choice
	}
	fn, ok := obj["function"].(map[string]any)
	if !ok {
		return choice
	}
	name, _ := fn["name"].(string)
	if name == "" {
		return choice
	}
	return map[string]any{"type": "function", "name": name}
}

func (s *OpenAIResponsesStandard) DecodeResponse(body []byte) (*ChatResponse, error) {
	var response openAIResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("unmarshal OpenAI Responses response: %w", err)
	}
	return response.toChatResponse()
}

func (s *OpenAIResponsesStandard) NewStreamDecoder() StreamDecoder {
	return &openAIResponsesStreamDecoder{seenProviderItems: make(map[string]struct{})}
}

// ModelsURL implements ModelLister; it inherits DecodeModelsPage from the
// embedded OpenAI envelope decoder.
func (s *OpenAIResponsesStandard) ModelsURL(base *url.URL, cursor string) *url.URL {
	return joinPath(base, "models")
}

// RequestExtras implements RequestExtras. The Responses API stores
// conversations server-side by default (enabling context reuse across
// requests); a user-chosen Store=false or ZeroDataRetention turns that off,
// and ZDR additionally requests encrypted reasoning passthrough so the
// reasoning continuity survives without server storage.
func (s *OpenAIResponsesStandard) RequestExtras(modelID, thinkingEffort string, settings ProtocolSettings) map[string]any {
	extras := map[string]any{}
	if !settings.ResolveStore(true) {
		extras["store"] = false
		if settings.ZeroDataRetention {
			extras["include"] = []string{"reasoning.encrypted_content"}
		}
	}
	if settings.Truncation != nil && *settings.Truncation {
		// "disabled" is the wire default; only the override is sent.
		extras["truncation"] = "auto"
	}
	if settings.ServiceTier != "" {
		extras["service_tier"] = settings.ServiceTier
	}
	if thinkingEffort != "" && (thinkingEffort != "none" || usesOpenAICompletionTokenLimit(modelID)) {
		reasoning := map[string]any{"effort": thinkingEffort}
		if thinkingEffort != "none" && settings.ResolveReasoningSummaries(true) {
			reasoning["summary"] = "auto"
		}
		extras["reasoning"] = reasoning
	}
	if len(extras) == 0 {
		return nil
	}
	return extras
}

// ContextWindow implements ProtocolMetadata.
func (s *OpenAIResponsesStandard) ContextWindow(modelID string) int {
	return LookupContextWindow(modelID)
}

type openAIResponse struct {
	ID                string                   `json:"id"`
	Model             string                   `json:"model"`
	Status            string                   `json:"status"`
	Output            []json.RawMessage        `json:"output"`
	Usage             *openAIResponsesUsage    `json:"usage"`
	Error             *openAIResponsesError    `json:"error"`
	IncompleteDetails *openAIIncompleteDetails `json:"incomplete_details"`
}

type openAIResponsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	InputTokensDetails *struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
}

type openAIResponsesError struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type openAIIncompleteDetails struct {
	Reason string `json:"reason"`
}

type openAIOutputItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
	Summary []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"summary"`
}

func (r *openAIResponse) toChatResponse() (*ChatResponse, error) {
	if r.Error != nil {
		return nil, &APIError{Type: r.Error.Type, Code: r.Error.Code, Message: r.Error.Message}
	}
	switch r.Status {
	case "completed", "incomplete":
	case "failed":
		return nil, &APIError{Type: "response_failed", Message: "OpenAI response failed"}
	case "cancelled":
		return nil, &APIError{Type: "response_cancelled", Message: "OpenAI response was cancelled"}
	case "queued", "in_progress":
		return nil, &APIError{
			Type:    "response_not_completed",
			Message: fmt.Sprintf("OpenAI response is still %s", r.Status),
		}
	case "":
		return nil, fmt.Errorf("OpenAI Responses response is missing status")
	default:
		return nil, fmt.Errorf("OpenAI Responses response has unknown status %q", r.Status)
	}

	msg := ChatMessage{Role: "assistant", Content: TextContent("")}
	var text strings.Builder
	var reasoning strings.Builder
	for index, raw := range r.Output {
		var item openAIOutputItem
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, fmt.Errorf("decode OpenAI output item %d: %w", index, err)
		}
		msg.ProviderItems = append(msg.ProviderItems, ProviderItem{
			Standard: "openai-responses",
			Type:     item.Type,
			Data:     bytes.Clone(raw),
		})
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				switch part.Type {
				case "output_text":
					text.WriteString(part.Text)
				case "refusal":
					text.WriteString(firstNonEmpty(part.Refusal, part.Text))
				}
			}
		case "function_call":
			if responseOutputItemComplete(item.Status) {
				arguments := item.Arguments
				if arguments == "" {
					arguments = "{}"
				}
				msg.ToolCalls = append(msg.ToolCalls, ToolCall{
					Index: index,
					ID:    item.CallID,
					Type:  "function",
					Function: FunctionCall{
						Name:      item.Name,
						Arguments: arguments,
					},
				})
			}
		case "reasoning":
			for _, part := range item.Summary {
				reasoning.WriteString(part.Text)
			}
			for _, part := range item.Content {
				if part.Type == "reasoning_text" {
					reasoning.WriteString(part.Text)
				}
			}
		}
	}
	msg.Content = TextContent(text.String())
	msg.ReasoningContent = reasoning.String()
	msg.Reasoning = reasoning.String()

	finishReason := "stop"
	if len(msg.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}
	if r.Status == "incomplete" {
		finishReason = "incomplete"
		if r.IncompleteDetails != nil && r.IncompleteDetails.Reason != "" {
			finishReason = r.IncompleteDetails.Reason
		}
	}
	resp := &ChatResponse{
		ID:      r.ID,
		Model:   r.Model,
		Choices: []ChatChoice{{Message: msg, FinishReason: finishReason}},
	}
	if r.Usage != nil {
		resp.Usage = &ChatUsage{
			PromptTokens:     r.Usage.InputTokens,
			CompletionTokens: r.Usage.OutputTokens,
			TotalTokens:      r.Usage.InputTokens + r.Usage.OutputTokens,
		}
		if r.Usage.InputTokensDetails != nil {
			resp.Usage.PromptTokensDetails = &PromptTokenDetails{
				CachedTokens:     r.Usage.InputTokensDetails.CachedTokens,
				CacheWriteTokens: r.Usage.InputTokensDetails.CacheWriteTokens,
			}
		}
	}
	return resp, nil
}

func responseOutputItemComplete(status string) bool {
	return status == "" || status == "completed"
}

type openAIResponsesStreamDecoder struct {
	seenProviderItems map[string]struct{}
	content           strings.Builder
	reasoning         strings.Builder
}

func (d *openAIResponsesStreamDecoder) Decode(event StreamEvent) (StreamUpdate, error) {
	data := bytes.TrimSpace(event.Data)
	if len(data) == 0 {
		return StreamUpdate{}, nil
	}
	if bytes.Equal(data, []byte("[DONE]")) {
		return StreamUpdate{Done: true}, nil
	}

	var envelope struct {
		Type        string                `json:"type"`
		Delta       string                `json:"delta"`
		Text        string                `json:"text"`
		Refusal     string                `json:"refusal"`
		Arguments   string                `json:"arguments"`
		Name        string                `json:"name"`
		ItemID      string                `json:"item_id"`
		OutputIndex int                   `json:"output_index"`
		Item        json.RawMessage       `json:"item"`
		Response    *openAIResponse       `json:"response"`
		Error       *openAIResponsesError `json:"error"`
		Code        string                `json:"code"`
		Message     string                `json:"message"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return StreamUpdate{}, fmt.Errorf("decode OpenAI Responses stream event: %w", err)
	}
	typ := envelope.Type
	if typ == "" {
		typ = event.Event
	}

	switch typ {
	case "response.output_text.delta", "response.refusal.delta":
		d.content.WriteString(envelope.Delta)
		return StreamUpdate{ContentDelta: envelope.Delta}, nil
	case "response.output_text.done", "response.refusal.done":
		delta := unseenStreamSuffix(d.content.String(), firstNonEmpty(envelope.Text, envelope.Refusal, envelope.Delta))
		d.content.WriteString(delta)
		return StreamUpdate{ContentDelta: delta}, nil
	case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
		d.reasoning.WriteString(envelope.Delta)
		return StreamUpdate{ReasoningDelta: envelope.Delta}, nil
	case "response.reasoning_text.done", "response.reasoning_summary_text.done":
		delta := unseenStreamSuffix(d.reasoning.String(), firstNonEmpty(envelope.Text, envelope.Delta))
		d.reasoning.WriteString(delta)
		return StreamUpdate{ReasoningDelta: delta}, nil
	case "response.function_call_arguments.delta":
		return StreamUpdate{ToolCalls: []ToolCallDelta{{
			Index:     envelope.OutputIndex,
			Arguments: envelope.Delta,
		}}}, nil
	case "response.function_call_arguments.done":
		arguments := envelope.Arguments
		if arguments == "" {
			arguments = "{}"
		}
		return StreamUpdate{ToolCalls: []ToolCallDelta{{
			Index:            envelope.OutputIndex,
			Name:             envelope.Name,
			Arguments:        arguments,
			ReplaceArguments: true,
		}}}, nil
	case "response.output_item.added", "response.output_item.done":
		return d.decodeOutputItem(envelope.OutputIndex, envelope.Item, typ == "response.output_item.done")
	case "response.completed", "response.incomplete":
		if envelope.Response == nil {
			return StreamUpdate{}, fmt.Errorf("%s event is missing response", typ)
		}
		return d.decodeTerminalResponse(envelope.Response, typ == "response.incomplete")
	case "response.failed":
		if envelope.Response != nil && envelope.Response.Error != nil {
			apiErr := envelope.Response.Error
			return StreamUpdate{}, &APIError{Type: apiErr.Type, Code: apiErr.Code, Message: apiErr.Message}
		}
		return StreamUpdate{}, &APIError{Type: "response_failed", Message: "OpenAI response failed"}
	case "response.cancelled":
		return StreamUpdate{}, &APIError{Type: "response_cancelled", Message: "OpenAI response was cancelled"}
	case "error":
		if envelope.Error != nil {
			return StreamUpdate{}, &APIError{Type: envelope.Error.Type, Code: envelope.Error.Code, Message: envelope.Error.Message}
		}
		return StreamUpdate{}, &APIError{Type: "stream_error", Code: envelope.Code, Message: envelope.Message}
	default:
		// Unknown semantic events are ignored for forward compatibility.
		return StreamUpdate{}, nil
	}
}

func (d *openAIResponsesStreamDecoder) decodeOutputItem(index int, raw json.RawMessage, done bool) (StreamUpdate, error) {
	if len(raw) == 0 {
		return StreamUpdate{}, nil
	}
	var item openAIOutputItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return StreamUpdate{}, fmt.Errorf("decode OpenAI response output item: %w", err)
	}

	var update StreamUpdate
	if done {
		itemID := item.ID
		if itemID == "" {
			itemID = fmt.Sprintf("%s:%d", item.Type, index)
		}
		if _, exists := d.seenProviderItems[itemID]; !exists {
			d.seenProviderItems[itemID] = struct{}{}
			update.ProviderItems = append(update.ProviderItems, ProviderItem{
				Standard: "openai-responses",
				Type:     item.Type,
				Data:     bytes.Clone(raw),
			})
		}
	}

	switch item.Type {
	case "function_call":
		if done && !responseOutputItemComplete(item.Status) {
			return update, nil
		}
		arguments := item.Arguments
		if done && arguments == "" {
			arguments = "{}"
		}
		update.ToolCalls = []ToolCallDelta{{
			Index:            index,
			ID:               item.CallID,
			Type:             "function",
			Name:             item.Name,
			Arguments:        arguments,
			ReplaceArguments: done,
		}}
		return update, nil
	case "reasoning":
		if !done {
			return StreamUpdate{}, nil
		}
		return update, nil
	default:
		return update, nil
	}
}

func (d *openAIResponsesStreamDecoder) decodeTerminalResponse(response *openAIResponse, incomplete bool) (StreamUpdate, error) {
	if response.Error != nil {
		return StreamUpdate{}, &APIError{
			Type: response.Error.Type, Code: response.Error.Code, Message: response.Error.Message,
		}
	}
	expectedStatus := "completed"
	if incomplete {
		expectedStatus = "incomplete"
	}
	if response.Status != expectedStatus {
		return StreamUpdate{}, fmt.Errorf(
			"OpenAI terminal event has response status %q, want %q",
			response.Status, expectedStatus,
		)
	}

	update := StreamUpdate{Done: true, FinishReason: "stop"}
	if incomplete {
		update.FinishReason = "incomplete"
		if response.IncompleteDetails != nil && response.IncompleteDetails.Reason != "" {
			update.FinishReason = response.IncompleteDetails.Reason
		}
	}
	if response.Usage != nil {
		usage := TokenUsage{
			PromptTokens:     response.Usage.InputTokens,
			CompletionTokens: response.Usage.OutputTokens,
		}
		if response.Usage.InputTokensDetails != nil {
			usage.PromptTokensCached = response.Usage.InputTokensDetails.CachedTokens
			usage.PromptTokensWritten = response.Usage.InputTokensDetails.CacheWriteTokens
		}
		update.Usage = &usage
	}

	var terminalContent strings.Builder
	var terminalReasoning strings.Builder
	for index, raw := range response.Output {
		var item openAIOutputItem
		if err := json.Unmarshal(raw, &item); err != nil {
			return StreamUpdate{}, fmt.Errorf("decode terminal OpenAI output item %d: %w", index, err)
		}
		itemID := item.ID
		if itemID == "" {
			itemID = fmt.Sprintf("%s:%d", item.Type, index)
		}
		if _, exists := d.seenProviderItems[itemID]; !exists {
			d.seenProviderItems[itemID] = struct{}{}
			update.ProviderItems = append(update.ProviderItems, ProviderItem{
				Standard: "openai-responses",
				Type:     item.Type,
				Data:     bytes.Clone(raw),
			})
		}
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				switch part.Type {
				case "output_text":
					terminalContent.WriteString(part.Text)
				case "refusal":
					terminalContent.WriteString(firstNonEmpty(part.Refusal, part.Text))
				}
			}
		case "function_call":
			if responseOutputItemComplete(item.Status) {
				if !incomplete {
					update.FinishReason = "tool_calls"
				}
				arguments := item.Arguments
				if arguments == "" {
					arguments = "{}"
				}
				update.ToolCalls = append(update.ToolCalls, ToolCallDelta{
					Index:            index,
					ID:               item.CallID,
					Type:             "function",
					Name:             item.Name,
					Arguments:        arguments,
					ReplaceArguments: true,
				})
			}
		case "reasoning":
			for _, part := range item.Summary {
				terminalReasoning.WriteString(part.Text)
			}
			for _, part := range item.Content {
				if part.Type == "reasoning_text" {
					terminalReasoning.WriteString(part.Text)
				}
			}
		}
	}
	update.ContentDelta = unseenStreamSuffix(d.content.String(), terminalContent.String())
	update.ReasoningDelta = unseenStreamSuffix(d.reasoning.String(), terminalReasoning.String())
	d.content.WriteString(update.ContentDelta)
	d.reasoning.WriteString(update.ReasoningDelta)
	return update, nil
}

func unseenStreamSuffix(seen, complete string) string {
	if complete == "" || complete == seen {
		return ""
	}
	if strings.HasPrefix(complete, seen) {
		return complete[len(seen):]
	}
	if seen == "" {
		return complete
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
