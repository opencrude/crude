package turnloop

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/sessiondb"
	"crude/agent/tools"
	"crude/internal/providersim"
	recoverytypes "crude/types/recovery"
)

func rejectionFixture(t *testing.T) (*providersim.Server, *Agent, []provider.ChatMessage) {
	t.Helper()
	sim := providersim.New(t)
	p := &provider.Provider{ID: "custom", AccountID: t.Name(), Model: "serving", StandardName: "openai-responses", BaseURL: sim.BaseURL(providersim.Responses)}
	p.SetContextWindow(200_000)
	a := newTestAgent(t, p, p.Model)
	a.CompactionProvider = &provider.Provider{ID: "custom", AccountID: "errand-" + t.Name(), Model: "errand", StandardName: "openai", BaseURL: sim.BaseURL(providersim.Chat)}
	a.CompactionProvider.SetContextWindow(200_000)
	a.FilteredTools = FilterTools(tools.AgentTools, []string{"read_file"}, nil, "", false)
	a.ContextCompactionPercent = 85
	return sim, a, servedBy(p, compactionTestHistory(provider.NewTextMessage("user", "Finish the audit.")))
}

// A forced checkpoint may use another account after a quota rejection, but
// its last resort must not send the history back to the rejected account.
func TestRequiredCompactionRateLimitRejectsLastResort(t *testing.T) {
	sim, a, history := rejectionFixture(t)
	commits := 0
	a.CommitCompaction = func(CompactionCommit) error { commits++; return nil }
	sim.Script(providersim.HTTPError(providersim.ContextOverflow),
		providersim.HTTPError(providersim.RateLimit).WithRetryHint(providersim.RetryAfterSeconds(7200)),
		providersim.HTTPError(providersim.Auth))
	// Record an unwanted fourth call instead of relying on a simulator error.
	sim.Respond(func(providersim.Request) providersim.Turn { return providersim.HTTPError(providersim.Auth) })
	supervisor := NewSupervisor("root", 1, nil)
	supervisor.Wait = func(context.Context, time.Duration) error {
		t.Error("exhausted accounts entered an automatic retry")
		return context.Canceled
	}
	result := a.RunSupervised(t.Context(), history, "Keep the audit intact.", nil, nil, "", nil, SupervisedRunOptions{
		Supervisor: supervisor, OnAttention: func(RecoveryFailure) AttentionDisposition { return AttentionParkAttempt },
	})
	reqs := sim.Requests()
	if len(reqs) != 3 || reqs[2].Wire != providersim.Chat || !strings.HasSuffix(reqs[1].Path, "/compact") {
		t.Fatalf("received %d requests; want the serving request, native refusal and one distinct writer", len(reqs))
	}
	var status *provider.StatusError
	if result.State != SupervisedRunParked || !result.Failure.ProviderVerdict || !errors.As(result.Failure.Cause, &status) ||
		status.StatusCode != 429 || !strings.Contains(result.Failure.Summary, "quota") || commits != 0 {
		t.Fatalf("lost the native quota verdict or committed an incomplete checkpoint: failure=%+v commits=%d", result.Failure, commits)
	}
}

func TestAutoCompactionRateLimitRecheck(t *testing.T) {
	const checkpoint = "<<<intent>>>\nRetain the audit\n<<<work>>>\nReviewed inputs\n<<<state>>>\nThe ticket is blue\n<<<continuation>>>\nFinish the check"
	for _, phase := range []string{"held", "manual", "advised", "main_success"} {
		t.Run(phase, func(t *testing.T) {
			sim, a, history := rejectionFixture(t)
			a.SetHTTPClient(&http.Client{Transport: agentRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				resp, err := http.DefaultTransport.RoundTrip(r)
				if err == nil && strings.HasSuffix(r.URL.Path, "/compact") {
					resp.Header.Set("x-should-retry", "false")
					resp.Header.Del("Retry-After")
					if phase == "advised" {
						resp.Header.Set("retry-after-ms", "125")
					}
				}
				return resp, err
			})})
			sim.Respond(func(r providersim.Request) providersim.Turn {
				switch {
				case r.Index == 1:
					return providersim.HTTPError(providersim.ContextOverflow)
				case strings.HasSuffix(r.Path, "/compact"):
					return providersim.HTTPError(providersim.RateLimit)
				case r.Wire == providersim.Chat:
					return providersim.Text(checkpoint)
				default:
					return providersim.Text("Finished.")
				}
			})
			// Ask for a second checkpoint while the same attempt owns the
			// source. Before a successful main request the refusal still stands.
			observed := false
			check := func() {
				observed = true
				if phase == "advised" {
					time.Sleep(125 * time.Millisecond)
				}
				before := len(sim.Requests())
				next := a.backgroundCompactor(200_000, 200_000)
				trigger := "automatic"
				if phase == "manual" {
					trigger = CompactionTriggerManual
				}
				result, err := next.CompactWithRequestID(t.Context(), history, "Keep the audit intact.", "second", trigger, nil)
				calls := sim.Requests()[before:]
				// A held refusal skips the native check. A human /compact in
				// Auto never asks the serving route: Errand writes it at once.
				want := 2
				if phase == "held" || phase == "manual" {
					want = 1
				}
				if err != nil || result.ModelID != "errand" || len(calls) != want || calls[len(calls)-1].Wire != providersim.Chat ||
					want == 2 && !strings.HasSuffix(calls[0].Path, "/compact") {
					t.Errorf("%s: second checkpoint made %d calls, want %d; writer=%q error=%v", phase, len(calls), want, result.ModelID, err)
				}
				if obs := provider.CompactionObservations(map[string]provider.Account{a.Provider.AccountID: {}}); len(obs) != 0 {
					t.Errorf("account rejection became a persisted capability check: %v", obs)
				}
			}
			a.CommitCompaction = func(CompactionCommit) error {
				if phase != "main_success" {
					check()
				}
				return nil
			}
			a.Run(t.Context(), history, "Keep the audit intact.", func(e AgentEvent) {
				if e.Type == "done" && phase == "main_success" {
					check()
				}
			}, nil, "", nil)
			if !observed || a.RunFailure() != nil {
				t.Fatalf("missing checkpoint boundary for %s: %v", phase, a.RunFailure())
			}
		})
	}
}

