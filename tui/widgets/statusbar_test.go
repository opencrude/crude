package widgets

import (
	"strings"
	"testing"
	"time"

	"crude/tui/editor"
	"crude/tui/rendering"
	"crude/tui/theme"
)

func newTestSB() *StatusBar {
	return NewStatusBar(theme.DefaultDark())
}

func setWidth(sb *StatusBar) {
	sb.SetWidth(120)
}

func TestStatusBar_InitialState(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	if sb.IsSystemBusy() {
		t.Fatal("expected system to be idle initially")
	}
	if n := sb.ActiveAgentCount(); n != 0 {
		t.Fatalf("expected 0 active agents, got %d", n)
	}
	if n := sb.SleepingAgentCount(); n != 0 {
		t.Fatalf("expected 0 sleeping agents, got %d", n)
	}

	v := sb.View()
	if !strings.Contains(v, "ready") {
		t.Fatalf("expected 'ready' in initial view, got: %q", v)
	}
}

func TestStatusBar_SingleAgentStreaming(t *testing.T) {
	sb := newTestSB()
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
	if !strings.Contains(v, "⚡1") {
		t.Fatalf("expected '⚡1' in view, got: %q", v)
	}
}

func TestStatusBar_SingleAgentThinking(t *testing.T) {
	sb := newTestSB()
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
	if !strings.Contains(v, "⚡1") {
		t.Fatalf("expected '⚡1' in view, got: %q", v)
	}
}

func TestStatusBar_SubAgentRunningOrchIdle(t *testing.T) {
	sb := newTestSB()
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
	if !strings.Contains(v, "⚡1") {
		t.Fatalf("expected '⚡1' in view (sub-agent), got: %q", v)
	}
}

func TestStatusBar_OrchStreamingWithSubAgents(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	sb.SetThinkingForAgent("orch_1", false, true, false)
	sb.SetThinkingForAgent("sub_1", false, true, false)
	sb.SetThinkingForAgent("sub_2", true, false, false)
	sb.SetCurrentAgent("orch_1")

	if n := sb.ActiveAgentCount(); n != 3 {
		t.Fatalf("expected 3 active agents, got %d", n)
	}
	if n := sb.SleepingAgentCount(); n != 0 {
		t.Fatalf("expected 0 sleeping agents, got %d", n)
	}

	v := sb.View()
	if !strings.Contains(v, "⚡3") {
		t.Fatalf("expected '⚡3' in view, got: %q", v)
	}
}

func TestStatusBar_AllAgentsDone(t *testing.T) {
	sb := newTestSB()
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
	if n := sb.SleepingAgentCount(); n != 2 {
		t.Fatalf("expected 2 sleeping agents, got %d", n)
	}

	v := sb.View()
	if !strings.Contains(v, "\U0001f4a42") {
		t.Fatalf("expected '💤2' in view, got: %q", v)
	}
}

func TestStatusBar_PhaseTag(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	sb.SetThinkingForAgent("orch_1", false, true, false)
	sb.SetPhaseForAgent("orch_1", "executing")
	sb.SetCurrentAgent("orch_1")

	v := sb.View()
	if !strings.Contains(v, "executing") {
		t.Fatalf("expected phase 'executing' in view, got: %q", v)
	}
}

func TestStatusBar_ErrorPriority(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	sb.SetThinkingForAgent("orch_1", false, true, false)
	sb.SetError("something went wrong")

	v := sb.View()
	if !strings.Contains(v, "something went wrong") {
		t.Fatalf("expected error message in view, got: %q", v)
	}
}

func TestStatusBar_InfoHiddenWhenActive(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	sb.SetThinkingForAgent("orch_1", false, true, false)
	sb.SetInfo("✓ done")

	v := sb.View()
	if strings.Contains(v, "✓ done") {
		t.Fatalf("expected info 'done' to be hidden when agents active, got: %q", v)
	}
}

func TestStatusBar_InfoShownWhenIdle(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	sb.SetInfo("✓ done")

	v := sb.View()
	if !strings.Contains(v, "✓ done") {
		t.Fatalf("expected info 'done' when idle, got: %q", v)
	}
}

func TestStatusBar_Confirming(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	sb.SetConfirming(true)
	sb.SetThinkingForAgent("orch_1", false, true, false)

	v := sb.View()
	if !strings.Contains(v, "confirming") {
		t.Fatalf("expected 'confirming' in view, got: %q", v)
	}
}

func TestStatusBar_Blocking(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	sb.SetThinking(true, false, true)

	v := sb.View()
	if !strings.Contains(v, "awaiting input") {
		t.Fatalf("expected 'awaiting input' in view, got: %q", v)
	}
}

func TestStatusBar_SubAgentBlocking(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	sb.SetThinkingForAgent("orch_1", false, false, false)
	sb.SetThinkingForAgent("sub_1", false, false, true)

	v := sb.View()
	if !strings.Contains(v, "sub-agent waiting") {
		t.Fatalf("expected 'sub-agent waiting' in view, got: %q", v)
	}
}

func TestStatusBar_BTWCount(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	sb.SetBtwCount(3)

	v := sb.View()
	if !strings.Contains(v, "📝3") {
		t.Fatalf("expected '📝3' in view, got: %q", v)
	}
}

