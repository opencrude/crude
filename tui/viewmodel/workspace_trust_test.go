package viewmodel

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types/setting"
	"crude/workspace"
)

func TestWorkspaceTrustPromptPaintAndEscGeneration(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		m := onboardingTestApp(t)
		m.width, m.height = size[0], size[1]
		m.Update(TickMsg{})
		window := m.overlayHost.Window(ui.OverlayWorkspaceTrust)
		if window == nil {
			t.Fatalf("trust prompt absent at %v", size)
		}
		frame := window.Content.(*ui.ComponentWidget)
		w, h := window.Size()
		if w > size[0] || h > size[1] {
			t.Fatalf("prompt exceeds screen %v: %dx%d", size, w, h)
		}
		frame.Host().Resize(w, h)
		frame.Host().Layout()
		buf := surface.NewCellBuffer(w, h)
		frame.Host().Paint(buf)
		text := bufferPlain(buf)
		for _, want := range []string{"Trust this folder?", "Trust lets project tools run code.",
			"Restricted mode allows editing, but not project tools.", "Change this in Workspace settings.", "Trust folder", "Open in restricted mode"} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %q at %v:\n%s", want, size, text)
			}
		}
		if h > 11 || strings.Contains(text, "Do you trust the authors") {
			t.Fatalf("the trust decision is not compact at %v: %d rows\n%s", size, h, text)
		}
		if ui.ConfirmButtonIndexFromID(frame.Host().Focused()) != 1 {
			t.Fatal("restricted choice was not focused")
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
		m.Update(TickMsg{})
		if m.overlayHost.IsOpen(ui.OverlayWorkspaceTrust) || !m.ws.LoadAppSettings().Analysis.DecisionNeeded() {
			t.Fatal("Esc persisted a decision or asked twice")
		}
		m.workspaceSwitchGen.Add(1)
		m.Update(TickMsg{})
		if !m.overlayHost.IsOpen(ui.OverlayWorkspaceTrust) {
			t.Fatal("new generation did not ask again")
		}
	}
}

func TestWorkspaceTrustPaintsOnFirstSizedFrameWithoutSetupReminder(t *testing.T) {
	for _, size := range [][2]int{{160, 48}, {84, 26}} {
		m := newFullTestApp(t)
		wsOf(m).Logger = stubLogger{}
		m.width, m.height = 0, 0
		wsOf(m).SetAppDefaults(setting.App{Onboarding: setting.Onboarding{AISetupReminderHidden: true}})
		m.Init()

		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		paint := m.View().RenderContent()
		if !strings.Contains(paint, "Trust this folder?") || !strings.Contains(paint, "Open in restricted mode") {
			t.Fatalf("the first sized frame at %v did not present the trust choice", size)
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
		m.Update(tea.WindowSizeMsg{Width: size[0] - 1, Height: size[1]})
		if strings.Contains(m.View().RenderContent(), "Trust this folder?") || !m.ws.LoadAppSettings().Analysis.DecisionNeeded() {
			t.Fatal("resizing after Esc reopened the prompt or saved a decision")
		}
	}
}

func TestWorkspaceTrustDoesNotStartOnLayoutAlone(t *testing.T) {
	m := newFullTestApp(t)
	wsOf(m).Logger = stubLogger{}
	m.width, m.height = 0, 0
	wsOf(m).SetAppDefaults(setting.App{Onboarding: setting.Onboarding{AISetupReminderHidden: true}})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if strings.Contains(m.View().RenderContent(), "Trust this folder?") {
		t.Fatal("layout without shell startup opened a modal")
	}
}

func TestWorkspaceTrustSaveBusyAndFailureRetry(t *testing.T) {
	m := onboardingTestApp(t)
	m.Update(TickMsg{})
	cmd := m.activateWorkspaceTrust(1)
	if cmd == nil || !m.onboarding.trust.prompt.busy {
		t.Fatal("save was not started")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if !m.overlayHost.IsOpen(ui.OverlayWorkspaceTrust) {
		t.Fatal("Esc dismissed an in-flight save")
	}
	// Deliver a failure without invoking the save body.
	m.handleWorkspaceTrustMessage(workspaceTrustSavedMsg{prompt: m.onboarding.trust.prompt, result: workspace.PreferencesSaveResult{Err: errors.New("disk full")}})
	if m.onboarding.trust.prompt == nil || !strings.Contains(m.onboarding.trust.prompt.err, "disk full") {
		t.Fatal("failure did not retain an actionable prompt")
	}
	if m.activateWorkspaceTrust(1) == nil {
		t.Fatal("failed save could not be retried")
	}
}

func TestWorkspaceTrustWaitsForAISetupReminder(t *testing.T) {
	m := onboardingTestApp(t)
	loadOnboarding(t, m)
	m.Update(TickMsg{})
	if !m.overlayHost.IsOpen(aiSetupReminderOverlay) || m.overlayHost.IsOpen(ui.OverlayWorkspaceTrust) {
		t.Fatal("trust prompt displaced AI setup")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m.Update(TickMsg{})
	if !m.overlayHost.IsOpen(ui.OverlayWorkspaceTrust) || m.overlayHost.IsOpen(aiSetupReminderOverlay) {
		t.Fatal("trust prompt did not follow AI setup")
	}
}
