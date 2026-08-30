package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func BenchmarkActiveThinkingView_160x40(b *testing.B) {
	app := newFullTestApp(b)
	app.Logger = stubLogger{}
	_, _ = app.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	app.AddOrchestrator("benchmark-agent")
	state := app.AgentMgr.Get("benchmark-agent").State
	state.Running = true
	state.Thinking = true
	state.Messages.AppendThinking(strings.Repeat("reasoning about the active task with enough content to fill the viewport\n", 80))
	app.syncAgentMessages("benchmark-agent", true)
	_ = app.View()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		app.SpinFrame++
		_ = app.View()
	}
}
