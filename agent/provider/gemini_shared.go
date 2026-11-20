package provider

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// This file holds the Gemini API pieces both Gemini protocols share: the API
// key header, the model list, typed decisions, function schemas, media parts,
// thinking levels, learned model limits and Google error details.

// geminiSkipSignature is the documented thought-signature value for function
// calls that Gemini did not produce in this exact context (another provider,
// a compacted prefix). Gemini 3 and later reject an unsigned function call in
// history; this value asks the API to skip the check for that call.
const geminiSkipSignature = "skip_thought_signature_validator"

// geminiCommon implements the parts of the Gemini API that do not depend on
// the generation endpoint.
type geminiCommon struct{}

// AuthHeaders implements ProtocolAuth: the Gemini API takes the key in
// x-goog-api-key. A key in the URL would leak into logs and proxies.
func (geminiCommon) AuthHeaders(apiKey string) http.Header {
	if apiKey == "" {
		return nil
	}
	headers := http.Header{}
	headers.Set("x-goog-api-key", apiKey)
	return headers
}

// ModelsURL implements ModelLister. The API pages with pageToken; 1000 is the
// largest page it accepts.
func (geminiCommon) ModelsURL(base *url.URL, cursor string) *url.URL {
	endpoint := joinPath(base, "models")
	query := endpoint.Query()
	query.Set("pageSize", "1000")
	if cursor != "" {
		query.Set("pageToken", cursor)
	}
	endpoint.RawQuery = query.Encode()
	return endpoint
}

// geminiModel is one entry of the Gemini model list.
type geminiModel struct {
	Name                       string   `json:"name"`
	DisplayName                string   `json:"displayName"`
	InputTokenLimit            int      `json:"inputTokenLimit"`
	OutputTokenLimit           int      `json:"outputTokenLimit"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
	Thinking                   bool     `json:"thinking"`
}

// DecodeModelsPage implements ModelLister. Only models that generate content
// or answer typed decisions are listed; embedding, live-audio and music models
// cannot serve a Crude agent.
func (geminiCommon) DecodeModelsPage(body []byte) (ModelPage, error) {
	var response struct {
		Models        []geminiModel `json:"models"`
		NextPageToken string        `json:"nextPageToken"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return ModelPage{}, fmt.Errorf("unmarshal Gemini models: %w", err)
	}
	models := make([]ModelInfo, 0, len(response.Models))
	for _, model := range response.Models {
		id := geminiModelID(model.Name)
		chat := slices.Contains(model.SupportedGenerationMethods, "generateContent")
		decision := slices.Contains(model.SupportedGenerationMethods, "createDecision")
		if id == "" || !chat && !decision {
			continue
		}
		models = append(models, ModelInfo{
			ID: id, Name: firstNonEmptyString(model.DisplayName, id),
			ContextWindow: model.InputTokenLimit, MaxOutput: model.OutputTokenLimit,
			Decision: decision && !chat,
		})
	}
	return ModelPage{Models: models, Next: response.NextPageToken}, nil
}

// geminiModelID strips the resource prefix from a model name.
func geminiModelID(name string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), "models/")
}

// geminiModelPath is the resource path of a model. Tuned models and other
// resource names that already carry a collection are kept.
func geminiModelPath(model string) string {
	model = strings.TrimSpace(model)
	if strings.Contains(model, "/") {
		return model
	}
	return "models/" + model
}

// ContextWindow implements ProtocolMetadata.
func (geminiCommon) ContextWindow(modelID string) int {
	return LookupContextWindow(modelID)
}

// EncodeDecision implements DecisionCodec for the Gemini Decisions API
// (POST /v1beta/decisions). Questions are keyed by Crude's slots; "noul" and
// "choice" have the same shape as Crude's DecisionQuestion.
func (geminiCommon) EncodeDecision(model, state string, questions map[string]DecisionQuestion) map[string]any {
	return map[string]any{
		"model":     model,
		"input":     []any{map[string]any{"type": "text", "text": state}},
		"questions": questions,
	}
}

// DecodeDecision implements DecisionCodec. Answers are keyed by slot, like
// Ollama System One.
func (geminiCommon) DecodeDecision(reply map[string]any, questions map[string]DecisionQuestion) (map[string]DecisionAnswer, error) {
	return decodeKeyedDecision(reply, questions)
}

