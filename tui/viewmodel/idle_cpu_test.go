package viewmodel

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"crude/tui/ui"
	"crude/types"
	"crude/types/recovery"
)

func TestConnectedCompletedTurnIdleKeepsTranscriptPaintRetained(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"SETTLED VISIBLE ANSWER\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	runtime := remoteJourneyRuntime(t, server.URL)
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	id := remoteJourneyCreate(t, shell)
	app := shell.app
	app.Input.SetInput("A completed reply")
	app.setFocusInput()
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return !app.ws.AgentTurnActive(id) && strings.Contains(app.View().RenderContent(), "SETTLED VISIBLE ANSWER")
	})
	app.View()
	group := app.mainPanes.groups[app.Win.ActiveGroup()]
	for range 4 {
		_, _ = app.Update(types.TickMsg(time.Now()))
		view := app.View()
		stats := app.rootHost.LastPaintStats()
		if stats.Full || stats.Cells >= group.chat.W*group.chat.H {
			t.Fatalf("real completed turn repainted static transcript on idle clock: %+v fast=%v activity=%+v frame=%d source=%d generation=%d focus=%s", stats, app.wantsFastTick(), app.ws.AgentActivity(id), app.SpinFrame, group.view.chatSource, group.view.chatGeneration, app.rootHost.Focused())
		}
		assertRegionFrame(t, app, view)
	}
}

func TestIdleClockRetainsStaticTranscriptAndPresentsCaret(t *testing.T) {
	for _, messages := range []int{20, 500} {
		t.Run(fmt.Sprint(messages), func(t *testing.T) {
			app := newFullTestApp(t)
			wsOf(app).Logger = stubLogger{}
			app.Update(tea.WindowSizeMsg{Width: 160, Height: 48})
			app.AddOrchestrator("idle-root")
			for _, msg := range seedBudgetSession(messages, 0) {
				app.appendActiveMessage(msg)
			}
			clearTurnTail(t, app)
			settleHistory(t, app)
			app.View()
			group := app.mainPanes.groups[app.Win.ActiveGroup()]
			app.rootHost.Focus(group.view.input.ID())
			app.SpinFrame = 0
			app.forceFrame()
			first := app.View()
			before := uv.NewScreenBuffer(app.width, app.height)
			first.Surface.Draw(before, before.Bounds())
			presented := app.frameGate.presented
			// Set the deterministic opposite caret phase without depending on
			// sleep/scheduler timing. Invalidate through the actual tick boundary.
			app.SpinFrame = ui.CursorActivityHoldMS + ui.CursorBlinkHalfPeriodMS
			app.Input.SetSpinFrame(app.SpinFrame)
			app.subPaneProjections.TickAll(app.SpinFrame)
			app.invalidateMessageRegions(types.TickMsg(time.Now()))
			view := app.View()
			stats := app.rootHost.LastPaintStats()
			if stats.Full || stats.Cells >= group.chat.W*group.chat.H {
				t.Fatalf("caret repainted static transcript: %+v focus=%q input=%q", stats, app.rootHost.Focused(), group.view.input.ID())
			}
			if app.frameGate.presented != presented+1 {
				t.Fatal("idle presentation was suppressed")
			}
			after := uv.NewScreenBuffer(app.width, app.height)
			view.Surface.Draw(after, after.Bounds())
			changed := false
			for y := group.input.Y; y < group.input.Y+group.input.H; y++ {
				for x := group.input.X; x < group.input.X+group.input.W; x++ {
					changed = changed || !before.CellAt(x, y).Equal(after.CellAt(x, y))
				}
			}
			if !changed {
				t.Fatal("idle caret did not blink")
			}
			assertRegionFrame(t, app, view)
			// Input is an immediate mutation/presentation, not buffered until
			// the next 400 ms heartbeat after leaving idle.
			app.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
			if app.Input.Value() != "z" {
				t.Fatalf("input mutation delayed: %q", app.Input.Value())
			}
			inputView := app.View()
			if !strings.Contains(inputView.RenderContent(), "z") {
				t.Fatal("input did not reach the next presented frame")
			}
			assertRegionFrame(t, app, inputView)
		})
	}
}

func TestIdleFrameSensitiveRowStillPresents(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	app.AddOrchestrator("idle-cursor")
	app.appendActiveMessage(types.ChatMessage{InternalID: "answer", Role: types.RoleAssistant, Content: "Retained answer"})
	app.View()
	// Keep the session idle; renderer-owned clock sensitivity is deliberately
	// independent of anyAgentAnimating and must still invalidate its screen.
	app.SpinFrame = 0
	app.forceFrame()
	app.View()
	group := app.mainPanes.groups[app.Win.ActiveGroup()]
	// A status/recovery row is likewise frame-sensitive while the agent is idle.
	app.appendActiveMessage(types.ChatMessage{InternalID: "status", Role: types.RoleStatus, Recovery: &recovery.Status{ID: "status", State: recovery.Recovering, RetryAt: time.UnixMilli(30000), Summary: "Retrying provider"}})
	app.forceFrame()
	app.View()
	app.SpinFrame = 2000
	app.subPaneProjections.TickAll(app.SpinFrame)
	app.invalidateMessageRegions(types.TickMsg(time.Now()))
	view := app.View()
	if app.rootHost.LastPaintStats().Cells < group.chat.W*group.chat.H {
		t.Fatal("frame-sensitive idle row was not painted")
	}
	assertRegionFrame(t, app, view)
}

// A real entry-point benchmark complements the historical handleTick-only
// benchmark: Update owns damage, and the direct View is what reaches the TTY.
func BenchmarkIdlePresentedFrame(b *testing.B) {
	app := newFullTestApp(b)
	wsOf(app).Logger = stubLogger{}
	app.Update(tea.WindowSizeMsg{Width: 160, Height: 48})
	app.AddOrchestrator("idle-bench")
	for _, msg := range seedBudgetSession(200, 0) {
		app.appendActiveMessage(msg)
	}
	app.paintShellFrame()
	settleHistory(b, app)
	app.View()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		app.Update(types.TickMsg(time.Now()))
		app.View()
	}
}
