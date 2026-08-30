package shellstatus

import (
	"strings"
	"testing"
	"time"

	"crude/tui/editor"
	"crude/tui/theme"
)

func newTestScreen() *Screen {
	return New(theme.DefaultDark())
}

func setWidth(sb *Screen) {
	sb.SetWidth(120)
}

func TestScreen_InitialState(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	if sb.IsSystemBusy() {
		t.Fatal("expected system to be idle initially")
	}
	if n := sb.ActiveAgentCount(); n != 0 {
		t.Fatalf("expected 0 active agents, got %d", n)
	}
	if n := sb.IdleAgentCount(); n != 0 {
		t.Fatalf("expected 0 idle agents, got %d", n)
	}

	v := sb.View()
	if !strings.Contains(v, "ready") {
		t.Fatalf("expected 'ready' in initial view, got: %q", v)
	}
}

func TestScreen_SingleAgentStreaming(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetThinkingForAgent("orch_1", false, true, false)
	sb.SetCurrentAgent("orch_1")

	if !sb.IsSystemBusy() {
		t.Fatal("expected system busy with streaming agent")
	}
	if n := sb.ActiveAgentCount(); n != 1 {
		t.Fatalf("expected 1 active agent, got %d", n)
	}

	v := sb.View()
	if !strings.Contains(v, "1 agent active") {
		t.Fatalf("expected aggregate active-agent report, got: %q", v)
	}
}

func TestScreen_SingleAgentThinking(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetThinkingForAgent("orch_1", true, false, false)
	sb.SetCurrentAgent("orch_1")

	if !sb.IsSystemBusy() {
		t.Fatal("expected system busy with thinking agent")
	}
	if n := sb.ActiveAgentCount(); n != 1 {
		t.Fatalf("expected 1 active agent, got %d", n)
	}

	v := sb.View()
	if !strings.Contains(v, "1 agent active") {
		t.Fatalf("expected aggregate active-agent report, got: %q", v)
	}
}

func TestScreen_SubAgentRunningOrchIdle(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetThinkingForAgent("orch_1", false, false, false)
	sb.SetThinkingForAgent("sub_1", false, true, false)
	sb.SetCurrentAgent("orch_1")

	if !sb.IsSystemBusy() {
		t.Fatal("expected system busy with sub-agent running")
	}
	if n := sb.ActiveAgentCount(); n != 1 {
		t.Fatalf("expected 1 active agent, got %d", n)
	}

	v := sb.View()
	if !strings.Contains(v, "1 agent active") || !strings.Contains(v, "1 idle") {
		t.Fatalf("expected aggregate sub-agent report, got: %q", v)
	}
}

func TestScreen_OrchStreamingWithSubAgents(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetThinkingForAgent("orch_1", false, true, false)
	sb.SetThinkingForAgent("sub_1", false, true, false)
	sb.SetThinkingForAgent("sub_2", true, false, false)
	sb.SetCurrentAgent("orch_1")

	if n := sb.ActiveAgentCount(); n != 3 {
		t.Fatalf("expected 3 active agents, got %d", n)
	}
	if n := sb.IdleAgentCount(); n != 0 {
		t.Fatalf("expected 0 idle agents, got %d", n)
	}

	v := sb.View()
	if !strings.Contains(v, "3 agents active") {
		t.Fatalf("expected aggregate active-agent report, got: %q", v)
	}
}

func TestScreen_AllAgentsDone(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetThinkingForAgent("orch_1", false, false, false)
	sb.SetThinkingForAgent("sub_1", false, false, false)
	sb.SetCurrentAgent("orch_1")

	if sb.IsSystemBusy() {
		t.Fatal("expected system idle with all agents done")
	}
	if n := sb.ActiveAgentCount(); n != 0 {
		t.Fatalf("expected 0 active agents, got %d", n)
	}
	if n := sb.IdleAgentCount(); n != 2 {
		t.Fatalf("expected 2 idle agents, got %d", n)
	}

	v := sb.View()
	if !strings.Contains(v, "2 agents idle") || strings.Contains(v, "💤") {
		t.Fatalf("expected compact idle-agent report, got: %q", v)
	}
}

func TestScreen_AggregateStatusHasNoPerAgentDetail(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetThinkingForAgent("orch_1", false, true, false)
	sb.SetCurrentAgent("orch_1")

	v := sb.View()
	if !strings.Contains(v, "1 agent active") || strings.Contains(v, "Executing") || strings.Contains(v, "specific/file") {
		t.Fatalf("per-agent detail leaked into aggregate status: %q", v)
	}
}