// geminiThinkingLevels is the Gemini thinking ladder, lowest first.
var geminiThinkingLevels = []string{"MINIMAL", "LOW", "MEDIUM", "HIGH", "XHIGH"}

// geminiThinkingLevel maps Crude's effort ladder to a Gemini thinking level.
// Gemini 3 and later cannot turn thinking off, so "none" is the lowest level.
// An empty effort keeps the model default.
func geminiThinkingLevel(effort string) string {
	switch effort {
	case "none":
		return "MINIMAL"
	case "low":
		return "LOW"
	case "medium":
		return "MEDIUM"
	case "high":
		return "HIGH"
	case "xhigh", "max":
		return "XHIGH"
	}
	return ""
}

// geminiModelLimits holds what a model rejected, learned from the API's own
// rejection. Each model accepts a different subset of thinking levels (Gemma
// takes MINIMAL and HIGH only; Flash has no XHIGH) and some models do not
// think at all. The list endpoint does not publish the levels.
type geminiModelLimits struct {
	noThinking  bool
	unsupported map[string]bool
}

var geminiLearnedLimits = struct {
	sync.Mutex
	entries map[string]geminiModelLimits
}{entries: map[string]geminiModelLimits{}}

func geminiLimitKey(scope, model string) string {
	return scope + "\x00" + strings.ToLower(geminiModelID(model))
}

func geminiLimitsFor(scope, model string) geminiModelLimits {
	geminiLearnedLimits.Lock()
	defer geminiLearnedLimits.Unlock()
	entry := geminiLearnedLimits.entries[geminiLimitKey(scope, model)]
	return geminiModelLimits{noThinking: entry.noThinking, unsupported: maps0Clone(entry.unsupported)}
}

func maps0Clone(in map[string]bool) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]bool, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func geminiLearn(scope, model string, update func(*geminiModelLimits)) {
	geminiLearnedLimits.Lock()
	defer geminiLearnedLimits.Unlock()
	key := geminiLimitKey(scope, model)
	entry := geminiLearnedLimits.entries[key]
	entry.unsupported = maps0Clone(entry.unsupported)
	update(&entry)
	geminiLearnedLimits.entries[key] = entry
}

// geminiNearestLevel returns the accepted level closest to the requested one,
// preferring the lower level on a tie, or "" when no level is left.
func geminiNearestLevel(level string, unsupported map[string]bool) string {
	start := slices.Index(geminiThinkingLevels, level)
	if start < 0 || !unsupported[level] {
		return level
	}
	for distance := 1; distance < len(geminiThinkingLevels); distance++ {
		for _, index := range []int{start - distance, start + distance} {
			if index >= 0 && index < len(geminiThinkingLevels) && !unsupported[geminiThinkingLevels[index]] {
				return geminiThinkingLevels[index]
			}
		}
	}
	return ""
}

var (
	geminiLevelRejected    = regexp.MustCompile(`(?i)^thinking level(?: ([a-z_]+))? is not supported for this model`)
	geminiThinkingDisabled = regexp.MustCompile(`(?i)^thinking is not enabled for models/`)
)

// geminiLearnThinkingRejection records the limitation an INVALID_ARGUMENT
// rejection proves about the thinking request that was sent, and reports
// whether a new encoding avoids it. sentLevel is the level in the rejected
// body ("" when none was sent).
func geminiLearnThinkingRejection(scope, model, sentLevel string, sentThinking bool, err *StatusError) bool {
	if err == nil || err.StatusCode != http.StatusBadRequest || !sentThinking {
		return false
	}
	message := err.Message
	if _, detail, found := strings.Cut(message, "INVALID_ARGUMENT: "); found {
		message = detail
	} else if _, detail, found := strings.Cut(message, "invalid_request: "); found {
		message = detail
	}
	message = strings.TrimSpace(message)
	if geminiThinkingDisabled.MatchString(message) {
		geminiLearn(scope, model, func(limits *geminiModelLimits) { limits.noThinking = true })
		return true
	}
	match := geminiLevelRejected.FindStringSubmatch(message)
	if match == nil || sentLevel == "" {
		return false
	}
	if named := strings.ToUpper(match[1]); named != "" && named != sentLevel {
		return false
	}
	geminiLearn(scope, model, func(limits *geminiModelLimits) {
		if limits.unsupported == nil {
			limits.unsupported = map[string]bool{}
		}
		limits.unsupported[sentLevel] = true
	})
	return true
}

