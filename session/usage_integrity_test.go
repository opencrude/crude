package session

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"crude/provider"
	"crude/sessiondb"
)

func TestUsageLedgerKeepsRequestsAcrossResume(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":10,\"prompt_tokens_details\":{\"cached_tokens\":20},\"completion_tokens_details\":{\"reasoning_tokens\":3}}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	cfg := modelAttributionConfig(server.URL, "accounting-model")
	home, directory := t.TempDir(), filepath.Join(t.TempDir(), "sessions")
	s, err := NewWithSessionDir("usage-integrity", cfg, home, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	dbPath := filepath.Join(directory, s.SessionID()+".db")
	for cycle := range 3 {
		if cycle > 0 {
			s.Close()
			s, err = ResumeSessionWithDir(dbPath, cfg, home, directory)
			if err != nil {
				t.Fatal(err)
			}
		}
		sender := newMockEventSender()
		orch, err := s.NewOrchestrator(sender)
		if err != nil {
			t.Fatal(err)
		}
		for turn := range 2 {
			if !orch.SubmitInput(provider.PlainInput("Reply ok, without tools.")) {
				t.Fatal("turn admission failed")
			}
			waitTurnDone(t, sender, orch.ID)
			waitAssistantCount(t, s, cycle*2+turn+1)
		}
		records, err := sessiondb.ListUsageRecords(s.db)
		if err != nil {
			t.Fatal(err)
		}
		want := (cycle + 1) * 2
		if len(records) != want {
			t.Fatalf("resume %d overwrote provider requests: ledger=%d want=%d", cycle, len(records), want)
		}
		cost := s.CostSnapshot()
		if cost.TotalPromptTokens != want*100 || cost.TotalCompletionTokens != want*10 || cost.TotalCachedTokens != want*20 {
			t.Fatalf("live totals disagree with completed HTTP requests: %+v", cost)
		}
	}
	records, err := sessiondb.ListUsageRecords(s.db)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.TurnGeneration == 0 || record.ContextUsed != 100 || record.ReasoningTokens != 3 || !record.ReasoningKnown {
			t.Fatalf("request metadata lost between provider and persistence: %+v", record)
		}
	}
}

func TestUsagePreservesUnsignedTurnGeneration(t *testing.T) {
	record, ok := sessiondb.UsageRecordFromEvent(map[string]any{
		"request_id": "request", "turn_generation": uint64(9),
		"prompt": 100, "completion": 10, "context_prompt": 100,
	}, "session", "agent", "", "model")
	if !ok || record.TurnGeneration != 9 {
		t.Fatalf("uint64 generation was discarded: %+v", record)
	}
}

func TestUsageAccountingAllProtocolsAcrossResume(t *testing.T) {
	for _, protocol := range []string{"openai", "anthropic", "ollama", "openai-responses", "codex"} {
		t.Run(protocol, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				switch protocol {
				case "anthropic":
					_, _ = fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":70,\"output_tokens\":1,\"cache_read_input_tokens\":20,\"cache_creation_input_tokens\":10}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":10}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
				case "ollama":
					w.Header().Set("Content-Type", "application/x-ndjson")
					_, _ = fmt.Fprintln(w, `{"done":true,"message":{"role":"assistant","content":"ok"},"prompt_eval_count":100,"eval_count":10,"eval_duration":500000000}`)
				case "openai-responses", "codex":
					_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"response\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}],\"usage\":{\"input_tokens\":100,\"output_tokens\":10,\"input_tokens_details\":{\"cached_tokens\":20},\"output_tokens_details\":{\"reasoning_tokens\":3}}}}\n\n")
				default:
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":10,\"prompt_tokens_details\":{\"cached_tokens\":20},\"completion_tokens_details\":{\"reasoning_tokens\":3}}}\n\ndata: [DONE]\n\n")
				}
			}))
			defer server.Close()
			cfg := modelAttributionConfig(server.URL, "accounting-model")
			pc := cfg.Accounts["account"].Connection
			pc.APIStandard = protocol
			account := cfg.Accounts["account"]
			account.Connection = pc
			cfg.Accounts["account"] = account
			home, directory := t.TempDir(), filepath.Join(t.TempDir(), "sessions")
			s, err := NewWithSessionDir("protocol-usage", cfg, home, directory)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Close() }()
			dbPath := filepath.Join(directory, s.SessionID()+".db")
			for cycle := range 2 {
				if cycle > 0 {
					s.Close()
					s, err = ResumeSessionWithDir(dbPath, cfg, home, directory)
					if err != nil {
						t.Fatal(err)
					}
				}
				sender := newMockEventSender()
				orch, err := s.NewOrchestrator(sender)
				if err != nil {
					t.Fatal(err)
				}
				if !orch.SubmitInput(provider.PlainInput("Reply ok, without tools.")) {
					t.Fatal("turn rejected")
				}
				waitTurnDone(t, sender, orch.ID)
				waitAssistantCount(t, s, cycle+1)
				records, err := sessiondb.ListUsageRecords(s.db)
				if err != nil || len(records) != cycle+1 {
					t.Fatalf("ledger count=%d err=%v", len(records), err)
				}
				for _, r := range records {
					if r.PromptTokens != 100 || r.CompletionTokens != 10 || r.ContextUsed != 100 || r.TurnGeneration == 0 || r.Estimated {
						t.Fatalf("provider counts changed during persistence/resume: %+v", r)
					}
					wantRead := protocol != "ollama"
					wantWrite := protocol == "anthropic"
					wantReasoning := protocol == "openai" || protocol == "openai-responses" || protocol == "codex"
					if r.CacheReadKnown != wantRead || r.CacheWriteKnown != wantWrite || r.ReasoningKnown != wantReasoning {
						t.Fatalf("protocol-specific availability changed: %+v", r)
					}
				}
			}
		})
	}
}
