package viewmodel

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"crude/shellport"
	"crude/tui/cell"
	"crude/tui/ui"
	"crude/types"
	"crude/types/reasoning"
	"crude/workspace"
)

func launcherClockFixture(t testing.TB, mode string) (*App, *streamTestService, workspace.TerminalInfo) {
	t.Helper()
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.width, app.height = 160, 44
	app.onboarding.trust.askedOwner = app.ws
	app.onboarding.trust.askedGen = app.workspaceSwitchGen.Load()
	app.launcherScreen.BeginLoad(app.ws.RootPath())
	service := &streamTestService{}
	info := workspace.TerminalInfo{ID: "clock-terminal", Number: 1, Alive: true}
	if mode != "closed" {
		app.ws = &streamTestWorkspace{Workspace: app.ws, terminal: service}
		app.dockTerminalInfo(info)
		app.applyTerminalFrame(regionTerminalFrame(service, info, 0))
	}
	app.SpinFrame = 0
	app.forceFrame()
	app.View()
	return app, service, info
}

func TestTerminalFocusKeepsChatActivityAnimating(t *testing.T) {
	app, _, _ := regionTerminalFixture(t, 200)
	state := app.ws.AgentMgr().Get("region-root").State
	state.Messages.AppendReasoning(reasoning.Delta{Text: "Inspecting the actual state", Kind: reasoning.Summary})
	state.Running = true
	app.syncAgentMessages("region-root", false)
	shellport.Unwrap(app.ws.(*streamTestWorkspace).Workspace).Status.SetAgent(types.AgentStatusSnapshot{
		Ref: types.ResourceRef{Kind: types.ResourceAgent, ID: "region-root"}, Phase: types.AgentThinking,
	})
	app.SpinFrame = 0
	app.forceFrame()
	app.View()
	group := app.mainPanes.groups[app.Win.ActiveGroup()]
	rect := ui.Rect{X: group.chat.X, Y: group.chat.Y + group.chat.H - 1, W: group.chat.W, H: 1}
	before := clockRegion(app, rect)
	generation := app.activeMessageList().ExportMessageRowsSnapshot(app.chatReflowInnerW).Generation
	app.SpinFrame = 700
	app.subPaneProjections.TickAll(app.SpinFrame)
	app.invalidateMessageRegions(types.TickMsg(time.UnixMilli(700)))
	app.presentImmediateFrame()
	view := app.View()
	if !app.terminalOwnsKeys() || reflect.DeepEqual(before, clockRegion(app, rect)) {
		t.Fatal("terminal focus stopped the neighbouring chat activity animation")
	}
	if app.activeMessageList().ExportMessageRowsSnapshot(app.chatReflowInnerW).Generation != generation {
		t.Fatal("paint-only activity animation invalidated dense transcript content")
	}
	assertRegionFrame(t, app, view)
}

func clockRegion(app *App, rect ui.Rect) []cell.Cell {
	out := make([]cell.Cell, 0, rect.W*rect.H)
	for y := rect.Y; y < rect.Y+rect.H; y++ {
		for x := rect.X; x < rect.X+rect.W; x++ {
			out = append(out, app.mainSurf.Buffer().Get(x, y))
		}
	}
	return out
}

func TestLauncherAnimationKeepsAdvancingWithTerminalFocus(t *testing.T) {
	for _, size := range [][2]int{{160, 44}, {80, 30}} {
		for _, mode := range []string{"closed", "idle", "stream"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], mode), func(t *testing.T) {
				app, service, info := launcherClockFixture(t, mode)
				app.width, app.height = size[0], size[1]
				app.termDockHeight = 8
				app.forceFrame()
				app.View()
				rect, mounted := app.rootHost.ScreenRect("launcher/rule")
				if !mounted || rect.Empty() {
					t.Fatal("visible loading rule is not mounted")
				}
				if !app.wantsFastTick() {
					t.Fatal("loading animation lost the shared fast clock")
				}
				before := clockRegion(app, rect)
				changed := 0
				for frame := 1; frame <= 30; frame++ {
					if mode == "stream" {
						app.Update(regionTerminalFrame(service, info, frame))
					}
					// The same deterministic clock boundary used by the caret
					// regressions: source time advances, not focus or pointer input.
					app.SpinFrame = frame * 100
					app.invalidateMessageRegions(types.TickMsg(time.UnixMilli(int64(app.SpinFrame))))
					app.presentImmediateFrame()
					app.View()
					after := clockRegion(app, rect)
					if !reflect.DeepEqual(before, after) {
						changed++
					}
					before = after
				}
				t.Logf("%s: loading animation advanced in %d of 30 presented clock frames", mode, changed)
				if changed < 25 {
					t.Fatalf("opening a terminal froze another visible animation: %d/30 changed frames", changed)
				}
				if mode != "closed" && !app.terminalOwnsKeys() {
					t.Fatal("test lost terminal focus")
				}
			})
		}
	}
}

func BenchmarkLauncherClockWithTerminal(b *testing.B) {
	for _, mode := range []string{"closed", "idle", "stream"} {
		b.Run(mode, func(b *testing.B) {
			app, service, info := launcherClockFixture(b, mode)
			b.ReportAllocs()
			b.ResetTimer()
			for frame := 1; b.Loop(); frame++ {
				if mode == "stream" {
					app.Update(regionTerminalFrame(service, info, frame))
				}
				app.SpinFrame = frame * 16
				app.invalidateMessageRegions(types.TickMsg(time.UnixMilli(int64(app.SpinFrame))))
				app.presentImmediateFrame()
				app.View()
			}
			b.ReportMetric(float64(app.frameGate.presented), "frames")
		})
	}
}
