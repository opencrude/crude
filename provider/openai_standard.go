package provider

import (
	"bytes"
	"crude/types/reasoning"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
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
		tools := make([]any, 0, len(req.Tools))
		for _, tool := range req.Tools {
			tool = jsonFunctionTool(tool)
			toolType := tool.Type
			if toolType == "" {
				toolType = "function"
			}
			tools = append(tools, openAIChatToolParam{
				Type: toolType, Function: chatFunctionDef(tool.Function),
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

// JSON-function protocols carry the same native/freeform input in one required
// string field. This is a wire projection; the offered canonical tool stays intact.
func jsonFunctionTool(tool ToolDef) ToolDef {
	if tool.Type != "custom" {
		return tool
	}
	tool.Type, tool.Format = "function", nil
	tool.Function.Parameters = json.RawMessage(`{"type":"object","properties":{"input":{"type":"string"}},"required":["input"],"additionalProperties":false}`)
	if tool.Function.Name == "apply_patch" {
		tool.Function.Description = "Apply a patch. Pass the complete *** Begin Patch ... *** End Patch text in the input string."
	}
	return tool
}

func encodeOpenAIChatMessages(messages []ChatMessage) ([]any, error) {
	result := make([]any, 0, len(messages))
	var deferredToolMedia []ContentPart
	toolNames := make(map[string]string)
	for index, message := range messages {
		for _, call := range message.ToolCalls {
			toolNames[call.ID] = call.Function.Name
		}
		contentSource := message.Content
		if message.Role == "tool" && !message.Content.IsText() {
			// Chat Completions tool messages accept text only. Keep the protocol
			// association here and move the COMPLETE ordered evidence, not only
			// its media, after all sibling tool replies have been acknowledged.
			attribution := fmt.Sprintf("Tool-result evidence for tool_call_id %q (tool %q). This is remote tool output, not new human input.", message.ToolCallID, toolNames[message.ToolCallID])
			parts := append([]ContentPart(nil), message.Content.Parts()...)
			if len(parts) > 0 && parts[0].Type == "text" {
				parts[0].Text = attribution + "\n" + parts[0].Text
			} else {
				deferredToolMedia = append(deferredToolMedia, ContentPart{Type: "text", Text: attribution})
			}
			deferredToolMedia = append(deferredToolMedia, parts...)
			contentSource = TextContent("Tool-result evidence for this call follows in the next media-bearing user message; that message is tool output, not human input.")
		}
		content, err := encodeOpenAIChatContent(contentSource)
		if err != nil {
			return nil, err
		}
		wire := map[string]any{
			"role":    message.Role,
			"content": content,
		}
		if message.Refusal != "" {
			wire["refusal"] = message.Refusal
		}
		if message.ToolCallID != "" {
			wire["tool_call_id"] = message.ToolCallID
		}
		if len(message.ToolCalls) > 0 {
			calls := make([]any, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				if call.Type == "custom" {
					args, err := json.Marshal(map[string]string{"input": call.Function.Arguments})
					if err != nil {
						return nil, fmt.Errorf("encode custom tool input: %w", err)
					}
					calls = append(calls, openAIChatToolCallParam{ID: call.ID, Type: "function", Function: FunctionCall{Name: call.Function.Name, Arguments: string(args)}})
					continue
				}
				callType := call.Type
				if callType == "" {
					callType = "function"
				}
				calls = append(calls, openAIChatToolCallParam{
					ID: call.ID, Type: callType, Function: chatFunctionCall(call.Function),
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
		toolStreakEnded := message.Role == "tool" && (index == len(messages)-1 || messages[index+1].Role != "tool")
		if toolStreakEnded && len(deferredToolMedia) > 0 {
			mediaContent, mediaErr := encodeOpenAIChatContent(PartsContent(deferredToolMedia))
			if mediaErr != nil {
				return nil, mediaErr
			}
			result = append(result, map[string]any{"role": "user", "content": mediaContent})
			deferredToolMedia = nil
		}
	}
	return result, nil
}

func encodeOpenAIChatContent(content MessageContent) (any, error) {
	if content.IsText() {
		return content.String(), nil
	}
	parts := content.Parts()
	wire := make([]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text":
			wire = append(wire, map[string]any{"type": "text", "text": part.Text})
		case "image_url":
			if part.ImageURL == nil || part.ImageURL.URL == "" {
				return nil, fmt.Errorf("OpenAI-compatible image content is missing a URL")
			}
			wire = append(wire, map[string]any{"type": "image_url", "image_url": part.ImageURL})
		case "document":
			wire = append(wire, map[string]any{
				"type": "file",
				"file": map[string]any{
					"filename":  part.Filename,
					"file_data": mediaDataURL(part),
				},
			})
		case "audio":
			format := strings.TrimPrefix(strings.ToLower(filepath.Ext(part.Filename)), ".")
			if format == "" {
				format = audioFormatFromMIME(part.MimeType)
			}
			wire = append(wire, map[string]any{
				"type": "input_audio",
				"input_audio": map[string]any{
					"data":   base64.StdEncoding.EncodeToString(part.Data),
					"format": format,
				},
			})
		case "video":
			wire = append(wire, map[string]any{
				"type":      "video_url",
				"video_url": map[string]any{"url": mediaDataURL(part)},
			})
		default:
			return nil, fmt.Errorf("OpenAI-compatible content part %q is unsupported", part.Type)
		}
	}
	return wire, nil
}

func mediaDataURL(part ContentPart) string {
	return "data:" + part.MimeType + ";base64," + base64.StdEncoding.EncodeToString(part.Data)
}

func audioFormatFromMIME(mime string) string {
	switch strings.ToLower(mime) {
	case "audio/mpeg":
		return "mp3"
	case "audio/wav", "audio/x-wav":
		return "wav"
	default:
		return strings.TrimPrefix(strings.ToLower(mime), "audio/")
	}
}

func (s *OpenAIStandard) DecodeResponse(body []byte) (*ChatResponse, error) {
	var resp ChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if resp.Error != nil {
		return nil, openAIError(resp.Error)
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
			var visible string
			for _, delta := range openAIVisibleReasoningFragments(details) {
				visible = choice.Message.ReasoningDisplay.Append(visible, delta)
			}
			choice.Message.ReasoningContent, choice.Message.Reasoning = visible, visible
		}
		captureOpenAIReasoningDetails(&choice.Message, choice.Delta.ReasoningDetails)
		if choice.Message.ContentString() == "" && choice.Message.Refusal != "" {
			choice.Message.Content = TextContent(choice.Message.Refusal)
		}
	}
	return &resp, nil
}

func (s *OpenAIStandard) NewStreamDecoder() StreamDecoder {
	return &streamErrorDecoder{protocol: s.Info().ID, inner: &openAIChatStreamDecoder{}}
}

type openAIChatStreamDecoder struct{ pendingCustom bool }

func (d *openAIChatStreamDecoder) Decode(event StreamEvent) (StreamUpdate, error) {
	data := strings.TrimSpace(string(event.Data))
	if data == "" {
		return StreamUpdate{}, nil
	}
	if data == "[DONE]" {
		if d.pendingCustom {
			return StreamUpdate{}, fmt.Errorf("stream ended before custom tool call completion")
		}
		return StreamUpdate{Done: true}, nil
	}

	var chunk ChatResponse
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return StreamUpdate{}, fmt.Errorf("decode OpenAI chat stream event: %w", err)
	}
	if chunk.Error != nil {
		return StreamUpdate{}, openAIError(chunk.Error)
	}

	var update StreamUpdate
	if chunk.Usage != nil {
		usage := chunk.Usage.normalized()
		usage.ServiceTier = chunk.ServiceTier
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
	if fragments := openAIVisibleReasoningFragments(choice.Delta.ReasoningDetails); len(fragments) > 0 {
		update.Reasoning = fragments
	} else {
		text := firstNonEmpty(choice.Delta.ReasoningContent, choice.Delta.Reasoning)
		if text != "" && len(choice.Delta.ReasoningDetails) == 0 {
			// Compatible gateways can flatten either raw or summarized text.
			update.Reasoning = []reasoning.Delta{{Text: text}}
		}
	}
	update.FinishReason = choice.FinishReason
	update.ProviderItems = openAIReasoningDetailItems(choice.Delta.ReasoningDetails, nil)
	for _, tc := range choice.Delta.ToolCalls {
		if tc.Type == "custom" || tc.Function.Name == "apply_patch" {
			d.pendingCustom = true
		}
		update.ToolCalls = append(update.ToolCalls, ToolCallDelta{
			Index:     tc.Index,
			ID:        tc.ID,
			Type:      tc.Type,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	if choice.FinishReason != "" {
		d.pendingCustom = false
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
		reasoning.WriteString(fragment.Text)
	}
	return reasoning.String(), true
}

func openAIVisibleReasoningFragments(details []json.RawMessage) []reasoning.Delta {
	var fragments []reasoning.Delta
	for _, raw := range details {
		if len(raw) == 0 || !json.Valid(raw) {
			continue
		}
		var detail struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Summary string `json:"summary"`
			ID      string `json:"id"`
			Index   int    `json:"index"`
		}
		if err := json.Unmarshal(raw, &detail); err != nil {
			continue
		}
		var fragment string
		kind := reasoning.Raw
		switch detail.Type {
		case "reasoning.text":
			fragment = detail.Text
		case "reasoning.summary":
			kind = reasoning.Summary
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
		fragments = append(fragments, reasoning.Delta{Text: fragment, Kind: kind, BlockID: fmt.Sprintf("%s:%s:%d", detail.Type, detail.ID, detail.Index)})
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

// CanonicalizeProviderItems collapses replay fragments whose protocol defines
// concatenation semantics. It is idempotent so storage, estimation, and replay
// all observe the same representation.
func CanonicalizeProviderItems(items []ProviderItem) []ProviderItem {
	if len(items) < 2 {
		return items
	}
	canonical := make([]ProviderItem, 0, len(items))
	for index := 0; index < len(items); {
		item := items[index]
		if item.Standard != "openai" || item.Type != "openai.reasoning_detail" {
			canonical = append(canonical, item)
			index++
			continue
		}
		end := index + 1
		for end < len(items) && items[end].Standard == item.Standard && items[end].Type == item.Type {
			end++
		}
		details := make([]json.RawMessage, 0, end-index)
		for _, fragment := range items[index:end] {
			details = append(details, fragment.Data)
		}
		for _, detail := range mergeOpenAIReasoningDetails(details) {
			canonical = append(canonical, ProviderItem{
				Standard: item.Standard,
				Type:     item.Type,
				Data:     detail,
			})
		}
		index = end
	}
	return canonical
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

func openAIError(wire *ChatAPIError) error {
	codeString := ""
	if wire.Code != nil {
		codeString = fmt.Sprint(wire.Code)
	}
	return &APIError{Type: wire.Type, Code: codeString, Message: wire.Message, Param: wire.Param}
}

// ModelsURL implements ModelLister.
func (s *OpenAIStandard) ModelsURL(base *url.URL, cursor string) *url.URL {
	return joinPath(base, "models")
}

// DecodeModelsPage implements ModelLister for the OpenAI {"data":[...]}
// envelope. Gateways like Command Code and OpenRouter add a non-standard
// context_length per entry (the enforced context window); it is captured
// when present so wire data beats the models.dev fallback.
func (s *OpenAIStandard) DecodeModelsPage(body []byte) (ModelPage, error) {
	var result struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return ModelPage{}, fmt.Errorf("unmarshal models: %w", err)
	}

	models := make([]ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, ModelInfo{
			ID:            m.ID,
			Name:          m.Name,
			ContextWindow: m.ContextLength,
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
	if thinkingEffort != "" && (thinkingEffort != "none" || usesOpenAICompletionTokenLimit(modelID)) {
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

func chatFunctionDef(def FunctionDef) FunctionDef {
	def.Name = wireFunctionName(def.Name)
	return def
}
func chatFunctionCall(call FunctionCall) FunctionCall {
	call.Name = wireFunctionName(call.Name)
	return call
}
