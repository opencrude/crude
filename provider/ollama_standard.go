package provider

import (
	"bytes"
	"crude/types/reasoning"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// OllamaStandard implements Protocol for Ollama's native /api/chat protocol.
// This is the format used by both a local Ollama daemon (http://localhost:11434)
// and Ollama Cloud (https://ollama.com/api). Unlike the OpenAI-compatible /v1
// endpoint, the native protocol streams newline-delimited JSON (application/
// x-ndjson) rather than Server-Sent Events, and exposes model listing via
// /api/tags.
type OllamaStandard struct{}

func (s *OllamaStandard) Info() ProtocolInfo {
	return ProtocolInfo{
		ID:          "ollama",
		Name:        "Ollama",
		Description: "Native Ollama protocol (local daemon and Ollama Cloud).",
		Root:        "/api",
		ChatPath:    "chat",
		ModelsPath:  "tags",
		Streaming:   FramingNDJSON,
	}
}

func (s *OllamaStandard) EncodeRequest(req *ChatRequest) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	messages, err := encodeOllamaMessages(req.Messages)
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
	// Ollama takes generation limits and sampling under "options", not as
	// top-level fields.
	options := map[string]any{}
	if req.MaxOutputTokens > 0 {
		options["num_predict"] = req.MaxOutputTokens
	}
	if req.Temperature != nil {
		options["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		options["top_p"] = *req.TopP
	}
	if len(options) > 0 {
		wire["options"] = options
	}
	return json.Marshal(wire)
}

// encodeOllamaMessages serializes chat messages into Ollama's native message
// shape. Text content is sent as a string; multipart content is split into a
// text "content" field plus an "images" array of raw base64 payloads (Ollama
// expects image bytes without the data:...;base64, URL prefix). Tool calls and
// tool-call IDs reuse the OpenAI wire shape, which Ollama understands.
func encodeOllamaMessages(messages []ChatMessage) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		wire := map[string]any{"role": m.Role}
		if m.Content.IsText() {
			wire["content"] = m.Content.String()
		} else {
			var texts []string
			var images []string
			for _, part := range m.Content.Parts() {
				switch part.Type {
				case "text":
					if part.Text != "" {
						texts = append(texts, part.Text)
					}
				case "image_url":
					if part.ImageURL != nil {
						images = append(images, dataURLToBase64(part.ImageURL.URL))
					} else if len(part.Data) > 0 {
						// Inline media attachment from the tool boundary.
						images = append(images, base64.StdEncoding.EncodeToString(part.Data))
					}
				}
			}
			wire["content"] = strings.Join(texts, "\n")
			if len(images) > 0 {
				wire["images"] = images
			}
		}
		if m.ToolCallID != "" {
			wire["tool_call_id"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			calls := make([]ollamaToolCallParam, 0, len(m.ToolCalls))
			for _, call := range m.ToolCalls {
				calls = append(calls, ollamaToolCallParam{
					ID: call.ID, Type: firstNonEmptyOllama(call.Type, "function"),
				})
				calls[len(calls)-1].Function.Name = call.Function.Name
				calls[len(calls)-1].Function.Arguments = ollamaToolCallArgsRaw(call.Function.Arguments)
				if call.Type == "custom" {
					calls[len(calls)-1].Type = "function"
					raw, err := json.Marshal(map[string]string{"input": call.Function.Arguments})
					if err != nil {
						return nil, err
					}
					calls[len(calls)-1].Function.Arguments = raw
				}
			}
			wire["tool_calls"] = calls
		}
		out = append(out, wire)
	}
	return out, nil
}

// dataURLToBase64 strips the "data:<mime>;base64," prefix from a data URL,
// returning the raw base64 payload Ollama expects in its "images" array.
func dataURLToBase64(dataURL string) string {
	if _, payload, ok := strings.Cut(dataURL, ","); ok {
		return payload
	}
	return dataURL
}

// ollamaChatChunk is the wire shape of both a non-streaming /api/chat response
// and a single streaming NDJSON line.
type ollamaChatChunk struct {
	Model   string `json:"model"`
	Message struct {
		Role      string                `json:"role"`
		Content   string                `json:"content"`
		Thinking  string                `json:"thinking"`
		ToolCalls []ollamaToolCallParam `json:"tool_calls"`
	} `json:"message"`
	DoneReason         string `json:"done_reason"`
	Done               bool   `json:"done"`
	PromptEvalCount    int    `json:"prompt_eval_count"`
	EvalCount          int    `json:"eval_count"`
	PromptEvalTokens   int    `json:"prompt_eval_tokens"` // newer field alias
	EvalTokens         int    `json:"eval_tokens"`        // newer field alias
	TotalDuration      int64  `json:"total_duration"`
	LoadDuration       int64  `json:"load_duration"`
	PromptEvalDuration int64  `json:"prompt_eval_duration"`
	EvalDuration       int64  `json:"eval_duration"`
	Error              string `json:"error"`
}

