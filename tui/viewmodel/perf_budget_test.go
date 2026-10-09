package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/perf"
	ttypes "crude/tui/types"
)

// Work-budget tests: a CPU-hammering regression in this TUI is almost never a
// single function getting fatter — it is cached state re-dirtied every tick
// (at idle or active cadence), which turns steady state into a full-session re-render. These tests
// pin the work per frame at the pipeline chokepoints via tui/perf counters:
// idle ticks must re-render ~nothing, hydration renders each row once, tab
// re-derivation renders zero, and render work must not scale with session
// size. CacheInvalidations is cheap (a guarded drop touches no item);
// ItemsRendered and LinesRendered are the CPU cost.

// seedBudgetSession returns a realistic mixed-role message set: user turns,
// markdown assistant bodies, finished tool calls, inactive thinking, plus
// statusCount status rows. Status rows legitimately re-render every tick (by
// design, never cached) so the budget tests keep their count FIXED across
// sizes and count them separately (perf.StatusRenders) — only the
// glamour-class re-render work is budgeted.
func seedBudgetSession(count, statusCount int) []ttypes.ChatMessage {
	const paragraph = "The renderer keeps a per-message line cache so idle frames do not " +
		"re-run glamour markdown or word wrapping. This paragraph is long enough to wrap " +
		"across the 80-column test viewport several times, mirroring real assistant output."
	const code = "```go\nfunc cached(msg *ChatMessage) []string {\n\treturn msg.CachedLines\n}\n```"
	msgs := make([]ttypes.ChatMessage, 0, count)
	statusAt := map[int]bool{}
	if statusCount > 0 {
		step := count / statusCount
		if step < 1 {
			step = 1
		}
		for i := 0; i < statusCount && i*step < count; i++ {
			statusAt[i*step] = true
		}
	}
	for i := range count {
		switch {
		case statusAt[i]:
			msgs = append(msgs, ttypes.ChatMessage{Role: ttypes.RoleStatus, Content: "agent turn started", Time: time.Now()})
		case i%10 == 0 || i%10 == 5:
			msgs = append(msgs, ttypes.ChatMessage{Role: ttypes.RoleUser, Content: "question " + fmt.Sprint(i), Time: time.Now()})
		case i%10 == 2 || i%10 == 7:
			msgs = append(msgs, ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: paragraph, Time: time.Now()})
		case i%10 == 3:
			msgs = append(msgs, ttypes.ChatMessage{Role: ttypes.RoleThinking, Content: "considering the cache layout", Time: time.Now()})
		case i%10 == 4:
			msgs = append(msgs, ttypes.ChatMessage{Role: ttypes.RoleTool, ToolName: "grep", ToolStatus: "done", Content: "3 matches in 2 files", Time: time.Now()})
		case i%10 == 6:
			msgs = append(msgs, ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: code + "\n\n" + paragraph, Time: time.Now()})
		case i%10 == 8:
			// One large assistant body per twenty messages, standing in for
			// the session's heavyweight messages.
			msgs = append(msgs, ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: strings.Repeat(paragraph+"\n\n", 3) + code, Time: time.Now()})
		case i%10 == 9:
			msgs = append(msgs, ttypes.ChatMessage{Role: ttypes.RoleTool, ToolName: "read", ToolStatus: "done", Content: strings.Repeat("line of output\n", 8), Time: time.Now()})
		}
	}
	for i := range msgs {
		msgs[i].InternalID = fmt.Sprintf("seed-%d", i)
	}
	// Real sessions end on assistant output, and the trailing assistant
	// message is the per-tick InvalidateCache's favorite victim: pin the
	// final row to the heavyweight shape so that path stays exercised.
	if len(msgs) > 0 && msgs[len(msgs)-1].Role != ttypes.RoleStatus {
		msgs[len(msgs)-1] = ttypes.ChatMessage{
			Role:       ttypes.RoleAssistant,
			Content:    strings.Repeat(paragraph+"\n\n", 3) + code,
			Time:       time.Now(),
			InternalID: fmt.Sprintf("seed-%d", len(msgs)),
		}
	}
	return msgs
}

