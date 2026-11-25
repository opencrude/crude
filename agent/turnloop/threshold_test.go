package turnloop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"crude/agent/provider"
	"crude/agent/tools"
	"crude/internal/providersim"
)

const thresholdHandoff = "<<<intent>>>\nKeep the test\n<<<work>>>\nThe old request is complete\n<<<state>>>\nTicket blue\n<<<continuation>>>\nContinue the current turn"

func thresholdMessages(t *testing.T, mutate ...func(*http.Response) error) (*providersim.Server, *Agent) {
	t.Helper()
	const model = "claude-haiku-4-5"
	s := providersim.New(t, providersim.WithModelCatalog(providersim.Messages, providersim.Model{ID: model}))
	p := &provider.Provider{ID: "threshold-fixture", AccountID: t.Name(), StandardName: "anthropic", APIKey: "key", Model: model, BaseURL: s.BaseURL(providersim.Messages)}
	p.SetContextWindow(200000)
	a := newTestAgent(t, p, model)
	if len(mutate) > 0 {
		// Only negative gateway tests alter a response after the real server wrote it.
		a.SetHTTPClient(&http.Client{Transport: agentRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			res, err := http.DefaultTransport.RoundTrip(r)
			if err == nil && r.Method == http.MethodPost {
				if err := mutate[0](res); err != nil {
					_ = res.Body.Close()
					return nil, err
				}
			}
			return res, err
		})})
	}
	a.ContextCompactionPercent = 3
	a.MaxOutputTokens = 4096
	a.FilteredTools = FilterTools(tools.AgentTools, []string{"reply"}, nil, "", false)
	return s, a
}

func thresholdHistory(n int) []provider.ChatMessage {
	return []provider.ChatMessage{provider.NewTextMessage("user", "Earlier request"), provider.NewTextMessage("assistant", strings.Repeat("source history ", n)), provider.NewTextMessage("user", "Current request")}
}

