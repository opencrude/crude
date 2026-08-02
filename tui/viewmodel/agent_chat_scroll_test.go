package viewmodel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"crude/provider"
	ttypes "crude/tui/types"
	"crude/tui/ui"
	"crude/workspace"
)

func setupAgentChatScrollTest(t *testing.T) *App {
	t.Helper()
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.agentChatScroll = make(map[string]agentMainChatScrollSnapshot)
	return app
}

func addOrchestrator(app *App, id string) {
	app.AddOrchestrator(
		id,
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
}

func fillAgentChat(app *App, agentID string, count int, prefix string) {
	node := app.AgentMgr.Get(agentID)
	if node == nil || node.State == nil || node.State.Messages == nil {
		panic("agent missing for fillAgentChat")
	}
	for i := 0; i < count; i++ {
		node.State.Messages.AppendMessage(workspace.Message{
			Role:    ttypes.RoleAssistant,
			Content: fmt.Sprintf("%s-%d\n%s", prefix, i, strings.Repeat("body line\n", 8)),
			Time:    time.Now(),
		})
	}
	app.syncAgentMessages(agentID, false)
}

func switchToAgentTab(app *App, agentID string) {
	title := app.agentTabTitle(agentID)
	if app.Win.TabByTitle(title) == nil {
		app.Win.OpenTab(title, "", true)
	}
	if tab := app.Win.TabByTitle(title); tab != nil {
		app.Win.SwitchTo(app.Win.IndexOf(tab.ID))
	}
	app.handleTabSwitch()
}

func renderAgentChat(app *App) {
	if app.ChatAreaHeight == 0 {
		app.ChatAreaHeight = app.height - 8
	}
	app.chatRect = ui.Rect{X: 0, Y: 2, W: app.width, H: app.ChatAreaHeight}
	app.configureChatScreen()
}

func TestAgentChatScrollSwitchSaveRestore(t *testing.T) {
	app := setupAgentChatScrollTest(t)
	addOrchestrator(app, "agent-a")
	addOrchestrator(app, "agent-b")
	fillAgentChat(app, "agent-a", 12, "A")
	fillAgentChat(app, "agent-b", 12, "B")

	switchToAgentTab(app, "agent-a")
	renderAgentChat(app)
	scroll := app.chatScreen.ScrollModel()
	scroll.SetAutoScroll(false)
	scroll.ScrollTo(4)
	wantAOff := scroll.Offset()
	wantAAuto := scroll.AutoScroll()

	switchToAgentTab(app, "agent-b")
	renderAgentChat(app)
	if !scroll.AutoScroll() {
		t.Fatal("new agent B should default to armed auto-scroll")
	}
	if scroll.Offset() != scroll.MaxOffset() {
		t.Fatalf("new agent B should land at bottom: offset=%d max=%d", scroll.Offset(), scroll.MaxOffset())
	}

	scroll.SetAutoScroll(false)
	scroll.ScrollTo(2)
	wantBOff := scroll.Offset()

	switchToAgentTab(app, "agent-a")
	renderAgentChat(app)
	if scroll.AutoScroll() != wantAAuto {
		t.Fatalf("restored A auto=%v want %v", scroll.AutoScroll(), wantAAuto)
	}
	if scroll.Offset() != wantAOff {
		t.Fatalf("restored A offset=%d want %d", scroll.Offset(), wantAOff)
	}

	switchToAgentTab(app, "agent-b")
	renderAgentChat(app)
	if scroll.Offset() != wantBOff {
		t.Fatalf("restored B offset=%d want %d", scroll.Offset(), wantBOff)
	}
}

func TestAgentChatScrollStreamingDoesNotAlterSavedSnapshot(t *testing.T) {
	app := setupAgentChatScrollTest(t)
	addOrchestrator(app, "agent-a")
	addOrchestrator(app, "agent-b")
	fillAgentChat(app, "agent-a", 10, "A")
	fillAgentChat(app, "agent-b", 6, "B")

	switchToAgentTab(app, "agent-a")
	renderAgentChat(app)
	scroll := app.chatScreen.ScrollModel()
	scroll.SetAutoScroll(false)
	scroll.ScrollTo(3)

	switchToAgentTab(app, "agent-b")
	renderAgentChat(app)

	nodeB := app.AgentMgr.Get("agent-b")
	nodeB.State.Streaming = true
	projB := app.subPaneProjections.GetExisting("agent-b")
	projB.renderer.SetStreaming(true)
	for i := 0; i < 5; i++ {
		nodeB.State.Messages.AppendMessage(workspace.Message{
			Role:    ttypes.RoleAssistant,
			Content: fmt.Sprintf("stream chunk %d\n", i),
			Time:    time.Now(),
		})
		app.syncAgentMessages("agent-b", true)
		renderAgentChat(app)
	}

	snap, ok := app.agentChatScrollSnapshotForTest("agent-a")
	if !ok {
		t.Fatal("expected saved snapshot for agent-a")
	}
	if snap.AutoScroll {
		t.Fatal("agent-a snapshot should remain disarmed")
	}
	if snap.Offset != 3 {
		t.Fatalf("agent-a snapshot offset=%d want 3", snap.Offset)
	}
}

func TestAgentChatScrollCloseClearsSnapshot(t *testing.T) {
	app := setupAgentChatScrollTest(t)
	addOrchestrator(app, "agent-a")
	fillAgentChat(app, "agent-a", 8, "A")
	switchToAgentTab(app, "agent-a")
	renderAgentChat(app)
	app.chatScreen.ScrollModel().SetAutoScroll(false)
	app.chatScreen.ScrollModel().ScrollTo(2)

	addOrchestrator(app, "agent-b")
	switchToAgentTab(app, "agent-b")
	renderAgentChat(app)

	if _, ok := app.agentChatScrollSnapshotForTest("agent-a"); !ok {
		t.Fatal("expected snapshot for agent-a after switch away")
	}

	app.cleanupAgent("agent-a")
	if _, ok := app.agentChatScrollSnapshotForTest("agent-a"); ok {
		t.Fatal("snapshot should be cleared when agent closes")
	}
	if app.subPaneProjections.Has("agent-a") {
		t.Fatal("subPane projection should be removed when agent closes")
	}
}

func TestAgentChatScrollZeroMessageNewAgentDefaults(t *testing.T) {
	app := setupAgentChatScrollTest(t)
	addOrchestrator(app, "agent-a")
	fillAgentChat(app, "agent-a", 8, "A")
	switchToAgentTab(app, "agent-a")
	renderAgentChat(app)
	app.chatScreen.ScrollModel().SetAutoScroll(false)
	app.chatScreen.ScrollModel().ScrollTo(2)

	addOrchestrator(app, "agent-empty")
	switchToAgentTab(app, "agent-empty")
	renderAgentChat(app)

	scroll := app.chatScreen.ScrollModel()
	if !scroll.AutoScroll() {
		t.Fatal("zero-message new agent should default to armed auto-scroll")
	}
	if scroll.Offset() != scroll.MaxOffset() {
		t.Fatalf("zero-message new agent should land at bottom: offset=%d max=%d", scroll.Offset(), scroll.MaxOffset())
	}
}

func TestAgentChatScrollContentShrinkClampsRestore(t *testing.T) {
	app := setupAgentChatScrollTest(t)
	addOrchestrator(app, "agent-a")
	fillAgentChat(app, "agent-a", 20, "A")

	switchToAgentTab(app, "agent-a")
	renderAgentChat(app)
	scroll := app.chatScreen.ScrollModel()
	scroll.SetAutoScroll(false)
	scroll.ScrollTo(50)
	app.agentChatScroll["agent-a"] = agentMainChatScrollSnapshot{Offset: 50, AutoScroll: false}

	node := app.AgentMgr.Get("agent-a")
	node.State.Messages = workspace.NewConversation()
	node.State.Messages.AppendMessage(workspace.Message{
		Role:    ttypes.RoleAssistant,
		Content: "tiny",
		Time:    time.Now(),
	})
	app.syncAgentMessages("agent-a", false)

	switchToAgentTab(app, "agent-a")
	app.chatScrollAgent = ""
	renderAgentChat(app)

	if scroll.Offset() > scroll.MaxOffset() {
		t.Fatalf("restored offset %d exceeds max %d after shrink", scroll.Offset(), scroll.MaxOffset())
	}
}
