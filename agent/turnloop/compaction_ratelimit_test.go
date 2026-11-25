package turnloop

import (
	"bytes"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/tools"
	"crude/internal/providersim"
)

// A hard account verdict is not a capability verdict. Auto may pay another
// account to write this checkpoint. It may not send a second request to the
// rejected account, even if Errand selects a different model there.
func TestAutoCompactionRateLimitPermanentVerdict(t *testing.T) {
	for _, cause := range []string{"quota", "veto"} {
		for _, transport := range []string{"http", "stream"} {
			for _, writer := range []string{"different_account", "serving", "errand_same_account"} {
				t.Run(cause+"/"+transport+"/"+writer, func(t *testing.T) {
					sim := providersim.New(t)
					main := &provider.Provider{ID: "custom", AccountID: "main-" + t.Name(), Model: "serving", StandardName: "openai-responses", BaseURL: sim.BaseURL(providersim.Responses)}
					main.SetContextWindow(200_000)
					extractor := main
					switch writer {
					case "different_account":
						extractor = &provider.Provider{ID: "custom", AccountID: "errand-" + t.Name(), Model: "errand", StandardName: "openai", BaseURL: sim.BaseURL(providersim.Chat)}
					case "errand_same_account":
						extractor = &provider.Provider{ID: main.ID, AccountID: main.AccountID, Model: "errand", StandardName: main.StandardName, BaseURL: main.BaseURL}
					}
					extractor.SetContextWindow(200_000)
					a := newTestAgent(t, extractor, extractor.Model)
					combinedCompactionFailures.Delete(combinedCompactionKey(extractor, extractor.Model))
					a.NativeCompactionProvider, a.CompactionTargetModel, a.CompactionTargetWindow = main, main.Model, 200_000
					a.FilteredTools = FilterTools(tools.AgentTools, []string{"read_file"}, nil, "", false)
					kind := providersim.Quota
					if cause == "veto" {
						kind = providersim.RateLimit
						a.SetHTTPClient(&http.Client{Transport: agentRoundTripFunc(func(r *http.Request) (*http.Response, error) {
							resp, err := http.DefaultTransport.RoundTrip(r)
							if err == nil {
								resp.Header.Set("x-should-retry", "false")
							}
							return resp, err
						})})
					}
					nativeRequests := 1
					if transport == "stream" {
						sim.Script(providersim.Turn{}, providersim.StreamError(0, kind))
						nativeRequests = 2 // The standalone probe succeeded. Its read-back failed.
					} else {
						sim.Script(providersim.HTTPError(kind))
					}
					sim.Respond(func(providersim.Request) providersim.Turn {
						return providersim.Text("<<<intent>>>\nRetain the audit\n<<<work>>>\nReviewed inputs\n<<<state>>>\nThe ticket is blue\n<<<continuation>>>\nFinish the check")
					})
					var notices []string
					history := compactionTestHistory(provider.NewTextMessage("user", "Finish my audit."))
					result, err := a.Compact(t.Context(), history, "Preserve the audit.", func(e AgentEvent) {
						if e.Type == "compaction_route_notice" {
							notices = append(notices, e.Data.(map[string]any)["reason"].(string))
						}
					})
					reqs := sim.Requests()
					text := strings.Join(notices, " ")
					if writer == "different_account" {
						if err != nil || result.ModelID != extractor.Model || len(reqs) != nativeRequests+1 || reqs[len(reqs)-1].Wire != providersim.Chat || len(notices) != 1 || !strings.Contains(text, "Errand") {
							t.Errorf("different writer: model=%q requests=%d notices=%q err=%v", result.ModelID, len(reqs), notices, err)
						}
					} else {
						if len(reqs) != nativeRequests {
							t.Errorf("sent %d requests on the rejected route; want %d, without a portable submission", len(reqs), nativeRequests)
						}
						var status *provider.StatusError
						var api *provider.APIError
						if err == nil || provider.Classify(err) != provider.FailurePermanent || !errors.As(err, &status) && !errors.As(err, &api) || len(result.Messages) != 0 {
							t.Errorf("same route did not propagate its permanent provider verdict: %v", err)
						}
						if err != nil {
							text += " " + err.Error()
						}
					}
					text = strings.ToLower(text)
					if cause == "quota" && !strings.Contains(text, "quota") || cause == "veto" && !strings.Contains(text, "not to retry") {
						t.Errorf("the user did not see the %s cause: %q", cause, text)
					}
				})
			}
		}
	}
}