func TestThresholdMessagesTurnBoundary(t *testing.T) {
	for _, fire := range []bool{true, false} {
		t.Run(fmt.Sprint(fire), func(t *testing.T) {
			s, a := thresholdMessages(t)
			old := servedBy(a.Provider, thresholdHistory(20000))
			if !fire {
				old = servedBy(a.Provider, thresholdHistory(3200))
			}
			var events []AgentEvent
			usage := providersim.Usage{Input: 72000, Output: 27, Cached: 114}
			if fire {
				a.Provider.SetExtraBody(map[string]any{"context_management": map[string]any{"edits": []any{map[string]any{"type": "compact_20260112", "pause_after_compaction": false, "instructions": "Keep the ticket.", "trigger": map[string]any{"type": "input_tokens", "value": 55000}}}}})
				s.Script(providersim.Text(thresholdHandoff).WithUsage(usage), providersim.Text("next answer"))
			} else {
				s.Script(providersim.Text("next answer").WithUsage(usage))
			}
			archive := a.Run(t.Context(), old, "system contract", func(e AgentEvent) { events = append(events, e) }, nil, "", nil)
			if a.RunFailure() != nil {
				t.Fatal(a.RunFailure())
			}
			requests := s.Requests()
			wantRequests := 2
			if fire {
				wantRequests++
			}
			if len(requests) != wantRequests {
				t.Fatalf("requests=%d", len(requests))
			}
			edit := requests[1]
			if !strings.Contains(edit.Header.Get("anthropic-beta"), "compact-2026-01-12") || !bytes.Contains(edit.Raw, []byte("pause_after_compaction")) || !bytes.Contains(edit.Raw, []byte("Do not call any tool")) {
				t.Fatal("missing boundary edit/header/instruction")
			}
			if archive[len(archive)-1].ContentString() != "next answer" || hasEvent(events, "context_compacted") != fire {
				t.Fatal("lost the turn or claimed a missing checkpoint")
			}
			if fire {
				if !bytes.Contains(edit.Raw, []byte("Keep the ticket.")) || !bytes.Contains(edit.Raw, []byte("55000")) {
					t.Fatal("lost account instruction or trigger")
				}
				next := requests[2]
				sent := conversationText(next.Conversation)
				if !reflect.DeepEqual(next.Tools, edit.Tools) || !reflect.DeepEqual(next.System, edit.System) || !strings.Contains(sent, "answered_human_requests") || !strings.Contains(sent, "Current request") || !strings.Contains(sent, "Ticket blue") || strings.Contains(sent, "source history") || bytes.Contains(next.Raw, []byte("context_management")) || hasSimulatedCheckpoint(next.Conversation) {
					t.Fatalf("wrong continuation (%d bytes)", len(next.Raw))
				}
			}
			var paid int
			for _, e := range events {
				if e.Type == "usage" {
					d := e.Data.(map[string]any)
					if d["prompt"] == 72000 {
						paid++
						if (d["request_kind"] == "compaction") != fire || d["prompt_cached"] != 114 {
							t.Fatalf("wrong usage: %+v", d)
						}
					}
				}
			}
			if paid != 1 {
				t.Fatalf("paid boundary reported %d times", paid)
			}
		})
	}
}
func TestThresholdResponsesSettlesMixedRound(t *testing.T) {
	s := providersim.New(t)
	var n int
	s.Respond(func(r providersim.Request) providersim.Turn {
		switch {
		case strings.HasSuffix(r.Path, "/compact"):
			return providersim.HTTPError(providersim.ModelNotFound)
		case bytes.Contains(r.Raw, []byte("compaction_trigger")):
			return providersim.HTTPError(providersim.InvalidRequest).WithFaultMessage("unsupported compaction trigger")
		case bytes.Contains(r.Raw, []byte("compact_threshold")):
			n++
			return providersim.Text("Checking once").WithTools(providersim.ToolCall{ID: "call-one", Name: "reply", Arguments: `{"content":"work in progress"}`}).WithUsage(providersim.Usage{Input: 18123, Output: 34})
		case isReadBack(conversationText(r.Conversation)):
			return providersim.Text(thresholdHandoff)
		default:
			return providersim.Text("finished")
		}
	})
	p := &provider.Provider{ID: "gateway", AccountID: t.Name(), StandardName: "openai-responses", Model: "translated-model", BaseURL: s.BaseURL(providersim.Responses)}
	p.SetContextWindow(100000)
	p.SetExtraBody(map[string]any{"context_management": []any{map[string]any{"type": "compaction", "compact_threshold": 1000}}})
	a := newTestAgent(t, p, p.Model)
	a.FilteredTools = FilterTools(tools.AgentTools, []string{"reply"}, nil, "", false)
	a.ContextCompactionPercent = 5
	a.MaxOutputTokens = 4096
	var events []AgentEvent
	var committed CompactionCommit
	a.CommitCompaction = func(c CompactionCommit) error { committed = c; return nil }
	archive := a.Run(t.Context(), servedBy(p, thresholdHistory(3800)), "system contract", func(e AgentEvent) { events = append(events, e) }, nil, "", nil)
	if a.RunFailure() != nil {
		t.Fatal(a.RunFailure())
	}
	requests := s.Requests()
	if len(requests) != 5 || n != 1 || len(committed.Messages) == 0 {
		t.Fatalf("%d requests, %d edits, %d committed messages", len(requests), n, len(committed.Messages))
	}
	for i := 3; i <= 4; i++ {
		r := requests[i]
		var body struct {
			Input []struct {
				Type, Role string
				CallID     string `json:"call_id"`
			}
			Management json.RawMessage `json:"context_management"`
		}
		if json.Unmarshal(r.Raw, &body) != nil {
			t.Fatal("invalid JSON")
		}
		start := 0
		if i == 4 {
			start = 1
			if body.Input[0].Role != "system" || !reflect.DeepEqual(r.Tools, requests[2].Tools) {
				t.Fatal("lost system or tools")
			}
		}
		want := []string{"compaction", "message", "function_call", "function_call_output"}
		if len(body.Input) < start+len(want) {
			t.Fatalf("%d: too few input items", i)
		}
		for j, typ := range want {
			if body.Input[start+j].Type != typ {
				t.Fatalf("%d: item %d=%s, want %s", i, j, body.Input[start+j].Type, typ)
			}
		}
		if body.Management != nil || bytes.Contains(r.Raw, []byte("source history")) || bytes.Count(r.Raw, []byte("call-one")) != 2 || bytes.Count(r.Raw, []byte("Checking once")) != 1 {
			t.Errorf("repeated or missing output on request %d", i)
		}
	}
	calls, paid := 0, 0
	for _, m := range archive {
		calls += len(m.ToolCalls)
	}
	for _, e := range events {
		if e.Type == "usage" {
			d := e.Data.(map[string]any)
			if d["prompt"] == 18123 {
				paid++
				if d["request_kind"] != "normal" {
					t.Fatal("aggregate usage not attributed to turn")
				}
			}
		}
	}
	if calls != 1 || paid != 1 || archive[len(archive)-1].ContentString() != "finished" {
		t.Fatalf("calls=%d paid=%d", calls, paid)
	}
}

