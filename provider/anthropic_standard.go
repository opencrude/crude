package provider

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const anthropicAPIVersion = "2023-06-01"

// AnthropicStandard implements the native Anthropic Messages API.
type AnthropicStandard struct{}

func (s *AnthropicStandard) Info() ProtocolInfo {
	return ProtocolInfo{
		ID:          "anthropic",
		Name:        "Anthropic Messages",
		Description: "Native Claude Messages with signed thinking blocks.",
		Root:        "/v1",
		ChatPath:    "messages",
		ModelsPath:  "models",
		Streaming:   FramingSSE,
		Options:     ProtocolOptions{ServiceTiers: []string{"auto", "standard_only"}},
	}
}

// Headers implements ProtocolHeaders: every request carries the API version.
func (s *AnthropicStandard) Headers() http.Header {
	headers := http.Header{}
	headers.Set("anthropic-version", anthropicAPIVersion)
	return headers
}

// AuthHeaders implements ProtocolAuth: Anthropic authenticates via x-api-key.
func (s *AnthropicStandard) AuthHeaders(apiKey string) http.Header {
	if apiKey == "" {
		return nil
	}
	headers := http.Header{}
	headers.Set("x-api-key", apiKey)
	return headers
}

func (s *AnthropicStandard) EncodeRequest(req *ChatRequest) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	system, messages, err := encodeAnthropicMessages(req.Messages)
	if err != nil {
		return nil, err
	}

	maxTokens := req.MaxOutputTokens
	if maxTokens == 0 {
		maxTokens = req.MaxCompletionTokens
	}
	if maxTokens == 0 {
		maxTokens = req.MaxTokens
	}
	if maxTokens == 0 {
		maxTokens = 8192
	}

	wire := map[string]any{
		"model":      req.Model,
		"messages":   messages,
		"max_tokens": maxTokens,
		"stream":     req.Stream,
	}
	if len(system) > 0 {
		wire["system"] = system
	}
	if len(req.Tools) > 0 {
		wire["tools"] = encodeAnthropicTools(req.Tools)
	}
	if req.ToolChoice != nil {
		wire["tool_choice"] = encodeAnthropicToolChoice(req.ToolChoice)
	}
	// Temperature is accepted on current Claude models; only send it when
	// the user explicitly chose a value.
	if req.Temperature != nil {
		wire["temperature"] = *req.Temperature
	}
	return json.Marshal(wire)
}

type anthropicMessageParam struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

