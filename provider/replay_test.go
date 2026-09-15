package provider

import (
	"encoding/json"
	"testing"
)

func TestOpaqueReplayRequiresProviderScopeAndExactPrefix(t *testing.T) {
	producer := &Provider{ID: "anthropic", BaseURL: "https://api.anthropic.com", StandardName: "anthropic"}
	other := &Provider{ID: "proxy", BaseURL: "https://proxy.example", StandardName: "anthropic"}
	tools := []ToolDef{{Type: "function", Function: FunctionDef{Name: "read_file"}}}
	prefix := []ChatMessage{NewTextMessage("system", "stable"), NewTextMessage("user", "inspect")}
	item := ProviderItem{Standard: "anthropic", Type: "redacted_thinking", Data: json.RawMessage(`{"type":"redacted_thinking","data":"opaque"}`)}
	stamped := StampProviderItems([]ProviderItem{item}, producer, "claude-a", RequestPrefixHash(prefix, tools))
	assistant := NewTextMessage("assistant", "visible")
	assistant.ProviderItems = stamped
	assistant.ReasoningContent = "provider reasoning summary"
	assistant.Reasoning = assistant.ReasoningContent
	assistant.ReasoningDetails = []json.RawMessage{json.RawMessage(`{"type":"signed"}`)}
	assistant.ReplayScope, assistant.ReplaySourceModel, assistant.ReplayPrefixHash = producer.ReplayScope(), "claude-a", RequestPrefixHash(prefix, tools)
	history := append(append([]ChatMessage(nil), prefix...), assistant)

	same := FilterReplayMessages(history, tools, producer)
	if len(same[len(same)-1].ProviderItems) != 1 || string(same[len(same)-1].ProviderItems[0].Data) != string(item.Data) {
		t.Fatalf("same-provider replay=%+v", same[len(same)-1].ProviderItems)
	}
	if got := FilterReplayMessages(history, tools, other); len(got[len(got)-1].ProviderItems) != 0 || got[len(got)-1].ReasoningContent != "" || len(got[len(got)-1].ReasoningDetails) != 0 {
		t.Fatalf("opaque state crossed provider endpoint: %+v", got[len(got)-1])
	}
	if history[len(history)-1].ReasoningContent == "" || len(history[len(history)-1].ProviderItems) != 1 {
		t.Fatal("request filtering mutated the immutable transcript")
	}
	changedPrefix := append([]ChatMessage(nil), history...)
	changedPrefix[1] = NewTextMessage("user", "changed")
	if got := FilterReplayMessages(changedPrefix, tools, producer); len(got[len(got)-1].ProviderItems) != 0 {
		t.Fatalf("opaque state survived changed prefix: %+v", got[len(got)-1].ProviderItems)
	}
}

func TestOpaqueReplayLetsProviderDecideModelCompatibility(t *testing.T) {
	providerConfig := &Provider{ID: "openai", BaseURL: "https://api.openai.com/v1", StandardName: "openai-responses"}
	prefix := []ChatMessage{NewTextMessage("user", "work")}
	item := ProviderItem{Standard: "openai-responses", Type: "reasoning", Data: json.RawMessage(`{"type":"reasoning","encrypted_content":"ciphertext"}`)}
	stamped := StampProviderItems([]ProviderItem{item}, providerConfig, "gpt-family-a", RequestPrefixHash(prefix, nil))
	assistant := NewTextMessage("assistant", "done")
	assistant.ProviderItems = stamped
	assistant.ReplayScope, assistant.ReplaySourceModel, assistant.ReplayPrefixHash = providerConfig.ReplayScope(), "gpt-family-a", RequestPrefixHash(prefix, nil)
	history := append(prefix, assistant)
	filtered := FilterReplayMessages(history, nil, providerConfig)
	if len(filtered[1].ProviderItems) != 1 || filtered[1].ProviderItems[0].SourceModel != "gpt-family-a" {
		t.Fatalf("same-provider model switch dropped provider-governed state: %+v", filtered[1].ProviderItems)
	}
}

func BenchmarkFilterReplayMessagesDense(b *testing.B) {
	providerConfig := &Provider{ID: "openai", BaseURL: "https://api.openai.com/v1", StandardName: "openai-responses"}
	tools := []ToolDef{{Type: "function", Function: FunctionDef{Name: "read_file"}}}
	history := make([]ChatMessage, 0, 1000)
	hasher := newPrefixHasher(tools)
	for index := 0; index < 1000; index++ {
		message := NewTextMessage("assistant", "stable visible response")
		prefix := hasher.value()
		message.ProviderItems = StampProviderItems([]ProviderItem{{Standard: "openai-responses", Type: "reasoning", Data: json.RawMessage(`{"type":"reasoning","encrypted_content":"opaque"}`)}}, providerConfig, "gpt-family", prefix)
		message.ReplayScope, message.ReplaySourceModel, message.ReplayPrefixHash = providerConfig.ReplayScope(), "gpt-family", prefix
		history = append(history, message)
		hasher.add(message)
	}
	b.ResetTimer()
	for range b.N {
		_ = FilterReplayMessages(history, tools, providerConfig)
	}
}