// A /compact is Errand's checkpoint, also on a Provider only account whose
// threshold edit could fire: it never sends the provider's compaction edit.
// Here Errand is the agent's own connection and model.
func TestThresholdManualNeedsACheckpoint(t *testing.T) {
	for _, mode := range []string{"idle", "below", "eligible", "miss"} {
		t.Run(mode, func(t *testing.T) {
			s, a := thresholdMessages(t)
			big := mode == "eligible"
			old := thresholdHistory(3200)
			if big {
				old = thresholdHistory(20000)
			}
			a.Provider.SetProtocolSettings(&provider.ProtocolSettings{Compaction: provider.CompactionProviderOnly})
			combinedCompactionFailures.Delete(combinedCompactionKey(a.Provider, a.Model))
			if mode == "miss" {
				a.SeedContextUsage(70000, 0, len(old)+1)
			}
			var edits, writers, normal int
			s.Respond(func(r providersim.Request) providersim.Turn {
				switch {
				case bytes.Contains(r.Raw, []byte("pause_after_compaction")):
					edits++
					if big {
						return providersim.Text(thresholdHandoff)
					}
					return providersim.Text("Already answered")
				case isReadBack(conversationText(r.Conversation)):
					writers++
					if !strings.Contains(conversationText(r.Conversation), "source history") {
						t.Error("portable writer did not get the full history")
					}
					return providersim.Text(thresholdHandoff)
				default:
					normal++
					return providersim.Text("Already answered")
				}
			})
			var committed []provider.ChatMessage
			if mode == "idle" {
				r, e := a.Compact(t.Context(), old, "system contract", nil)
				if e != nil {
					t.Fatal(e)
				}
				committed = r.Messages
			} else {
				a.ContextCompactionPercent = CompactionOff
				requested := true
				a.CompactionRequest = func() string {
					if requested {
						requested = false
						return "manual"
					}
					return ""
				}
				a.CommitCompaction = func(c CompactionCommit) error {
					if c.Trigger != "requested" || c.RequestID != "manual" {
						t.Error("lost manual operation")
					}
					committed = c.Messages
					return nil
				}
				archive := a.Run(t.Context(), old, "system contract", func(AgentEvent) {}, nil, "", nil)
				if a.RunFailure() != nil || archive[len(archive)-1].ContentString() != "Already answered" {
					t.Fatalf("turn not settled: %v", a.RunFailure())
				}
			}
			wantEdits, wantWriters, wantNormal := 0, 1, 1
			if mode == "idle" {
				wantNormal = 0
			}
			if edits != wantEdits || writers != wantWriters || normal != wantNormal || len(committed) == 0 {
				t.Fatalf("edits=%d writers=%d ordinary=%d commit=%t", edits, writers, normal, len(committed) > 0)
			}
		})
	}
}

