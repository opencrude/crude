package session

import (
	"strings"
	"testing"

	"crude/provider"
	"crude/sessiondb"
	"crude/types/reasoning"
)

func TestInferredReasoningKindUsesMatchingGatewayObservations(t *testing.T) {
	s := &Session{usageRecords: map[string]sessiondb.UsageRecord{}}
	for i, evidence := range []struct {
		generation     uint64
		length, billed int
	}{
		{1, 249, 736}, {2, 317, 1002}, {3, 390, 1309},
	} {
		s.msgs = append(s.msgs, provider.ChatMessage{Role: "assistant", ReasoningContent: strings.Repeat("x", evidence.length)})
		s.displayMetas = append(s.displayMetas, sessiondb.DisplayMeta{TurnGeneration: evidence.generation, ProviderID: "custom", AccountID: "account", ModelID: "gateway-model"})
		s.usageRecords[string(rune('a'+i))] = sessiondb.UsageRecord{AgentID: "owner", ProviderID: "custom", AccountID: "account", ModelID: "gateway-model", RequestKind: "normal", TurnGeneration: evidence.generation, ReasoningKnown: true, ReasoningTokens: evidence.billed}
	}
	for _, tc := range []struct {
		agent, provider, account, model string
		want                            reasoning.Kind
	}{
		{"owner", "custom", "account", "gateway-model", reasoning.InferredSummary},
		{"other", "custom", "account", "gateway-model", reasoning.Unknown},
		{"owner", "other", "account", "gateway-model", reasoning.Unknown},
		{"owner", "custom", "other", "gateway-model", reasoning.Unknown},
		{"owner", "custom", "account", "other-model", reasoning.Unknown},
	} {
		if got := s.inferredReasoningKind(tc.agent, tc.provider, tc.account, tc.model); got != tc.want {
			t.Fatalf("connection %+v: %q want %q", tc, got, tc.want)
		}
	}
	s.msgs[0].ReasoningDisplay.Kind = reasoning.InferredSummary
	if got := s.inferredReasoningKind("owner", "custom", "account", "gateway-model"); got != reasoning.InferredSummary {
		t.Fatalf("resumed measured summary lost its source: %q", got)
	}
	s.msgs[0].ReasoningDisplay.Kind = reasoning.Raw
	if got := s.inferredReasoningKind("owner", "custom", "account", "gateway-model"); got != reasoning.Unknown {
		t.Fatal("raw wire evidence lost precedence")
	}
	s.msgs[0].ReasoningDisplay.Kind = reasoning.Unknown
	s.usageRecords["a"] = sessiondb.UsageRecord{AgentID: "owner", ProviderID: "custom", AccountID: "account", ModelID: "gateway-model", RequestKind: "normal", TurnGeneration: 1, ReasoningKnown: true, ReasoningTokens: 200, Partial: true}
	if got := s.inferredReasoningKind("owner", "custom", "account", "gateway-model"); got != reasoning.InferredSummary {
		t.Fatal("partial record incorrectly contradicted completed evidence")
	}
}