func encodeAnthropicMessages(messages []ChatMessage) (system []any, result []anthropicMessageParam, err error) {
	appendMessage := func(role string, blocks []any) {
		if len(blocks) == 0 {
			return
		}
		if len(result) > 0 && result[len(result)-1].Role == role {
			result[len(result)-1].Content = append(result[len(result)-1].Content, blocks...)
			return
		}
		result = append(result, anthropicMessageParam{Role: role, Content: blocks})
	}

	for _, msg := range messages {
		if msg.Role == "system" || msg.Role == "developer" {
			blocks, blockErr := anthropicTextAndImageBlocks(msg, false)
			if blockErr != nil {
				return nil, nil, blockErr
			}
			if len(blocks) > 0 {
				applyCacheControlToLastBlock(blocks, msg.CacheControl)
			}
			for _, block := range blocks {
				if blockMap, ok := block.(map[string]any); ok && blockMap["type"] == "text" {
					system = append(system, block)
				}
			}
			continue
		}

		switch msg.Role {
		case "tool":
			content := any(msg.ContentString())
			if !msg.Content.IsText() {
				contentBlocks, blockErr := anthropicTextAndImageBlocks(msg, true)
				if blockErr != nil {
					return nil, nil, blockErr
				}
				content = contentBlocks
			}
			block := map[string]any{
				"type":        "tool_result",
				"tool_use_id": msg.ToolCallID,
				"content":     content,
			}
			if msg.ToolError {
				block["is_error"] = true
			}
			applyAnthropicCacheControl(block, msg.CacheControl)
			appendMessage("user", []any{block})
		case "assistant":
			blocks := make([]any, 0, len(msg.ProviderItems)+len(msg.ToolCalls)+1)
			hasExactReplay := false
			for _, item := range msg.ProviderItems {
				if item.Standard != "anthropic" || len(item.Data) == 0 {
					continue
				}
				if !json.Valid(item.Data) {
					return nil, nil, fmt.Errorf("invalid preserved Anthropic content block")
				}
				var descriptor struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal(item.Data, &descriptor); err != nil {
					return nil, nil, fmt.Errorf("decode preserved Anthropic content block: %w", err)
				}
				switch descriptor.Type {
				case "text", "refusal", "tool_use":
					hasExactReplay = true
				}
				blocks = append(blocks, json.RawMessage(bytes.Clone(item.Data)))
			}
			if hasExactReplay {
				appendMessage("assistant", blocks)
				continue
			}
			contentBlocks, blockErr := anthropicTextAndImageBlocks(msg, false)
			if blockErr != nil {
				return nil, nil, blockErr
			}
			blocks = append(blocks, contentBlocks...)
			for _, call := range msg.ToolCalls {
				input := any(map[string]any{})
				var decoded map[string]any
				if err := json.Unmarshal([]byte(call.Function.Arguments), &decoded); err == nil && decoded != nil {
					input = decoded
				}
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    call.ID,
					"name":  call.Function.Name,
					"input": input,
				})
			}
			if len(blocks) > 0 {
				applyCacheControlToLastBlock(blocks, msg.CacheControl)
			}
			appendMessage("assistant", blocks)
		default:
			blocks, blockErr := anthropicTextAndImageBlocks(msg, true)
			if blockErr != nil {
				return nil, nil, blockErr
			}
			if len(blocks) > 0 {
				applyCacheControlToLastBlock(blocks, msg.CacheControl)
			}
			appendMessage("user", blocks)
		}
	}
	retainLastAnthropicCacheControl(system)
	var messageBlocks []any
	for i := range result {
		messageBlocks = append(messageBlocks, result[i].Content...)
	}
	retainLastAnthropicCacheControl(messageBlocks)
	return system, result, nil
}

func retainLastAnthropicCacheControl(blocks []any) {
	var last map[string]any
	var value any
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if cache, exists := block["cache_control"]; exists {
			last = block
			value = cache
			delete(block, "cache_control")
		}
	}
	if last != nil {
		last["cache_control"] = value
	}
}

func anthropicTextAndImageBlocks(msg ChatMessage, allowImages bool) ([]any, error) {
	parts := msg.Content.Parts()
	blocks := make([]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text":
			if part.Text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": part.Text})
			}
		case "image_url":
			if !allowImages {
				continue
			}
			if len(part.Data) > 0 {
				// Inline media attachment: encode directly.
				blocks = append(blocks, map[string]any{
					"type": "image",
					"source": map[string]any{
						"type":       "base64",
						"media_type": part.MimeType,
						"data":       base64.StdEncoding.EncodeToString(part.Data),
					},
				})
				continue
			}
			if part.ImageURL == nil || part.ImageURL.URL == "" {
				return nil, fmt.Errorf("anthropic image content is missing a URL")
			}
			source, err := anthropicImageSource(part.ImageURL.URL)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, map[string]any{"type": "image", "source": source})
		case "document":
			if len(part.Data) == 0 {
				continue
			}
			blocks = append(blocks, map[string]any{
				"type": "document",
				"source": map[string]any{
					"type":       "base64",
					"media_type": part.MimeType,
					"data":       base64.StdEncoding.EncodeToString(part.Data),
				},
			})
		}
	}
	return blocks, nil
}

func anthropicImageSource(value string) (map[string]any, error) {
	if !strings.HasPrefix(value, "data:") {
		parsed, err := url.ParseRequestURI(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, fmt.Errorf("anthropic image URL must be an absolute HTTP(S) URL")
		}
		return map[string]any{"type": "url", "url": value}, nil
	}
	metadata, data, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !ok {
		return nil, fmt.Errorf("anthropic image data URL is missing a comma")
	}
	mediaType, encoding, hasEncoding := strings.Cut(metadata, ";")
	if mediaType == "" || !hasEncoding || encoding != "base64" {
		return nil, fmt.Errorf("anthropic image data URL must use base64 encoding")
	}
	switch mediaType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		return nil, fmt.Errorf("anthropic image MIME type %q is not supported", mediaType)
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		if _, rawErr := base64.RawStdEncoding.DecodeString(data); rawErr != nil {
			return nil, fmt.Errorf("anthropic image data is not valid base64: %w", err)
		}
	}
	return map[string]any{
		"type":       "base64",
		"media_type": mediaType,
		"data":       data,
	}, nil
}