func TestScreenAggregateStatusDoesNotRotateWittyOrPerAgentText(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)
	sb.SetCurrentAgent("orch_1")
	sb.SetThinkingForAgent("orch_1", true, false, false)

	first := sb.View()
	for range 10 {
		view := sb.View()
		if !strings.Contains(view, "1 agent active") || strings.Contains(view, "Getting next turn ready") {
			t.Fatalf("aggregate status was cluttered by per-agent detail: %q", view)
		}
		for _, unwanted := range []string{"Pondering", "Crunching", "Making Moves", "Bits On The Wire"} {
			if strings.Contains(view, unwanted) {
				t.Fatalf("witty status %q survived in %q", unwanted, view)
			}
		}
		if view != first || strings.Contains(view, "●") || strings.Contains(view, "○") {
			t.Fatalf("status bar activity must remain static and sign-free: %q", view)
		}
	}
}

func TestScreen_ErrorPriority(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetThinkingForAgent("orch_1", false, true, false)
	sb.SetError("something went wrong")

	v := sb.View()
	if !strings.Contains(v, "✕ something went wrong") {
		t.Fatalf("expected error message in view, got: %q", v)
	}
}

func TestScreen_InfoHiddenWhenActive(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetThinkingForAgent("orch_1", false, true, false)
	sb.SetInfo("✓ done")

	v := sb.View()
	if strings.Contains(v, "✓ done") {
		t.Fatalf("expected info 'done' to be hidden when agents active, got: %q", v)
	}
}

func TestScreen_InfoShownWhenIdle(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetInfo("✓ done")

	v := sb.View()
	if !strings.Contains(v, "✓ done") {
		t.Fatalf("expected info 'done' when idle, got: %q", v)
	}
}

func TestScreen_Confirming(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetConfirming(true)
	sb.SetThinkingForAgent("orch_1", false, true, false)

	v := sb.View()
	if !strings.Contains(v, "⚠ Agent needs confirmation") {
		t.Fatalf("expected explicit confirmation request in view, got: %q", v)
	}
}

func TestScreen_Blocking(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetThinking(true, false, true)

	v := sb.View()
	if !strings.Contains(v, "⚠ Agent awaiting input") {
		t.Fatalf("expected 'awaiting input' in view, got: %q", v)
	}
}

func TestScreen_SubAgentBlocking(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetThinkingForAgent("orch_1", false, false, false)
	sb.SetThinkingForAgent("sub_1", false, false, true)

	v := sb.View()
	if !strings.Contains(v, "⚠ Agent awaiting input") {
		t.Fatalf("expected aggregate waiting state in view, got: %q", v)
	}
}

func TestScreen_BTWCount(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetBtwCount(3)

	v := sb.View()
	if !strings.Contains(v, "📝3") {
		t.Fatalf("expected '📝3' in view, got: %q", v)
	}
}

func TestScreen_LSPDiagnostics(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetProjectInsights(editor.ProjectInsights{
		ServerName:  "gopls",
		ServerReady: true,
		Errors:      2,
		Warnings:    1,
	})

	v := sb.View()
	if !strings.Contains(v, "✕2") {
		t.Fatalf("expected '✕2' in view, got: %q", v)
	}
	if !strings.Contains(v, "⚠1") {
		t.Fatalf("expected '⚠1' in view, got: %q", v)
	}
	if !strings.Contains(v, "gopls") {
		t.Fatalf("expected 'gopls' server name in view, got: %q", v)
	}
}

func TestScreen_TokenUsage(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.UpdateUsage(100, 50, 10, 0.005, 4096, 2048, false, false)

	v := sb.View()
	if !strings.Contains(v, "↑100") {
		t.Fatalf("expected prompt token count in view, got: %q", v)
	}
	if !strings.Contains(v, "↓50") {
		t.Fatalf("expected completion token count in view, got: %q", v)
	}
	if !strings.Contains(v, "50%") {
		t.Fatalf("expected context usage percentage in view, got: %q", v)
	}
}

func TestScreen_OllamaCacheBlindCostIsShownAsUpperBound(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)
	sb.UpdateUsage(338000, 2100, 0, 2.02, 1000000, 338000, false, true)
	if usage := sb.UsageView(); !strings.Contains(usage, "≤$2.02") || !strings.Contains(usage, "cache ?") {
		t.Fatalf("cache-blind cost did not render as an upper bound: %q", usage)
	}
}