// enableIdleTicks gives the app the default EditorState pieces handleTick's
// disk-check path touches when the test harness starts with an empty state.
func enableIdleTicks(t *testing.T, app *App) {
	t.Helper()
	if app.EditorState == nil {
		app.EditorState = NewEditorState()
	}
	if app.EditorState.Editor == nil {
		app.EditorState.Editor = editor.New(app.Theme)
	}
}

// idleWindowWork pumps ticks frames of tick+paint and reports the work done.
func idleWindowWork(t *testing.T, app *App, ticks int) perf.Stats {
	t.Helper()
	enableIdleTicks(t, app)
	base := perf.Snapshot()
	for range ticks {
		app.handleTick(ttypes.TickMsg(time.Now()))
		app.paintShellFrame()
	}
	return perf.Snapshot().Sub(base)
}

// Steady-state budgets over pumpTicks idle frames (tick+paint). Only
// glamour-class work counts: status rows and subpanes are counted separately
// by the perf package. There should be nothing to re-render in steady state —
// budgets exist for tolerated edge cases, not for steady churn. A single
// 200-message session re-rendered per tick measures in the tens of thousands
// of lines over the same window.
const (
	pumpTicks         = 25
	itemBudgetPerTick = uint64(4)
	lineBudgetPerTick = uint64(32)
)

// clearTurnTail mirrors a completed agent turn: ClearTurnStatus removes the
// trailing turn-status row the conversation keeps pinned at the visual tail,
// so the session ends on a real (heavyweight) assistant body — exactly the
// shape the guarded trailing-assistant drop in InvalidateCache hunts for.
func clearTurnTail(t *testing.T, app *App) {
	t.Helper()
	if node := app.ws.AgentMgr().Get(app.AgentView.ActiveID()); node != nil && node.State != nil && node.State.Messages != nil {
		node.State.Messages.ClearTurnStatus()
	}
	app.paintShellFrame()
}

// TestIdleTicksStayWithinWorkBudget is the steady-state tripwire: with a real
// session shape hydrated and caches warm, ticking and painting must re-render
// nothing but the by-design short status rows. This catches any code path
// that re-dirties the retained render cache per tick.
func TestIdleTicksStayWithinWorkBudget(t *testing.T) {
	perf.EnableProbe()
	t.Cleanup(perf.DisableProbe)
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	for _, msg := range seedBudgetSession(200, 4) {
		app.appendActiveMessage(msg)
	}
	clearTurnTail(t, app)

	work := idleWindowWork(t, app, pumpTicks)
	if work.ItemsRendered > 4 || work.LinesRendered > 64 {
		t.Fatalf("idle window re-rendered %d items / %d lines over %d ticks (cache_inval=%d); expected ~nothing outside status-row churn — cache dropped per tick?\nprobe:\n%s",
			work.ItemsRendered, work.LinesRendered, pumpTicks, work.CacheInvalidations, perf.ProbeDump())
	}
}

// nilCacheExempt reports whether a message may legitimately hold no cached
// render lines at frame end: status rows (never cached), the streaming
// cursor, pending btw notes, and mid-flight rows (live stream).
func nilCacheExempt(msg *ttypes.ChatMessage, streaming bool, isLast bool) bool {
	if msg.Role == ttypes.RoleStatus || msg.PendingSteering {
		return true
	}
	if isLast && streaming && (msg.Role == ttypes.RoleAssistant || (msg.Role == ttypes.RoleThinking && msg.ThinkingActive)) {
		return true
	}
	if msg.Role == ttypes.RoleTool && msg.ToolStatus == "streaming" {
		return true
	}
	if msg.Role == ttypes.RoleThinking && msg.ThinkingActive {
		return true
	}
	return false
}

// TestRenderCachePersistsAfterIdleWindow is the direct catch of the
// write-to-discarded-copy bug class: after rendering, the render cache must
// persist on the retained messages so subsequent renders are cache hits. A
// cache written to a transient copy is invisible to steady-state budgets
// (items render once, then stay rendered), so this pins the invariant
// directly: after warm-up plus an idle window, every non-exempt message
// holds CachedLines.
func TestRenderCachePersistsAfterIdleWindow(t *testing.T) {
	perf.EnableProbe()
	t.Cleanup(perf.DisableProbe)
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	for _, msg := range seedBudgetSession(200, 4) {
		app.appendActiveMessage(msg)
	}
	app.paintShellFrame()
	_ = idleWindowWork(t, app, pumpTicks)

	project := app.subPaneProjections.GetExisting("root")
	if project == nil || project.renderer == nil {
		t.Fatal("active projection missing")
	}
	streaming := false
	messages := project.renderer.Messages()
	nils := 0
	for i, msg := range messages {
		if msg.CachedLines == nil && !nilCacheExempt(msg, streaming, i == len(messages)-1) {
			nils++
		}
	}
	if nils > 0 {
		t.Fatalf("%d/%d messages lost their render cache over an idle window — cache written to a transient copy?\nprobe:\n%s",
			nils, len(messages), perf.ProbeDump())
	}
}

