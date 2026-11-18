package provider

import (
	"crude/metrics"
)

// Model request series, keyed by model so a slow provider is named.
var (
	modelConnect = metrics.NewTable(metrics.Meta{Name: "agent.connect", Group: metrics.GroupAgents, Title: "Request accepted",
		Help: "Sending a model request until the provider answers with a stream (includes retries and upload).",
		Warn: 3_000_000, Crit: 15_000_000, Stall: 30_000_000})
	modelFirstOutput = metrics.NewTable(metrics.Meta{Name: "agent.first_output", Group: metrics.GroupAgents, Title: "First output",
		Help: "Sending a model request until its first text, reasoning or tool call arrives.",
		Warn: 5_000_000, Crit: 30_000_000, Stall: 60_000_000})
	modelResponse = metrics.NewTable(metrics.Meta{Name: "agent.response", Group: metrics.GroupAgents, Title: "Complete response",
		Help: "Whole streamed responses per model."})
	modelOutput = metrics.NewCounter(metrics.Meta{Name: "agent.output", Group: metrics.GroupAgents, Title: "Streamed output", Unit: "chars",
		Help: "Text, reasoning and tool arguments received from models."})
	modelRetries = metrics.NewCounter(metrics.Meta{Name: "agent.retries", Group: metrics.GroupAgents, Title: "Provider retries",
		Help: "Automatic retries after transport errors or retryable statuses."})
	modelStreams = metrics.NewGauge(metrics.Meta{Name: "agent.streams", Group: metrics.GroupAgents, Title: "Open model streams", Unit: "items",
		Help: "Model responses currently streaming."}, nil)
)

func updateOutputSize(update StreamUpdate) int {
	n := len(update.ContentDelta) + len(update.ReasoningText())
	for _, call := range update.ToolCalls {
		n += len(call.Arguments)
	}
	return n
}