// ollamaToolCallParam is Ollama's native tool-call wire shape. Unlike OpenAI,
// arguments travel as a JSON object rather than a string; some gateways send
// the OpenAI string form anyway, so both decode and the object form is
// encoded.
type ollamaToolCallParam struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	} `json:"function"`
}

// ollamaToolCallArguments normalizes a tool call's arguments payload to the
// internal string form: a quoted string decodes to its contents, any other
// value keeps its raw JSON text.
func ollamaToolCallArguments(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err == nil {
			return s
		}
	}
	return string(trimmed)
}

// ollamaToolCallArgsRaw converts internal string arguments to Ollama's object
// form: valid JSON passes through, anything else degrades to a quoted string.
func ollamaToolCallArgsRaw(args string) json.RawMessage {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return json.RawMessage("{}")
	}
	if json.Valid([]byte(trimmed)) && (trimmed[0] == '{' || trimmed[0] == '[') {
		return json.RawMessage(trimmed)
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(encoded)
}

// ollamaToolCallsFromWire converts wire tool calls to the internal shape,
// defaulting the type field the way OpenAI clients expect.
func ollamaToolCallsFromWire(calls []ollamaToolCallParam) []ToolCall {
	out := make([]ToolCall, 0, len(calls))
	for _, tc := range calls {
		out = append(out, ToolCall{
			ID:   tc.ID,
			Type: firstNonEmptyOllama(tc.Type, "function"),
			Function: FunctionCall{
				Name:      tc.Function.Name,
				Arguments: ollamaToolCallArguments(tc.Function.Arguments),
			},
		})
	}
	return out
}

func (s *OllamaStandard) DecodeResponse(body []byte) (*ChatResponse, error) {
	var chunk ollamaChatChunk
	if err := json.Unmarshal(body, &chunk); err != nil {
		// Gateways that label NDJSON streams "application/json" land whole
		// chunk sequences here. When the body really is a chunk sequence,
		// fold it — and surface the fold's own error (a chunk-level parse
		// failure or an in-band error chunk) rather than the misleading
		// "invalid character '{' after top-level value".
		if looksLikeNDJSON(body) {
			if merged, mergeErr := decodeOllamaNDJSON(body); mergeErr != nil {
				return nil, mergeErr
			} else {
				return merged, nil
			}
		}
		return nil, fmt.Errorf("unmarshal ollama response: %w", err)
	}
	if chunk.Error != "" {
		return nil, fmt.Errorf("ollama: %s", chunk.Error)
	}
	resp := &ChatResponse{Model: chunk.Model}
	choice := ChatChoice{
		Message: ChatMessage{
			Role:    firstNonEmptyOllama(chunk.Message.Role, "assistant"),
			Content: TextContent(chunk.Message.Content),
		},
		FinishReason: ollamaFinishReason(chunk.DoneReason, chunk.Message.ToolCalls),
	}
	if chunk.Message.Thinking != "" {
		choice.Message.Reasoning = chunk.Message.Thinking
		choice.Message.ReasoningContent = chunk.Message.Thinking
		choice.Message.ReasoningDisplay.Kind = reasoning.Raw
	}
	choice.Message.ToolCalls = ollamaToolCallsFromWire(chunk.Message.ToolCalls)
	resp.Choices = []ChatChoice{choice}
	prompt, completion := ollamaUsage(&chunk)
	if prompt > 0 || completion > 0 {
		resp.Usage = &ChatUsage{
			PromptTokens:     prompt,
			CompletionTokens: completion,
			TotalTokens:      prompt + completion,
		}
	}
	return resp, nil
}

func (s *OllamaStandard) NewStreamDecoder() StreamDecoder {
	return &ollamaStreamDecoder{}
}

// decodeOllamaNDJSON folds a newline-delimited sequence of chat chunks into
// one non-streaming response: text and thinking concatenate, tool calls
// accumulate, and the final done chunk supplies the finish reason and usage.
// It backs DecodeResponse's tolerance for mislabeled NDJSON bodies.
func decodeOllamaNDJSON(body []byte) (*ChatResponse, error) {
	resp := &ChatResponse{}
	choice := &ChatChoice{}
	sawChunk := false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var chunk ollamaChatChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			return nil, err
		}
		sawChunk = true
		if chunk.Error != "" {
			return nil, fmt.Errorf("ollama: %s", chunk.Error)
		}
		if chunk.Model != "" {
			resp.Model = chunk.Model
		}
		choice.Message.Role = firstNonEmptyOllama(choice.Message.Role, chunk.Message.Role, "assistant")
		choice.Message.Content = TextContent(choice.Message.Content.String() + chunk.Message.Content)
		if chunk.Message.Thinking != "" {
			choice.Message.Reasoning += chunk.Message.Thinking
			choice.Message.ReasoningContent = choice.Message.Reasoning
			choice.Message.ReasoningDisplay.Kind = reasoning.Raw
		}
		choice.Message.ToolCalls = append(choice.Message.ToolCalls,
			ollamaToolCallsFromWire(chunk.Message.ToolCalls)...)
		if chunk.Done {
			choice.FinishReason = ollamaFinishReason(chunk.DoneReason, chunk.Message.ToolCalls)
			prompt, completion := ollamaUsage(&chunk)
			if prompt > 0 || completion > 0 {
				resp.Usage = &ChatUsage{
					PromptTokens:     prompt,
					CompletionTokens: completion,
					TotalTokens:      prompt + completion,
				}
			}
		}
	}
	if !sawChunk {
		return nil, fmt.Errorf("ollama response contained no chunks")
	}
	resp.Choices = []ChatChoice{*choice}
	return resp, nil
}

