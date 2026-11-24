package provider

import (
	"encoding/json"
	"testing"
)

func TestOpaqueReplayRequiresProviderScopeAndExactPrefix(t *testing.T) {
	producer := &Provider{ID: "anthropic", BaseURL: "https://api.anthropic.com", StandardName: "anthropic"}
	other := &Provider{ID: "other-provider", BaseURL: "https://other.example", StandardName: "anthropic"}

	prefix := []ChatMessage{NewTextMessage("system", "stable"), NewTextMessage("user", "inspect")}
	item := Item{Standard: "anthropic", Type: "redacted_thinking", Data: json.RawMessage(`{"type":"redacted_thinking","data":"opaque"}`)}
	stamped := StampProviderItems([]Item{item}, producer, "claude-a", MessagePrefixHash(prefix))
	assistant := NewTextMessage("assistant", "visible")
	assistant.ProviderItems = stamped
	assistant.ReasoningContent = "provider reasoning summary"
	assistant.Reasoning = assistant.ReasoningContent
	assistant.ReasoningDetails = []json.RawMessage{json.RawMessage(`{"type":"signed"}`)}
	assistant.ReplayScope, assistant.ReplaySourceModel, assistant.ReplayPrefixHash = producer.ReplayScope(), "claude-a", MessagePrefixHash(prefix)
	history := append(append([]ChatMessage(nil), prefix...), assistant)

	same := FilterReplayMessages(history, producer)
	if len(same[len(same)-1].ProviderItems) != 1 || string(same[len(same)-1].ProviderItems[0].Data) != string(item.Data) {
		t.Fatalf("same-provider replay=%+v", same[len(same)-1].ProviderItems)
	}
	if got := FilterReplayMessages(history, other); len(got[len(got)-1].ProviderItems) != 0 || got[len(got)-1].ReasoningContent != "" || len(got[len(got)-1].ReasoningDetails) != 0 {
		t.Fatalf("opaque state crossed provider endpoint: %+v", got[len(got)-1])
	}
	if history[len(history)-1].ReasoningContent == "" || len(history[len(history)-1].ProviderItems) != 1 {
		t.Fatal("request filtering mutated the immutable transcript")
	}
	changedPrefix := append([]ChatMessage(nil), history...)
	changedPrefix[1] = NewTextMessage("user", "changed")
	if got := FilterReplayMessages(changedPrefix, producer); len(got[len(got)-1].ProviderItems) != 0 {
		t.Fatalf("opaque state survived changed prefix: %+v", got[len(got)-1].ProviderItems)
	}
}

func TestOpaqueReplayLetsProviderDecideModelCompatibility(t *testing.T) {
	providerConfig := &Provider{ID: "openai", BaseURL: "https://api.openai.com/v1", StandardName: "openai-responses"}
	prefix := []ChatMessage{NewTextMessage("user", "work")}
	item := Item{Standard: "openai-responses", Type: "reasoning", Data: json.RawMessage(`{"type":"reasoning","encrypted_content":"ciphertext"}`)}
	stamped := StampProviderItems([]Item{item}, providerConfig, "gpt-family-a", MessagePrefixHash(prefix))
	assistant := NewTextMessage("assistant", "done")
	assistant.ProviderItems = stamped
	assistant.ReplayScope, assistant.ReplaySourceModel, assistant.ReplayPrefixHash = providerConfig.ReplayScope(), "gpt-family-a", MessagePrefixHash(prefix)
	history := append(prefix, assistant)
	filtered := FilterReplayMessages(history, providerConfig)
	if len(filtered[1].ProviderItems) != 1 || filtered[1].ProviderItems[0].SourceModel != "gpt-family-a" {
		t.Fatalf("same-provider model switch dropped provider-governed state: %+v", filtered[1].ProviderItems)
	}
}

func TestNewConnectionReplayWarmsAfterColdTransition(t *testing.T) {

	for _, boundary := range []string{"protocol", "endpoint", "system"} {
		t.Run(boundary, func(t *testing.T) {
			old := &Provider{ID: "provider", BaseURL: "https://old.test/v1", StandardName: "anthropic"}
			current := Provider{ID: old.ID, BaseURL: old.BaseURL, StandardName: old.StandardName}
			prefix := []ChatMessage{NewTextMessage("system", "stable"), NewTextMessage("user", "work")}
			prior := NewTextMessage("assistant", "old result")
			prior.ProviderItems = StampProviderItems([]Item{{Standard: "anthropic", Type: "thinking", Data: json.RawMessage(`{"type":"thinking","thinking":"trace","signature":"old-signature"}`)}}, old, "model", MessagePrefixHash(prefix))
			prior.ReplayScope, prior.ReplayPrefixHash = old.ReplayScope(), MessagePrefixHash(prefix)
			history := append(prefix, prior, NewTextMessage("user", "next"))
			switch boundary {
			case "protocol":
				current.StandardName = "openai-responses"
			case "endpoint":
				current.BaseURL = "https://new.test/v1"
			case "system":
				history[0] = NewTextMessage("system", "new policy")
			}
			projected := FilterReplayMessages(history, &current)
			if len(projected[2].ProviderItems) != 0 {
				t.Fatal("transition retained invalid old replay")
			}
			fresh := NewTextMessage("assistant", "new result")
			fresh.ReplayScope, fresh.ReplayPrefixHash = current.ReplayScope(), MessagePrefixHash(projected)
			fresh.ProviderItems = StampProviderItems([]Item{{Standard: current.ProtocolID(), Type: "reasoning", Data: json.RawMessage(`{"type":"reasoning","encrypted_content":"new-valid-replay"}`)}}, &current, "model", fresh.ReplayPrefixHash)
			history = append(history, fresh, NewTextMessage("user", "steady request"))
			warm := FilterReplayMessages(history, &current)
			if len(warm[4].ProviderItems) != 1 || string(warm[4].ProviderItems[0].Data) != string(fresh.ProviderItems[0].Data) {
				t.Fatal("stable new connection never warmed its own replay prefix after the transition")
			}
			if len(history[2].ProviderItems) != 1 || len(history[4].ProviderItems) != 1 {
				t.Fatal("projection changed canonical producer records")
			}
		})
	}
}

func BenchmarkFilterReplayMessagesDense(b *testing.B) {
	providerConfig := &Provider{ID: "openai", BaseURL: "https://api.openai.com/v1", StandardName: "openai-responses"}

	history := make([]ChatMessage, 0, 1000)
	hasher := newPrefixHasher()
	for index := 0; index < 1000; index++ {
		message := NewTextMessage("assistant", "stable visible response")
		prefix := hasher.value()
		message.ProviderItems = StampProviderItems([]Item{{Standard: "openai-responses", Type: "reasoning", Data: json.RawMessage(`{"type":"reasoning","encrypted_content":"opaque"}`)}}, providerConfig, "gpt-family", prefix)
		message.ReplayScope, message.ReplaySourceModel, message.ReplayPrefixHash = providerConfig.ReplayScope(), "gpt-family", prefix
		history = append(history, message)
		hasher.add(message)
	}
	b.ResetTimer()
	for range b.N {
		_ = FilterReplayMessages(history, providerConfig)
	}
}