// A context overflow leaves no room to defer the checkpoint. Both portable
// writers must recover their own rate-limited extraction, commit the complete
// source once, then send the pending turn from the smaller checkpoint.
func TestRequiredCompactionRateLimit(t *testing.T) {
	const handoff = "<<<intent>>>\nRetain the audit\n<<<work>>>\nReviewed inputs\n<<<state>>>\nThe ticket is blue\n<<<continuation>>>\nFinish the check"
	const pending = "Complete my pending audit."
	for _, writer := range []string{"serving", "errand_after_native_429"} {
		for _, mode := range []string{"http", "stream"} {
			t.Run(writer+"/"+mode, func(t *testing.T) {
				sim := providersim.New(t)
				wire, standard := providersim.Chat, "openai"
				if writer != "serving" {
					wire, standard = providersim.Responses, "openai-responses"
				}
				p := &provider.Provider{ID: "custom", AccountID: t.Name(), Model: "serving", StandardName: standard, BaseURL: sim.BaseURL(wire)}
				p.SetContextWindow(200_000)
				a := newTestAgent(t, p, p.Model)
				a.FilteredTools = FilterTools(tools.AgentTools, []string{"read_file"}, nil, "", false)
				a.ContextCompactionPercent = 85
				if writer == "serving" {
					// newTestAgent normally scripts one reply per section.
					combinedCompactionFailures.Delete(combinedCompactionKey(p, p.Model))
				} else {
					a.CompactionProvider = &provider.Provider{ID: "custom", AccountID: t.Name() + "-errand", Model: "errand", StandardName: "openai", BaseURL: sim.BaseURL(providersim.Chat)}
					a.CompactionProvider.SetContextWindow(200_000)
				}
				history := compactionTestHistory(provider.NewTextMessage("user", pending))
				var sources []CompactionSource
				a.PrepareCompaction = func(s CompactionSource) error { sources = append(sources, s); return nil }
				var commits []CompactionCommit
				a.CommitCompaction = func(c CompactionCommit) error { commits = append(commits, c); return nil }

				var attempts []time.Time
				var nativeWrites int
				sim.Respond(func(r providersim.Request) providersim.Turn {
					if r.Index == 1 {
						return providersim.HTTPError(providersim.ContextOverflow)
					}
					if strings.HasSuffix(r.Path, "/compact") {
						if bytes.Contains(r.Raw, []byte("old evidence")) {
							nativeWrites++
							return providersim.HTTPError(providersim.RateLimit)
						}
						return providersim.Turn{}
					}
					if code := probeTicket(conversationText(r.Effective)); code != "" {
						return providersim.Text(code)
					}
					if r.Wire == providersim.Chat && string(r.ToolChoice) == `"none"` {
						attempts = append(attempts, time.Now())
						if len(attempts) == 1 {
							if mode == "stream" {
								return providersim.StreamError(0, providersim.RateLimit)
							}
							return providersim.HTTPError(providersim.RateLimit).WithRetryHint(providersim.RetryAfterMilliseconds(1250))
						}
						return providersim.Text(handoff)
					}
					return providersim.Text("Audit complete.")
				})
				var retries []float64
				archive := a.Run(t.Context(), history, "Keep the audit intact.", func(e AgentEvent) {
					if e.Type == "context_compaction_retrying" {
						retries = append(retries, e.Data.(map[string]any)["backoff"].(float64))
					}
				}, nil, "", nil)
				log := sim.Requests()
				if len(archive) == 0 || archive[len(archive)-1].ContentString() != "Audit complete." || len(commits) != 1 || len(sources) != 1 {
					t.Fatalf("did not resume after overflow: prepared=%d commits=%d requests=%d failure=%v", len(sources), len(commits), len(log), a.runFailure)
				}
				wantWriter, wantNative, wantCalls := "serving", 0, 4
				if writer != "serving" {
					wantWriter, wantNative, wantCalls = "errand", 1, 7
				}
				if commits[0].ModelID != wantWriter || nativeWrites != wantNative || len(log) != wantCalls {
					t.Fatalf("committed writer=%q, native uploads=%d requests=%d; want %q, %d, %d", commits[0].ModelID, nativeWrites, len(log), wantWriter, wantNative, wantCalls)
				}
				if sources[0].RequestID != commits[0].RequestID || !reflect.DeepEqual(sources[0].Messages, history) {
					t.Fatal("the commit did not preserve its complete original source")
				}
				wantDelay := time.Second
				if mode == "http" {
					wantDelay = 1250 * time.Millisecond // Longer than the first local backoff.
				}
				if !slices.Equal(retries, []float64{wantDelay.Seconds()}) || len(attempts) != 2 || attempts[1].Sub(attempts[0]) < wantDelay {
					t.Fatalf("scheduled backoffs=%v, actual request times=%v; want one wait of at least %v", retries, attempts, wantDelay)
				}
				first, retry := log[len(log)-3], log[len(log)-2]
				if !reflect.DeepEqual(first.Conversation, retry.Conversation) || !bytes.Contains(first.Raw, []byte("old evidence")) || !bytes.Contains(first.Raw, []byte(pending)) {
					t.Fatal("the extraction retry lost or changed the checkpoint source")
				}
				next := log[len(log)-1]
				if next.Model != p.Model || !bytes.Contains(next.Raw, []byte(pending)) || !bytes.Contains(next.Raw, []byte("The ticket is blue")) || bytes.Contains(next.Raw, []byte("old evidence")) || !reflect.DeepEqual(next.Tools, log[0].Tools) || !reflect.DeepEqual(next.System, log[0].System) {
					t.Fatal("the serving request did not resume its pending turn with the checkpoint, system and tools")
				}
			})
		}
	}
}
