package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"crude/agent/provider"
)

// A human request compacts before the next model request even when
// automatic compaction is off and the context is far from any threshold.
func TestRunHonoursHumanCompactionRequestWithAutomaticCompactionOff(t *testing.T) {
	var requests atomic.Int32
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/x-ndjson")
		if requests.Add(1) <= int32(len(compactionStagesForTest)) {
			encoded, _ := json.Marshal("- compact stage: preserved facts")
			_, _ = fmt.Fprintf(w, `{"model":"llama3","message":{"role":"assistant","content":%s},"done":true,"done_reason":"stop","prompt_eval_count":1000,"eval_count":10}`+"\n", encoded)
			return
		}
		_, _ = fmt.Fprint(w, `{"model":"llama3","message":{"role":"assistant","content":"done"},"done":true,"done_reason":"stop","prompt_eval_count":1000,"eval_count":1}`+"\n")
	}))
	defer server.Close()
	p := provider.FromPreset("ollama-cloud", "ollama-key", server.URL)
	p.Model = "compaction-request-model"
	provider.SetModelContext(p.Model, 1_000_000)
	a := newTestAgent(t, p, p.Model)
	a.ContextCompactionPercent = CompactionOff
	taken := 0
	a.CompactionRequest = func() string {
		taken++
		if taken == 1 {
			return "human-request"
		}
		return ""
	}
	var history []provider.ChatMessage
	for i := range 6 {
		history = append(history,
			provider.NewTextMessage("user", fmt.Sprintf("request %d %s", i, strings.Repeat("context ", 400))),
			provider.NewTextMessage("assistant", fmt.Sprintf("answer %d %s", i, strings.Repeat("analysis ", 400))))
	}
	history = append(history, provider.NewTextMessage("user", "current instruction must survive"))
	var compacted map[string]any
	a.Run(context.Background(), history, "system", func(event AgentEvent) {
		if event.Type == "context_compacted" {
			compacted, _ = event.Data.(map[string]any)
		}
	}, nil, "", nil)
	if compacted == nil || compacted["trigger"] != "requested" || compacted["request_id"] != "human-request" {
		t.Fatalf("requested compaction did not run: %+v (requests=%d)", compacted, requests.Load())
	}
	final := string(bodies[len(bodies)-1])
	if !strings.Contains(final, "context_checkpoint") || strings.Contains(final, "request 0 ") || !strings.Contains(final, "current instruction must survive") {
		t.Fatal("the next model request did not use the compacted context")
	}
}

// A compaction choice the user makes while a run works applies at the next
// step. The run starts with automatic compaction off; the owner's current
// settings ask for 5%, so the step compacts and adopts the other passive
// settings without waiting for another run.
func TestRunAppliesLiveSettingsBeforeEachStep(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/x-ndjson")
		if requests.Add(1) <= int32(len(compactionStagesForTest)) {
			encoded, _ := json.Marshal("- compact stage: preserved facts")
			_, _ = fmt.Fprintf(w, `{"model":"llama3","message":{"role":"assistant","content":%s},"done":true,"done_reason":"stop","prompt_eval_count":1000,"eval_count":10}`+"\n", encoded)
			return
		}
		_, _ = fmt.Fprint(w, `{"model":"llama3","message":{"role":"assistant","content":"done"},"done":true,"done_reason":"stop","prompt_eval_count":1000,"eval_count":1}`+"\n")
	}))
	defer server.Close()
	p := provider.FromPreset("ollama-cloud", "ollama-key", server.URL)
	p.Model = "live-settings-model"
	provider.SetModelContext(p.Model, 100_000)
	a := newTestAgent(t, p, p.Model)
	a.ContextCompactionPercent = CompactionOff
	reads := 0
	a.LiveSettings = func() LiveSettings {
		reads++
		return LiveSettings{CompactionPercent: 5, RetentionTokens: a.CompactionRetentionTokens, MaxOutputTokens: 4096,
			CompactionProvider: a.CompactionProvider, CompactionMaxOutput: a.CompactionMaxOutput}
	}
	var history []provider.ChatMessage
	for i := range 6 {
		history = append(history,
			provider.NewTextMessage("user", fmt.Sprintf("request %d %s", i, strings.Repeat("context ", 1200))),
			provider.NewTextMessage("assistant", fmt.Sprintf("answer %d %s", i, strings.Repeat("analysis ", 1200))))
	}
	history = append(history, provider.NewTextMessage("user", "current instruction must survive"))
	var compacted map[string]any
	a.Run(context.Background(), history, "system", func(event AgentEvent) {
		if event.Type == "context_compacted" {
			compacted, _ = event.Data.(map[string]any)
		}
	}, nil, "", nil)
	if reads == 0 || a.ContextCompactionPercent != 5 || a.MaxOutputTokens != 4096 {
		t.Fatalf("live settings not adopted: reads=%d percent=%d max_output=%d", reads, a.ContextCompactionPercent, a.MaxOutputTokens)
	}
	if compacted == nil || compacted["trigger"] != "automatic" {
		t.Fatalf("the live 5%% choice did not compact: %+v (requests=%d)", compacted, requests.Load())
	}
}