func applyAnthropicCacheControl(block map[string]any, cache *CacheControl) {
	if cache == nil || cache.Type == "" {
		return
	}
	block["cache_control"] = map[string]any{"type": cache.Type}
}

func applyCacheControlToLastBlock(blocks []any, cache *CacheControl) {
	if cache == nil || cache.Type == "" || len(blocks) == 0 {
		return
	}
	if block, ok := blocks[len(blocks)-1].(map[string]any); ok {
		applyAnthropicCacheControl(block, cache)
	}
}

func encodeAnthropicTools(tools []ToolDef) []any {
	result := make([]any, 0, len(tools))
	for index, tool := range tools {
		item := map[string]any{
			"name":         tool.Function.Name,
			"description":  tool.Function.Description,
			"input_schema": tool.Function.Parameters,
		}
		if index == len(tools)-1 {
			applyAnthropicCacheControl(item, tool.CacheControl)
		}
		result = append(result, item)
	}
	return result
}

func encodeAnthropicToolChoice(choice any) any {
	if value, ok := choice.(string); ok {
		switch value {
		case "required":
			return map[string]any{"type": "any"}
		case "none":
			return map[string]any{"type": "none"}
		default:
			return map[string]any{"type": value}
		}
	}
	raw, err := json.Marshal(choice)
	if err != nil {
		return choice
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return choice
	}
	if fn, ok := obj["function"].(map[string]any); ok {
		if name, _ := fn["name"].(string); name != "" {
			return map[string]any{"type": "tool", "name": name}
		}
	}
	return choice
}

func (s *AnthropicStandard) DecodeResponse(body []byte) (*ChatResponse, error) {
	var response anthropicResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("unmarshal Anthropic response: %w", err)
	}
	return response.toChatResponse()
}

func (s *AnthropicStandard) NewStreamDecoder() StreamDecoder {
	return &anthropicStreamDecoder{blocks: make(map[int]*anthropicStreamBlock)}
}

// ModelsURL implements ModelLister. Anthropic pages models with an opaque
// after_id cursor; a large page size keeps well-known catalogs to one
// round-trip.
func (s *AnthropicStandard) ModelsURL(base *url.URL, cursor string) *url.URL {
	endpoint := joinPath(base, "models")
	query := endpoint.Query()
	query.Set("limit", "1000")
	if cursor != "" {
		query.Set("after_id", cursor)
	}
	endpoint.RawQuery = query.Encode()
	return endpoint
}

