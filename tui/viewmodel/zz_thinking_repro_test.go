package viewmodel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	ttypes "crude/tui/types"
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
				b.WriteString(fmt.Sprintf("[%d:%c]", x, ch))
			}
		}
		t.Logf("row %02d: %s", y, b.String())
	}
}

// trackColumns returns columns in a row range that contain '░' or '█'.
func trackColumns(app *App, y0, y1 int) map[int]rune {
	buf := app.mainSurf.Buffer()
	cols := map[int]rune{}
	for y := y0; y < y1 && y < buf.Height(); y++ {
		for x := 0; x < app.width; x++ {
			c := buf.Get(x, y)
			if c.Char == '░' || c.Char == '█' {
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
		id,
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
}

func TestZZThinkingScrollbarGeometry(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.agentChatScroll = make(map[string]agentMainChatScrollSnapshot)
	setupReproAgent(t, app, "repro-agent")
	node := app.AgentMgr.Get("repro-agent")
	agent := node.State

	// Long chat so the MAIN chat overflows too.
	fillAgentChat(app, "repro-agent", 6, "fill")

	// Promote thinking via the real threshold path.
	app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "repro-agent", Data: thinkingLines(40)})
	if agent.ThinkingPaneID == "" {
		t.Fatal("thinking was not promoted")
	}
	for i := 0; i < 10; i++ {
		app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "repro-agent", Data: thinkingLines(5)})
	}

	// Assistant reply right after the pane.
	agent.Messages.AppendMessage(workspace.Message{
		Role:    ttypes.RoleAssistant,
		Content: "REPLY-MARKER after thinking\nsecond line",
		Time:    time.Now(),
	})
	app.syncAgentMessages("repro-agent", false)

	app.paintShellFrame()

	lay := app.computeContentLayout()
	t.Logf("chatRect=%+v padLR=%d innerW=%d", app.chatRect, lay.PadLR, lay.InnerW)
	dumpChatRows(app, t)

	tracks := trackColumns(app, app.chatRect.Y, app.chatRect.Y+app.chatRect.H)
	t.Logf("track columns (░/█): %v", tracks)
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
	paneID := agent.ThinkingPaneID
	scroll := app.subPaneProjections.ScrollModel(paneID)
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

func TestZZThinkingScrolledWindowStable(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.agentChatScroll = make(map[string]agentMainChatScrollSnapshot)
	setupReproAgent(t, app, "scro-agent")
	node := app.AgentMgr.Get("scro-agent")
	agent := node.State

	app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "scro-agent", Data: thinkingLines(40)})
	if agent.ThinkingPaneID == "" {
		t.Fatal("thinking was not promoted")
	}
	for i := 0; i < 10; i++ {
		app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "scro-agent", Data: thinkingLines(5)})
	}
	agent.Messages.AppendMessage(workspace.Message{
		Role:    ttypes.RoleAssistant,
		Content: "REPLY-MARKER",
		Time:    time.Now(),
	})
	app.syncAgentMessages("scro-agent", false)
	app.paintShellFrame()

	paneID := agent.ThinkingPaneID
	scroll := app.subPaneProjections.ScrollModel(paneID)
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
