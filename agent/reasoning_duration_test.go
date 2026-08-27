package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"crude/provider"
)

type durationStreamDecoder struct{}

func (durationStreamDecoder) Decode(event provider.StreamEvent) (provider.StreamUpdate, error) {
	switch string(event.Data) {
	case "reasoning":
		return provider.StreamUpdate{ReasoningDelta: "thinking"}, nil
	case "done":
		time.Sleep(2 * time.Millisecond)
		return provider.StreamUpdate{Done: true, FinishReason: "stop"}, nil
	default:
		return provider.StreamUpdate{}, nil
	}
}

func TestConsumeSSERecordsReasoningDuration(t *testing.T) {
	a := &Agent{}
	result, err := a.consumeSSE(
		context.Background(),
		strings.NewReader("data: reasoning\n\ndata: done\n\n"),
		durationStreamDecoder{}, nil, nil, nil, nil, time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.ReasoningContent != "thinking" || result.ReasoningDuration <= 0 {
		t.Fatalf("completion result = %+v", result)
	}
}
