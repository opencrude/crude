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

func TestContextMeterProjectsChangedToolSchemasWithoutDoubleCounting(t *testing.T) {
	messages := []provider.ChatMessage{provider.NewTextMessage("system", "instructions"), provider.NewTextMessage("user", "request")}
	oldTools := []provider.ToolDef{{Function: provider.FunctionDef{Name: "old", Description: "small schema"}}}
	newTools := append(append([]provider.ToolDef(nil), oldTools...), provider.ToolDef{Function: provider.FunctionDef{Name: "new", Description: "large new schema", Parameters: json.RawMessage(`{"type":"object","required":["path"]}`)}})
	var meter contextMeter
	meter.observe(10_000, 20, len(messages), estimateCanonicalRequest(messages[:1], oldTools, "model"))
	if got := meter.projected(messages, oldTools, "model"); got != 10_000 {
		t.Fatalf("unchanged overhead counted twice: %d", got)
	}
	want := 10_000 + estimateCanonicalRequest(nil, newTools[1:], "model")
	if got := meter.projected(messages, newTools, "model"); got != want {
		t.Fatalf("tool schema delta not projected: %d want %d", got, want)
	}
}
