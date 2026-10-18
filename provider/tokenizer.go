package provider

import (
	"encoding/json"
	"unicode/utf8"
)

// EstimateTokens is the fallback used until a provider reports authoritative
// usage. It deliberately measures semantic text only: wire framing, JSON keys,
// and streaming envelopes are not model context.
func EstimateTokens(text string, _ string) int {
	count := utf8.RuneCountInString(text)
	if count == 0 {
		return 0
	}
	return (count + 3) / 4
}

// EstimateMessageTokens estimates one canonical message. Protocol replay data
// is counted only when no protocol-neutral content represents it.
func EstimateMessageTokens(msg ChatMessage, model string) int {
	// Decode multipart once. Its encoded image bytes are transport data, not
	// text; repeatedly calling ContentString and Parts decoded megabytes three
	// times for every context estimate before a screenshot could be sent.
	text := string(msg.Content)
	var parts []ContentPart
	if !msg.Content.IsText() {
		parts = msg.Content.Parts()
		text = contentPartsText(parts)
	}
	tokens := EstimateTokens(text, model)
	hasReasoning := msg.ReasoningContent != "" || msg.Reasoning != ""
	if msg.ReasoningContent != "" {
		tokens += EstimateTokens(msg.ReasoningContent, model)
	}
	if msg.Reasoning != "" && msg.Reasoning != msg.ReasoningContent {
		tokens += EstimateTokens(msg.Reasoning, model)
	}
	if msg.Refusal != "" && msg.Refusal != text {
		tokens += EstimateTokens(msg.Refusal, model)
	}
	if !hasReasoning {
		for _, detail := range msg.ReasoningDetails {
			tokens += estimateJSONText(detail, model)
		}
	}
	for _, part := range parts {
		if part.Type == "image_url" && part.ImageURL != nil {
			// Image tokenization varies by dimensions/detail and provider.
			tokens += 1024
		}
	}
	for _, call := range msg.ToolCalls {
		tokens += EstimateTokens(call.Function.Name, model)
		tokens += EstimateTokens(call.Function.Arguments, model)
	}
	if text == "" && !hasReasoning && len(msg.ToolCalls) == 0 {
		for _, item := range CanonicalizeProviderItems(msg.ProviderItems) {
			tokens += estimateJSONText(item.Data, model)
		}
	}
	return tokens
}

func EstimateMessagesTotal(messages []ChatMessage, model string) int {
	total := 0
	for _, message := range messages {
		total += EstimateMessageTokens(message, model)
	}
	return total
}

func estimateJSONText(data json.RawMessage, model string) int {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return EstimateTokens(string(data), model)
	}
	return estimateJSONValueText(value, model)
}

func estimateJSONValueText(value any, model string) int {
	switch typed := value.(type) {
	case string:
		return EstimateTokens(typed, model)
	case []any:
		total := 0
		for _, item := range typed {
			total += estimateJSONValueText(item, model)
		}
		return total
	case map[string]any:
		total := 0
		for _, item := range typed {
			total += estimateJSONValueText(item, model)
		}
		return total
	default:
		return 0
	}
}
