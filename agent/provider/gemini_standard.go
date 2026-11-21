package provider

import (
	"bytes"
	"crude/types/reasoning"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// GeminiStandard implements the native Gemini API (Google AI Studio keys):
// models/{model}:streamGenerateContent with SSE framing. Model turns are kept
// as the exact ordered parts Gemini returned, so thought signatures replay at
// their original positions.
type GeminiStandard struct{ geminiCommon }

// geminiContentItem is the ProviderItem type holding one model turn's parts.
const geminiContentItem = "content"

func (s *GeminiStandard) Info() ProtocolInfo {
	return ProtocolInfo{
		ID:          "gemini",
		Name:        "Gemini",
		Description: "Native Gemini API with signed thought parts.",
		Root:        "/v1beta",
		// The chat URL names the model; see ChatURL.
		ChatPath:            "models",
		ModelsPath:          "models",
		DecisionPath:        "decisions",
		WebSearch:           WebSearchTool,
		ListsDecisionModels: true,
		Streaming:           FramingSSE,
		Options: ProtocolOptions{
			ReasoningSummaries: true,
			ServiceTiers:       []string{"standard", "flex", "priority"},
		},
	}
}

func (s *GeminiStandard) EncodeWebSearch(_ string, query string, _ int) map[string]any {
	return map[string]any{
		"contents":         []geminiContent{{Role: "user", Parts: []any{map[string]string{"text": query}}}},
		"tools":            []map[string]any{{"google_search": map[string]any{}}},
		"generationConfig": map[string]int{"maxOutputTokens": 1024},
	}
}

func (s *GeminiStandard) DecodeWebSearch(body []byte, limit int) ([]WebSearchSource, error) {
	var response struct {
		Candidates []struct {
			Grounding struct {
				Chunks []struct {
					Web *struct {
						URI   string `json:"uri"`
						Title string `json:"title"`
					} `json:"web"`
				} `json:"groundingChunks"`
			} `json:"groundingMetadata"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, errWebSearchRead
	}
	var sources []WebSearchSource
	for _, candidate := range response.Candidates {
		for _, chunk := range candidate.Grounding.Chunks {
			if chunk.Web != nil {
				// Keep the URI as given, including a redirect URL. A search
				// issues only one request and does not fetch its results.
				sources = append(sources, WebSearchSource{URL: chunk.Web.URI, Title: chunk.Web.Title})
			}
		}
	}
	if len(sources) == 0 {
		return nil, errWebSearchNoSources
	}
	return checkedWebSearchSources(sources, limit)
}

// ChatURL implements ChatEndpoint: models/{model}:streamGenerateContent?alt=sse
// for streams and models/{model}:generateContent otherwise.
func (s *GeminiStandard) ChatURL(base *url.URL, model string, stream bool) *url.URL {
	method := ":generateContent"
	if stream {
		method = ":streamGenerateContent"
	}
	endpoint := joinPath(base, geminiModelPath(model)+method)
	if stream {
		endpoint.RawQuery = "alt=sse"
	}
	return endpoint
}

// NestedExtraKeys implements NestedRequestExtras.
func (s *GeminiStandard) NestedExtraKeys() []string { return []string{"generationConfig"} }

func (s *GeminiStandard) EncodeRequest(req *ChatRequest) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	system, contents, err := encodeGeminiContents(req.Messages)
	if err != nil {
		return nil, err
	}
	wire := map[string]any{"contents": contents}
	if len(system) > 0 {
		wire["systemInstruction"] = map[string]any{"parts": system}
	}
	if len(req.Tools) > 0 {
		declarations := make([]any, 0, len(req.Tools))
		for _, tool := range req.Tools {
			tool = jsonFunctionTool(tool)
			schema, err := geminiFunctionSchema(tool.Function.Parameters)
			if err != nil {
				return nil, fmt.Errorf("tool %s: %w", tool.Function.Name, err)
			}
			declarations = append(declarations, map[string]any{
				"name":                 wireFunctionName(tool.Function.Name),
				"description":          tool.Function.Description,
				"parametersJsonSchema": schema,
			})
		}
		wire["tools"] = []any{map[string]any{"functionDeclarations": declarations}}
		if config := geminiToolConfig(req.ToolChoice); config != nil {
			wire["toolConfig"] = map[string]any{"functionCallingConfig": config}
		}
	}
	generation := map[string]any{}
	if limit := firstPositive(req.MaxOutputTokens, req.MaxCompletionTokens, req.MaxTokens); limit > 0 {
		generation["maxOutputTokens"] = limit
	}
	if req.Temperature != nil {
		generation["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		generation["topP"] = *req.TopP
	}
	if len(generation) > 0 {
		wire["generationConfig"] = generation
	}
	return json.Marshal(wire)
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

// geminiToolConfig maps Crude's tool choice to functionCallingConfig.
func geminiToolConfig(choice any) map[string]any {
	switch value := choice.(type) {
	case nil:
		return nil
	case string:
		switch value {
		case "auto", "":
			return nil
		case "none":
			return map[string]any{"mode": "NONE"}
		case "required", "any":
			return map[string]any{"mode": "ANY"}
		}
		return nil
	}
	raw, err := json.Marshal(choice)
	if err != nil {
		return nil
	}
	var named struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &named) != nil {
		return nil
	}
	if name := firstNonEmptyString(named.Function.Name, named.Name); name != "" {
		return map[string]any{"mode": "ANY", "allowedFunctionNames": []string{wireFunctionName(name)}}
	}
	return nil
}

// geminiContent is one wire Content.
type geminiContent struct {
	Role  string `json:"role"`
	Parts []any  `json:"parts"`
}

// encodeGeminiContents builds systemInstruction parts and the ordered
// contents. Consecutive tool results form one user turn of functionResponse
// parts, in call order, right after the model turn that called them.
func encodeGeminiContents(messages []ChatMessage) (system []any, contents []geminiContent, err error) {
	callNames := map[string]string{}
	// wireIDs maps Crude's call ID to the ID Gemini gave the call; a call
	// Gemini sent without an ID is answered without one.
	wireIDs := map[string]string{}
	toolTurn := -1
	for _, message := range messages {
		switch message.Role {
		case "system", "developer":
			if text := strings.TrimSpace(message.Content.String()); text != "" {
				system = append(system, map[string]any{"text": message.Content.String()})
			}
			toolTurn = -1
		case "assistant":
			parts, err := geminiModelParts(message, callNames, wireIDs)
			if err != nil {
				return nil, nil, err
			}
			if len(parts) > 0 {
				contents = append(contents, geminiContent{Role: "model", Parts: parts})
			}
			toolTurn = -1
		case "tool":
			part, err := geminiFunctionResponse(message, callNames, wireIDs)
			if err != nil {
				return nil, nil, err
			}
			if toolTurn < 0 {
				contents = append(contents, geminiContent{Role: "user"})
				toolTurn = len(contents) - 1
			}
			contents[toolTurn].Parts = append(contents[toolTurn].Parts, part)
		default:
			parts, err := geminiUserParts(message)
			if err != nil {
				return nil, nil, err
			}
			if len(parts) > 0 {
				contents = append(contents, geminiContent{Role: "user", Parts: parts})
			}
			toolTurn = -1
		}
	}
	return system, contents, nil
}

func geminiUserParts(message ChatMessage) ([]any, error) {
	var parts []any
	for _, part := range message.Content.Parts() {
		if part.Type == "text" {
			if part.Text != "" {
				parts = append(parts, map[string]any{"text": part.Text})
			}
			continue
		}
		media, ok, err := geminiMediaOf(part)
		if err != nil {
			return nil, err
		}
		if ok {
			parts = append(parts, geminiMediaPart(media))
		}
	}
	return parts, nil
}

func geminiMediaPart(media geminiMedia) map[string]any {
	if media.URI != "" {
		data := map[string]any{"fileUri": media.URI}
		if media.MimeType != "" {
			data["mimeType"] = media.MimeType
		}
		return map[string]any{"fileData": data}
	}
	return map[string]any{"inlineData": map[string]any{"mimeType": media.MimeType, "data": media.Data}}
}

// geminiModelParts returns a model turn: its exact stored parts when replay
// is valid, otherwise text and function calls rebuilt from the transcript.
func geminiModelParts(message ChatMessage, callNames, wireIDs map[string]string) ([]any, error) {
	for _, call := range message.ToolCalls {
		callNames[call.ID] = wireFunctionName(call.Function.Name)
		wireIDs[call.ID] = call.ID
	}
	for _, item := range message.ProviderItems {
		if item.Standard != "gemini" || item.Type != geminiContentItem || len(item.Data) == 0 {
			continue
		}
		var stored struct {
			Parts []json.RawMessage `json:"parts"`
		}
		if err := json.Unmarshal(item.Data, &stored); err != nil {
			return nil, fmt.Errorf("decode preserved Gemini content: %w", err)
		}
		parts := make([]any, 0, len(stored.Parts))
		callIndex := 0
		for _, raw := range stored.Parts {
			var probe struct {
				FunctionCall *struct {
					ID string `json:"id"`
				} `json:"functionCall"`
			}
			if json.Unmarshal(raw, &probe) == nil && probe.FunctionCall != nil {
				if callIndex < len(message.ToolCalls) {
					wireIDs[message.ToolCalls[callIndex].ID] = probe.FunctionCall.ID
				}
				callIndex++
			}
			parts = append(parts, json.RawMessage(bytes.Clone(raw)))
		}
		if len(parts) > 0 {
			return parts, nil
		}
	}
	var parts []any
	if text := message.Content.String(); text != "" {
		parts = append(parts, map[string]any{"text": text})
	}
	for index, call := range message.ToolCalls {
		part := map[string]any{"functionCall": map[string]any{
			"id": call.ID, "name": wireFunctionName(call.Function.Name), "args": geminiToolArgs(call),
		}}
		// Each model turn's first call carries the turn's signature. This
		// turn was not produced in this context, so it has none.
		if index == 0 {
			part["thoughtSignature"] = geminiSkipSignature
		}
		parts = append(parts, part)
	}
	return parts, nil
}

// geminiFunctionResponse encodes one tool result. Text goes in response
// (output, or error for a failed tool); media follows as response parts.
func geminiFunctionResponse(message ChatMessage, callNames, wireIDs map[string]string) (map[string]any, error) {
	name := callNames[message.ToolCallID]
	if name == "" {
		return nil, fmt.Errorf("gemini tool result %q has no matching function call", message.ToolCallID)
	}
	key := "output"
	if message.ToolError {
		key = "error"
	}
	response := map[string]any{"name": name, "response": map[string]any{key: geminiToolText(message)}}
	if id := wireIDs[message.ToolCallID]; id != "" {
		response["id"] = id
	}
	if !message.Content.IsText() {
		var media []any
		for _, part := range message.Content.Parts() {
			item, ok, err := geminiMediaOf(part)
			if err != nil {
				return nil, err
			}
			if ok {
				media = append(media, geminiMediaPart(item))
			}
		}
		if len(media) > 0 {
			response["parts"] = media
		}
	}
	return map[string]any{"functionResponse": response}, nil
}

// RequestExtras implements RequestExtras: the thinking level, thought
// summaries and the service tier.
func (s *GeminiStandard) RequestExtras(modelID, thinkingEffort string, settings ProtocolSettings) map[string]any {
	extras := map[string]any{}
	thinking := map[string]any{}
	if level := geminiThinkingLevel(thinkingEffort); level != "" {
		thinking["thinkingLevel"] = level
	}
	if thinkingEffort != "none" && settings.ResolveReasoningSummaries(true) {
		thinking["includeThoughts"] = true
	}
	if len(thinking) > 0 {
		extras["generationConfig"] = map[string]any{"thinkingConfig": thinking}
	}
	if settings.ServiceTier != "" {
		extras["serviceTier"] = settings.ServiceTier
	}
	if len(extras) == 0 {
		return nil
	}
	return extras
}

// FinalizeRequest implements ModelLimitLearner: it removes thinking from a
// model that does not think and moves a rejected level to the nearest level
// the model accepts.
func (s *GeminiStandard) FinalizeRequest(scope string, req *ChatRequest, body map[string]any) {
	generation, _ := body["generationConfig"].(map[string]any)
	thinking, _ := generation["thinkingConfig"].(map[string]any)
	if thinking == nil || req == nil {
		return
	}
	limits := geminiLimitsFor(scope, req.Model)
	if limits.noThinking {
		delete(generation, "thinkingConfig")
		if len(generation) == 0 {
			delete(body, "generationConfig")
		}
		return
	}
	if level, _ := thinking["thinkingLevel"].(string); level != "" {
		if nearest := geminiNearestLevel(level, limits.unsupported); nearest != "" {
			thinking["thinkingLevel"] = nearest
		} else {
			delete(thinking, "thinkingLevel")
		}
	}
}

// LearnRejection implements ModelLimitLearner.
func (s *GeminiStandard) LearnRejection(scope string, req *ChatRequest, body []byte, err *StatusError) bool {
	var sent struct {
		GenerationConfig struct {
			ThinkingConfig *struct {
				ThinkingLevel string `json:"thinkingLevel"`
			} `json:"thinkingConfig"`
		} `json:"generationConfig"`
	}
	if req == nil || json.Unmarshal(body, &sent) != nil || sent.GenerationConfig.ThinkingConfig == nil {
		return false
	}
	return geminiLearnThinkingRejection(scope, req.Model, sent.GenerationConfig.ThinkingConfig.ThinkingLevel, true, err)
}

func (s *GeminiStandard) DecodeResponse(body []byte) (*ChatResponse, error) {
	decoder := &geminiStreamDecoder{}
	update, err := decoder.Decode(StreamEvent{Data: body})
	if err != nil {
		return nil, err
	}
	if !decoder.finished {
		// A response without a finish reason still carries the whole turn.
		update = decoder.finish("", update)
	}
	return decoder.response(update), nil
}

func (s *GeminiStandard) NewStreamDecoder() StreamDecoder {
	return &streamErrorDecoder{protocol: s.Info().ID, inner: &geminiStreamDecoder{}}
}

// geminiResponse is one GenerateContentResponse (a whole response or one
// stream chunk).
type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []json.RawMessage `json:"parts"`
		} `json:"content"`
		FinishReason  string `json:"finishReason"`
		FinishMessage string `json:"finishMessage"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata *geminiUsage `json:"usageMetadata"`
	ModelVersion  string       `json:"modelVersion"`
	ResponseID    string       `json:"responseId"`
}

// geminiPart is the subset of a Part the decoder interprets. The stored turn
// keeps every part verbatim.
type geminiPart struct {
	Text             *string `json:"text"`
	Thought          bool    `json:"thought"`
	ThoughtSignature string  `json:"thoughtSignature"`
	FunctionCall     *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
}

type geminiUsage struct {
	PromptTokenCount        int    `json:"promptTokenCount"`
	CandidatesTokenCount    int    `json:"candidatesTokenCount"`
	CachedContentTokenCount int    `json:"cachedContentTokenCount"`
	ThoughtsTokenCount      int    `json:"thoughtsTokenCount"`
	ToolUsePromptTokenCount int    `json:"toolUsePromptTokenCount"`
	ServiceTier             string `json:"serviceTier"`
}

// tokenUsage normalizes Gemini usage. Cached tokens are part of the prompt;
// thoughts are billed as output. The API omits a zero cache count, so cache
// reads are always known.
func (u geminiUsage) tokenUsage() TokenUsage {
	return TokenUsage{
		PromptTokens:       u.PromptTokenCount + u.ToolUsePromptTokenCount,
		CompletionTokens:   u.CandidatesTokenCount + u.ThoughtsTokenCount,
		PromptTokensCached: u.CachedContentTokenCount,
		CacheReadKnown:     true,
		ReasoningTokens:    u.ThoughtsTokenCount,
		ReasoningKnown:     true,
		ReasoningInOutput:  true,
		ServiceTier:        u.ServiceTier,
	}
}

// geminiStreamDecoder assembles one model turn from stream chunks. Text
// chunks of one unsigned part merge; a signed part, a function call and any
// other part keep their own position.
type geminiStreamDecoder struct {
	finished    bool
	parts       []json.RawMessage
	merging     *geminiPart // last part, when it is unsigned text that may grow
	calls       int
	thoughtTurn int
	inThought   bool
	usage       *TokenUsage
}

func (d *geminiStreamDecoder) Decode(event StreamEvent) (StreamUpdate, error) {
	data := bytes.TrimSpace(event.Data)
	if len(data) == 0 {
		return StreamUpdate{}, nil
	}
	var chunk geminiResponse
	if err := json.Unmarshal(data, &chunk); err != nil {
		return StreamUpdate{}, fmt.Errorf("decode Gemini stream chunk: %w", err)
	}
	var update StreamUpdate
	if chunk.UsageMetadata != nil {
		usage := chunk.UsageMetadata.tokenUsage()
		d.usage = &usage
		update.Usage = &usage
	}
	if len(chunk.Candidates) == 0 {
		if chunk.PromptFeedback != nil && chunk.PromptFeedback.BlockReason != "" {
			return StreamUpdate{}, &APIError{Type: "content_filter", Code: strings.ToLower(chunk.PromptFeedback.BlockReason),
				Message: "Gemini blocked the prompt (" + chunk.PromptFeedback.BlockReason + ")"}
		}
		return update, nil
	}
	candidate := chunk.Candidates[0]
	if d.finished {
		// A frame after the finish carries late usage only.
		return update, nil
	}
	for _, raw := range candidate.Content.Parts {
		if err := d.part(raw, &update); err != nil {
			return StreamUpdate{}, err
		}
	}
	if geminiFinishReason(candidate.FinishReason, false) == "" {
		return update, nil
	}
	if geminiFailedGeneration(candidate.FinishReason) {
		return StreamUpdate{}, geminiFailedGenerationError(candidate.FinishReason, candidate.FinishMessage)
	}
	return d.finish(candidate.FinishReason, update), nil
}

// CompleteAtEOF implements eofTerminalDecoder: a Gemini stream has no
// terminal event. The chunk with the finish reason ends the turn, and the
// body ends after any late usage frame.
func (d *geminiStreamDecoder) CompleteAtEOF() bool { return d.finished }

func (d *geminiStreamDecoder) part(raw json.RawMessage, update *StreamUpdate) error {
	var part geminiPart
	if err := json.Unmarshal(raw, &part); err != nil {
		return fmt.Errorf("decode Gemini part: %w", err)
	}
	if part.FunctionCall != nil {
		d.closeMerge()
		d.inThought = false
		args := strings.TrimSpace(string(part.FunctionCall.Args))
		if args == "" || args == "null" {
			args = "{}"
		}
		update.ToolCalls = append(update.ToolCalls, ToolCallDelta{
			Index: d.calls, ID: part.FunctionCall.ID, Type: "function",
			Name: canonicalToolName(part.FunctionCall.Name), Arguments: args, ReplaceArguments: true,
		})
		d.calls++
		d.parts = append(d.parts, bytes.Clone(raw))
		return nil
	}
	if part.Text == nil {
		// Media, code execution and other parts replay as they came.
		d.closeMerge()
		d.parts = append(d.parts, bytes.Clone(raw))
		return nil
	}
	text := *part.Text
	if part.Thought {
		if !d.inThought {
			d.thoughtTurn++
			d.inThought = true
		}
		if text != "" {
			update.Reasoning = append(update.Reasoning, reasoning.Delta{Text: text, Kind: reasoning.Summary, BlockID: fmt.Sprintf("thought-%d", d.thoughtTurn)})
		}
	} else {
		if text != "" {
			d.inThought = false
		}
		update.ContentDelta += text
	}
	plain := geminiPlainTextPart(raw)
	if plain && d.merging != nil && part.ThoughtSignature == "" && d.merging.Thought == part.Thought {
		merged := *d.merging.Text + text
		d.merging.Text = &merged
		return nil
	}
	d.closeMerge()
	if part.ThoughtSignature != "" || !plain {
		// Signed text and text with other fields keep their own position.
		d.parts = append(d.parts, bytes.Clone(raw))
		return nil
	}
	copy := part
	d.merging = &copy
	return nil
}

// geminiPlainTextPart reports a part made only of text, thought and
// thoughtSignature; any other field keeps the part verbatim.
func geminiPlainTextPart(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	for key := range fields {
		switch key {
		case "text", "thought", "thoughtSignature":
		default:
			return false
		}
	}
	return true
}

func (d *geminiStreamDecoder) closeMerge() {
	if d.merging == nil {
		return
	}
	part := map[string]any{"text": *d.merging.Text}
	if d.merging.Thought {
		part["thought"] = true
	}
	encoded, _ := json.Marshal(part)
	d.parts = append(d.parts, encoded)
	d.merging = nil
}

// finish closes the turn: the finish reason, the exact parts as one provider
// item, and the terminal flag.
func (d *geminiStreamDecoder) finish(reason string, update StreamUpdate) StreamUpdate {
	d.closeMerge()
	d.finished = true
	update.FinishReason = geminiFinishReason(reason, d.calls > 0)
	if update.FinishReason == "" {
		update.FinishReason = "stop"
		if d.calls > 0 {
			update.FinishReason = "tool_calls"
		}
	}
	if len(d.parts) > 0 {
		encoded, err := json.Marshal(map[string]any{"parts": d.parts})
		if err == nil {
			update.ProviderItems = append(update.ProviderItems, ProviderItem{Standard: "gemini", Type: geminiContentItem, Data: encoded})
		}
	}
	if update.Usage == nil && d.usage != nil {
		usage := *d.usage
		update.Usage = &usage
	}
	return update
}

// response converts a terminal update of a whole response to ChatResponse.
func (d *geminiStreamDecoder) response(update StreamUpdate) *ChatResponse {
	message := ChatMessage{Role: "assistant", Content: TextContent(update.ContentDelta), ProviderItems: update.ProviderItems}
	var trace string
	for _, delta := range update.Reasoning {
		trace = message.ReasoningDisplay.Append(trace, delta)
	}
	message.ReasoningContent, message.Reasoning = trace, trace
	for _, call := range update.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, ToolCall{
			Index: call.Index, ID: call.ID, Type: "function",
			Function: FunctionCall{Name: call.Name, Arguments: call.Arguments},
		})
	}
	response := &ChatResponse{Choices: []ChatChoice{{Message: message, FinishReason: update.FinishReason}}}
	if update.Usage != nil {
		usage := *update.Usage
		response.ServiceTier = usage.ServiceTier
		response.Usage = &ChatUsage{
			PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens,
			TotalTokens:             usage.PromptTokens + usage.CompletionTokens,
			PromptTokensDetails:     &PromptTokenDetails{CachedTokens: usage.PromptTokensCached, CacheReadKnown: true},
			CompletionTokensDetails: &CompletionTokenDetails{ReasoningTokens: usage.ReasoningTokens, ReasoningKnown: true},
		}
	}
	return response
}
