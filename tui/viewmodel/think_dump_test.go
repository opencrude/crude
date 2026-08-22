package viewmodel

import (
	"fmt"
	"testing"

	"crude/provider"
	ttypes "crude/tui/types"
)

func TestDumpThinkingPaneFullScreen(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.agentChatScroll = make(map[string]agentMainChatScrollSnapshot)
	app.AddOrchestrator(
		"dump-agent",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
	node := app.AgentMgr.Get("dump-agent")
	agent := node.State
	// Busy chat: pre-fill messages so the main chat itself scrolls.
	fillAgentChat(app, "dump-agent", 5, "fill")
	app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "dump-agent", Data: thinkingLines(40)})
	if agent.ThinkingPaneID == "" {
		t.Fatal("not promoted")
	}
	for i := 0; i < 20; i++ {
		app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "dump-agent", Data: thinkingLines(5)})
	}
	app.paintShellFrame()

	buf := app.mainSurf.Buffer()
	for y := app.chatRect.Y; y < app.chatRect.Y+app.chatRect.H; y++ {
		var line []rune
		var cols []int
		for x := 0; x < app.width; x++ {
			c := buf.Get(x, y)
			ch := ' '
			if c.Char != 0 {
				ch = c.Char
			}
			if ch != ' ' || (c.Char == 0 && !c.Transparent && c.Width > 0) {
				line = append(line, ch)
				cols = append(cols, x)
			}
		}
		fmt.Printf("%02d: ", y)
		for i, ch := range line {
			fmt.Printf("[%d:%c] ", cols[i], ch)
		}
		fmt.Println()
	}
}
