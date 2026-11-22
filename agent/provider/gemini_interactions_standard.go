package provider

import (
	"bytes"
	"crude/types/reasoning"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// GeminiInteractionsStandard implements the Gemini Interactions API
// (POST /v1beta/interactions), Google's step-based API. Crude owns the
// conversation: every request sends the full step history with store:false,
// so Google keeps no interaction state. Model steps (thoughts with their
// signatures, outputs and function calls) replay exactly as they came.
type GeminiInteractionsStandard struct{ geminiCommon }

func (s *GeminiInteractionsStandard) Info() ProtocolInfo {
	return ProtocolInfo{
		ID:                  "gemini-interactions",
		Name:                "Gemini Interactions",
		Description:         "Gemini Interactions API steps. Crude sends the full history and Google stores nothing.",
		Root:                "/v1beta",
		ChatPath:            "interactions",
		ModelsPath:          "models",
		DecisionPath:        "decisions",
		ListsDecisionModels: true,
		Streaming:           FramingSSE,
		Options: ProtocolOptions{
			ReasoningSummaries: true,
			ServiceTiers:       []string{"standard", "flex", "priority"},
		},
	}
}

// geminiInteractionsRevision pins the Interactions API revision whose step
// shapes this codec speaks (the steps-based input and output).
const geminiInteractionsRevision = "2026-05-20"

// Headers implements ProtocolHeaders.
func (s *GeminiInteractionsStandard) Headers() http.Header {
	headers := http.Header{}
	headers.Set("Api-Revision", geminiInteractionsRevision)
	return headers
}

// ChatURL implements ChatEndpoint: streams ask for SSE framing.
func (s *GeminiInteractionsStandard) ChatURL(base *url.URL, _ string, stream bool) *url.URL {
	endpoint := joinPath(base, "interactions")
	if stream {
		endpoint.RawQuery = "alt=sse"
	}
	return endpoint
}

// NestedExtraKeys implements NestedRequestExtras.
func (s *GeminiInteractionsStandard) NestedExtraKeys() []string {
	return []string{"generation_config"}
}

func (s *GeminiInteractionsStandard) EncodeRequest(req *ChatRequest) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	system, steps, err := encodeGeminiSteps(req.Messages)
	if err != nil {
		return nil, err
	}
	wire := map[string]any{"model": geminiModelID(req.Model), "input": steps, "stream": req.Stream, "store": false}
	if system != "" {
		wire["system_instruction"] = system
	}
	generation := map[string]any{}
	if len(req.Tools) > 0 {
		tools := make([]any, 0, len(req.Tools))
		for _, tool := range req.Tools {
			tool = jsonFunctionTool(tool)
			schema, err := geminiFunctionSchema(tool.Function.Parameters)
			if err != nil {
				return nil, fmt.Errorf("tool %s: %w", tool.Function.Name, err)
			}
			tools = append(tools, map[string]any{
				"type": "function", "name": wireFunctionName(tool.Function.Name),
				"description": tool.Function.Description, "parameters": schema,
			})
		}
		wire["tools"] = tools
		if choice := geminiInteractionsToolChoice(req.ToolChoice); choice != nil {
			generation["tool_choice"] = choice
		}
	}
	if limit := firstPositive(req.MaxOutputTokens, req.MaxCompletionTokens, req.MaxTokens); limit > 0 {
		generation["max_output_tokens"] = limit
	}
	if req.Temperature != nil {
		generation["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		generation["top_p"] = *req.TopP
	}
	if len(generation) > 0 {
		wire["generation_config"] = generation
	}
	return json.Marshal(wire)
}

func geminiInteractionsToolChoice(choice any) any {
	config := geminiToolConfig(choice)
	if config == nil {
		return nil
	}
	switch config["mode"] {
	case "NONE":
		return "none"
	case "ANY":
		if names, ok := config["allowedFunctionNames"].([]string); ok {
			return map[string]any{"allowed_tools": map[string]any{"mode": "any", "tools": names}}
		}
		return "any"
	}
	return nil
}

// encodeGeminiSteps converts the transcript to Interactions input steps and
// the system instruction text.
func encodeGeminiSteps(messages []ChatMessage) (string, []any, error) {
	var system []string
	var steps []any
	callNames := map[string]string{}
	for _, message := range messages {
		switch message.Role {
		case "system", "developer":
			if text := message.Content.String(); strings.TrimSpace(text) != "" {
				system = append(system, text)
			}
		case "assistant":
			modelSteps, err := geminiModelSteps(message, callNames)
			if err != nil {
				return "", nil, err
			}
			steps = append(steps, modelSteps...)
		case "tool":
			name := callNames[message.ToolCallID]
			if name == "" {
				return "", nil, fmt.Errorf("gemini tool result %q has no matching function call", message.ToolCallID)
			}
			result, err := geminiStepResult(message)
			if err != nil {
				return "", nil, err
			}
			step := map[string]any{"type": "function_result", "call_id": message.ToolCallID, "name": name, "result": result}
			if message.ToolError {
				step["is_error"] = true
			}
			steps = append(steps, step)
		default:
			content, err := geminiStepContent(message.Content.Parts())
			if err != nil {
				return "", nil, err
			}
			if len(content) > 0 {
				steps = append(steps, map[string]any{"type": "user_input", "content": content})
			}
		}
	}
	return strings.Join(system, "\n\n"), steps, nil
}

// geminiStepContent converts text and media parts to Interactions content.
func geminiStepContent(parts []ContentPart) ([]any, error) {
	var content []any
	for _, part := range parts {
		if part.Type == "text" {
			if part.Text != "" {
				content = append(content, map[string]any{"type": "text", "text": part.Text})
			}
			continue
		}
		media, ok, err := geminiMediaOf(part)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		item := map[string]any{"type": geminiStepMediaType(media.MimeType, part.Type)}
		if media.MimeType != "" {
			item["mime_type"] = media.MimeType
		}
		if media.URI != "" {
			item["uri"] = media.URI
		} else {
			item["data"] = media.Data
		}
		content = append(content, item)
	}
	return content, nil
}

func geminiStepMediaType(mime, partType string) string {
	switch {
	case strings.HasPrefix(mime, "image/") || partType == "image_url":
		return "image"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	}
	return "document"
}

// geminiStepResult is a function result: text, or content items when the
// tool returned media.
func geminiStepResult(message ChatMessage) (any, error) {
	if message.Content.IsText() {
		return message.Content.String(), nil
	}
	content, err := geminiStepContent(message.Content.Parts())
	if err != nil {
		return nil, err
	}
	return content, nil
}

// geminiModelSteps returns a model turn's steps: exactly the stored steps
// when replay is valid, otherwise output and function calls rebuilt from the
// transcript.
func geminiModelSteps(message ChatMessage, callNames map[string]string) ([]any, error) {
	for _, call := range message.ToolCalls {
		callNames[call.ID] = wireFunctionName(call.Function.Name)
	}
	var stored []any
	for _, item := range message.ProviderItems {
		if item.Standard != "gemini-interactions" || len(item.Data) == 0 {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(item.Data))
		decoder.UseNumber()
		var step map[string]any
		if err := decoder.Decode(&step); err != nil || step == nil {
			return nil, fmt.Errorf("invalid preserved Gemini step")
		}
		if input, ok := geminiInputStep(step); ok {
			stored = append(stored, input)
		}
	}
	if len(stored) > 0 {
		return stored, nil
	}
	var steps []any
	if text := message.Content.String(); text != "" {
		steps = append(steps, map[string]any{"type": "model_output", "content": []any{map[string]any{"type": "text", "text": text}}})
	}
	for index, call := range message.ToolCalls {
		step := map[string]any{"type": "function_call", "id": call.ID, "name": wireFunctionName(call.Function.Name), "arguments": geminiToolArgs(call)}
		if index == 0 {
			step["signature"] = geminiSkipSignature
		}
		steps = append(steps, step)
	}
	return steps, nil
}

// geminiInputStep converts a model step to the input shape the API accepts
// back: only function calls keep an ID, content items lose theirs, and a
// thought without a signature carries nothing the model can use.
func geminiInputStep(step map[string]any) (map[string]any, bool) {
	kind, _ := step["type"].(string)
	keep := func(fields ...string) map[string]any {
		out := map[string]any{"type": kind}
		for _, field := range fields {
			if value, ok := step[field]; ok {
				out[field] = value
			}
		}
		return out
	}
	switch kind {
	case "thought":
		if signature, _ := step["signature"].(string); signature == "" {
			return nil, false
		}
		return keep("signature", "summary"), true
	case "model_output":
		out := keep("content")
		if items, ok := out["content"].([]any); ok {
			content := make([]any, 0, len(items))
			for _, raw := range items {
				if item, ok := raw.(map[string]any); ok {
					clean := make(map[string]any, len(item))
					for key, value := range item {
						if key != "id" {
							clean[key] = value
						}
					}
					content = append(content, clean)
				}
			}
			out["content"] = content
		}
		return out, true
	case "function_call":
		return keep("id", "name", "arguments", "signature"), true
	}
	out := make(map[string]any, len(step))
	for key, value := range step {
		if key != "id" {
			out[key] = value
		}
	}
	return out, true
}

// RequestExtras implements RequestExtras.
func (s *GeminiInteractionsStandard) RequestExtras(modelID, thinkingEffort string, settings ProtocolSettings) map[string]any {
	extras := map[string]any{}
	generation := map[string]any{}
	if level := geminiThinkingLevel(thinkingEffort); level != "" {
		generation["thinking_level"] = strings.ToLower(level)
	}
	switch {
	case settings.ReasoningSummaries != nil && !*settings.ReasoningSummaries:
		generation["thinking_summaries"] = "none"
	case thinkingEffort != "none":
		generation["thinking_summaries"] = "auto"
	}
	if len(generation) > 0 {
		extras["generation_config"] = generation
	}
	if settings.ServiceTier != "" {
		extras["service_tier"] = settings.ServiceTier
	}
	if len(extras) == 0 {
		return nil
	}
	return extras
}

// FinalizeRequest implements ModelLimitLearner. Crude owns the history, so
// no extra field may ask Google to keep this interaction.
func (s *GeminiInteractionsStandard) FinalizeRequest(scope string, req *ChatRequest, body map[string]any) {
	body["store"] = false
	delete(body, "previous_interaction_id")
	generation, _ := body["generation_config"].(map[string]any)
	if generation == nil || req == nil {
		return
	}
	limits := geminiLimitsFor(scope, req.Model)
	if limits.noThinking {
		delete(generation, "thinking_level")
		delete(generation, "thinking_summaries")
		if len(generation) == 0 {
			delete(body, "generation_config")
		}
		return
	}
	if level, _ := generation["thinking_level"].(string); level != "" {
		if nearest := geminiNearestLevel(strings.ToUpper(level), limits.unsupported); nearest != "" {
			generation["thinking_level"] = strings.ToLower(nearest)
		} else {
			delete(generation, "thinking_level")
		}
	}
}

// LearnRejection implements ModelLimitLearner.
func (s *GeminiInteractionsStandard) LearnRejection(scope string, req *ChatRequest, body []byte, err *StatusError) bool {
	var sent struct {
		Generation map[string]any `json:"generation_config"`
	}
	if req == nil || json.Unmarshal(body, &sent) != nil || sent.Generation == nil {
		return false
	}
	level, _ := sent.Generation["thinking_level"].(string)
	_, summaries := sent.Generation["thinking_summaries"]
	return geminiLearnThinkingRejection(scope, req.Model, strings.ToUpper(level), level != "" || summaries, err)
}

func (s *GeminiInteractionsStandard) DecodeResponse(body []byte) (*ChatResponse, error) {
	var interaction geminiInteraction
	if err := json.Unmarshal(body, &interaction); err != nil {
		return nil, fmt.Errorf("unmarshal Gemini interaction: %w", err)
	}
	decoder := &geminiStepDecoder{steps: map[int]*geminiStepState{}}
	var update StreamUpdate
	for index, raw := range interaction.Steps {
		if err := decoder.start(index, raw, &update); err != nil {
			return nil, err
		}
		if err := decoder.stop(index, &update); err != nil {
			return nil, err
		}
	}
	final, err := decoder.complete(interaction, update)
	if err != nil {
		return nil, err
	}
	return (&geminiStreamDecoder{}).response(final), nil
}

func (s *GeminiInteractionsStandard) NewStreamDecoder() StreamDecoder {
	return &streamErrorDecoder{protocol: s.Info().ID, inner: &geminiStepDecoder{steps: map[int]*geminiStepState{}}}
}

type geminiInteraction struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  *struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	ServiceTier string            `json:"service_tier"`
	Steps       []json.RawMessage `json:"steps"`
	Usage       *struct {
		TotalInputTokens   int `json:"total_input_tokens"`
		TotalCachedTokens  int `json:"total_cached_tokens"`
		TotalOutputTokens  int `json:"total_output_tokens"`
		TotalThoughtTokens int `json:"total_thought_tokens"`
		TotalToolUseTokens int `json:"total_tool_use_tokens"`
	} `json:"usage"`
}