func TestScreenSplitsActivityFromHeaderUsage(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	if got := sb.ActivityView(); strings.Contains(got, "ready") {
		t.Fatalf("bottom activity view should not contain readiness: %q", got)
	}
	if got := sb.UsageView(); !strings.Contains(got, "ready") {
		t.Fatalf("header usage view should contain readiness: %q", got)
	}

	sb.UpdateUsage(1200, 300, 0, 0.02, 4000, 1500, false, false)
	usage := sb.UsageView()
	if !strings.Contains(usage, "↑1.2K") || !strings.Contains(usage, "$0.0200") {
		t.Fatalf("header usage should carry token and cost state: %q", usage)
	}
	if got := sb.ActivityView(); strings.Contains(got, "↑1.2K") || strings.Contains(got, "$0.0200") {
		t.Fatalf("bottom activity view should not duplicate usage: %q", got)
	}
}

func TestScreen_ClearAgentState(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetThinkingForAgent("orch_1", true, false, false)
	sb.SetCurrentAgent("orch_1")

	if n := sb.ActiveAgentCount(); n != 1 {
		t.Fatalf("expected 1 active agent before clear, got %d", n)
	}

	sb.ClearAgentState("orch_1")

	if sb.IsSystemBusy() {
		t.Fatal("expected system idle after clearing agent")
	}
}

func TestScreen_Retry(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetRetry("retrying in 10s... (attempt 2)")

	v := sb.View()
	if !strings.Contains(v, "retrying") {
		t.Fatalf("expected retry message in view, got: %q", v)
	}
}

func TestScreen_ErrorExpiry(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetError("oops")
	sb.errorTime = time.Now().Add(-15 * time.Second)
	sb.ClearExpired()

	v := sb.View()
	if strings.Contains(v, "oops") {
		t.Fatal("expected error to be cleared after expiry")
	}
}

func TestScreen_InfoExpiry(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetInfo("done")
	sb.infoMsgTime = time.Now().Add(-10 * time.Second)
	sb.ClearExpired()

	v := sb.View()
	if strings.Contains(v, "done") {
		t.Fatal("expected info to be cleared after expiry")
	}
}

func TestScreen_GlobalFallback(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetThinking(true, false, false)

	if !sb.IsSystemBusy() {
		t.Fatal("expected system busy via global flag fallback")
	}
	if n := sb.ActiveAgentCount(); n != 1 {
		t.Fatalf("expected 1 active from global flag, got %d", n)
	}
}

func TestScreen_PerAgentOverridesGlobal(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetThinking(true, false, false)
	sb.SetThinkingForAgent("orch_1", false, false, false)

	if sb.IsSystemBusy() {
		t.Fatal("expected system idle when per-agent states override global")
	}
}

func TestScreen_AgentStatusView(t *testing.T) {
	sb := newTestScreen()

	sb.SetThinkingForAgent("orch_1", false, true, false)
	v := sb.AgentStatusView("orch_1")
	if v == "" {
		t.Fatal("expected non-empty status view for streaming agent")
	}

	v2 := sb.AgentStatusView("nonexistent")
	if v2 != "" {
		t.Fatal("expected empty status view for nonexistent agent")
	}

	v3 := sb.AgentStatusView("")
	if v3 != "" {
		t.Fatal("expected empty status view for empty agentID")
	}
}

func TestScreen_IdleWithBTW(t *testing.T) {
	sb := newTestScreen()
	setWidth(sb)

	sb.SetThinkingForAgent("orch_1", false, false, false)
	sb.SetThinkingForAgent("sub_1", false, false, false)
	sb.SetBtwCount(2)

	v := sb.View()
	if !strings.Contains(v, "2 agents idle") || strings.Contains(v, "💤") {
		t.Fatalf("expected aggregate idle state, got: %q", v)
	}
	if !strings.Contains(v, "📝2") {
		t.Fatalf("expected '📝2' in view, got: %q", v)
	}
}

func TestScreen_AgentStatusViewThinking(t *testing.T) {
	sb := newTestScreen()

	sb.SetThinkingForAgent("orch_1", true, false, false)
	v := sb.AgentStatusView("orch_1")
	if v == "" {
		t.Fatal("expected non-empty status view for thinking agent")
	}

	sb.SetThinkingForAgent("orch_1", false, false, false)
	v = sb.AgentStatusView("orch_1")
	if v != "" {
		t.Fatal("expected empty status view for idle agent")
	}
}

func TestScreen_IsSystemBusyConfirming(t *testing.T) {
	sb := newTestScreen()
	sb.SetConfirming(true)
	if !sb.IsSystemBusy() {
		t.Fatal("expected system busy when confirming")
	}
}

func TestScreen_IsSystemBusyBlocking(t *testing.T) {
	sb := newTestScreen()
	sb.SetThinking(false, false, true)
	if !sb.IsSystemBusy() {
		t.Fatal("expected system busy when blocking")
	}
}
