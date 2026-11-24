package session

import (
	"bytes"
	"crude/agent/provider"
	"crude/agent/sessiondb"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Exercise the session operation, not just a codec: no Errand binding, exact
// archival, durable portable text plus opaque item, attribution and passive resume.
func TestManualNativeCheckpointWithoutErrand(t *testing.T) {
	var real, follow atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(w, `{"id":"adaptive-main-model","capabilities":{"compaction":{"supported":true,"summarize":{"supported":true}}}}`)
			return
		}
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		if body["model"] != "adaptive-main-model" || r.URL.Path != "/v1/messages" {
			t.Error("used another model or endpoint")
		}
		if body["compaction"] != nil {
			real.Add(1)
			if !bytes.Contains(data, []byte("build-evidence-27")) {
				t.Error("native operation missed the source")
			}
			fmt.Fprint(w, `{"type":"message","role":"assistant","stop_reason":"compaction","content":[{"type":"compaction","signature":"signed-block","content":"build-evidence-27"}],"usage":{"iterations":[{"input_tokens":11000,"output_tokens":200}]}}`)
			return
		}
		follow.Add(1)
		if !bytes.Contains(data, []byte("signed-block")) || bytes.Contains(data, []byte("raw tool body")) {
			t.Error("verification did not use the signed block")
		}
		fmt.Fprint(w, `{"type":"message","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"<<<intent>>>\nFix the build\n<<<work>>>\nRead the test\n<<<state>>>\nbuild-evidence-27\n<<<continuation>>>\nRun the focused test"}],"usage":{"input_tokens":299,"output_tokens":60}}`)
	}))
	defer server.Close()
	history := []provider.ChatMessage{provider.NewTextMessage("assistant", strings.Repeat("raw tool body ", 3000)+"build-evidence-27"), provider.NewTextMessage("user", "Fix the build")}
	s, path := newAdaptiveCompactionSession(t, server.URL, 20000, history)
	account := s.cfg.Accounts["account"]
	account.Connection.APIStandard = "anthropic"
	account.Connection.Protocol = &provider.ProtocolSettings{Compaction: provider.CompactionProviderOnly}
	s.cfg.Accounts["account"] = account
	profile := s.cfg.Agents.Profiles[s.cfg.Agents.ActiveProfile]
	profile.Categories[CategoryErrand] = CategoryConfig{AccountID: "deleted", Model: "removed"}
	s.cfg.Agents.Profiles[s.cfg.Agents.ActiveProfile] = profile
	r := s.HandleCompact()
	if r.Error != "" || real.Load() != 1 || follow.Load() != 1 {
		t.Fatalf("complete=%+v calls=%d/%d", r, real.Load(), follow.Load())
	}
	records := s.CompactionRecords(s.sessionID)
	if len(records) != 1 || records[0].ModelID != "adaptive-main-model" || records[0].State != sessiondb.CompactionCompleted {
		t.Fatalf("wrong attribution: %+v", records)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sessiondb.OpenAndMigrate(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	state, ok, err := sessiondb.LoadContextState(db, s.sessionID, s.sessionID)
	if err != nil || !ok || len(state.Messages) < 3 || len(state.Messages[0].ProviderItems) != 1 || !strings.Contains(state.Messages[0].ContentString(), "build-evidence-27") || state.Messages[len(state.Messages)-1].Origin != provider.OriginRuntimeContext {
		t.Fatalf("passive resume lost usable checkpoint: %v", err)
	}
	var source []byte
	if err := db.QueryRow(`SELECT messages_json FROM agent_context_generations WHERE generation=7`).Scan(&source); err != nil || !bytes.Contains(source, []byte("raw tool body")) {
		t.Fatal("source not archived")
	}
}