// TestHydrationRendersOnceThenStaysWarm covers resuming an interrupted
// session: rows persisted mid-flight (streaming tools, active thinking) must
// be transitioned to a dead-stream state on hydration. If the cleanup is
// skipped they stay in their live states forever, and the per-tick
// InvalidateCache re-renders them every frame. A hydrated session is idle by
// definition, so the budget here is strict: ZERO re-rendered rows.
func TestHydrationRendersOnceThenStaysWarm(t *testing.T) {
	const tailParagraph = "The hydrated session ends on assistant output — the shape the per-tick " +
		"trailing-assistant InvalidateCache drop hunts for — so this tail is deliberately " +
		"heavyweight: a reintroduced per-tick drop re-renders all of it every frame."
	perf.EnableProbe()
	t.Cleanup(perf.DisableProbe)
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	msgs := seedBudgetSession(120, 3)
	// Two mid-flight rows with real body volume, as a session interrupted
	// mid-turn would have persisted them: a tool still streaming output and
	// an active thinking block mid-reasoning.
	msgs[30].Role = ttypes.RoleTool
	msgs[30].ToolName = "bash"
	msgs[30].ToolStatus = "streaming"
	msgs[30].Content = strings.Repeat("live tool output line\n", 40)
	msgs[60].Role = ttypes.RoleThinking
	msgs[60].ThinkingActive = true
	msgs[60].Content = strings.Repeat("weighing cache eviction options against session size ", 60)
	// Hydration routes through the same append path, so (as on the live path)
	// the seeded tail is a still-pinned turn-status row; clearTurnTail below
	// removes it. Pin a heavyweight assistant body second-to-last so that,
	// once the status row is gone, the session ends on the exact shape the
	// guarded trailing-assistant drop in InvalidateCache hunts for.
	msgs[len(msgs)-1] = ttypes.ChatMessage{
		Role:       ttypes.RoleAssistant,
		Content:    strings.Repeat(tailParagraph+"\n\n", 10),
		Time:       time.Now(),
		InternalID: "seed-tail",
	}
	app.Update(ttypes.HistoryMessagesMsg(msgs))
	clearTurnTail(t, app)

	work := idleWindowWork(t, app, pumpTicks)
	// A hydrated (dead) session re-renders almost nothing per idle tick.
	// Master tolerates a couple of one-off edge rows per window, but a stuck
	// mid-flight row re-renders EVERY tick — so the item ceiling is one
	// tick's worth, not pumpTicks worth.
	if work.ItemsRendered > pumpTicks || work.LinesRendered > lineBudgetPerTick*pumpTicks {
		churn := drainChurnReport(app)
		t.Fatalf("hydrated dead stream keeps re-rendering: items=%d lines=%d over %d idle ticks (cache_inval=%d)\nchurn rows:\n%s\nprobe:\n%s",
			work.ItemsRendered, work.LinesRendered, pumpTicks, work.CacheInvalidations, churn, perf.ProbeDump())
	}
}

// drainChurnReport lists the non-exempt retained rows that hold no render
// cache at frame end (the rows a per-tick loop keeps re-dirtying).
func drainChurnReport(app *App) string {
	project := app.subPaneProjections.GetExisting("root")
	if project == nil || project.renderer == nil {
		return "(no projection)"
	}
	messages := project.renderer.Messages()
	var b strings.Builder
	for i, msg := range messages {
		if msg.CachedLines == nil && !nilCacheExempt(msg, false, i == len(messages)-1) {
			fmt.Fprintf(&b, "\tid=%s role=%s tool=%s status=%q contentLen=%d\n", msg.InternalID, msg.Role, msg.ToolName, msg.ToolStatus, len(msg.Content))
		}
	}
	return b.String()
}

