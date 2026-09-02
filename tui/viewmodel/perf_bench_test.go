package viewmodel

import (
	"testing"
	"time"

	"crude/tui/editor"
	ttypes "crude/tui/types"
)

// Scenario benches for the work-budget system. Leaf paint benches
// (docs/reference/performance-baselines.md) catch a function getting fatter;
// these catch the "cache dropped per tick turns steady state into a full
// session re-render" class: steady state should measure orders of magnitude
// cheaper than a cold frame. Ceilings live in scripts/perf_ceilings.txt.

// BenchmarkIdleTick_200Msgs measures one idle tick+paint of a warm
// 200-message session — the steady state the user actually lives in on a
// resumed session.
func BenchmarkIdleTick_200Msgs(b *testing.B) {
	app := newFullTestApp(b)
	app.AddOrchestrator("root")
	enableIdleTicksB(b, app)
	for _, msg := range seedBudgetSession(200, 4) {
		app.appendActiveMessage(msg)
	}
	app.paintShellFrame()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		app.handleTick(ttypes.TickMsg(time.Now()))
		app.paintShellFrame()
	}
}

// BenchmarkTabRederive_80Msgs measures syncAgentWindowTabs' per-tick
// re-derivation of the active agent tab — the path the lastTabSwitchAgent
// guard protects. Should be allocation-quiet.
func BenchmarkTabRederive_80Msgs(b *testing.B) {
	app := newFullTestApp(b)
	app.AddOrchestrator("root")
	enableIdleTicksB(b, app)
	for _, msg := range seedBudgetSession(80, 2) {
		app.appendActiveMessage(msg)
	}
	app.handleTabSwitch()
	app.paintShellFrame()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		app.handleTabSwitch()
		app.paintShellFrame()
	}
}

// enableIdleTicksB is enableIdleTicks for benchmarks.
func enableIdleTicksB(tb testing.TB, app *App) {
	if app.EditorState == nil {
		app.EditorState = NewEditorState()
	}
	if app.EditorState.Editor == nil {
		app.EditorState.Editor = editor.New(app.Theme)
	}
}
