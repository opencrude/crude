package turnloop

import (
	"bytes"
	"testing"

	"crude/agent/provider"
)

func TestAnsweredSourceMetadataIsLocal(t *testing.T) {
	history := provider.NewTextMessage("user", "Already answered: keep the log.")
	history.Origin, history.Visibility = provider.OriginCheckpoint, provider.VisibilityContextOnly
	request := provider.ChatRequest{Model: "model", Messages: []provider.ChatMessage{provider.NewTextMessage("system", "Continue."), history}, MaxOutputTokens: 256}
	for _, protocol := range []provider.Protocol{&provider.OpenAIStandard{}, &provider.OpenAIResponsesStandard{}, &provider.AnthropicStandard{}, &provider.CodexStandard{}, &provider.GeminiStandard{}, &provider.GeminiInteractionsStandard{}, &provider.OllamaStandard{}} {
		t.Run(protocol.Info().ID, func(t *testing.T) {
			request.Messages[1] = history
			before := provider.EstimateMessagesTotal(request.Messages, request.Model)
			want, err := protocol.EncodeRequest(&request)
			if err != nil {
				t.Fatal(err)
			}
			request.Messages[1].AnsweredRequests = []provider.AnsweredRequest{{SourceID: "local-source-id", Text: "keep the log"}}
			got, err := protocol.EncodeRequest(&request)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) || provider.EstimateMessagesTotal(request.Messages, request.Model) != before {
				t.Error("source metadata changed the request or token estimate")
			}
		})
	}
}

func TestRepeatedAnsweredIdentityDoesNotBecomePending(t *testing.T) {
	p := provider.FromPreset("openai-chat", "key", "http://example.invalid")
	a := newTestAgent(t, p, "model")
	a.CompactionRetentionTokens = 1000
	answered := provider.NewTextMessage("user", "Preserve & check.")
	answered.InternalID = "turn-one"
	history := provider.NewTextMessage("user", "Historical block")
	history.Origin, history.Visibility = provider.OriginCheckpoint, provider.VisibilityContextOnly
	history.AnsweredRequests = []provider.AnsweredRequest{{SourceID: answered.InternalID, Text: answered.ContentString()}}
	again := answered
	again.InternalID = "turn-two"
	source := []provider.ChatMessage{provider.NewTextMessage("system", "System"), history, again, answered}
	checkpoint := provider.NewTextMessage("user", "Checkpoint")
	checkpoint.Origin = provider.OriginCheckpoint
	result, err := a.finishCheckpoint(t.Context(), source, nil, checkpoint, "model", 20000, 0, compactOutcome{Role: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 4 || len(result.Messages[1].AnsweredRequests) != 1 || result.Messages[1].AnsweredRequests[0].SourceID != answered.InternalID || result.Messages[2].InternalID != again.InternalID {
		t.Fatal("the answered identity was lost or replayed, or a distinct repeated prompt was dropped")
	}
}
