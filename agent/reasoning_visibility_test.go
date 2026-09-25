package agent

import (
	"strings"
	"testing"

	"crude/provider"
	"crude/types/reasoning"
)

func TestAgentLearnsFromCompletedFlattenedRequestsWithoutOverridingRaw(t *testing.T) {
	a := NewAgent(nil, "gateway-model", nil)
	for _, tc := range []struct {
		visible, reported int
		want              reasoning.Kind
	}{
		{100, 350, reasoning.Unknown},
		{90, 400, reasoning.InferredSummary},
	} {
		completion := completionResult{ReasoningContent: strings.Repeat("x", tc.visible), ReasoningDisplay: reasoning.Display{Kind: reasoning.Provisional}, Usage: provider.TokenUsage{ReasoningTokens: tc.reported, ReasoningKnown: true}}
		if got := a.observeReasoningVisibility(&completion); got != reasoning.InferredSummary {
			t.Fatalf("settled provisional response as %q", got)
		}
		if a.ReasoningContentKind != tc.want {
			t.Fatalf("inference after %d/%d = %q", tc.visible, tc.reported, a.ReasoningContentKind)
		}
	}
	raw := completionResult{ReasoningDisplay: reasoning.Display{Kind: reasoning.Raw}, ReasoningContent: "raw", Usage: provider.TokenUsage{ReasoningTokens: 300, ReasoningKnown: true}}
	a.observeReasoningVisibility(&raw)
	flattened := completionResult{ReasoningDisplay: reasoning.Display{Kind: reasoning.Provisional}, ReasoningContent: strings.Repeat("x", 40), Usage: provider.TokenUsage{ReasoningTokens: 600, ReasoningKnown: true}}
	a.observeReasoningVisibility(&flattened)
	if a.ReasoningContentKind != reasoning.Unknown {
		t.Fatal("raw provider evidence lost precedence")
	}
}