// TestTabRederivationRendersNothing pins the guard: handleTabSwitch runs on
// every tick via syncAgentWindowTabs, and re-deriving the SAME agent tab must
// not dirty the projection (renders happen only on real transitions).
func TestTabRederivationRendersNothing(t *testing.T) {
	perf.EnableProbe()
	t.Cleanup(perf.DisableProbe)
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	for _, msg := range seedBudgetSession(80, 2) {
		app.appendActiveMessage(msg)
	}
	app.handleTabSwitch()
	app.paintShellFrame()

	work := idleWindowWork(t, app, pumpTicks)
	if work.ItemsRendered != 0 || work.LinesRendered != 0 {
		t.Fatalf("same-tab re-derivation re-rendered %d items / %d lines (cache_inval=%d) — should be zero\nprobe:\n%s",
			work.ItemsRendered, work.LinesRendered, work.CacheInvalidations, perf.ProbeDump())
	}
}

func TestAgentToAgentSwitchKeepsBothProjectionsWarm(t *testing.T) {
	perf.EnableProbe()
	t.Cleanup(perf.DisableProbe)
	app := newFullTestApp(t)
	app.AddOrchestrator("first")
	app.appendAgentMessage("first", ttypes.ChatMessage{InternalID: "first-body", Role: ttypes.RoleAssistant, Content: strings.Repeat("first cached body ", 20), Time: time.Now()})
	app.switchToAgentView("first")
	app.syncAgentMessages("first", false)
	app.paintShellFrame()
	app.AddOrchestrator("second")
	app.appendAgentMessage("second", ttypes.ChatMessage{InternalID: "second-body", Role: ttypes.RoleAssistant, Content: strings.Repeat("second cached body ", 20), Time: time.Now()})
	app.switchToAgentView("second")
	app.syncAgentMessages("second", false)
	app.paintShellFrame()
	if cached, _ := tabSwitchCacheProbe(app, "first", strings.Repeat("first cached body ", 20)); !cached {
		t.Fatal("first projection was not warm")
	}
	base := perf.Snapshot()
	app.switchToAgentView("first")
	app.syncAgentMessages("first", false)
	app.paintShellFrame()
	work := perf.Snapshot().Sub(base)
	if work.ItemsRendered != 0 || work.LinesRendered != 0 {
		t.Fatalf("warm agent switch rendered %d items/%d lines\n%s", work.ItemsRendered, work.LinesRendered, perf.ProbeDump())
	}
}

func TestSidebarResizeDefersChatReflowUntilRelease(t *testing.T) {
	perf.EnableProbe()
	t.Cleanup(perf.DisableProbe)
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	for _, msg := range seedBudgetSession(200, 2) {
		app.appendActiveMessage(msg)
	}
	clearTurnTail(t, app)
	tree := filetree.NewFileTreeModel(t.TempDir())
	app.Tree = NewTreeState(tree, 30)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(tree)
	_ = app.View()
	divider := app.activityChromeWidth() + app.Tree.Width
	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: divider, Y: 8, Button: tea.MouseLeft})
	if !app.sidebarResizeActive() {
		t.Fatal("sidebar divider did not capture")
	}
	app.forceFrame()
	_ = app.View()
	initialSnapshot := app.activeMessageList().ExportMessageRowsSnapshot(app.chatReflowInnerW)
	base := perf.Snapshot()
	for i := 0; i < 20; i++ {
		before := app.activeMessageList().ExportMessageRowsSnapshot(app.chatReflowInnerW)
		_, _ = app.dispatchMouseMotion(tea.MouseMotionMsg{X: divider + (i % 10), Y: 8, Button: tea.MouseLeft})
		app.forceFrame()
		_ = app.View()
		after := app.activeMessageList().ExportMessageRowsSnapshot(app.chatReflowInnerW)
		if after.Generation != before.Generation {
			t.Fatalf("presented frame %d changed projection generation %d -> %d\n%s", i, before.Generation, after.Generation, perf.ProbeDump())
		}
	}
	during := perf.Snapshot().Sub(base)
	finalSnapshot := app.activeMessageList().ExportMessageRowsSnapshot(app.chatReflowInnerW)
	if finalSnapshot.Generation != initialSnapshot.Generation {
		t.Fatalf("resize changed projection generation %d -> %d", initialSnapshot.Generation, finalSnapshot.Generation)
	}
	if during.ItemsRendered != 0 || during.LinesRendered != 0 {
		t.Fatalf("resize reflowed chat during drag: %d items/%d lines", during.ItemsRendered, during.LinesRendered)
	}
	_, _ = app.Update(tea.MouseReleaseMsg{X: divider + 9, Y: 8, Button: tea.MouseLeft})
	_ = app.View()
	after := perf.Snapshot().Sub(base)
	if after.ItemsRendered > 220 {
		t.Fatalf("release rendered more than one chat pass: %d", after.ItemsRendered)
	}
}