// geminiStepState accumulates one streamed step.
type geminiStepState struct {
	kind      string
	raw       map[string]any
	content   []any // model_output content items; text deltas grow the last text item
	summary   strings.Builder
	signature string
	arguments strings.Builder
	callIndex int
}

// appendText adds streamed text to the last text item, or starts one.
func (s *geminiStepState) appendText(text string) {
	if last := len(s.content) - 1; last >= 0 {
		if item, ok := s.content[last].(map[string]any); ok && item["type"] == "text" {
			current, _ := item["text"].(string)
			item["text"] = current + text
			return
		}
	}
	s.content = append(s.content, map[string]any{"type": "text", "text": text})
}

// geminiStepDecoder decodes Interactions events: step.start, step.delta and
// step.stop per step index, then interaction.completed.
type geminiStepDecoder struct {
	steps map[int]*geminiStepState
	calls int
}

func (d *geminiStepDecoder) Decode(event StreamEvent) (StreamUpdate, error) {
	data := bytes.TrimSpace(event.Data)
	if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return StreamUpdate{}, nil
	}
	var envelope struct {
		EventType   string             `json:"event_type"`
		Index       int                `json:"index"`
		Step        json.RawMessage    `json:"step"`
		Delta       json.RawMessage    `json:"delta"`
		Interaction *geminiInteraction `json:"interaction"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return StreamUpdate{}, fmt.Errorf("decode Gemini interaction event: %w", err)
	}
	var update StreamUpdate
	var err error
	switch firstNonEmptyString(envelope.EventType, event.Event) {
	case "step.start":
		err = d.start(envelope.Index, envelope.Step, &update)
	case "step.delta":
		err = d.delta(envelope.Index, envelope.Delta, &update)
	case "step.stop":
		err = d.stop(envelope.Index, &update)
	case "interaction.completed", "interaction.failed":
		if envelope.Interaction == nil {
			return StreamUpdate{}, fmt.Errorf("gemini %s has no interaction", envelope.EventType)
		}
		return d.complete(*envelope.Interaction, update)
	}
	return update, err
}

func (d *geminiStepDecoder) start(index int, raw json.RawMessage, update *StreamUpdate) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var step map[string]any
	if err := decoder.Decode(&step); err != nil || step == nil {
		return fmt.Errorf("decode Gemini step: %v", err)
	}
	state := &geminiStepState{raw: step}
	state.kind, _ = step["type"].(string)
	state.signature, _ = step["signature"].(string)
	switch state.kind {
	case "model_output":
		state.content, _ = step["content"].([]any)
		for _, item := range geminiStepTexts(state.content) {
			update.ContentDelta += item
		}
	case "thought":
		for _, item := range geminiStepTexts(step["summary"]) {
			state.summary.WriteString(item)
			update.Reasoning = append(update.Reasoning, reasoning.Delta{Text: item, Kind: reasoning.Summary, BlockID: fmt.Sprintf("step-%d", index)})
		}
	case "function_call":
		state.callIndex = d.calls
		d.calls++
		id, _ := step["id"].(string)
		name, _ := step["name"].(string)
		if arguments, ok := step["arguments"].(map[string]any); ok && len(arguments) > 0 {
			encoded, _ := json.Marshal(arguments)
			state.arguments.Write(encoded)
		}
		update.ToolCalls = append(update.ToolCalls, ToolCallDelta{Index: state.callIndex, ID: id, Type: "function",
			Name: canonicalToolName(name), Arguments: state.arguments.String(), ReplaceArguments: true})
	}
	d.steps[index] = state
	return nil
}

func geminiStepTexts(value any) []string {
	items, _ := value.([]any)
	var texts []string
	for _, raw := range items {
		if item, ok := raw.(map[string]any); ok {
			if text, ok := item["text"].(string); ok && text != "" {
				texts = append(texts, text)
			}
		}
	}
	return texts
}

func (d *geminiStepDecoder) delta(index int, raw json.RawMessage, update *StreamUpdate) error {
	state := d.steps[index]
	if state == nil {
		return fmt.Errorf("gemini step %d delta before start", index)
	}
	var delta struct {
		Type      string `json:"type"`
		Text      string `json:"text"`
		Arguments string `json:"arguments"`
		Signature string `json:"signature"`
		Content   *struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &delta); err != nil {
		return fmt.Errorf("decode Gemini step delta: %w", err)
	}
	switch delta.Type {
	case "text":
		text := delta.Text
		if text == "" && delta.Content != nil {
			text = delta.Content.Text
		}
		state.appendText(text)
		update.ContentDelta += text
	case "thought_summary":
		if delta.Content != nil && delta.Content.Text != "" {
			state.summary.WriteString(delta.Content.Text)
			update.Reasoning = append(update.Reasoning, reasoning.Delta{Text: delta.Content.Text, Kind: reasoning.Summary, BlockID: fmt.Sprintf("step-%d", index)})
		}
	case "thought_signature":
		state.signature += delta.Signature
	case "arguments_delta":
		state.arguments.WriteString(delta.Arguments)
		update.ToolCalls = append(update.ToolCalls, ToolCallDelta{Index: state.callIndex, Arguments: delta.Arguments})
	}
	return nil
}

// stop closes a step and keeps it, in input form, for exact replay.
func (d *geminiStepDecoder) stop(index int, update *StreamUpdate) error {
	state := d.steps[index]
	if state == nil {
		return nil
	}
	delete(d.steps, index)
	step := state.raw
	switch state.kind {
	case "model_output":
		step["content"] = state.content
	case "thought":
		if summary := state.summary.String(); summary != "" {
			step["summary"] = []any{map[string]any{"type": "text", "text": summary}}
		}
	case "function_call":
		arguments := strings.TrimSpace(state.arguments.String())
		if arguments == "" {
			arguments = "{}"
		}
		decoder := json.NewDecoder(strings.NewReader(arguments))
		decoder.UseNumber()
		var object map[string]any
		if decoder.Decode(&object) != nil || object == nil {
			object = map[string]any{}
		}
		step["arguments"] = object
		update.ToolCalls = append(update.ToolCalls, ToolCallDelta{Index: state.callIndex, Arguments: arguments, ReplaceArguments: true})
	}
	if state.signature != "" {
		step["signature"] = state.signature
	}
	step, keep := geminiInputStep(step)
	if !keep {
		return nil
	}
	encoded, err := json.Marshal(step)
	if err != nil {
		return fmt.Errorf("encode Gemini step: %w", err)
	}
	update.ProviderItems = append(update.ProviderItems, Item{Standard: "gemini-interactions", Type: state.kind, Data: encoded})
	return nil
}

func (d *geminiStepDecoder) complete(interaction geminiInteraction, update StreamUpdate) (StreamUpdate, error) {
	for _, index := range slices.Sorted(maps.Keys(d.steps)) {
		if err := d.stop(index, &update); err != nil {
			return StreamUpdate{}, err
		}
	}
	if failure := interaction.Error; failure != nil && (failure.Message != "" || failure.Code != nil) {
		return StreamUpdate{}, &APIError{Type: "server_error", Code: fmt.Sprint(failure.Code), Message: failure.Message}
	}
	switch interaction.Status {
	case "failed", "cancelled":
		return StreamUpdate{}, &APIError{Type: "server_error", Code: interaction.Status, Message: "Gemini interaction " + interaction.Status}
	case "completed", "requires_action", "":
		update.FinishReason = "stop"
		if d.calls > 0 {
			update.FinishReason = "tool_calls"
		}
	case "incomplete", "requires_continuation":
		update.FinishReason = "length"
	default:
		update.FinishReason = strings.ToLower(interaction.Status)
	}
	if usage := interaction.Usage; usage != nil {
		update.Usage = &TokenUsage{
			PromptTokens:       usage.TotalInputTokens + usage.TotalToolUseTokens,
			CompletionTokens:   usage.TotalOutputTokens + usage.TotalThoughtTokens,
			PromptTokensCached: usage.TotalCachedTokens,
			CacheReadKnown:     true,
			ReasoningTokens:    usage.TotalThoughtTokens,
			ReasoningKnown:     true,
			ReasoningInOutput:  true,
			ServiceTier:        interaction.ServiceTier,
		}
	}
	update.Done = true
	return update, nil
}
