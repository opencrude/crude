package viewmodel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/session"
	"crude/types"
	"crude/workspace"
)

// Exercise the composer's real slash-command route and the painted shell, not
// just the settings helper: feedback and the persistent indicators have
// separate lifetimes.
func TestFastCommandShowsFeedbackAndPersistentIndicators(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	id := addBackendTestOrchestrator(t, app)
	app.switchToAgentView(id)
	app.width, app.height = 120, 32
	app.setFocusInput()

	sendFast := func() {
		t.Helper()
		app.Input.SetInput("/fast")
		_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("/fast did not schedule a settings save")
		}
		runAppCommand(t, app, cmd)
	}
	paint := func() (string, string) {
		t.Helper()
		app.paintShellFrame()
		return rowText(app, 0), rowText(app, app.height-1)
	}
	header, footer := paint()
	if strings.Contains(header, " fast") || strings.Contains(footer, "⚡") {
		t.Fatalf("fast indicator present before enabling: %q / %q", header, footer)
	}
	sendFast()
	header, footer = paint()
	if !strings.Contains(header, "test-model fast") || !strings.Contains(footer, "⚡") || !strings.Contains(footer, "Fast on") {
		t.Fatalf("enabled fast is not visible in header and feedback: %q / %q", header, footer)
	}
	for _, n := range app.ws.Status().Notifications(false) {
		if n.Transient {
			app.ws.Status().Dismiss(n.ID)
		}
	}
	header, footer = paint()
	if !strings.Contains(header, "test-model fast") || !strings.Contains(footer, "⚡") || !strings.Contains(footer, "Fast") || !strings.Contains(footer, "history") {
		t.Fatalf("fast did not remain beside the model and composer hints: %q / %q", header, footer)
	}
	sendFast()
	header, footer = paint()
	if strings.Contains(header, "test-model fast") || !strings.Contains(footer, "Fast off") {
		t.Fatalf("disabled fast still looks enabled or has no feedback: %q / %q", header, footer)
	}
}