// TestEditorAgentSwitchWorkBounded: toggling editor/agent is a real
// transition — returning to the agent refreshes (renders at most the session
// once), and while the editor is active the chat must render nothing at all.
// Steady re-derivations after the switch must stay at zero.
func TestEditorAgentSwitchWorkBounded(t *testing.T) {
	perf.EnableProbe()
	t.Cleanup(perf.DisableProbe)
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	msgs := seedBudgetSession(80, 2)
	for _, msg := range msgs {
		app.appendActiveMessage(msg)
	}
	app.handleTabSwitch()
	app.paintShellFrame()

	main := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(main, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.EditorState = NewEditorState()
	app.EditorState.Editor = editor.New(app.Theme)
	app.BufferMgr = editor.NewBufferManager()
	if _, err := app.openEditorTab(main); err != nil {
		t.Fatal(err)
	}

	for range 8 {
		base := perf.Snapshot()
		app.handleTabSwitch()
		app.paintShellFrame()
		roundTrip := perf.Snapshot().Sub(base)
		if roundTrip.ItemsRendered > uint64(len(msgs))+8 || roundTrip.LinesRendered > 4*uint64(len(msgs))+256 {
			t.Fatalf("editor<->agent round trip re-rendered %d items / %d lines; bounded at one session pass\nprobe:\n%s",
				roundTrip.ItemsRendered, roundTrip.LinesRendered, perf.ProbeDump())
		}
		// Whatever the fresh pass rendered, another same-tab handleTabSwitch
		// inside an unchanged frame must not render again.
		steady := idleWindowWork(t, app, 2)
		if steady.ItemsRendered != 0 {
			t.Fatalf("steady re-derivation after switch re-rendered %d items — should be zero\nprobe:\n%s",
				steady.ItemsRendered, perf.ProbeDump())
		}
	}
}

// TestIdleWorkScalesSublinearlyWithSessionSize is the scaling guard: render
// work per idle frame must stay ~flat as the session grows 10x. Failing
// together means an O(N)-per-frame or O(N²) re-render crept back in (virtual
// scrolling regressed), even if every single frame still looks cheap.
func TestIdleWorkScalesSublinearlyWithSessionSize(t *testing.T) {
	perf.EnableProbe()
	t.Cleanup(perf.DisableProbe)
	idleWorkFor := func(count, statusCount int) perf.Stats {
		app := newFullTestApp(t)
		app.AddOrchestrator("root")
		for _, msg := range seedBudgetSession(count, statusCount) {
			app.appendActiveMessage(msg)
		}
		app.paintShellFrame()
		return idleWindowWork(t, app, pumpTicks)
	}
	small := idleWorkFor(50, 4)
	large := idleWorkFor(500, 4)
	itemLimit := 3*small.ItemsRendered + 16
	lineLimit := 3*small.LinesRendered + 256
	if large.ItemsRendered > itemLimit || large.LinesRendered > lineLimit {
		t.Fatalf("idle render work scaled superlinearly: %d -> %d msgs (items %d -> %d [limit %d], lines %d -> %d [limit %d]) — O(N) re-render crept in\nprobe:\n%s",
			50, 500, small.ItemsRendered, large.ItemsRendered, itemLimit, small.LinesRendered, large.LinesRendered, lineLimit, perf.ProbeDump())
	}
	if large.StatusRenders > small.StatusRenders+8 {
		// Status churn is not budgeted, but it must also not depend on
		// session size beyond the seeded status count.
		t.Fatalf("status re-renders scaled with session size: %d -> %d", small.StatusRenders, large.StatusRenders)
	}
}