func TestStatusBar_LSPDiagnostics(t *testing.T) {
	sb := newTestSB()
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

func TestStatusBar_TokenUsage(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	sb.UpdateUsage(100, 50, 10, 0.005, 4096, 2048, false)

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

func TestStatusBarSplitsActivityFromHeaderUsage(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	if got := stripAnsi(sb.ActivityView()); strings.Contains(got, "ready") {
		t.Fatalf("bottom activity view should not contain readiness: %q", got)
	}
	if got := stripAnsi(sb.UsageView()); !strings.Contains(got, "ready") {
		t.Fatalf("header usage view should contain readiness: %q", got)
	}

	sb.UpdateUsage(1200, 300, 0, 0.02, 4000, 1500, false)
	usage := stripAnsi(sb.UsageView())
	if !strings.Contains(usage, "↑1.2K") || !strings.Contains(usage, "$0.0200") {
		t.Fatalf("header usage should carry token and cost state: %q", usage)
	}
	if got := stripAnsi(sb.ActivityView()); strings.Contains(got, "↑1.2K") || strings.Contains(got, "$0.0200") {
		t.Fatalf("bottom activity view should not duplicate usage: %q", got)
	}
}

func TestRenderHeaderPlacesUsageBeforeClock(t *testing.T) {
	th := theme.DefaultDark()
	header := RenderHeader(th, "openrouter/model", false, time.Now().Add(-time.Hour), "ready", "", 90)
	plain := stripAnsi(header)
	usageAt := strings.Index(plain, "ready")
	clockAt := strings.LastIndex(plain, "1h")
	if usageAt < 0 || clockAt < 0 || usageAt >= clockAt {
		t.Fatalf("expected usage before elapsed clock, got %q", plain)
	}

	narrow := RenderHeader(th, strings.Repeat("model", 20), true, time.Now().Add(-time.Hour), strings.Repeat("usage ", 20), strings.Repeat("agent", 20), 50)
	if got, maxWidth := rendering.VisibleWidth(narrow), 50-th.Spacing.PadLR*2; got > maxWidth {
		t.Fatalf("header overflow: width=%d max=%d line=%q", got, maxWidth, stripAnsi(narrow))
	}
}

func TestStatusBar_ClearAgentState(t *testing.T) {
	sb := newTestSB()
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

func TestStatusBar_Retry(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	sb.SetRetry("retrying in 10s... (attempt 2)")

	v := sb.View()
	if !strings.Contains(v, "retrying") {
		t.Fatalf("expected retry message in view, got: %q", v)
	}
}

func TestStatusBar_ErrorExpiry(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	sb.SetError("oops")
	sb.errorTime = time.Now().Add(-15 * time.Second)
	sb.ClearExpired()

	v := sb.View()
	if strings.Contains(v, "oops") {
		t.Fatal("expected error to be cleared after expiry")
	}
}

func TestStatusBar_InfoExpiry(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	sb.SetInfo("done")
	sb.infoMsgTime = time.Now().Add(-10 * time.Second)
	sb.ClearExpired()

	v := sb.View()
	if strings.Contains(v, "done") {
		t.Fatal("expected info to be cleared after expiry")
	}
}

func TestStatusBar_CurrentAgentPhase(t *testing.T) {
	sb := newTestSB()

	sb.SetPhaseForAgent("orch_1", "planning")
	sb.SetCurrentAgent("orch_1")

	if p := sb.currentAgentPhase(); p != "planning" {
		t.Fatalf("expected phase 'planning', got %q", p)
	}
}

func TestStatusBar_CurrentAgentEmpty(t *testing.T) {
	sb := newTestSB()

	sb.SetCurrentAgent("")
	if p := sb.currentAgentPhase(); p != "" {
		t.Fatalf("expected empty phase for empty agent, got %q", p)
	}
}

func TestStatusBar_GlobalFallback(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	sb.SetThinking(true, false, false)

	if !sb.IsSystemBusy() {
		t.Fatal("expected system busy via global flag fallback")
	}
	if n := sb.ActiveAgentCount(); n != 1 {
		t.Fatalf("expected 1 active from global flag, got %d", n)
	}
}

func TestStatusBar_PerAgentOverridesGlobal(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	sb.SetThinking(true, false, false)
	sb.SetThinkingForAgent("orch_1", false, false, false)

	if sb.IsSystemBusy() {
		t.Fatal("expected system idle when per-agent states override global")
	}
}

func TestStatusBar_AgentStatusView(t *testing.T) {
	sb := newTestSB()

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

func TestStatusBar_RecordAndResetActivity(t *testing.T) {
	sb := newTestSB()

	sb.RecordActivity()
	sb.RecordActivityForAgent("orch_1")

	if sb.activityStep == 0 {
		t.Fatal("expected activity step to increment after RecordActivity")
	}

	sb.ResetActivity()
	if sb.activityStep != 0 {
		t.Fatal("expected activity step reset to 0")
	}
}

func TestStatusBar_SleepingWithBTW(t *testing.T) {
	sb := newTestSB()
	setWidth(sb)

	sb.SetThinkingForAgent("orch_1", false, false, false)
	sb.SetThinkingForAgent("sub_1", false, false, false)
	sb.SetBtwCount(2)

	v := sb.View()
	if !strings.Contains(v, "💤2") {
		t.Fatalf("expected '💤2' in view, got: %q", v)
	}
	if !strings.Contains(v, "📝2") {
		t.Fatalf("expected '📝2' in view, got: %q", v)
	}
}

func TestStatusBar_AgentStatusViewThinking(t *testing.T) {
	sb := newTestSB()

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

func TestStatusBar_IsSystemBusyConfirming(t *testing.T) {
	sb := newTestSB()
	sb.SetConfirming(true)
	if !sb.IsSystemBusy() {
		t.Fatal("expected system busy when confirming")
	}
}

func TestStatusBar_IsSystemBusyBlocking(t *testing.T) {
	sb := newTestSB()
	sb.SetThinking(false, false, true)
	if !sb.IsSystemBusy() {
		t.Fatal("expected system busy when blocking")
	}
}