// geminiFunctionSchema converts a tool's JSON Schema to what Gemini enforces
// in parametersJsonSchema. Gemini ignores const, so a constant becomes a
// one-value enum; required names that the schema does not declare are
// rejected, so they are dropped; $schema is not accepted. Everything else
// passes unchanged. The walk follows schema keywords only, so a property that
// happens to be named "const" and literal values (enum, default, examples)
// are never rewritten.
func geminiFunctionSchema(raw json.RawMessage) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{"type": "object", "properties": map[string]any{}}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var schema any
	if err := decoder.Decode(&schema); err != nil {
		return nil, fmt.Errorf("decode tool schema: %w", err)
	}
	return geminiSchema(schema), nil
}

// Keywords whose value is one subschema, a list of subschemas, or a map of
// names to subschemas (JSON Schema 2020-12 and draft-07).
var (
	geminiSubschemaKeys     = []string{"items", "additionalProperties", "not", "if", "then", "else", "contains", "propertyNames", "unevaluatedItems", "unevaluatedProperties", "additionalItems"}
	geminiSubschemaListKeys = []string{"anyOf", "allOf", "oneOf", "prefixItems"}
	geminiSubschemaMapKeys  = []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"}
)

func geminiSchema(node any) any {
	schema, ok := node.(map[string]any)
	if !ok {
		// true/false schemas and malformed nodes pass unchanged.
		return node
	}
	out := make(map[string]any, len(schema))
	for key, value := range schema {
		switch {
		case key == "$schema":
			continue
		case key == "const":
			continue
		case slices.Contains(geminiSubschemaKeys, key):
			if items, isList := value.([]any); isList {
				out[key] = geminiSchemaList(items)
			} else {
				out[key] = geminiSchema(value)
			}
		case slices.Contains(geminiSubschemaListKeys, key):
			items, _ := value.([]any)
			out[key] = geminiSchemaList(items)
		case slices.Contains(geminiSubschemaMapKeys, key):
			children, isMap := value.(map[string]any)
			if !isMap {
				out[key] = value
				continue
			}
			mapped := make(map[string]any, len(children))
			for name, child := range children {
				mapped[name] = geminiSchema(child)
			}
			out[key] = mapped
		default:
			out[key] = value
		}
	}
	if _, hasConst := schema["const"]; hasConst {
		// const fixes the value; an enum beside it adds nothing.
		out["enum"] = []any{schema["const"]}
	}
	if required, ok := out["required"].([]any); ok {
		properties, _ := out["properties"].(map[string]any)
		kept := make([]any, 0, len(required))
		for _, name := range required {
			if text, ok := name.(string); ok && properties != nil {
				if _, declared := properties[text]; declared {
					kept = append(kept, name)
				}
			}
		}
		if len(kept) == 0 {
			delete(out, "required")
		} else {
			out["required"] = kept
		}
	}
	return out
}

func geminiSchemaList(items []any) []any {
	out := make([]any, len(items))
	for index, item := range items {
		out[index] = geminiSchema(item)
	}
	return out
}

// geminiToolArgs decodes a call's argument string to the JSON object Gemini
// expects. Custom (freeform) tools carry their text in "input".
func geminiToolArgs(call ToolCall) map[string]any {
	if call.Type == "custom" {
		return map[string]any{"input": call.Function.Arguments}
	}
	decoder := json.NewDecoder(strings.NewReader(call.Function.Arguments))
	decoder.UseNumber()
	var args map[string]any
	if err := decoder.Decode(&args); err != nil || args == nil {
		return map[string]any{}
	}
	return args
}

// geminiMedia is one media attachment: inline base64 bytes or a remote URI.
type geminiMedia struct {
	MimeType string
	Data     string
	URI      string
}

