package viewmodel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	ttypes "crude/tui/types"
	"crude/types/reasoning"
	"crude/workspace"
)

// dumpChatRows prints every non-space cell with column indices for the chat rect.
func dumpChatRows(app *App, t *testing.T) {
	buf := app.mainSurf.Buffer()
	for y := app.chatRect.Y; y < app.chatRect.Y+app.chatRect.H && y < buf.Height(); y++ {
		var b strings.Builder
		for x := 0; x < app.width; x++ {
			c := buf.Get(x, y)
			ch := ' '
			if c.Char != 0 {
				ch = c.Char
			}
			if ch != ' ' {
				fmt.Fprintf(&b, "[%d:%c]", x, ch)
			}
		}
		t.Logf("row %02d: %s", y, b.String())
	}
}

// trackColumns returns columns in a row range that contain scrollbar chrome.
func trackColumns(app *App, y0, y1 int) map[int]rune {
	buf := app.mainSurf.Buffer()
	cols := map[int]rune{}
	for y := y0; y < y1 && y < buf.Height(); y++ {
		for x := 0; x < app.width; x++ {
			c := buf.Get(x, y)
			if c.Char == '│' || c.Char == '┃' {
				if _, ok := cols[x]; !ok {
					cols[x] = c.Char
				}
			}
		}
	}
	return cols
}

func setupReproAgent(t *testing.T, app *App, id string) {
	t.Helper()
	app.AddOrchestrator(
		id)

}

func TestThinkingScrollbarGeometry(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	setupReproAgent(t, app, "repro-agent")
	node := app.ws.AgentMgr().Get("repro-agent")
	agent := node.State

	// Long chat so the MAIN chat overflows too.
	fillAgentChat(app, "repro-agent", 6, "fill")

	// Promote thinking via the real threshold path.
	app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "repro-agent", Data: reasoning.Delta{Text: thinkingLines(40)}})
	if app.thinkingPaneID(agent) == "" {
		t.Fatal("thinking was not promoted")
	}
	for i := 0; i < 10; i++ {
		app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "repro-agent", Data: reasoning.Delta{Text: thinkingLines(5)}})
	}

	app.paintShellFrame()

	lay := app.computeContentLayout()
	t.Logf("chatRect=%+v padLR=%d innerW=%d", app.chatRect, lay.PadLR, lay.InnerW)
	dumpChatRows(app, t)

	tracks := trackColumns(app, app.chatRect.Y, app.chatRect.Y+app.chatRect.H)
	t.Logf("track columns (│/┃): %v", tracks)
	// Sort columns
	colList := make([]int, 0, len(tracks))
	for c := range tracks {
		colList = append(colList, c)
	}
	t.Logf("track cols sorted: %v", colList)

	// Check gap between pane gutter and main scrollbar.
	if len(colList) < 2 {
		t.Logf("only %d track columns found", len(colList))
	}

	// Wheel over pane content: pane scroll must move, main chat must not.
	paneID := app.thinkingPaneID(agent)
	scroll := app.subPaneProjections.ForOwner(app.AgentView.ActiveID()).ScrollModel(paneID)
	if scroll == nil {
		t.Fatal("pane scroll model missing")
	}
	mainBefore := app.chatScreen.ScrollModel().Offset()
	paneBefore := scroll.Offset()

	// Find an actual pane content row: scan the painted buffer for rows whose
	// col 10 has the pane border '│' AND col 14 has 'x' (thinking content).
	paneRow := -1
	buf := app.mainSurf.Buffer()
	for y := app.chatRect.Y; y < app.chatRect.Y+app.chatRect.H; y++ {
		if buf.Get(10, y).Char == '│' && buf.Get(14, y).Char == 'x' {
			paneRow = y
			break
		}
	}
	if paneRow < 0 {
		t.Fatal("no pane content row found in buffer")
	}
	t.Logf("pane content row=%d", paneRow)

	// Wheel over pane CONTENT (middle of the x's).
	app.rootHost.Handle(tea.MouseWheelMsg{X: 30, Y: paneRow, Button: tea.MouseWheelUp})
	t.Logf("wheel up over pane CONTENT: paneOffset %d -> %d, mainOffset %d -> %d",
		paneBefore, scroll.Offset(), mainBefore, app.chatScreen.ScrollModel().Offset())
	if scroll.Offset() == paneBefore {
		t.Errorf("pane content wheel did not scroll the pane (offset still %d)", scroll.Offset())
	}
	if app.chatScreen.ScrollModel().Offset() != mainBefore {
		t.Errorf("pane content wheel moved main chat: %d -> %d", mainBefore, app.chatScreen.ScrollModel().Offset())
	}

	// Wheel over the pane gutter: pane scrollbar, blank separator, main scrollbar.
	paneBefore = scroll.Offset()
	mainBefore = app.chatScreen.ScrollModel().Offset()
	mainScrollbarX := app.chatRect.X + app.chatRect.W - lay.PadLR - 1
	paneScrollbarX := mainScrollbarX - 2
	app.rootHost.Handle(tea.MouseWheelMsg{X: paneScrollbarX, Y: paneRow, Button: tea.MouseWheelUp})
	t.Logf("wheel up over pane GUTTER: paneOffset %d -> %d, mainOffset %d -> %d",
		paneBefore, scroll.Offset(), mainBefore, app.chatScreen.ScrollModel().Offset())
	if scroll.Offset() == paneBefore {
		t.Errorf("pane gutter wheel did not scroll the pane")
	}
	if app.chatScreen.ScrollModel().Offset() != mainBefore {
		t.Errorf("pane gutter wheel moved main chat: %d -> %d", mainBefore, app.chatScreen.ScrollModel().Offset())
	}
}

