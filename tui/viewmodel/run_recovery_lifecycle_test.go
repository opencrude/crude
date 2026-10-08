package viewmodel

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/types"
	"crude/types/reasoning"
	"crude/types/recovery"
	"crude/workspace"
)

func TestRecoveryRetryDoesNotOverwriteResumedActivity(t *testing.T) {
	for _, width := range []int{80, 160} {
		for _, initial := range []recovery.State{recovery.Recovering, recovery.NeedsAttention, recovery.Interrupted} {
			t.Run(fmt.Sprintf("%d/%s", width, initial), func(t *testing.T) {
				app := newFullTestApp(t)
				app.Logger = stubLogger{}
				app.width, app.height = width, 40
				app.AddOrchestrator("neighbor")
				app.AddOrchestrator("owner")
				app.maybeHydrateComposerDraft()
				const draft = "Keep this next instruction."
				app.Input.SetInput(draft)
				emit := func(typ string, data any) {
					_, _ = app.Update(types.AgentMsg{Type: typ, AgentID: "owner", Data: data})
				}
				status := recovery.Status{
					ID: "incident", AgentID: "owner", Generation: 1, State: initial,
					StartedAt: time.Now(), UpdatedAt: time.Now(), Summary: "Connection unavailable",
				}
				if initial == recovery.Recovering {
					status.RetryAt = time.Now().Add(time.Minute)
				}
				emit("recovery", status)
				app.paintShellFrame()
				if !app.canRetryAgentRecovery("owner") {
					t.Fatal("pending recovery has no retry action")
				}

				// This is the owner's acknowledgement after either an automatic
				// cooldown or an exact Retry now/Continue action. It retains the
				// incident but removes the old deadline while the attempt runs.
				status.State, status.RetryAt = recovery.Recovering, time.Time{}
				status.Summary, status.UpdatedAt = "Retrying unfinished run", time.Now()
				emit("recovery", status)
				app.paintShellFrame()
				if phase := app.AgentActivity("owner").Status.Phase; phase != types.AgentPreparing {
					t.Fatalf("acknowledged retry still claims cooldown: %s", phase)
				}
				for _, event := range []struct {
					typ   string
					data  any
					phase types.AgentPhase
					text  string
				}{
					{"phase", map[string]any{"name": "waiting_provider", "provider": "Test Provider", "request_id": "new-request"}, types.AgentWaiting, "Waiting for Test Provider"},
					{"thinking", reasoning.Delta{Text: "Checking the resumed work", Kind: reasoning.Summary, BlockID: "resumed-thought"}, types.AgentThinking, "Checking the resumed work"},
					{"stream_token", "Accepted resumed response", types.AgentResponding, "Receiving response"},
					{"tool_call", map[string]any{"name": "read_async_output", "tool_id": "resumed-tool", "args": `{"handle":"owned-command"}`}, types.AgentTool, "Running read_async_output"},
				} {
					emit(event.typ, event.data)
					for frame := 0; frame < 3; frame++ {
						// Inventory reconciliation used to overwrite the new
						// request's event-owned phase on the very next tick.
						if frame == 2 {
							app.switchToAgentView("neighbor")
							app.paintShellFrame()
							app.switchToAgentView("owner")
						}
						if frame > 0 {
							_, _ = app.Update(types.TickMsg(time.Now()))
						}
						app.paintShellFrame()
						snapshot, _ := app.Status.Agent("owner")
						activity := app.AgentActivity("owner")
						if snapshot.Phase != event.phase || activity.Status.Phase != event.phase || snapshot.NeedsAction {
							t.Fatalf("%s frame %d: resumed phase overwritten: snapshot=%+v activity=%+v", event.typ, frame, snapshot, activity.Status)
						}
						line := shellBufferRow(app, app.chatRect.Y+app.chatRect.H-1)
						if !strings.Contains(line, event.text) || strings.Contains(line, "Recovering") || strings.Contains(line, "/retry") {
							t.Fatalf("%s frame %d: stale recovery strip: %q", event.typ, frame, line)
						}
						var transcript strings.Builder
						for row := app.chatRect.Y; row < app.chatRect.Y+app.chatRect.H-1; row++ {
							transcript.WriteString(rowText(app, row))
						}
						if strings.Contains(transcript.String(), "Recovering") || strings.Contains(transcript.String(), "(Retry now)") || !strings.Contains(transcript.String(), "Run resumed") {
							t.Fatalf("%s frame %d: stale incident above resumed work: %q", event.typ, frame, transcript.String())
						}
					}
				}
				if app.canRetryAgentRecovery("owner") {
					t.Fatal("active retry attempt still offers another Continue action")
				}
				for _, issue := range app.Status.Issues() {
					if issue.ID == "agent:owner" && issue.ResolvedAt.IsZero() {
						t.Fatalf("acknowledged retry left a live needs-attention issue: %+v", issue)
					}
				}
				if app.Input.Value() != draft {
					t.Fatal("recovery consumed the draft")
				}
				status.State, status.UpdatedAt = recovery.Recovered, time.Now()
				emit("recovery", status)
				emit("done", map[string]any{"reason": "completed"})
				_, _ = app.Update(types.TickMsg(time.Now()))
				app.paintShellFrame()
				if app.AgentActivity("owner").Status.Phase != types.AgentCompleted {
					t.Fatal("recovery completion did not settle the activity")
				}
			})
		}
	}
}