// DecodeModelsPage implements ModelLister for the Anthropic {"data":[...]}
// envelope with has_more/last_id pagination.
func (s *AnthropicStandard) DecodeModelsPage(body []byte) (ModelPage, error) {
	var response struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
		HasMore bool   `json:"has_more"`
		LastID  string `json:"last_id"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return ModelPage{}, fmt.Errorf("unmarshal Anthropic models: %w", err)
	}
	if response.HasMore && response.LastID == "" {
		return ModelPage{}, fmt.Errorf("anthropic models response has_more without last_id")
	}
	models := make([]ModelInfo, 0, len(response.Data))
	for _, model := range response.Data {
		name := model.DisplayName
		if name == "" {
			name = model.ID
		}
		models = append(models, ModelInfo{ID: model.ID, Name: name})
	}
	next := ""
	if response.HasMore {
		next = response.LastID
	}
	return ModelPage{Models: models, Next: next}, nil
}

// RequestExtras implements RequestExtras. Anthropic has no store, ZDR, or
// truncation request switches (retention is organization-level), so only
// thinking configuration applies.
func (s *AnthropicStandard) RequestExtras(modelID, thinkingEffort string, _ ProtocolSettings) map[string]any {
	if thinkingEffort == "" {
		return nil
	}
	if thinkingEffort == "none" {
		if !supportsAnthropicThinking(modelID) {
			return nil
		}
		return map[string]any{"thinking": map[string]any{"type": "disabled"}}
	}
	if !supportsAnthropicAdaptiveThinking(modelID) {
		// Older Claude models reject adaptive thinking. Their manual thinking
		// budgets depend on max_tokens, which is not available at this layer,
		// so omission is safer than sending an invalid request.
		return nil
	}
	return map[string]any{
		"thinking":      map[string]any{"type": "adaptive"},
		"output_config": map[string]any{"effort": thinkingEffort},
	}
}

func supportsAnthropicAdaptiveThinking(modelID string) bool {
	modelID = strings.ToLower(modelID)
	return strings.Contains(modelID, "-4-6") || strings.Contains(modelID, "4.6")
}

func supportsAnthropicThinking(modelID string) bool {
	modelID = strings.ToLower(modelID)
	return strings.Contains(modelID, "-3-7") ||
		strings.Contains(modelID, "3.7") ||
		strings.Contains(modelID, "-4-") ||
		strings.Contains(modelID, "4.")
}

// ContextWindow implements ProtocolMetadata.
func (s *AnthropicStandard) ContextWindow(modelID string) int {
	return LookupContextWindow(modelID)
}

type anthropicResponse struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Role       string            `json:"role"`
	Model      string            `json:"model"`
	Content    []json.RawMessage `json:"content"`
	StopReason string            `json:"stop_reason"`
	Usage      anthropicUsage    `json:"usage"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

type anthropicContentBlock struct {
	Type      string            `json:"type"`
	Text      string            `json:"text"`
	Thinking  string            `json:"thinking"`
	Signature string            `json:"signature"`
	Data      string            `json:"data"`
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Input     json.RawMessage   `json:"input"`
	Citations []json.RawMessage `json:"citations"`
}

func (r *anthropicResponse) toChatResponse() (*ChatResponse, error) {
	if r.Type == "error" || r.Error != nil {
		if r.Error == nil {
			return nil, &APIError{Type: "anthropic_error", Message: "Anthropic request failed"}
		}
		return nil, &APIError{Type: r.Error.Type, Message: r.Error.Message}
	}
	if r.Type != "message" {
		return nil, fmt.Errorf("anthropic response has invalid type %q", r.Type)
	}

	msg := ChatMessage{Role: "assistant", Content: TextContent("")}
	var content strings.Builder
	var reasoning strings.Builder
	for index, raw := range r.Content {
		var block anthropicContentBlock
		if err := json.Unmarshal(raw, &block); err != nil {
			return nil, fmt.Errorf("decode Anthropic content block %d: %w", index, err)
		}
		msg.ProviderItems = append(msg.ProviderItems, ProviderItem{
			Standard: "anthropic", Type: block.Type, Data: bytes.Clone(raw),
		})
		switch block.Type {
		case "text", "refusal":
			content.WriteString(block.Text)
		case "thinking":
			reasoning.WriteString(block.Thinking)
		case "redacted_thinking":
		case "tool_use":
			args := string(block.Input)
			if args == "" {
				args = "{}"
			}
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				Index: index,
				ID:    block.ID,
				Type:  "function",
				Function: FunctionCall{
					Name:      block.Name,
					Arguments: args,
				},
			})
		}
	}
	msg.Content = TextContent(content.String())
	msg.ReasoningContent = reasoning.String()
	msg.Reasoning = reasoning.String()

	promptTokens := r.Usage.InputTokens + r.Usage.CacheCreationInputTokens + r.Usage.CacheReadInputTokens
	resp := &ChatResponse{
		ID:      r.ID,
		Model:   r.Model,
		Choices: []ChatChoice{{Message: msg, FinishReason: r.StopReason}},
		Usage: &ChatUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: r.Usage.OutputTokens,
			TotalTokens:      promptTokens + r.Usage.OutputTokens,
		},
	}
	if r.Usage.CacheReadInputTokens > 0 || r.Usage.CacheCreationInputTokens > 0 {
		resp.Usage.PromptTokensDetails = &PromptTokenDetails{
			CachedTokens:     r.Usage.CacheReadInputTokens,
			CacheWriteTokens: r.Usage.CacheCreationInputTokens,
		}
	}
	return resp, nil
}

