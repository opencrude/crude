package viewmodel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/filetree"
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

func BenchmarkPointerMotionCoalesced(b *testing.B) {
	app := newFullTestApp(b)
	model := filetree.NewFileTreeModel(b.TempDir())
	app.Tree = NewTreeState(model, 30)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(model)
	_ = app.View()
	dividerX := app.activityChromeWidth() + app.Tree.Width
	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: dividerX, Y: 8, Button: tea.MouseLeft})
	app.frameGate.lastPresented = time.Now()
	app.frameGate.wakePending = true
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		_, _ = app.Update(tea.MouseMotionMsg{X: dividerX + (i % 10), Y: 8, Button: tea.MouseLeft})
		_ = app.View()
	}
}

func BenchmarkAgentSwitchWarm_80Msgs(b *testing.B) {
	app := newFullTestApp(b)
	bodyA := strings.Repeat("cached first ", 12)
	bodyB := strings.Repeat("cached second ", 12)
	app.AddOrchestrator("first")
	for _, msg := range seedBudgetSession(80, 2) {
		app.appendAgentMessage("first", msg)
	}
	app.switchToAgentView("first")
	app.syncAgentMessages("first", false)
	app.paintShellFrame()
	app.AddOrchestrator("second")
	for _, msg := range seedBudgetSession(80, 2) {
		app.appendAgentMessage("second", msg)
	}
	app.appendAgentMessage("first", ttypes.ChatMessage{InternalID: "a", Role: ttypes.RoleAssistant, Content: bodyA})
	app.appendAgentMessage("second", ttypes.ChatMessage{InternalID: "b", Role: ttypes.RoleAssistant, Content: bodyB})
	app.switchToAgentView("second")
	app.syncAgentMessages("second", false)
	app.paintShellFrame()
	next := "first"
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		app.switchToAgentView(next)
		app.syncAgentMessages(next, false)
		app.paintShellFrame()
		if next == "first" {
			next = "second"
		} else {
			next = "first"
		}
	}
}

func BenchmarkSidebarResizePresentedFrame_500Msgs(b *testing.B) {
	app := newFullTestApp(b)
	app.AddOrchestrator("root")
	for _, msg := range seedBudgetSession(500, 2) {
		app.appendActiveMessage(msg)
	}
	if node := app.AgentMgr.Get(app.AgentMgr.ActiveID()); node != nil && node.State != nil && node.State.Messages != nil {
		node.State.Messages.ClearTurnStatus()
	}
	app.paintShellFrame()
	tree := filetree.NewFileTreeModel(b.TempDir())
	app.Tree = NewTreeState(tree, 30)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(tree)
	_ = app.View()
	divider := app.activityChromeWidth() + app.Tree.Width
	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: divider, Y: 8, Button: tea.MouseLeft})
	app.forceFrame()
	_ = app.View()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		_, _ = app.dispatchMouseMotion(tea.MouseMotionMsg{X: divider + (i % 10), Y: 8, Button: tea.MouseLeft})
		app.forceFrame()
		_ = app.View()
	}
}

func BenchmarkSidebarResizeMutation_500Msgs(b *testing.B) {
	app := newFullTestApp(b)
	app.AddOrchestrator("root")
	for _, msg := range seedBudgetSession(500, 2) {
		app.appendActiveMessage(msg)
	}
	if node := app.AgentMgr.Get(app.AgentMgr.ActiveID()); node != nil && node.State != nil && node.State.Messages != nil {
		node.State.Messages.ClearTurnStatus()
	}
	app.paintShellFrame()
	tree := filetree.NewFileTreeModel(b.TempDir())
	app.Tree = NewTreeState(tree, 30)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(tree)
	_ = app.View()
	divider := app.activityChromeWidth() + app.Tree.Width
	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: divider, Y: 8, Button: tea.MouseLeft})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		_, _ = app.dispatchMouseMotion(tea.MouseMotionMsg{X: divider + (i % 10), Y: 8, Button: tea.MouseLeft})
	}
}

func BenchmarkSidebarResizePresentedFrame_EmptyChat(b *testing.B) {
	app := newFullTestApp(b)
	tree := filetree.NewFileTreeModel(b.TempDir())
	app.Tree = NewTreeState(tree, 30)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(tree)
	_ = app.View()
	divider := app.activityChromeWidth() + app.Tree.Width
	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: divider, Y: 8, Button: tea.MouseLeft})
	app.forceFrame()
	_ = app.View()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		_, _ = app.dispatchMouseMotion(tea.MouseMotionMsg{X: divider + (i % 10), Y: 8, Button: tea.MouseLeft})
		app.forceFrame()
		_ = app.View()
	}
}

func benchmarkDenseChatSingleRowChange(b *testing.B, count int) {
	app := newFullTestApp(b)
	app.AddOrchestrator("root")
	for _, msg := range seedBudgetSession(count, 2) {
		app.appendActiveMessage(msg)
	}
	animated := ttypes.ChatMessage{InternalID: "animated-tail", Role: ttypes.RoleAssistant, Content: "animated stable body", Time: time.Now()}
	app.appendActiveMessage(animated)
	app.handleTabSwitch()
	_ = app.View()
	renderer := app.activeMessageList()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		renderer.InvalidateItem(animated.InternalID)
		app.forceFrame()
		_ = app.View()
	}
}
func BenchmarkDenseChatSingleRowChange_50Msgs(b *testing.B) { benchmarkDenseChatSingleRowChange(b, 50) }
func BenchmarkDenseChatSingleRowChange_500Msgs(b *testing.B) {
	benchmarkDenseChatSingleRowChange(b, 500)
}

func BenchmarkDenseChatWidthCommit_500HeavyMsgs(b *testing.B) {
	app := newFullTestApp(b)
	app.AddOrchestrator("root")
	body := strings.Repeat("A heavyweight markdown paragraph with `inline code`, unicode 界, and wrapping content for resize.\n", 12)
	for i := 0; i < 500; i++ {
		app.appendActiveMessage(ttypes.ChatMessage{InternalID: fmt.Sprintf("heavy-%d", i), Role: ttypes.RoleAssistant, Content: body, Time: time.Now()})
	}
	tree := filetree.NewFileTreeModel(b.TempDir())
	app.Tree = NewTreeState(tree, 30)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(tree)
	_ = app.View()
	direction := 1
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		b.StopTimer()
		divider := app.activityChromeWidth() + app.Tree.Width
		_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: divider, Y: 8, Button: tea.MouseLeft})
		_, _ = app.dispatchMouseMotion(tea.MouseMotionMsg{X: divider + direction, Y: 8, Button: tea.MouseLeft})
		app.forceFrame()
		_ = app.View()
		b.StartTimer()
		_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{X: divider + direction, Y: 8, Button: tea.MouseLeft})
		app.forceFrame()
		_ = app.View()
		direction = -direction
	}
}

// BenchmarkTabRederive_80Msgs measures syncAgentWindowTabs' per-tick
// re-derivation of the active agent tab. Focus-only derivation preserves
// source-version-owned caches and should be allocation-quiet.
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