func TestRecoveryActivityHonorsExplicitStopWhileIncidentIsPending(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("owner")
	_ = app.handleAgentEvent(types.AgentEventMsg{Type: "recovery", AgentID: "owner", Data: recovery.Status{
		ID: "incident", AgentID: "owner", Generation: 1, State: recovery.Recovering,
		RetryAt: time.Now().Add(time.Minute),
	}})
	app.AgentMgr.Get("owner").State.BeginCancellation()
	app.syncAgentStatusInventory()
	_ = app.handleAgentEvent(types.AgentEventMsg{Type: "recovery", AgentID: "owner", Data: recovery.Status{
		ID: "incident", AgentID: "owner", Generation: 1, State: recovery.NeedsAttention,
		Summary: "This queued event must not undo Stop", UpdatedAt: time.Now(),
	}})
	snapshot, _ := app.Status.Agent("owner")
	if snapshot.Phase != types.AgentCancelling || snapshot.NeedsAction {
		t.Fatalf("rejected queued recovery changed live status: %+v", snapshot)
	}
	for _, issue := range app.Status.Issues() {
		if issue.ID == "agent:owner" && issue.ResolvedAt.IsZero() {
			t.Fatalf("rejected queued recovery raised an attention issue: %+v", issue)
		}
	}
	app.paintShellFrame()
	view := app.AgentActivity("owner")
	if view.Status.Phase != types.AgentCancelling || view.Active {
		t.Fatalf("pending incident hid Stop: %+v", view)
	}
	if got := shellBufferRow(app, app.chatRect.Y+app.chatRect.H-1); !strings.Contains(got, "Stopping") || strings.Contains(got, "Recovering") {
		t.Fatalf("Stop still painted recovery: %q", got)
	}
}

type recoveryJourneySender struct {
	events chan types.AgentMsg
}

func (s *recoveryJourneySender) SendAgentEvent(typ string, data any) {
	s.SendAgentEventWithID(typ, data, "")
}

func (s *recoveryJourneySender) SendAgentEventWithID(typ string, data any, id string) {
	s.events <- types.AgentMsg{Type: typ, Data: data, AgentID: id}
}