type anthropicStreamBlock struct {
	Type      string
	Text      strings.Builder
	Thinking  strings.Builder
	Signature string
	ToolID    string
	ToolName  string
	ToolInput strings.Builder
	Citations []json.RawMessage
	Raw       json.RawMessage
}

type anthropicStreamDecoder struct {
	blocks map[int]*anthropicStreamBlock
	usage  TokenUsage
}

func (d *anthropicStreamDecoder) Decode(event StreamEvent) (StreamUpdate, error) {
	data := bytes.TrimSpace(event.Data)
	if len(data) == 0 {
		return StreamUpdate{}, nil
	}

	var envelope struct {
		Type    string `json:"type"`
		Index   int    `json:"index"`
		Message *struct {
			Usage anthropicUsage `json:"usage"`
		} `json:"message"`
		ContentBlock json.RawMessage `json:"content_block"`
		Delta        struct {
			Type        string          `json:"type"`
			Text        string          `json:"text"`
			PartialJSON string          `json:"partial_json"`
			Thinking    string          `json:"thinking"`
			Signature   string          `json:"signature"`
			StopReason  string          `json:"stop_reason"`
			Citation    json.RawMessage `json:"citation"`
		} `json:"delta"`
		Usage anthropicUsage `json:"usage"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return StreamUpdate{}, fmt.Errorf("decode Anthropic stream event: %w", err)
	}
	typ := envelope.Type
	if typ == "" {
		typ = event.Event
	}

	switch typ {
	case "message_start":
		if envelope.Message != nil {
			d.setInputUsage(envelope.Message.Usage)
			d.usage.CompletionTokens = envelope.Message.Usage.OutputTokens
			usage := d.usage
			return StreamUpdate{Usage: &usage}, nil
		}
		return StreamUpdate{}, nil
	case "content_block_start":
		return d.startBlock(envelope.Index, envelope.ContentBlock)
	case "content_block_delta":
		return d.deltaBlock(envelope.Index, envelope.Delta.Type, envelope.Delta.Text,
			envelope.Delta.PartialJSON, envelope.Delta.Thinking, envelope.Delta.Signature,
			envelope.Delta.Citation)
	case "content_block_stop":
		return d.stopBlock(envelope.Index)
	case "message_delta":
		if envelope.Usage.OutputTokens > 0 {
			d.usage.CompletionTokens = envelope.Usage.OutputTokens
		}
		usage := d.usage
		return StreamUpdate{
			FinishReason: envelope.Delta.StopReason,
			Usage:        &usage,
		}, nil
	case "message_stop":
		return StreamUpdate{Done: true}, nil
	case "ping":
		return StreamUpdate{}, nil
	case "error":
		if envelope.Error == nil {
			return StreamUpdate{}, &APIError{Type: "anthropic_stream_error", Message: "Anthropic stream failed"}
		}
		return StreamUpdate{}, &APIError{Type: envelope.Error.Type, Message: envelope.Error.Message}
	default:
		// Ignore unknown event types so newer optional events remain compatible.
		return StreamUpdate{}, nil
	}
}

func (d *anthropicStreamDecoder) setInputUsage(usage anthropicUsage) {
	d.usage.PromptTokens = usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
	d.usage.PromptTokensCached = usage.CacheReadInputTokens
	d.usage.PromptTokensWritten = usage.CacheCreationInputTokens
}

func (d *anthropicStreamDecoder) startBlock(index int, raw json.RawMessage) (StreamUpdate, error) {
	var block anthropicContentBlock
	if err := json.Unmarshal(raw, &block); err != nil {
		return StreamUpdate{}, fmt.Errorf("decode Anthropic content block start: %w", err)
	}
	state := &anthropicStreamBlock{
		Type: block.Type, Signature: block.Signature,
		ToolID: block.ID, ToolName: block.Name, Raw: bytes.Clone(raw),
	}
	for _, citation := range block.Citations {
		state.Citations = append(state.Citations, bytes.Clone(citation))
	}
	if block.Text != "" {
		state.Text.WriteString(block.Text)
	}
	if block.Thinking != "" {
		state.Thinking.WriteString(block.Thinking)
	}
	if len(block.Input) > 0 && string(block.Input) != "{}" {
		state.ToolInput.Write(block.Input)
	}
	d.blocks[index] = state

	switch block.Type {
	case "text", "refusal":
		return StreamUpdate{ContentDelta: block.Text}, nil
	case "thinking":
		return StreamUpdate{ReasoningDelta: block.Thinking}, nil
	case "tool_use":
		args := state.ToolInput.String()
		return StreamUpdate{ToolCalls: []ToolCallDelta{{
			Index:            index,
			ID:               block.ID,
			Type:             "function",
			Name:             block.Name,
			Arguments:        args,
			ReplaceArguments: args != "",
		}}}, nil
	default:
		return StreamUpdate{}, nil
	}
}

func (d *anthropicStreamDecoder) deltaBlock(
	index int,
	typ, text, partialJSON, thinking, signature string,
	citation json.RawMessage,
) (StreamUpdate, error) {
	state := d.blocks[index]
	switch typ {
	case "text_delta":
		if state != nil {
			state.Text.WriteString(text)
		}
		return StreamUpdate{ContentDelta: text}, nil
	case "input_json_delta":
		if state != nil {
			state.ToolInput.WriteString(partialJSON)
		}
		return StreamUpdate{ToolCalls: []ToolCallDelta{{
			Index:     index,
			Arguments: partialJSON,
		}}}, nil
	case "thinking_delta":
		if state != nil {
			state.Thinking.WriteString(thinking)
		}
		return StreamUpdate{ReasoningDelta: thinking}, nil
	case "signature_delta":
		if state != nil {
			state.Signature += signature
		}
		return StreamUpdate{}, nil
	case "citations_delta":
		if state != nil && len(citation) > 0 {
			state.Citations = append(state.Citations, bytes.Clone(citation))
		}
		return StreamUpdate{}, nil
	default:
		return StreamUpdate{}, nil
	}
}

func (d *anthropicStreamDecoder) stopBlock(index int) (StreamUpdate, error) {
	state := d.blocks[index]
	delete(d.blocks, index)
	if state == nil {
		return StreamUpdate{}, nil
	}
	switch state.Type {
	case "text", "refusal":
		block := map[string]any{
			"type": state.Type,
			"text": state.Text.String(),
		}
		if len(state.Citations) > 0 {
			block["citations"] = state.Citations
		}
		raw, err := json.Marshal(block)
		if err != nil {
			return StreamUpdate{}, fmt.Errorf("encode Anthropic text block: %w", err)
		}
		return anthropicProviderItemUpdate(state.Type, raw), nil
	case "thinking":
		raw, err := json.Marshal(map[string]any{
			"type":      "thinking",
			"thinking":  state.Thinking.String(),
			"signature": state.Signature,
		})
		if err != nil {
			return StreamUpdate{}, fmt.Errorf("encode Anthropic thinking block: %w", err)
		}
		return anthropicProviderItemUpdate(state.Type, raw), nil
	case "redacted_thinking":
		return anthropicProviderItemUpdate(state.Type, bytes.Clone(state.Raw)), nil
	case "tool_use":
		arguments := state.ToolInput.String()
		if arguments == "" {
			arguments = "{}"
		}
		input := json.RawMessage(arguments)
		if !json.Valid(input) {
			input = json.RawMessage("{}")
		}
		raw, err := json.Marshal(map[string]any{
			"type":  "tool_use",
			"id":    state.ToolID,
			"name":  state.ToolName,
			"input": input,
		})
		if err != nil {
			return StreamUpdate{}, fmt.Errorf("encode Anthropic tool-use block: %w", err)
		}
		update := anthropicProviderItemUpdate(state.Type, raw)
		update.ToolCalls = []ToolCallDelta{{
			Index:            index,
			ID:               state.ToolID,
			Type:             "function",
			Name:             state.ToolName,
			Arguments:        arguments,
			ReplaceArguments: true,
		}}
		return update, nil
	default:
		if len(state.Raw) == 0 {
			return StreamUpdate{}, nil
		}
		return anthropicProviderItemUpdate(state.Type, bytes.Clone(state.Raw)), nil
	}
}

func anthropicProviderItemUpdate(typ string, raw json.RawMessage) StreamUpdate {
	return StreamUpdate{ProviderItems: []ProviderItem{{
		Standard: "anthropic", Type: typ, Data: raw,
	}}}
}