func TestThresholdMessagesFallbackKeepsSource(t *testing.T) {
	for _, fault := range []string{"null", "null_provider_only", "missing", "unsupported", "429", "late_stream"} {
		t.Run(fault, func(t *testing.T) {
			s, a := thresholdMessages(t, func(r *http.Response) error {
				if (strings.HasPrefix(fault, "null") || fault == "missing") && strings.Contains(r.Request.Header.Get("anthropic-beta"), "compact-2026-01-12") {
					data, err := io.ReadAll(r.Body)
					r.Body.Close()
					if err != nil {
						return err
					}
					if strings.HasPrefix(fault, "null") {
						text, _ := json.Marshal(thresholdHandoff)
						content := append([]byte(`"content":`), text...)
						if !bytes.Contains(data, content) {
							return fmt.Errorf("scripted compaction delta not found")
						}
						data = bytes.ReplaceAll(data, content, []byte(`"content":null`))
					} else {
						lines := strings.Split(string(data), "\n\n")
						var kept []string
						for _, line := range lines {
							if !strings.Contains(line, "content_block_") {
								kept = append(kept, line)
							}
						}
						data = []byte(strings.Join(kept, "\n\n"))
					}
					gatewayBody(r, string(data))
				}
				return nil
			})
			errand := providersim.New(t)
			writer := &provider.Provider{ID: "errand", AccountID: "other-" + t.Name(), StandardName: "openai", Model: "writer", BaseURL: errand.BaseURL(providersim.Chat)}
			writer.SetContextWindow(200000)
			a.CompactionProvider = writer
			if fault == "null_provider_only" {
				a.Provider.SetProtocolSettings(&provider.ProtocolSettings{Compaction: provider.CompactionProviderOnly})
			}
			errand.Respond(func(r providersim.Request) providersim.Turn {
				if !bytes.Contains(r.Raw, []byte("source history")) || !bytes.Contains(r.Raw, []byte("Current request")) {
					t.Error("writer lost source")
				}
				return providersim.Text(thresholdHandoff)
			})
			var edits int
			s.Respond(func(r providersim.Request) providersim.Turn {
				if bytes.Contains(r.Raw, []byte("pause_after_compaction")) {
					edits++
					switch fault {
					case "429":
						return providersim.HTTPError(providersim.RateLimit)
					case "late_stream":
						return providersim.StreamError(2, providersim.RateLimit)
					case "unsupported":
						return providersim.HTTPError(providersim.InvalidRequest).WithFaultMessage("unknown field: context_management")
					default:
						return providersim.Text(thresholdHandoff)
					}
				}
				if bytes.Contains(r.Raw, []byte("source history")) || !bytes.Contains(r.Raw, []byte("Ticket blue")) {
					t.Error("unsafe follow-up")
				}
				return providersim.Text("finished")
			})
			var events []AgentEvent
			out := a.Run(t.Context(), servedBy(a.Provider, thresholdHistory(20000)), "system contract", func(e AgentEvent) { events = append(events, e) }, nil, "", nil)
			if fault == "null_provider_only" {
				if edits != 1 || len(errand.Requests()) != 0 || a.RunFailure() == nil || !hasEvent(events, "context_compaction_failed") || len(s.Requests()) != 2 || !strings.Contains(out[2].ContentString(), "source history") {
					t.Fatalf("null summary lost source or billed another request; %v", a.RunFailure())
				}
				return
			}
			if edits != 1 || len(errand.Requests()) != 1 || a.RunFailure() != nil || out[len(out)-1].ContentString() != "finished" || !hasEvent(events, "context_compacted") {
				t.Fatalf("edits=%d writes=%d failure=%v", edits, len(errand.Requests()), a.RunFailure())
			}
			obs := provider.CompactionObservations(map[string]provider.Account{a.Provider.AccountID: {}})
			want := "cooling"
			if fault == "unsupported" {
				want = "unavailable"
			}
			if len(obs) != 1 || obs[0].Status != want {
				t.Fatalf("route evidence: %v; want %s", obs, want)
			}
		})
	}
}