func TestRecoveryRealComposerRetryClickAndAutomaticContinuation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, failure := range []int{http.StatusUnauthorized, http.StatusServiceUnavailable, http.StatusRequestTimeout} {
		t.Run(http.StatusText(failure), func(t *testing.T) {
			var requests atomic.Int32
			finish := make(chan struct{})
			defer close(finish)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(failure)
					if failure == http.StatusRequestTimeout {
						_, _ = fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"stream error: stream disconnected before completion: stream closed before response.completed"}}`)
					} else {
						_, _ = fmt.Fprint(w, `{"error":{"message":"provider unavailable for this attempt"}}`)
					}
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Accepted resumed response\"}}]}\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-finish:
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			app := newFullTestApp(t)
			app.Logger = stubLogger{}
			app.RootPath, app.WorkspaceDir = t.TempDir(), t.TempDir()
			app.SessionsByID = make(map[string]*workspace.Session)
			app.SessionsByAgent = make(map[string]*workspace.Session)
			app.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
			cfg := testAccountConfig()
			account := cfg.Accounts["test"]
			template := account.Connection
			template.ProviderURL, template.Dialer = server.URL, &provider.DialSettings{MaxAttempts: 1}
			account.Connection = template
			cfg.Accounts["test"] = account
			cfg.Agents.Enabled = false
			if _, err := app.CreateSession(cfg, t.TempDir()); err != nil {
				t.Fatal(err)
			}
			// CloseAgent releases the orchestrator, not its session's log/database.
			// Join the backend before TempDir's Windows cleanup removes those files.
			defer app.CloseAllSessions()
			sender := &recoveryJourneySender{events: make(chan types.AgentMsg, 512)}
			orch, err := app.StartOrchestrator(sender)
			if err != nil {
				t.Fatal(err)
			}
			defer app.CloseAgent(orch.ID)
			app.AddOrchestrator(orch.ID)
			app.maybeHydrateComposerDraft()
			app.Input.SetInput("Continue this exact task once the connection is restored.")
			_, submit := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if submit == nil {
				t.Fatal("Enter did not schedule durable admission")
			}
			const draft = "A different instruction that must stay unsent."
			app.Input.SetInput(draft)
			app.Input.SetCursorFlat(7)
			runAppCommand(t, app, submit)
			waitFor := func(typ string) types.AgentMsg {
				t.Helper()
				timeout := time.NewTimer(8 * time.Second)
				defer timeout.Stop()
				for {
					select {
					case event := <-sender.events:
						if event.AgentID == "" {
							event.AgentID = orch.ID
						}
						_, _ = app.Update(event)
						if event.Type == typ {
							return event
						}
					case <-timeout.C:
						t.Fatalf("missing owner event %s; requests=%d", typ, requests.Load())
						return types.AgentMsg{}
					}
				}
			}
			incident := waitFor("recovery").Data.(recovery.Status)
			app.paintShellFrame()
			if failure == http.StatusUnauthorized {
				if incident.State != recovery.NeedsAttention {
					t.Fatalf("authentication did not park for attention: %+v", incident)
				}
				// Find the actual painted action and use the product pointer
				// entry point, not a direct call to the backend retry method.
				x, y := -1, -1
				for row := app.chatRect.Y; row < app.chatRect.Y+app.chatRect.H-1; row++ {
					runes := []rune(rowText(app, row))
					if column := strings.Index(string(runes), "(Retry now)"); column >= 0 {
						x, y = len([]rune(string(runes)[:column]))+3, row
						break
					}
				}
				if x < 0 {
					t.Fatal("the recovery row did not paint Retry now")
				}
				_, _ = app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
				_, command := app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
				if command == nil {
					t.Fatal("Retry now click did not submit an owner action")
				}
				// This release has one command: the exact durable retry request.
				result := command()
				if batch, ok := result.(tea.BatchMsg); ok {
					for _, cmd := range batch {
						_, _ = app.Update(cmd())
					}
				} else {
					_, _ = app.Update(result)
				}
			} else if incident.State != recovery.Recovering || incident.RetryAt.IsZero() {
				t.Fatalf("transport exhaustion has no automatic cooldown: %+v", incident)
			} else {
				var transcript strings.Builder
				for row := app.chatRect.Y; row < app.chatRect.Y+app.chatRect.H-1; row++ {
					transcript.WriteString(rowText(app, row))
				}
				if !strings.Contains(transcript.String(), "retry in ") || !strings.Contains(transcript.String(), "(Retry now)") || strings.Contains(transcript.String(), "Needs attention") {
					t.Fatalf("real automatic deadline was not painted: %s", transcript.String())
				}
			}
			waitFor("stream_token")
			_, _ = app.Update(types.TickMsg(time.Now()))
			app.paintShellFrame()
			snapshot, _ := app.Status.Agent(orch.ID)
			view := app.AgentActivity(orch.ID)
			line := shellBufferRow(app, app.chatRect.Y+app.chatRect.H-1)
			if snapshot.Phase != types.AgentResponding || view.Status.Phase != types.AgentResponding || !strings.Contains(line, "Receiving response") {
				t.Fatalf("real resumed output still shows recovery: snapshot=%+v activity=%+v strip=%q", snapshot, view.Status, line)
			}
			resumed := false
			for row := app.chatRect.Y; row < app.chatRect.Y+app.chatRect.H-1; row++ {
				text := rowText(app, row)
				resumed = resumed || strings.Contains(text, "Run resumed")
				if strings.Contains(text, "Recovering") || strings.Contains(text, "(Retry now)") {
					t.Fatalf("real owner acknowledgement left a stale recovery row: %q", text)
				}
			}
			if !resumed {
				t.Fatal("real retry lost its retained resumed boundary")
			}
			if app.canRetryAgentRecovery(orch.ID) || app.Input.Value() != draft || app.Input.CursorFlat() != 7 || requests.Load() != 2 {
				t.Fatalf("retry duplicated work or consumed draft: retry=%v draft=%q requests=%d", app.canRetryAgentRecovery(orch.ID), app.Input.Value(), requests.Load())
			}
			finish <- struct{}{}
			waitFor("done")
			_, _ = app.Update(types.TickMsg(time.Now()))
			app.paintShellFrame()
			if view := app.AgentActivity(orch.ID); view.Active || view.Status.Phase != types.AgentCompleted || view.Recovery.State != recovery.Recovered {
				t.Fatalf("real completion did not settle recovery: %+v", view)
			}
			var transcript strings.Builder
			for row := app.chatRect.Y; row < app.chatRect.Y+app.chatRect.H; row++ {
				transcript.WriteString(rowText(app, row))
				transcript.WriteByte('\n')
			}
			if !strings.Contains(transcript.String(), "Run recovered") || strings.Contains(transcript.String(), "(Retry now)") || strings.Contains(transcript.String(), "retry in") {
				t.Fatalf("settled real recovery retained stale action/countdown: %s", transcript.String())
			}
		})
	}
}
