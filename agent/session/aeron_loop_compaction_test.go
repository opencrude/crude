package session

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent"
	"crude/agent/provider"
	"crude/agent/sessiondb"
)

// When the workspace Errand's agentic loop ends (the model answers without
// another tool call), Crude compacts its context once. Any other root keeps
// its context, a loop that called tools compacts only after its answer, and a
// loop too small to shrink is left alone instead of failing visibly.
func TestAeronCompactsWhenItsAgenticLoopEnds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		aeron bool
		lines int
	}{{"aeron", true, 3000}, {"main agent", false, 3000}, {"small aeron loop", true, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			var turns, extractions atomic.Int32
			var compactedBeforeAnswer atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct{ Messages []provider.ChatMessage }
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if len(body.Messages) > 0 && strings.Contains(body.Messages[len(body.Messages)-1].ContentString(), "<<<intent>>>") {
					extractions.Add(1)
					encoded, _ := json.Marshal("<<<intent>>>\n- inspect the workspace\n<<<work>>>\n- read notes.txt\n<<<state>>>\n- clean\n<<<continuation>>>\n- None")
					_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", encoded)
					return
				}
				if turns.Add(1) == 1 {
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"notes.txt\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				if extractions.Load() != 0 {
					compactedBeforeAnswer.Store(true)
				}
				_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Checked.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()
			cfg := modelAttributionConfig(server.URL, "aeron-model")
			profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
			profile.Categories[CategoryAeron] = CategoryConfig{AccountID: "account", Model: "aeron-model"}
			cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
			provider.SetModelContext("aeron-model", 200_000)
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte(strings.Repeat("workspace notes line\n", tc.lines)), 0o600); err != nil {
				t.Fatal(err)
			}
			sess, err := NewWithSessionDir("aeron-loop", cfg, t.TempDir(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer sess.Close()
			sess.SetWorkDir(root)
			if tc.aeron {
				sess.mu.Lock()
				sess.selectedMainAgent = CategoryAeron
				sess.mu.Unlock()
			}
			sender := newMockEventSender()
			o, err := sess.NewOrchestrator(sender)
			if err != nil {
				t.Fatal(err)
			}
			if !o.SubmitInput(provider.PlainInput("Check the workspace notes")) {
				t.Fatal("input not admitted")
			}
			waitTurnDone(t, sender, o.ID)
			deadline := time.Now().Add(10 * time.Second)
			var records []sessiondb.CompactionRecord
			for time.Now().Before(deadline) {
				records = sess.CompactionRecords(o.ID)
				if !tc.aeron || tc.lines == 1 || len(records) == 1 && records[0].State == sessiondb.CompactionCompleted {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !tc.aeron || tc.lines == 1 {
				time.Sleep(200 * time.Millisecond)
				if extractions.Load() != 0 || len(sess.CompactionRecords(o.ID)) != 0 {
					t.Fatal("this loop must keep its context")
				}
				return
			}
			if len(records) != 1 || records[0].State != sessiondb.CompactionCompleted || records[0].Trigger != agent.CompactionTriggerLoopEnd {
				t.Fatalf("loop end compaction records = %+v", records)
			}
			if extractions.Load() != 1 || turns.Load() != 2 || compactedBeforeAnswer.Load() {
				t.Fatalf("compaction ran %d times over %d model turns (before answer: %v)", extractions.Load(), turns.Load(), compactedBeforeAnswer.Load())
			}
			sess.msgsMu.Lock()
			reason := sess.contextReason
			sess.msgsMu.Unlock()
			if reason != agent.CompactionTriggerLoopEnd+"_compaction" {
				t.Fatalf("active context was not replaced by the checkpoint: %q", reason)
			}
		})
	}
}
