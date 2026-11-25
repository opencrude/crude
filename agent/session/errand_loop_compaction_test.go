package session

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/sessiondb"
	"crude/agent/turnloop"
	"crude/internal/providersim"
)

// When the workspace Errand's agentic loop ends (the model answers without
// another tool call), Crude compacts its context once. Any other root keeps
// its context, a loop that called tools compacts only after its answer, and a
// loop too small to shrink is left alone instead of failing visibly.
func TestErrandCompactsWhenItsAgenticLoopEnds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		errand bool
		lines  int
	}{{"errand", true, 3000}, {"main agent", false, 3000}, {"small errand loop", true, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			var turns, extractions atomic.Int32
			var compactedBeforeAnswer atomic.Bool
			server := providersim.New(t)
			server.Respond(func(req providersim.Request) providersim.Turn {
				if len(req.Messages) > 0 && strings.Contains(simText(req.Messages[len(req.Messages)-1].Parts), "<<<intent>>>") {
					extractions.Add(1)
					return providersim.Text("<<<intent>>>\n- inspect the workspace\n<<<work>>>\n- read notes.txt\n<<<state>>>\n- clean\n<<<continuation>>>\n- None").WithoutUsage()
				}
				if turns.Add(1) == 1 {
					return providersim.CallTools(providersim.ToolCall{ID: "call1", Name: "read_file", Arguments: `{"path":"notes.txt"}`}).WithoutUsage()
				}
				if extractions.Load() != 0 {
					compactedBeforeAnswer.Store(true)
				}
				return providersim.Text("Checked.").WithoutUsage()
			})
			cfg := modelAttributionConfig(server.BaseURL(providersim.Chat), "errand-model")
			profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
			profile.Categories[CategoryErrand] = CategoryConfig{AccountID: "account", Model: "errand-model"}
			cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
			provider.SetModelContext("errand-model", 200_000)
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte(strings.Repeat("workspace notes line\n", tc.lines)), 0o600); err != nil {
				t.Fatal(err)
			}
			sess, err := NewWithSessionDir("errand-loop", cfg, t.TempDir(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer sess.Close()
			sess.SetWorkDir(root)
			if tc.errand {
				sess.mu.Lock()
				sess.selectedMainAgent = CategoryErrand
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
				if !tc.errand || tc.lines == 1 || len(records) == 1 && records[0].State == sessiondb.CompactionCompleted {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !tc.errand || tc.lines == 1 {
				time.Sleep(200 * time.Millisecond)
				if extractions.Load() != 0 || len(sess.CompactionRecords(o.ID)) != 0 {
					t.Fatal("this loop must keep its context")
				}
				return
			}
			if len(records) != 1 || records[0].State != sessiondb.CompactionCompleted || records[0].Trigger != turnloop.CompactionTriggerLoopEnd {
				t.Fatalf("loop end compaction records = %+v", records)
			}
			if extractions.Load() != 1 || turns.Load() != 2 || compactedBeforeAnswer.Load() {
				t.Fatalf("compaction ran %d times over %d model turns (before answer: %v)", extractions.Load(), turns.Load(), compactedBeforeAnswer.Load())
			}
			sess.msgsMu.Lock()
			reason := sess.contextReason
			sess.msgsMu.Unlock()
			if reason != turnloop.CompactionTriggerLoopEnd+"_compaction" {
				t.Fatalf("active context was not replaced by the checkpoint: %q", reason)
			}
		})
	}
}
