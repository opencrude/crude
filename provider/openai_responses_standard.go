package provider

import (
	"bytes"
	"crude/types/reasoning"
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
			Store:               true,
			StoreDefault:        true,
			MediaReplayRecovery: true,
			ZeroDataRetention:   true,
			ReasoningSummaries:  true,
			Truncation:          true,
			ServiceTiers:        []string{"auto", "default", "flex", "priority", "fast"},
		},
	}
}

func (s *OpenAIResponsesStandard) EncodeRequest(req *ChatRequest) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	previous, start := responseContinuation(req)
	input, err := encodeResponsesInput(req.Messages, start)
	if err != nil {
		return nil, err
	}
	wire := map[string]any{
		"model":  req.Model,
		"input":  input,
		"stream": req.Stream,
	}
	if previous != "" {
		wire["previous_response_id"] = previous
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

func encodeResponsesInput(messages []ChatMessage, start int) ([]any, error) {
	input := make([]any, 0, len(messages))
	customCalls := map[string]bool{}
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.Type == "custom" {
				customCalls[call.ID] = true
			}
		}
	}
	for _, message := range messages[:start] {
		for _, item := range message.ProviderItems {
			if item.Standard == "openai-responses" && item.Type == "custom_tool_call" {
				var call openAIOutputItem
				if err := json.Unmarshal(item.Data, &call); err != nil {
					return nil, fmt.Errorf("decode omitted custom tool call: %w", err)
				}
				customCalls[call.CallID] = true
			}
		}
	}
	for _, msg := range messages[start:] {
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
				case "function_call", "custom_tool_call":
					preservedCalls[descriptor.CallID] = true
					if descriptor.Type == "custom_tool_call" {
						customCalls[descriptor.CallID] = true
					}
				}
				input = append(input, json.RawMessage(bytes.Clone(item.Data)))
			}
		}

		switch msg.Role {
		case "tool":
			output := any(msg.ContentString())
			if !msg.Content.IsText() {
				content, err := encodeResponsesContent(msg)
				if err != nil {
					return nil, err
				}
				output = content
			}
			outputType := "function_call_output"
			if customCalls[msg.ToolCallID] {
				outputType = "custom_tool_call_output"
			}
			input = append(input, map[string]any{
				"type":    outputType,
				"call_id": msg.ToolCallID,
				"output":  output,
			})
			continue
		case "user", "system", "developer", "assistant":
			if (msg.ContentString() != "" || msg.Content.HasImage() || msg.Content.HasMedia() || msg.Refusal != "") &&
				(msg.Role != "assistant" || !preservedMessage) {
				var content any
				if msg.Role == "assistant" {
					// Canonical history has no server output-message ID. Use the
					// documented string-content message, not input-only blocks or
					// a fabricated ResponseOutputMessage. Exact output items above
					// retain their original IDs, phases and output_text/refusal parts.
					content = msg.ContentString()
					if content == "" {
						content = msg.Refusal
					}
				} else {
					var err error
					content, err = encodeResponsesContent(msg)
					if err != nil {
						return nil, err
					}
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
				if call.Type == "custom" {
					input = append(input, map[string]any{"type": "custom_tool_call", "call_id": call.ID, "name": call.Function.Name, "input": call.Function.Arguments})
					continue
				}
				wireName := wireFunctionName(call.Function.Name)
				item := map[string]any{"type": "function_call", "call_id": call.ID, "name": wireName, "arguments": call.Function.Arguments}
				if namespace, name, ok := strings.Cut(wireName, "."); ok {
					item["namespace"] = namespace
					item["name"] = name
				}
				input = append(input, item)
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
			content = append(content, map[string]any{"type": "input_text", "text": part.Text})
		case "image_url":
			if msg.Role == "assistant" {
				continue
			}
			if len(part.Data) > 0 {
				// Inline media attachment from the tool boundary.
				dataURL := "data:" + part.MimeType + ";base64," + base64.StdEncoding.EncodeToString(part.Data)
				content = append(content, map[string]any{
					"type":      "input_image",
					"image_url": dataURL,
				})
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
		case "document":
			if len(part.Data) == 0 {
				return nil, fmt.Errorf("OpenAI file content is empty")
			}
			content = append(content, map[string]any{
				"type":      "input_file",
				"filename":  part.Filename,
				"file_data": mediaDataURL(part),
			})
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
		if tool.Type == "custom" {
			result = append(result, map[string]any{"type": "custom", "name": tool.Function.Name, "description": tool.Function.Description, "format": tool.Format})
			continue
		}
		wireName := wireFunctionName(tool.Function.Name)
		definition := map[string]any{"type": "function", "name": wireName, "description": tool.Function.Description, "parameters": tool.Function.Parameters}
		if namespace, name, ok := strings.Cut(wireName, "."); ok {
			definition["name"] = name
			result = append(result, map[string]any{"type": "namespace", "name": namespace, "description": "Parallel tool execution", "tools": []any{definition}})
		} else {
			result = append(result, definition)
		}
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
	if !ok && obj["type"] != "function" {
		return choice
	}
	if !ok {
		fn = obj
	}
	name, _ := fn["name"].(string)
	if name == "" {
		return choice
	}
	name = wireFunctionName(name)
	result := map[string]any{"type": "function", "name": name}
	if namespace, function, ok := strings.Cut(name, "."); ok {
		result["namespace"], result["name"] = namespace, function
	}
	return result
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
	store := settings.ResolveStore(true)
	extras := map[string]any{"store": store}
	if !store {
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
	Store             *bool                    `json:"store"`
	ID                string                   `json:"id"`
	Model             string                   `json:"model"`
	Status            string                   `json:"status"`
	Output            []json.RawMessage        `json:"output"`
	Usage             *openAIResponsesUsage    `json:"usage"`
	Error             *openAIResponsesError    `json:"error"`
	IncompleteDetails *openAIIncompleteDetails `json:"incomplete_details"`
}

type openAIResponsesUsage struct {
	InputTokens         int                     `json:"input_tokens"`
	OutputTokens        int                     `json:"output_tokens"`
	InputTokensDetails  *PromptTokenDetails     `json:"input_tokens_details"`
	OutputTokensDetails *CompletionTokenDetails `json:"output_tokens_details"`
}

type openAIResponsesError struct {
	Param   string `json:"param"`
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
	Namespace string `json:"namespace"`
	Arguments string `json:"arguments"`
	Input     string `json:"input"`
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
		return nil, &APIError{Type: r.Error.Type, Code: r.Error.Code, Message: r.Error.Message, Param: r.Error.Param}
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
	var trace string
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
		case "function_call", "custom_tool_call":
			if responseOutputItemComplete(item.Status) {
				arguments, callType := item.toolInput()
				msg.ToolCalls = append(msg.ToolCalls, ToolCall{
					Index: index,
					ID:    item.CallID,
					Type:  callType,
					Function: FunctionCall{
						Name:      item.toolName(),
						Arguments: arguments,
					},
				})
			}
		case "reasoning":
			for partIndex, part := range item.Summary {
				trace = msg.ReasoningDisplay.Append(trace, reasoning.Delta{Text: part.Text, Kind: reasoning.Summary, BlockID: fmt.Sprintf("%d:%d:summary", index, partIndex)})
			}
			for partIndex, part := range item.Content {
				if part.Type == "reasoning_text" {
					trace = msg.ReasoningDisplay.Append(trace, reasoning.Delta{Text: part.Text, Kind: reasoning.Raw, BlockID: fmt.Sprintf("%d:%d:raw", index, partIndex)})
				}
			}
		}
	}
	msg.Content = TextContent(text.String())
	msg.ReasoningContent = trace
	msg.Reasoning = trace

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
	if r.Status == "completed" && (r.Store == nil || *r.Store) {
		resp.ContinuationID = r.ID
		if r.ID != "" {
			resp.ContinuationReason = "response_id_retained"
		} else {
			resp.ContinuationReason = "response_id_missing"
		}
	} else if r.Status == "completed" {
		resp.ContinuationReason = "response_not_stored"
	} else {
		resp.ContinuationReason = "response_incomplete"
	}
	if r.Usage != nil {
		resp.Usage = &ChatUsage{
			PromptTokens:     r.Usage.InputTokens,
			CompletionTokens: r.Usage.OutputTokens,
			TotalTokens:      r.Usage.InputTokens + r.Usage.OutputTokens,
		}
		if r.Usage.InputTokensDetails != nil {
			resp.Usage.PromptTokensDetails = r.Usage.InputTokensDetails
		}
		resp.Usage.CompletionTokensDetails = r.Usage.OutputTokensDetails
	}
	return resp, nil
}

func responseOutputItemComplete(status string) bool {
	return status == "" || status == "completed"
}

type openAIResponsesStreamDecoder struct {
	seenProviderItems  map[string]struct{}
	pendingCustomCalls map[string]bool
	content            strings.Builder
	reasoningBlocks    map[string]string
	responseID         string
	responseStore      *bool
}

func (d *openAIResponsesStreamDecoder) customCallState(item openAIOutputItem, done bool) {
	if item.Type != "custom_tool_call" {
		return
	}
	if d.pendingCustomCalls == nil {
		d.pendingCustomCalls = make(map[string]bool)
	}
	d.pendingCustomCalls[item.CallID] = !done
}

func (d *openAIResponsesStreamDecoder) checkCustomCallsComplete() error {
	for id, pending := range d.pendingCustomCalls {
		if pending {
			return fmt.Errorf("stream ended before custom tool call %s completed", id)
		}
	}
	return nil
}

func (d *openAIResponsesStreamDecoder) Decode(event StreamEvent) (StreamUpdate, error) {
	data := bytes.TrimSpace(event.Data)
	if len(data) == 0 {
		return StreamUpdate{}, nil
	}
	if bytes.Equal(data, []byte("[DONE]")) {
		if err := d.checkCustomCallsComplete(); err != nil {
			return StreamUpdate{}, err
		}
		return StreamUpdate{Done: true}, nil
	}

	var envelope struct {
		Type         string                `json:"type"`
		Delta        string                `json:"delta"`
		Text         string                `json:"text"`
		Refusal      string                `json:"refusal"`
		Arguments    string                `json:"arguments"`
		Input        string                `json:"input"`
		Name         string                `json:"name"`
		ItemID       string                `json:"item_id"`
		OutputIndex  int                   `json:"output_index"`
		SummaryIndex int                   `json:"summary_index"`
		ContentIndex int                   `json:"content_index"`
		Item         json.RawMessage       `json:"item"`
		Response     *openAIResponse       `json:"response"`
		Error        *openAIResponsesError `json:"error"`
		Code         string                `json:"code"`
		Message      string                `json:"message"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return StreamUpdate{}, fmt.Errorf("decode OpenAI Responses stream event: %w", err)
	}
	typ := envelope.Type
	if typ == "" {
		typ = event.Event
	}

	switch typ {
	case "response.created", "response.queued", "response.in_progress":
		if envelope.Response == nil {
			return StreamUpdate{}, fmt.Errorf("%s event is missing response", typ)
		}
		if err := d.observeResponse(envelope.Response); err != nil {
			return StreamUpdate{}, err
		}
		return StreamUpdate{}, nil
	case "response.output_text.delta", "response.refusal.delta":
		d.content.WriteString(envelope.Delta)
		return StreamUpdate{ContentDelta: envelope.Delta}, nil
	case "response.output_text.done", "response.refusal.done":
		delta := unseenStreamSuffix(d.content.String(), firstNonEmpty(envelope.Text, envelope.Refusal, envelope.Delta))
		d.content.WriteString(delta)
		return StreamUpdate{ContentDelta: delta}, nil
	case "response.reasoning_text.delta", "response.reasoning_summary_text.delta", "response.reasoning_text.done", "response.reasoning_summary_text.done":
		kind, index := reasoning.Raw, envelope.ContentIndex
		if strings.Contains(typ, "summary") {
			kind, index = reasoning.Summary, envelope.SummaryIndex
		}
		return StreamUpdate{Reasoning: []reasoning.Delta{d.reasoningDelta(envelope.OutputIndex, index, kind, firstNonEmpty(envelope.Text, envelope.Delta), strings.HasSuffix(typ, ".done"))}}, nil
	case "response.custom_tool_call_input.delta":
		return StreamUpdate{ToolCalls: []ToolCallDelta{{Index: envelope.OutputIndex, Type: "custom", Arguments: envelope.Delta}}}, nil
	case "response.custom_tool_call_input.done":
		return StreamUpdate{ToolCalls: []ToolCallDelta{{Index: envelope.OutputIndex, Type: "custom", Name: envelope.Name, Arguments: envelope.Input, ReplaceArguments: true}}}, nil
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
			Name:             canonicalToolName(envelope.Name),
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
			return StreamUpdate{}, &APIError{Type: apiErr.Type, Code: apiErr.Code, Message: apiErr.Message, Param: apiErr.Param}
		}
		return StreamUpdate{}, &APIError{Type: "response_failed", Message: "OpenAI response failed"}
	case "response.cancelled":
		return StreamUpdate{}, &APIError{Type: "response_cancelled", Message: "OpenAI response was cancelled"}
	case "error":
		if envelope.Error != nil {
			return StreamUpdate{}, &APIError{Type: envelope.Error.Type, Code: envelope.Error.Code, Message: envelope.Error.Message, Param: envelope.Error.Param}
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
	case "function_call", "custom_tool_call":
		if done && !responseOutputItemComplete(item.Status) {
			if item.Type == "custom_tool_call" {
				return update, fmt.Errorf("incomplete custom tool call %s", item.CallID)
			}
			return update, nil
		}
		d.customCallState(item, done)
		arguments, callType := item.toolInput()
		if !done && item.Type == "function_call" {
			arguments = item.Arguments
		}
		update.ToolCalls = []ToolCallDelta{{
			Index:            index,
			ID:               item.CallID,
			Type:             callType,
			Name:             item.toolName(),
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
	if err := d.observeResponse(response); err != nil {
		return StreamUpdate{}, err
	}
	if response.Error != nil {
		return StreamUpdate{}, &APIError{
			Type: response.Error.Type, Code: response.Error.Code, Message: response.Error.Message, Param: response.Error.Param,
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
	stored := response.Store
	if stored == nil {
		stored = d.responseStore
	}
	switch {
	case incomplete:
		update.ContinuationReason = "response_incomplete"
	case stored != nil && !*stored:
		update.ContinuationReason = "response_not_stored"
	case d.responseID == "":
		update.ContinuationReason = "response_id_missing"
	default:
		update.ContinuationID = d.responseID
		update.ContinuationReason = "response_id_retained"
	}
	if incomplete {
		update.FinishReason = "incomplete"
		if response.IncompleteDetails != nil && response.IncompleteDetails.Reason != "" {
			update.FinishReason = response.IncompleteDetails.Reason
		}
	}
	if response.Usage != nil {
		wire := ChatUsage{
			PromptTokens:            response.Usage.InputTokens,
			CompletionTokens:        response.Usage.OutputTokens,
			PromptTokensDetails:     response.Usage.InputTokensDetails,
			CompletionTokensDetails: response.Usage.OutputTokensDetails,
		}
		usage := wire.normalized()
		update.Usage = &usage
	}

	var terminalContent strings.Builder
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
		case "function_call", "custom_tool_call":
			if item.Type == "custom_tool_call" && !responseOutputItemComplete(item.Status) && !incomplete {
				return StreamUpdate{}, fmt.Errorf("terminal response contains incomplete custom tool call %s", item.CallID)
			}
			if responseOutputItemComplete(item.Status) {
				if !incomplete {
					update.FinishReason = "tool_calls"
				}
				d.customCallState(item, true)
				arguments, callType := item.toolInput()
				update.ToolCalls = append(update.ToolCalls, ToolCallDelta{
					Index:            index,
					ID:               item.CallID,
					Type:             callType,
					Name:             item.toolName(),
					Arguments:        arguments,
					ReplaceArguments: true,
				})
			}
		case "reasoning":
			for partIndex, part := range item.Summary {
				update.Reasoning = append(update.Reasoning, d.reasoningDelta(index, partIndex, reasoning.Summary, part.Text, true))
			}
			for partIndex, part := range item.Content {
				if part.Type == "reasoning_text" {
					update.Reasoning = append(update.Reasoning, d.reasoningDelta(index, partIndex, reasoning.Raw, part.Text, true))
				}
			}
		}
	}
	if !incomplete {
		if err := d.checkCustomCallsComplete(); err != nil {
			return StreamUpdate{}, err
		}
	}
	update.ContentDelta = unseenStreamSuffix(d.content.String(), terminalContent.String())
	d.content.WriteString(update.ContentDelta)
	return update, nil
}

func (d *openAIResponsesStreamDecoder) observeResponse(response *openAIResponse) error {
	if response == nil {
		return nil
	}
	if response.ID != "" {
		if d.responseID != "" && d.responseID != response.ID {
			return fmt.Errorf("OpenAI stream response ID changed from %q to %q", d.responseID, response.ID)
		}
		d.responseID = response.ID
	}
	if response.Store != nil {
		stored := *response.Store
		if d.responseStore != nil && *d.responseStore != stored {
			return fmt.Errorf("OpenAI stream storage state changed during one response")
		}
		d.responseStore = &stored
	}
	return nil
}

func (d *openAIResponsesStreamDecoder) reasoningDelta(output, index int, kind reasoning.Kind, text string, complete bool) reasoning.Delta {
	id := fmt.Sprintf("%d:%d:%s", output, index, kind)
	if d.reasoningBlocks == nil {
		d.reasoningBlocks = make(map[string]string)
	}
	if complete {
		text = unseenStreamSuffix(d.reasoningBlocks[id], text)
	}
	d.reasoningBlocks[id] += text
	return reasoning.Delta{Text: text, Kind: kind, BlockID: id}
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

func (item openAIOutputItem) toolInput() (string, string) {
	if item.Type == "custom_tool_call" {
		return item.Input, "custom"
	}
	if item.Arguments == "" {
		return "{}", "function"
	}
	return item.Arguments, "function"
}

func (item openAIOutputItem) toolName() string {
	if item.Namespace != "" {
		return item.Namespace + "." + item.Name
	}
	return canonicalToolName(item.Name)
}