type ollamaStreamDecoder struct{}

func (d *ollamaStreamDecoder) Decode(event StreamEvent) (StreamUpdate, error) {
	data := strings.TrimSpace(string(event.Data))
	if data == "" {
		return StreamUpdate{}, nil
	}
	var chunk ollamaChatChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return StreamUpdate{}, fmt.Errorf("decode ollama stream line: %w", err)
	}
	if chunk.Error != "" {
		return StreamUpdate{}, fmt.Errorf("ollama: %s", chunk.Error)
	}
	update := StreamUpdate{
		ContentDelta: chunk.Message.Content,
		Reasoning:    []reasoning.Delta{{Text: chunk.Message.Thinking, Kind: reasoning.Raw}},
	}
	for _, call := range ollamaToolCallsFromWire(chunk.Message.ToolCalls) {
		update.ToolCalls = append(update.ToolCalls, ToolCallDelta{
			ID:        call.ID,
			Type:      call.Type,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}
	if chunk.Done {
		update.Done = true
		if chunk.DoneReason != "" {
			update.FinishReason = chunk.DoneReason
		} else if len(update.ToolCalls) > 0 {
			update.FinishReason = "tool_calls"
		} else {
			update.FinishReason = "stop"
		}
		prompt, completion := ollamaUsage(&chunk)
		if prompt > 0 || completion > 0 {
			update.Usage = &TokenUsage{
				PromptTokens:       prompt,
				CompletionTokens:   completion,
				TotalDuration:      time.Duration(chunk.TotalDuration),
				LoadDuration:       time.Duration(chunk.LoadDuration),
				PromptEvalDuration: time.Duration(chunk.PromptEvalDuration),
				EvalDuration:       time.Duration(chunk.EvalDuration),
			}
		}
	}
	return update, nil
}

// ModelsURL implements ModelLister.
func (s *OllamaStandard) ModelsURL(base *url.URL, cursor string) *url.URL {
	return joinPath(base, "tags")
}

// DecodeModelsPage implements ModelLister for Ollama's /api/tags shape.
func (s *OllamaStandard) DecodeModelsPage(body []byte) (ModelPage, error) {
	var result struct {
		Models []struct {
			Name    string `json:"name"`
			Model   string `json:"model"`
			Details struct {
				ParameterSize     string `json:"parameter_size"`
				QuantizationLevel string `json:"quantization_level"`
				Family            string `json:"family"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return ModelPage{}, fmt.Errorf("unmarshal ollama models: %w", err)
	}
	models := make([]ModelInfo, 0, len(result.Models))
	for _, m := range result.Models {
		id := m.Name
		if id == "" {
			id = m.Model
		}
		if id == "" {
			continue
		}
		models = append(models, ModelInfo{ID: id, Name: id})
	}
	return ModelPage{Models: models}, nil
}

// RequestExtras implements RequestExtras. Ollama exposes a single boolean
// "think" toggle (qwen3, gpt-oss, ...) rather than graded effort levels; a
// non-default effort enables thinking.
func (s *OllamaStandard) RequestExtras(modelID, thinkingEffort string, _ ProtocolSettings) map[string]any {
	if thinkingEffort == "" || thinkingEffort == "none" {
		return nil
	}
	return map[string]any{"think": true}
}

// ContextWindow implements ProtocolMetadata.
func (s *OllamaStandard) ContextWindow(modelID string) int {
	return LookupContextWindow(modelID)
}

// ollamaFinishReason maps Ollama's done_reason to an OpenAI-style finish reason,
// inferring from tool calls when the field is absent.
func ollamaFinishReason(doneReason string, toolCalls []ollamaToolCallParam) string {
	if doneReason != "" {
		return doneReason
	}
	if len(toolCalls) > 0 {
		return "tool_calls"
	}
	return "stop"
}

// ollamaUsage extracts prompt/completion token counts from a chunk, tolerating
// both the legacy *_count and newer *_tokens field names.
func ollamaUsage(chunk *ollamaChatChunk) (prompt, completion int) {
	prompt = chunk.PromptEvalCount
	if prompt == 0 {
		prompt = chunk.PromptEvalTokens
	}
	completion = chunk.EvalCount
	if completion == 0 {
		completion = chunk.EvalTokens
	}
	return prompt, completion
}

func firstNonEmptyOllama(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
