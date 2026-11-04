package session

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
)

// A prepared input (a skill invocation, Aeron's work order) shows its
// DisplayText in the transcript while the model receives the full Text. The
// saved transcript row once took the full Text, so Aeron's chat showed the raw
// XML work order.
func TestPreparedInputTranscriptShowsDisplayTextAndModelGetsText(t *testing.T) {
	requests := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		raw, _ := json.Marshal(body["messages"])
		requests <- string(raw)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	s, err := NewWithSessionDir("display-text", modelAttributionConfig(server.URL, "display-text"), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(t.TempDir())
	o, err := s.NewOrchestrator(newMockEventSender())
	if err != nil {
		t.Fatal(err)
	}
	input := provider.UserInput{InternalID: "order", Origin: provider.OriginHuman,
		Text: "<tool_setup_task><goal>full order</goal></tool_setup_task>", DisplayText: "Set up 1 code intelligence tool: marksman."}
	intent, err := s.PrepareInputAdmission(o.ID, input, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result := s.CommitInputAdmission(t.Context(), intent); result.Status != InputAdmissionAccepted {
		t.Fatalf("input was not accepted: %+v", result)
	}
	select {
	case sent := <-requests:
		if !strings.Contains(sent, "full order") || strings.Contains(sent, "marksman") {
			t.Fatalf("the model did not receive the full order: %s", sent)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("provider request did not arrive")
	}
	rows, err := sessiondb.LoadMessagesWithMeta(s.db)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Msg.InternalID != input.InternalID {
			continue
		}
		if len(row.Meta.Snapshots) != 1 || row.Meta.Snapshots[0].Content != input.DisplayText {
			t.Fatalf("transcript row does not show the display text: %+v", row.Meta.Snapshots)
		}
		return
	}
	t.Fatalf("the input was not saved: %+v", rows)
}
