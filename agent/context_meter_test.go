package agent

import (
	"encoding/json"
	"testing"

	"crude/provider"
)

func TestContextMeterPrefersProviderBaselineOverReplayWrappers(t *testing.T) {
	messages := []provider.ChatMessage{provider.NewTextMessage("system", "base")}
	requestMessages := len(messages)
	response := provider.NewTextMessage("assistant", "answer")
	response.ReasoningContent = "reasoning"
	for index := 0; index < 4000; index++ {
		response.ProviderItems = append(response.ProviderItems, provider.ProviderItem{
			Standard: "openai", Type: "openai.reasoning_detail",
			Data: json.RawMessage(`{"type":"reasoning.text","text":"word","index":0}`),
		})
	}
	messages = append(messages, response, provider.NewTextMessage("tool", "small result"))

	var meter contextMeter
	meter.observe(117_532, 38_727, requestMessages)
	got := meter.projected(messages, nil, "deepseek")
	if got < 156_000 || got > 157_000 {
		t.Fatalf("projected context = %d, want provider baseline plus semantic delta", got)
	}
}

func TestContextMeterFallsBackToSemanticRequestEstimate(t *testing.T) {
	messages := []provider.ChatMessage{
		provider.NewTextMessage("system", "12345678"),
		provider.NewTextMessage("user", "12345678"),
	}
	tools := []provider.ToolDef{{Function: provider.FunctionDef{
		Name: "tool", Description: "1234", Parameters: json.RawMessage(`{"type":"object"}`),
	}}}
	var meter contextMeter
	if got, want := meter.projected(messages, tools, "model"), estimateCanonicalRequest(messages, tools, "model"); got != want {
		t.Fatalf("projected = %d, want %d", got, want)
	}
}
