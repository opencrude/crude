package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"crude/agent/memory"
	"crude/agent/provider"
	"crude/types/setting"
)

// Exercise real session admission, two tool rounds, both account transports,
// hidden context checkpointing and resume. Neither server can inject prose.
func TestRankedMemoryReachesNextRequestAndResumes(t *testing.T) {
	t.Run("ranked", func(t *testing.T) { testRankingJourney(t, false) })
	t.Run("decision service unavailable", func(t *testing.T) { testRankingJourney(t, true) })
}

func testRankingJourney(t *testing.T, unavailable bool) {
	const keep = "Use the calibrated spindle fixture."
	const drop = "Use the retired spindle fixture."
	var chats, ranks atomic.Int32
	decision := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/decisions" || req.Header.Get("Authorization") != "Bearer rank-only" {
			t.Errorf("incorrect decision route or credential")
		}
		var body struct {
			Model, Input string
			Questions    []struct{ Type, Name string }
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		var state actionMemoryState
		if err := json.Unmarshal([]byte(body.Input), &state); err != nil {
			t.Error(err)
			return
		}
		if body.Model != "ranker" || !strings.Contains(strings.Join(state.Actions, " "), "spindle") || strings.Contains(body.Input, "private-pass") || chats.Load() != 2 {
			t.Errorf("unexpected decision input or cadence: %+v round %d", state, chats.Load())
		}
		ranks.Add(1)
		if unavailable {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var answers []any
		for key, v := range state.Candidates {
			p := 0.7
			if strings.Contains(v, keep) {
				p = 0.98
			}
			answers = append(answers, map[string]any{"type": "predicate", "name": key, "probability": p})
		}
		if len(answers) != 2 {
			t.Errorf("wanted both candidates, got %d", len(answers))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers})
	}))
	defer decision.Close()
	chat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct{ Messages []provider.ChatMessage }
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		n := chats.Add(1)
		var copies int
		for _, m := range body.Messages {
			text := m.ContentString()
			if strings.Contains(text, keep) {
				copies++
			}
			if strings.Contains(text, drop) {
				t.Error("lower-ranked memory leaked into chat")
			}
		}
		want := 0
		if n >= 3 && !unavailable {
			want = 1
		}
		if copies != want {
			t.Errorf("request %d has %d ranked blocks, want %d", n, copies, want)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if n <= 2 {
			target := "ordinary.txt"
			if n == 2 {
				target = "spindle.txt"
			}
			args, _ := json.Marshal(map[string]string{"path": target})
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call%d\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", n, string(args))
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Complete.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer chat.Close()
	cfg := modelAttributionConfig(chat.URL, "coding")
	acc := cfg.Accounts["account"]
	acc.ID = "decision"
	acc.APIKey = "rank-only"
	acc.Connection.ProviderURL = decision.URL
	cfg.Accounts[acc.ID] = acc
	cfg.Memory = setting.Memory{AutoExtract: "off", Consolidation: "off", Ranker: setting.MemoryRanker{Enabled: "on", AccountID: acc.ID, Model: "ranker", ModelType: setting.ModelTypeDecision, EveryActions: 2, TopK: 1}}
	home, dir, root := t.TempDir(), t.TempDir(), t.TempDir()
	for _, name := range []string{"ordinary.txt", "spindle.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("password=private-pass"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sess, err := NewWithSessionDir("rank-journey", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetWorkDir(root)
	store := newTestMemoryStore(t)
	sess.SetMemoryStore(store)
	var best memory.Record
	for _, text := range []string{keep, drop} {
		rec, err := store.Remember(t.Context(), memory.Record{Title: "spindle fixture", Content: text, ScopePaths: []string{"spindle.txt"}})
		if err != nil {
			t.Fatal(err)
		}
		if text == keep {
			best = rec
		}
	}
	sender := newMockEventSender()
	o, err := sess.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !o.SubmitInput(provider.PlainInput("Investigate")) {
		t.Fatal("input not admitted")
	}
	waitTurnDone(t, sender, o.ID)
	if chats.Load() != 3 || ranks.Load() != 1 {
		t.Fatalf("wrong request counts: %d chat, %d rank", chats.Load(), ranks.Load())
	}
	seen, err := store.ExposuresForMemory(t.Context(), best.ID, 4)
	if err != nil {
		t.Fatal(err)
	}
	if unavailable {
		if len(seen) != 0 {
			t.Fatal("provider failure recorded an exposure", seen)
		}
		return
	}
	if len(seen) != 1 || seen[0].Kind != "ranked" {
		t.Fatal(seen, err)
	}
	id := sess.SessionID()
	sess.Close()
	resumed, err := ResumeSessionWithDir(filepath.Join(dir, id+".db"), cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	for _, m := range resumed.activeContext {
		if m.Origin == provider.OriginRuntimeContext && m.Visibility == provider.VisibilityContextOnly && strings.Contains(m.ContentString(), keep) {
			return
		}
	}
	t.Fatal("resume lost ranked context")
}

func TestRankedMemoryAbstentionDisableAndRevisionFence(t *testing.T) {
	for _, tc := range []string{"abstain", "changed", "disabled", "budget", "duplicate", "cancelled", "reconfigured", "feedback"} {
		t.Run(tc, func(t *testing.T) {
			store := newTestMemoryStore(t)
			rec, err := store.Remember(t.Context(), memory.Record{Title: "spindle", Content: "Check runout"})
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				score := 0.95
				if tc == "abstain" {
					score = 0.5
				}
				if tc == "feedback" {
					var b struct{ Input string }
					_ = json.NewDecoder(r.Body).Decode(&b)
					if !strings.Contains(b.Input, "contradicted") || !strings.Contains(b.Input, "Inspection found wear") {
						t.Error("revision feedback absent")
					}
				}
				fmt.Fprintf(w, `{"answers":[{"type":"predicate","name":"m0","probability":%g}]}`, score)
			}))
			defer srv.Close()
			cfg := modelAttributionConfig(srv.URL, "rank")
			cfg.Memory.Ranker = setting.MemoryRanker{Enabled: "on", AccountID: "account", Model: "rank", ModelType: setting.ModelTypeDecision, EveryActions: 1}
			s := &Session{cfg: cfg, memoryStore: store}
			active := []provider.ChatMessage{provider.NewTextMessage("user", "spindle")}
			batch := []provider.ChatMessage{{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "1", Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"spindle"}`}}}}, {Role: "tool", ToolCallID: "1", Content: "spindle"}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc == "budget" {
				cfg.Memory.RecallBudgetTokens = 1
			}
			if tc == "duplicate" {
				active = append(active, provider.ChatMessage{Role: "user", Origin: provider.OriginRuntimeContext, Visibility: provider.VisibilityContextOnly, Content: provider.TextContent(recallBlock([]memory.ScoredRecord{{Record: rec}}))})
			}
			if tc == "feedback" {
				err := store.Feedback(ctx, []memory.Feedback{{RevisionRef: memory.RevisionRef{ID: rec.ID, Revision: rec.Revision}, Kind: memory.FeedbackContradicted, SessionID: "prior", Evidence: []memory.Evidence{{SessionID: "prior", MessageID: "user", Kind: memory.EvidenceUser, Outcome: memory.OutcomeUnmeasured, Summary: "Inspection found wear"}}}})
				if err != nil {
					t.Fatal(err)
				}
			}
			admit, err := s.rankActionMemories(ctx, "owner", ".", 1, active, batch)
			if err != nil {
				t.Fatal(err)
			}
			if tc == "abstain" || tc == "budget" || tc == "duplicate" {
				if admit != nil {
					t.Fatal("unusable candidate selected")
				}
				if tc == "duplicate" && calls.Load() != 0 {
					t.Fatal("duplicate sent to provider")
				}
				return
			}
			if admit == nil {
				t.Fatal("expected pending memory")
			}
			switch tc {
			case "changed":
				_, err = store.Update(ctx, rec.ID, memory.Record{Revision: rec.Revision, Content: "Correction"}, memory.ActorUser, "source")
			case "disabled":
				cfg.Memory.AutoRecall = "off"
			case "reconfigured":
				s.settingsRevision++
			case "cancelled":
				cancel()
			}
			if err != nil {
				t.Fatal(err)
			}
			block, err := admit()
			if tc == "feedback" {
				if block == "" || err != nil {
					t.Fatal(block, err)
				}
				return
			}
			if block != "" {
				t.Fatal("late result delivered")
			}
			seen, e := store.ExposuresForMemory(t.Context(), rec.ID, 10)
			if e != nil || len(seen) != 0 {
				t.Fatal("false exposure", seen, e)
			}
		})
	}
}

func TestActionRankingUsesParallelOperationsNotWrapper(t *testing.T) {
	batch := []provider.ChatMessage{{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "pair", Function: provider.FunctionCall{Name: "multi_tool_use.parallel", Arguments: `{"tool_uses":[{"recipient_name":"functions.read_file","parameters":{"path":"a.go"}},{"recipient_name":"functions.read_file","parameters":{"path":"b.go"}}]}`}}}}, {Role: "tool", ToolCallID: "pair", DisplayContent: `{"results":[{"recipient_name":"read_file","result":"ok"},{"recipient_name":"read_file","result":"missing fixture","error":true,"outcome":{"state":"error"}}]}`}}
	state, paths := actionRankingState(nil, batch, ".")
	if len(paths) != 2 || len(state.Actions) != 2 || !strings.Contains(state.Actions[1], "b.go") || !strings.Contains(state.Actions[1], "missing fixture") {
		t.Fatal(state, paths)
	}
}
