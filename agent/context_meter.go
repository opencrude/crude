package agent

import "crude/provider"

// contextMeter projects the next request from the latest accepted request.
// Provider input/completion usage is authoritative; only messages appended
// after that request are estimated locally.
type contextMeter struct {
	inputTokens      int
	completionTokens int
	requestMessages  int
}

func (m *contextMeter) observe(inputTokens, completionTokens, requestMessages int) {
	m.inputTokens = inputTokens
	m.completionTokens = completionTokens
	m.requestMessages = requestMessages
}

func (m *contextMeter) reset() {
	*m = contextMeter{}
}

func (m contextMeter) projected(messages []provider.ChatMessage, tools []provider.ToolDef, model string) int {
	if m.inputTokens <= 0 || m.requestMessages < 0 || m.requestMessages > len(messages) {
		return estimateCanonicalRequest(messages, tools, model)
	}
	total := m.inputTokens
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

func estimateCanonicalRequest(messages []provider.ChatMessage, tools []provider.ToolDef, model string) int {
	total := provider.EstimateMessagesTotal(messages, model)
	for _, tool := range tools {
		total += provider.EstimateTokens(tool.Function.Name, model)
		total += provider.EstimateTokens(tool.Function.Description, model)
		total += provider.EstimateTokens(string(tool.Function.Parameters), model)
	}
	return total
}
