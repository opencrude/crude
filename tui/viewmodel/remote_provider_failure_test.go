package viewmodel

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/agent/provider"
	"crude/agent/session"
	"crude/types/recovery"
	"crude/types/statuscenter"
)

func TestRemoteQuotaFailureIsVisibleAfterRecoveryAndOnFirstRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const reason = "Provider quota or billing limit reached"
	const nextPrompt = "Try the next request."
	var requests atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		switch requests.Add(1) {
		case 1:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, `{"error":{"message":"Temporary outage"}}`)
		case 2:
			w.Header().Set("Content-Type", "text/event-stream")
			text := strings.Repeat("Accepted work after recovery.\n\n", 20)
			_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", text)
			_, _ = fmt.Fprint(w, "data: "+`{"type":"response.completed","response":{"id":"before-limit","status":"completed","store":false,"output":[{"type":"function_call","call_id":"read-plan","name":"get_plan","arguments":"{}","status":"completed"}]}}`+"\n\n")
		case 3, 4:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "86400")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprint(w, `{"error":{"type":"rate_limit_error","message":"This request would exceed your account's rate limit. Please try again later."}}`)
		default:
			t.Error("a settled quota verdict was retried")
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(providerServer.Close)
	configure := func(cfg *session.Config) {
		account := cfg.Accounts["test"]
		account.Connection.APIStandard = "openai-responses"
		account.Connection.Dialer = &provider.DialSettings{MaxAttempts: 1}
		cfg.Accounts["test"] = account
	}
	runtime := remoteJourneyRuntimeConfig(t, providerServer.URL, configure)
	root := t.TempDir()
	shell := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, shell)
	app := shell.app
	app.Tree.Visible = false
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	failures := func(app *App, agentID string, count int) []string {
		t.Helper()
		var ids []string
		lastWork, lastFault, next := -1, -1, -1
		for i, row := range app.ws.AgentMgr().Get(agentID).State.Messages.Messages() {
			if row.ToolName == "get_plan" && row.ToolStatus == "success" {
				lastWork = i
			}
			if row.Content == nextPrompt {
				next = i
			}
			if row.Recovery != nil && row.Recovery.State == recovery.Failed {
				if lastFault < 0 {
					lastFault = i
				}
				if !strings.Contains(row.Recovery.Detail, reason) || !strings.Contains(row.Recovery.Detail, "API 429") || row.Recovery.CanRetry() {
					t.Fatalf("wrong quota evidence: %+v", row.Recovery)
				}
				ids = append(ids, row.InternalID)
			}
		}
		if len(ids) != count || lastWork < 0 || lastFault <= lastWork || next >= 0 && next <= lastFault {
			t.Fatalf("quota rows do not follow their failed turns: faults=%v work=%d first fault=%d next prompt=%d", ids, lastWork, lastFault, next)
		}
		return ids
	}
	submitAndCheck := func(text string, count int, attempts int32) {
		app.Input.SetInput(text)
		app.setFocusInput()
		_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		runAppCommand(t, app, cmd)
		const unsent = "Keep this unsent draft."
		app.Input.SetInput(unsent)
		remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
			status := app.ws.AgentActivity(id).Status
			if requests.Load() < attempts || status.TurnGeneration != uint64(count) || status.Phase != statuscenter.AgentFailed {
				return false
			}
			// The incomplete-response acknowledgement is applied by the terminal
			// event, not by the preceding recovery record.
			for _, row := range app.ws.AgentMgr().Get(id).State.Messages.Messages() {
				if row.TurnGeneration == status.TurnGeneration && row.IncompleteReason == "failed" {
					return true
				}
			}
			return false
		})
		app.forceFrame()
		app.View()
		if paint := chatAreaText(app); !strings.Contains(paint, reason) || strings.Contains(paint, "Retry now") {
			t.Fatalf("failure was not visible at the live chat tail: %s", paint)
		}
		failures(app, id, count)
		if requests.Load() != attempts || app.Input.Value() != unsent || app.canRetryAgentRecovery(id) {
			t.Fatal("terminal failure retried or consumed the draft")
		}
	}
	submitAndCheck("Keep going after a recoverable request.", 1, 3)
	submitAndCheck(nextPrompt, 2, 4)
	live := failures(app, id, 2)
	sessionID := app.ActiveSession.SessionID
	app.StopAsyncIO()
	if err := app.CloseWorkspaceAttachment(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	resumed := newRemoteJourneyShell(t, remoteJourneyRuntimeConfig(t, providerServer.URL, configure), root)
	runAppCommand(t, resumed.app, resumed.app.launcherResumeSession(sessionID, nil))
	remoteJourneyUntil(t, []remoteJourneyShell{resumed}, func() bool { return resumed.app.AgentView.ActiveID() != "" })
	cold := failures(resumed.app, resumed.app.AgentView.ActiveID(), 2)
	if live[0] != cold[0] || live[1] != cold[1] || requests.Load() != 4 {
		t.Fatalf("cold resume lost evidence or called the provider: live=%v reopened=%v", live, cold)
	}
}
