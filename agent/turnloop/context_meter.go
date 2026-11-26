package turnloop

import "crude/agent/provider"

// contextMeter projects the next request from the latest accepted request.
// Provider input/completion usage is authoritative; only messages appended
// after that request are estimated locally.
type contextMeter struct {
	inputTokens      int
	completionTokens int
	requestMessages  int
	requestFixed     int
	fixedKnown       bool
}

func (m *contextMeter) observe(inputTokens, completionTokens, requestMessages int, fixedTokens ...int) {
	m.inputTokens = inputTokens
	m.completionTokens = completionTokens
	m.requestMessages = requestMessages
	m.fixedKnown = len(fixedTokens) > 0
	if m.fixedKnown {
		m.requestFixed = fixedTokens[0]
	}
}

func (m *contextMeter) reset() {
	*m = contextMeter{}
}

func (m contextMeter) projected(messages []provider.ChatMessage, tools []provider.ToolDef, model string) int {
	if m.inputTokens <= 0 || m.requestMessages < 0 || m.requestMessages > len(messages) {
		return estimateCanonicalRequest(messages, tools, model)
	}
	total := m.inputTokens
	if m.fixedKnown && len(messages) > 0 {
		total += estimateCanonicalRequest(messages[:1], tools, model) - m.requestFixed
	}
	index := m.requestMessages
	if index < len(messages) && messages[index].Role == "assistant" {
		if m.completionTokens > 0 {
			total += m.completionTokens
		} else {
			total += provider.EstimateMessageTokens(messages[index], model)
		}
		index++
	}
	total += provider.EstimateMessagesTotal(messages[index:], model)
	return total
}

// estimateRequestBytes approximates the encoded size of a request: message
// content including inline media, reasoning, tool calls, provider items and
// tool declarations. A byte limit learned from one request is compared with
// the same estimate of the next.
func estimateRequestBytes(messages []provider.ChatMessage, tools []provider.ToolDef) int {
	total := 0
	for _, message := range messages {
		total += 32 + len(message.Content) + len(message.ReasoningContent) + len(message.Refusal)
		for _, call := range message.ToolCalls {
			total += 32 + len(call.ID) + len(call.Function.Name) + len(call.Function.Arguments)
		}
		for _, item := range message.ProviderItems {
			total += len(item.Data)
		}
	}
	for _, tool := range tools {
		total += 32 + len(tool.Function.Name) + len(tool.Function.Description) + len(tool.Function.Parameters)
	}
	return total
}

func estimateCanonicalRequest(messages []provider.ChatMessage, tools []provider.ToolDef, model string) int {
	total := provider.EstimateMessagesTotal(messages, model)
	for _, tool := range tools {
		total += provider.EstimateTokens(tool.Function.Name, model)
		total += provider.EstimateTokens(tool.Function.Description, model)
		total += provider.EstimateTokens(string(tool.Function.Parameters), model)
	}
	return total
}