func TestThinkingScrolledWindowStable(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	setupReproAgent(t, app, "scro-agent")
	node := app.ws.AgentMgr().Get("scro-agent")
	agent := node.State

	app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "scro-agent", Data: reasoning.Delta{Text: thinkingLines(40)}})
	if app.thinkingPaneID(agent) == "" {
		t.Fatal("thinking was not promoted")
	}
	for i := 0; i < 10; i++ {
		app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "scro-agent", Data: reasoning.Delta{Text: thinkingLines(5)}})
	}
	app.paintShellFrame()

	paneID := app.thinkingPaneID(agent)
	scroll := app.subPaneProjections.ForOwner(app.AgentView.ActiveID()).ScrollModel(paneID)
	if scroll == nil {
		t.Fatal("pane scroll model missing")
	}
	t.Logf("pane scroll content=%d viewport=%d maxOff=%d auto=%v",
		scroll.Content(), scroll.Viewport(), scroll.MaxOffset(), scroll.AutoScroll())
	scroll.SetAutoScroll(false)
	scroll.ScrollTo(3)
	app.paintShellFrame()
	dumpChatRows(app, t)
}

func TestCompletedExpandedThoughtScrollsWithTranscript(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	setupReproAgent(t, app, "done-scroll")
	node := app.ws.AgentMgr().Get("done-scroll")
	state := node.State

	var uniqueThinking strings.Builder
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&uniqueThinking, "reasoning-row-%02d %s\n", i, strings.Repeat("x", 35))
	}
	app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "done-scroll", Data: reasoning.Delta{Text: uniqueThinking.String()}})
	paneID := app.thinkingPaneID(state)
	if paneID == "" {
		t.Fatal("thinking was not promoted")
	}
	app.handleAgentEvent(ttypes.AgentEventMsg{Type: "done", AgentID: "done-scroll", Data: map[string]any{
		"content": "", "reason": "completed",
	}})
	state.Messages.AppendMessage(workspace.Message{
		InternalID: "after-thought", Role: ttypes.RoleAssistant, Content: "assistant after thought", Time: time.Now(),
	})
	app.syncAgentMessages("done-scroll", false)

	thoughtID := ""
	for _, message := range state.Messages.Messages() {
		if message.Role == ttypes.RoleThinking {
			thoughtID = message.InternalID
			break
		}
	}
	if thoughtID == "" || app.ws.PaneMgr().Has(paneID) {
		t.Fatal("completed thought did not return to transcript ownership")
	}
	ml := app.activeMessageList()
	if ml == nil {
		t.Fatal("active message list missing")
	}
	ml.ToggleDisclosure(thoughtID)
	rows, _, _ := ml.ExportMessageRows(80)
	for i := 0; i+1 < len(rows); i++ {
		if rows[i].ID != thoughtID {
			continue
		}
		if len(rows[i].Lines) == 0 || strings.TrimSpace(rows[i].Lines[len(rows[i].Lines)-1]) == "" {
			t.Fatalf("expanded thought ended with an extra spacer: %#v", rows[i].Lines)
		}
		if len(rows[i+1].Lines) < 2 || strings.TrimSpace(rows[i+1].Lines[0]) != "" || strings.TrimSpace(rows[i+1].Lines[1]) == "" {
			t.Fatalf("expanded thought -> assistant spacing is not exactly one row: %#v", rows[i+1].Lines)
		}
	}
	app.paintShellFrame()

	scroll := app.chatScreen.ScrollModel()
	if scroll == nil || scroll.MaxOffset() <= 0 {
		t.Fatalf("expanded thought scroll = %+v", scroll)
	}
	scroll.StickToBottom()
	before := scroll.Offset()
	visibleText := func() string {
		buf := app.mainSurf.Buffer()
		var text strings.Builder
		for screenY := app.chatRect.Y; screenY < app.chatRect.Y+app.chatRect.H; screenY++ {
			for x := app.chatRect.X; x < app.chatRect.X+app.chatRect.W; x++ {
				cell := buf.Get(x, screenY)
				if cell.Char != 0 {
					text.WriteRune(cell.Char)
				}
			}
			text.WriteByte('\n')
		}
		return text.String()
	}
	beforeText := visibleText()
	_, _ = app.dispatchMouseWheel(tea.MouseWheelMsg{
		X: app.chatRect.X + app.Theme.Spacing.PadLR + 8,
		Y: app.chatRect.Y + 2, Button: tea.MouseWheelUp,
	})
	if scroll.Offset() >= before {
		t.Fatalf("completed thought wheel did not scroll: %d -> %d", before, scroll.Offset())
	}
	app.paintShellFrame()
	if afterText := visibleText(); afterText == beforeText {
		t.Fatalf("offset moved but visible thought stayed stale:\n%s", afterText)
	}
}