// geminiMediaOf decodes a Crude content part to Gemini media. Text parts
// return ok=false.
func geminiMediaOf(part ContentPart) (geminiMedia, bool, error) {
	switch part.Type {
	case "text":
		return geminiMedia{}, false, nil
	case "image_url", "document", "video", "audio":
	default:
		return geminiMedia{}, false, fmt.Errorf("gemini content part %q is unsupported", part.Type)
	}
	if len(part.Data) > 0 {
		if part.MimeType == "" {
			return geminiMedia{}, false, fmt.Errorf("gemini media part has no MIME type")
		}
		return geminiMedia{MimeType: part.MimeType, Data: base64.StdEncoding.EncodeToString(part.Data)}, true, nil
	}
	if part.ImageURL == nil || part.ImageURL.URL == "" {
		return geminiMedia{}, false, fmt.Errorf("gemini media part has no bytes or URL")
	}
	value := part.ImageURL.URL
	if strings.HasPrefix(value, "data:") {
		metadata, data, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
		mediaType, encoding, hasEncoding := strings.Cut(metadata, ";")
		if !ok || mediaType == "" || !hasEncoding || encoding != "base64" {
			return geminiMedia{}, false, fmt.Errorf("gemini image data URL must be base64")
		}
		return geminiMedia{MimeType: mediaType, Data: data}, true, nil
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return geminiMedia{}, false, fmt.Errorf("gemini image URL must be an absolute HTTP(S) URL")
	}
	return geminiMedia{MimeType: part.MimeType, URI: value}, true, nil
}

// geminiToolText joins the text parts of a tool result.
func geminiToolText(message ChatMessage) string {
	if message.Content.IsText() {
		return message.Content.String()
	}
	return contentPartsText(message.Content.Parts())
}

// googleErrorFacts is what a google.rpc.Status error detail list proves.
type googleErrorFacts struct {
	// RetryDelay is google.rpc.RetryInfo.retryDelay.
	RetryDelay time.Duration
	// Markers are lower-case classification codes: ErrorInfo reasons, and
	// "insufficient_quota" when a QuotaFailure names a daily quota (waiting
	// minutes cannot help).
	Markers []string
}

func parseGoogleErrorDetails(details []json.RawMessage) googleErrorFacts {
	var facts googleErrorFacts
	for _, raw := range details {
		var detail struct {
			Type       string `json:"@type"`
			RetryDelay string `json:"retryDelay"`
			Reason     string `json:"reason"`
			Violations []struct {
				QuotaID string `json:"quotaId"`
			} `json:"violations"`
		}
		if json.Unmarshal(raw, &detail) != nil {
			continue
		}
		switch strings.TrimPrefix(detail.Type, "type.googleapis.com/") {
		case "google.rpc.RetryInfo":
			if delay, ok := parseProtoDuration(detail.RetryDelay); ok && delay > facts.RetryDelay {
				facts.RetryDelay = delay
			}
		case "google.rpc.ErrorInfo":
			if detail.Reason != "" {
				facts.Markers = append(facts.Markers, strings.ToLower(detail.Reason))
			}
		case "google.rpc.QuotaFailure":
			for _, violation := range detail.Violations {
				if strings.Contains(violation.QuotaID, "PerDay") && !slices.Contains(facts.Markers, "insufficient_quota") {
					facts.Markers = append(facts.Markers, "insufficient_quota")
				}
			}
		}
	}
	return facts
}

// parseProtoDuration parses a protobuf JSON Duration such as "13s" or "0.5s".
func parseProtoDuration(value string) (time.Duration, bool) {
	seconds, ok := strings.CutSuffix(strings.TrimSpace(value), "s")
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(seconds, 64)
	if err != nil || parsed < 0 || parsed > float64(24*time.Hour/time.Second) {
		return 0, false
	}
	return time.Duration(parsed * float64(time.Second)), true
}

// geminiFinishReason maps a Gemini finish reason to Crude's canonical
// reasons. Filter and policy stops become "content_filter".
func geminiFinishReason(reason string, hasCalls bool) string {
	switch strings.ToUpper(reason) {
	case "", "FINISH_REASON_UNSPECIFIED":
		return ""
	case "STOP":
		if hasCalls {
			return "tool_calls"
		}
		return "stop"
	case "MAX_TOKENS", "CONTINUATION":
		return "length"
	case "SAFETY", "RECITATION", "LANGUAGE", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII",
		"IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_RECITATION":
		return "content_filter"
	default:
		return strings.ToLower(reason)
	}
}

// geminiFailedGeneration reports finish reasons that mean the model produced
// no usable turn. The stream owner retries them before any output was kept.
func geminiFailedGeneration(reason string) bool {
	switch strings.ToUpper(reason) {
	case "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL", "MALFORMED_RESPONSE", "MISSING_THOUGHT_SIGNATURE", "TOO_MANY_TOOL_CALLS":
		return true
	}
	return false
}

func geminiFailedGenerationError(reason, message string) error {
	if message == "" {
		message = "Gemini could not produce a valid response (" + reason + ")"
	}
	return &APIError{Type: "server_error", Code: strings.ToLower(reason), Message: message}
}