func TestFastCommandConnectedShellsAndRequests(t *testing.T) {
	tiers := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		tier, _ := body["service_tier"].(string)
		tiers <- tier
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	runtime := remoteJourneyRuntime(t, server.URL)
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	remoteJourneyUntil(t, shells, func() bool { return two.app.AgentView.ActiveID() == id })

	send := func(shell remoteJourneyShell, text string) {
		t.Helper()
		shell.app.Input.SetInput(text)
		shell.app.setFocusInput()
		_, cmd := shell.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil {
			t.Fatalf("%q produced no command", text)
		}
		runAppCommand(t, shell.app, cmd)
	}
	send(one, "/fast")
	remoteJourneyUntil(t, shells, func() bool {
		for _, shell := range shells {
			owner := shell.app.ws.SessionForAgent(id)
			if owner == nil || owner.Settings.Effective.FastMode != types.FastModeOn {
				return false
			}
		}
		return true
	})
	for _, shell := range shells {
		shell.app.paintShellFrame()
		if row := rowText(shell.app, 0); !strings.Contains(row, "test-model fast") {
			t.Fatalf("connected header did not update: %q", row)
		}
		if row := rowText(shell.app, shell.app.height-1); !strings.Contains(row, "Fast") || !strings.Contains(row, "⚡") {
			t.Fatalf("connected footer did not update: %q", row)
		}
	}
	// A newly attached shell must reconstruct the same indicator from the core.
	three := newRemoteJourneyShell(t, runtime, root)
	shells = append(shells, three)
	remoteJourneyUntil(t, shells, func() bool {
		owner := three.app.ws.SessionForAgent(id)
		return three.app.AgentView.ActiveID() == id && owner != nil && owner.Settings.Effective.FastMode == types.FastModeOn
	})
	three.app.paintShellFrame()
	if row := rowText(three.app, 0); !strings.Contains(row, "test-model fast") {
		t.Fatalf("reattached header lost fast: %q", row)
	}
	send(one, "first request")
	select {
	case tier := <-tiers:
		if tier != "fast" {
			t.Fatalf("enabled request tier = %q", tier)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("enabled request was not sent")
	}
	remoteJourneyUntil(t, shells, func() bool { return one.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted })
	send(one, "/fast")
	remoteJourneyUntil(t, shells, func() bool {
		for _, shell := range shells {
			if shell.app.ws.SessionForAgent(id).Settings.Effective.FastMode != types.FastModeOff {
				return false
			}
		}
		return true
	})
	send(one, "second request")
	select {
	case tier := <-tiers:
		if tier != "" {
			t.Fatalf("disabled request retained tier %q", tier)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("disabled request was not sent")
	}
}

func TestFastCommandUnsupportedConnectionIsNotPresentedAsFast(t *testing.T) {
	runtime := remoteJourneyRuntimeConfig(t, "https://example.invalid/v1", func(cfg *session.Config) {
		account := cfg.Accounts["test"]
		account.Connection.APIStandard = "anthropic"
		cfg.Accounts["test"] = account
	})
	one := newRemoteJourneyShell(t, runtime, t.TempDir())
	id := remoteJourneyCreate(t, one)
	one.app.Input.SetInput("/fast")
	one.app.setFocusInput()
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, one.app, cmd)
	one.app.paintShellFrame()
	if got := one.app.ws.SessionForAgent(id).Settings.Effective.FastMode; got != types.FastModeUnsupported {
		t.Fatalf("unsupported selection = %q", got)
	}
	header, footer := rowText(one.app, 0), rowText(one.app, one.app.height-1)
	if strings.Contains(header, "test-model fast") || !strings.Contains(footer, "Fast unsupported") {
		t.Fatalf("unsupported tier misrepresented: %q / %q", header, footer)
	}
	for _, n := range one.app.ws.Status().Notifications(false) {
		if n.Transient {
			one.app.ws.Status().Dismiss(n.ID)
		}
	}
	one.app.paintShellFrame()
	if row := rowText(one.app, one.app.height-1); !strings.Contains(row, "Fast unsupported") {
		t.Fatalf("unsupported indication disappeared with the notice: %q", row)
	}
}

func TestMissingFastConnectionIsNotGuessedUnsupported(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	id := addBackendTestOrchestrator(t, app)
	app.width, app.height = 120, 32
	app.switchToAgentView(id)
	app.ws.SessionForAgent(id).Settings.Effective.FastMode = types.FastModeUnavailable
	app.paintShellFrame()
	header, footer := rowText(app, 0), rowText(app, app.height-1)
	if strings.Contains(header, "test-model fast") || !strings.Contains(footer, "Fast unavailable") || strings.Contains(footer, "unsupported") {
		t.Fatalf("unresolved connection guessed as supported or unsupported: %q / %q", header, footer)
	}
}

func TestFastIndicatorStaysWithItsRootSession(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	first := addBackendTestOrchestrator(t, app)
	second := addBackendTestOrchestrator(t, app)
	app.width, app.height = 160, 32
	app.switchToAgentView(first)
	runAppCommand(t, app, app.executeSlashCommand("/fast"))
	for _, n := range app.ws.Status().Notifications(false) {
		if n.Transient {
			app.ws.Status().Dismiss(n.ID)
		}
	}
	pane := app.ws.PaneMgr().GetOrCreate("fast-child", workspace.PaneAgent, "child")
	child := workspace.NewAgentState("fast-child", pane.Messages, app.ws.Logger())
	app.ws.AgentMgr().AddSubAgent("fast-child", first, "Child", "child-model", "researcher", child)
	for _, agentID := range []string{second, "fast-child"} {
		app.switchToAgentView(agentID)
		app.paintShellFrame()
		header, footer := rowText(app, 0), rowText(app, app.height-1)
		if strings.Contains(header, " fast") || strings.Contains(footer, "⚡") {
			t.Fatalf("%s borrowed another agent's tier: %q / %q", agentID, header, footer)
		}
	}
	app.switchToAgentView(first)
	app.paintShellFrame()
	if header, footer := rowText(app, 0), rowText(app, app.height-1); !strings.Contains(header, "test-model fast") || !strings.Contains(footer, "⚡") {
		t.Fatalf("returning to root lost fast: %q / %q", header, footer)
	}
}
