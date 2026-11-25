package viewmodel

import (
	"strconv"
	"strings"
	"testing"

	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/conversation"
	"crude/workspace"
)

func denseChildPanes(t testing.TB, perAgent int) (*App, []*workspace.SubPane) {
	t.Helper()
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	panes := make([]*workspace.SubPane, 4)
	for i := range panes {
		id := "child-" + strconv.Itoa(i)
		pane := app.ws.PaneMgr().Ensure(id, workspace.PaneAgent, id)
		app.ws.AgentMgr().AddSubAgent(id, "root", id, "model", "crafter", workspace.NewAgentState(id, pane.Messages, app.ws.Logger()))
		app.ensureAgentProjection(id)
		for j := range perAgent {
			msg := conversation.Message{Role: conversation.RoleAssistant, Content: "Read the evidence and check the result."}
			if j%4 == 0 {
				msg = conversation.Message{Role: conversation.RoleTool, ToolName: "read_file", ToolArgs: `{"path":"main.go"}`, ToolStatus: "success", ToolPreviewPhase: "done"}
			}
			pane.Messages.AppendMessage(msg)
		}
		// An active tool animates without changing the retained history.
		pane.Messages.AppendMessage(conversation.Message{Role: conversation.RoleTool, ToolName: "go_vet", ToolStatus: conversation.ToolStatusCalling})
		app.syncAgentMessages(id, false)
		panes[i] = pane
	}
	return app, panes
}

// BenchmarkDenseChildPaneSync measures the tick's existing sync entry point
// over four warm children. It deliberately does not time first hydration.
func BenchmarkDenseChildPaneSync(b *testing.B) {
	app, panes := denseChildPanes(b, 2000)
	b.ReportAllocs()
	for b.Loop() {
		for _, pane := range panes {
			app.syncAgentMessages(pane.ID, false)
		}
	}
}

// BenchmarkDenseChildPanePaint measures the inline compact projection across
// animation frames while no transcript edits have arrived.
func BenchmarkDenseChildPanePaint(b *testing.B) {
	app, panes := denseChildPanes(b, 2000)
	for _, pane := range panes {
		app.subPaneProjections.Render(pane.ID, pane, 80, 8, 0, true)
	}
	b.ReportAllocs()
	for frame := 0; b.Loop(); frame += ui.ActivityBlinkPhaseMillis {
		for _, pane := range panes {
			app.subPaneProjections.Render(pane.ID, pane, 80, 8, frame, true)
		}
	}
}

// BenchmarkDenseChildPaneUpdate measures the shell replica's sync plus the
// first compact render, not constructing or delivering the source delta.
func BenchmarkDenseChildPaneUpdate(b *testing.B) {
	const width, height = 80, 8
	commandResult := `{"output_descriptor":{"status":"completed"},"output":` +
		strconv.Quote(strings.Repeat("ok   example.org/project/package   0.013s\n", 512)) + `}`
	for _, stage := range []string{"TextDelta", "ToolResult", "OffscreenToolResult"} {
		b.Run(stage, func(b *testing.B) {
			pane := workspace.NewSubPane("child", workspace.PaneAgent, "Child")
			reg := newSubPaneProjectionRegistry(theme.DefaultDark()).ForOwner("parent")
			for i := range 200 {
				msg := conversation.Message{Role: conversation.RoleTool, ToolStatus: conversation.ToolStatusSuccess, ToolPreviewPhase: conversation.ToolPreviewDone}
				if i%2 == 0 {
					msg.ToolName, msg.ToolArgs = "apply_patch", "source.go"
					msg.Content = "```diff\n--- a/source.go\n+++ b/source.go\n@@ -1 +1 @@\n-return count\n+return count + " + strconv.Itoa(i) + "\n```"
				} else {
					msg.ToolName, msg.ToolCommand, msg.Content = "execute_command", "go test ./...", commandResult
				}
				pane.Messages.AppendMessage(msg)
			}
			call := conversation.Message{InternalID: "live", Role: conversation.RoleTool, ToolName: "execute_command", ToolCommand: "go test ./live", ToolStatus: conversation.ToolStatusCalling, ToolPreviewPhase: conversation.ToolPreviewStreaming}
			result := call
			result.ToolStatus, result.ToolPreviewPhase, result.Content = conversation.ToolStatusSuccess, conversation.ToolPreviewDone, commandResult
			pane.Messages.AppendMessage(call)
			pane.Messages.AppendMessage(conversation.Message{Role: conversation.RoleAssistant, Content: "Checking"})
			render := func() {
				// Embedded Render synchronizes from the workspace before painting.
				if got := reg.Render(pane.ID, pane, width, height, 0, true); len(got) != height {
					b.Fatalf("compact pane returned %d lines, want %d", len(got), height)
				}
			}
			if stage == "OffscreenToolResult" {
				render = offscreenChildExport(b, reg, pane)
			}
			render()
			install := func(message conversation.Message) {
				base := pane.Messages.Version()
				if err := pane.Messages.InstallDelta(workspace.ConversationDelta{BaseRevision: base, Revision: base + 1, Upserts: []conversation.Message{message}}); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				if stage != "TextDelta" {
					// Restore the same in-flight call and its warm presentation.
					install(call)
					render()
					install(result)
				} else {
					pane.Messages.AppendToken(" more")
				}
				b.StartTimer()
				render()
			}
		})
	}
}

// offscreenChildExport keeps a live child above 2,000 parent messages while
// following the bottom. Export includes the child's sync, blink and paint.
func offscreenChildExport(b *testing.B, reg *subPaneProjectionRegistry, pane *workspace.SubPane) func() {
	b.Helper()
	parent := chatprojection.NewRenderer(theme.DefaultDark())
	parent.SetSize(80, 24)
	parent.SetChatAreaBounds(0, 24)
	scroll := ui.NewScrollModel()
	scroll.SetAutoScroll(true)
	parent.BindScroll(scroll)
	parent.SetSubPaneContentFn(func(id string, w, h, frame int, expanded bool) []chatprojection.SubPaneLine {
		return reg.Render(id, pane, w, h, frame, expanded)
	})
	messages := make([]conversation.ChatMessage, 2001)
	messages[0] = conversation.ChatMessage{InternalID: "owner", Role: conversation.RoleTool, SubPaneID: pane.ID}
	for i := 1; i < len(messages); i++ {
		messages[i] = conversation.ChatMessage{InternalID: strconv.Itoa(i), Role: conversation.RoleUser, Content: "Check this source"}
	}
	parent.SyncMessages(messages, 1, 1)
	for {
		parent.ExportMessageRowsSnapshot(80)
		if !parent.Materializing() {
			break
		}
	}
	scroll.SetContent(parent.ContentHeight())
	scroll.SetViewport(24)
	parent.ScrollToBottom()
	snapshot := parent.ExportMessageRowsSnapshot(80)
	if scroll.Offset() < len(snapshot.Rows[0].Lines) {
		b.Fatal("dense child is not offscreen")
	}
	frame := 0
	return func() {
		frame += ui.ActivityBlinkPhaseMillis
		parent.SetSpinFrame(frame)
		parent.InvalidateSubPane(pane.ID)
		parent.ExportMessageRowsSnapshot(80)
		if proj := reg.Existing(pane.ID); proj.renderer.SourceVersion() != pane.Messages.Version() {
			b.Fatal("parent export did not install the latest child update")
		}
	}
}