func TestRequiredCompactionRateLimitParkReopenAndRetry(t *testing.T) {
	for _, cause := range []string{"quota", "veto"} {
		t.Run(cause, func(t *testing.T) {
			sim, a, history := rejectionFixture(t)
			a.CompactionProvider = nil
			fault := providersim.HTTPError(providersim.RateLimit)
			if cause == "quota" {
				fault = fault.WithRetryHint(providersim.RetryAfterSeconds(7200))
			} else {
				a.SetHTTPClient(&http.Client{Transport: agentRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					resp, err := http.DefaultTransport.RoundTrip(r)
					if err == nil && strings.HasSuffix(r.URL.Path, "/compact") {
						resp.Header.Set("x-should-retry", "false")
					}
					return resp, err
				})})
			}
			sim.Respond(func(r providersim.Request) providersim.Turn {
				if strings.HasSuffix(r.Path, "/compact") {
					return fault
				}
				if string(r.ToolChoice) == `"none"` {
					return providersim.HTTPError(providersim.Auth)
				}
				return providersim.HTTPError(providersim.ContextOverflow)
			})
			path := filepath.Join(t.TempDir(), "session.db")
			db, err := sessiondb.OpenAndMigrate(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			if err := sessiondb.CreateSessionRow(db, "session"); err != nil {
				t.Fatal(err)
			}
			run := func(initial *recoverytypes.Status) SupervisedRunResult {
				supervisor := NewSupervisor("root", 1, initial)
				supervisor.Persist = func(status recoverytypes.Status) error {
					return sessiondb.SaveRecoveryCheckpoint(db, "session", sessiondb.RecoveryCheckpoint{
						Status: status, Archive: history, ActiveContext: history, InputAdmitted: true,
					})
				}
				supervisor.Wait = func(context.Context, time.Duration) error {
					t.Error("the account refusal scheduled a retry")
					return context.Canceled
				}
				return a.RunSupervised(t.Context(), history, "Keep the audit intact.", nil, nil, "", nil, SupervisedRunOptions{
					Supervisor: supervisor, OnAttention: func(RecoveryFailure) AttentionDisposition { return AttentionParkAttempt },
				})
			}
			first := run(nil)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = sessiondb.OpenAndMigrate(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := sessiondb.InterruptActiveRecoveries(db, "session"); err != nil {
				t.Fatal(err)
			}
			loaded, found, err := sessiondb.LoadRecoveryCheckpoint(db, "session", "root")
			if err != nil || !found {
				t.Fatalf("reopen parked run: found=%v err=%v", found, err)
			}
			wantCause, wantAction := "quota", "credit"
			if cause == "veto" {
				wantCause, wantAction = "not to retry", "request"
			}
			s := loaded.Status
			if first.State != SupervisedRunParked || first.Failure.Kind != RecoveryFailureCompaction || !strings.Contains(s.Detail, "API 429") ||
				s.State != recoverytypes.NeedsAttention || !s.CanRetry() || !s.RetryAt.IsZero() ||
				!strings.Contains(s.Summary, wantCause) || !strings.Contains(s.Action, wantAction) || cause == "veto" && strings.Contains(s.Action, "billing") {
				t.Errorf("reopened %s incident has wrong summary or action: %q / %q", cause, s.Summary, s.Action)
			}
			before := len(sim.Requests())
			next := run(&loaded.Status)
			calls := sim.Requests()[before:]
			if next.State != SupervisedRunParked || len(calls) != 2 || !strings.HasSuffix(calls[1].Path, "/compact") {
				t.Errorf("new attempt made %d calls; want one serving request and one real native recheck", len(calls))
			}
		})
	}
}
