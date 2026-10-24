package viewmodel

import (
	"crude/provider"
	"crude/sessiondb"
	"crude/tui/rendering"
	"crude/tui/screens/shellchat"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestLatestDenseHydratedWidthReflowPaintsTailAndPreservesOtherPaneAndDraft(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.Tree.Visible = false
	_, _ = app.Update(tea.WindowSizeMsg{Width: 180, Height: 40})
	app.AddOrchestrator("other")
	app.appendAgentMessage("other", types.ChatMessage{InternalID: "other-text", Role: types.RoleAssistant, Content: "OTHER PANE UNCHANGED"})
	app.AddOrchestrator("owner")
	// Enter through resume's authoritative snapshot converter, not fabricated rows.
	stored := make([]sessiondb.MessageWithMeta, 150)
	for i := range stored {
		body := strings.Repeat("Dense hydrated wrapping content with `code` and unicode 界. ", 18)
		if i == len(stored)-1 {
			body += "\n\nACTUAL HYDRATED TAIL"
		}
		stored[i] = sessiondb.MessageWithMeta{Msg: provider.NewTextMessage("assistant", body), Meta: sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []types.ConversationMessage{{InternalID: fmt.Sprintf("saved-%d", i), Role: types.RoleAssistant, Content: body}}}}
	}
	for _, msg := range ChatMessagesFromViews(workspace.ConvertMessagesForDisplay(stored)) {
		app.appendAgentMessage("owner", msg)
	}
	ownerGroup := app.Win.ActiveGroup()
	otherGroup := app.Win.SplitGroup(ownerGroup, ui.Horizontal, false)
	otherTab := app.Win.TabByTitle(app.agentTabTitle("other"))
	app.Win.MoveTabToGroup(otherTab.ID, otherGroup, 0)
	app.focusMainGroup(ownerGroup)
	app.Input.SetInput("draft must survive Latest")
	app.paintShellFrame()
	screen := app.chatScreen
	beforeHeight := screen.ScrollModel().Content()
	otherScreen := app.mainPanes.groups[otherGroup].view.chat
	otherOffset := otherScreen.ScrollModel().Offset()
	otherSelected := app.Win.GroupActiveTab(otherGroup).ID
	// Narrow only geometry: source messages stay unchanged, old offscreen row
	// estimates coexist with materialized new-width visible rows.
	_, _ = app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	app.paintShellFrame()
	if screen.ScrollModel().Content() == beforeHeight {
		t.Fatal("fixture did not change virtual heights on width-only reflow")
	}
	r, ok := app.rootHost.ScreenRect(screen.ID())
	if !ok {
		t.Fatal("owning chat not mounted")
	}
	_, _ = app.dispatchMouseWheel(tea.MouseWheelMsg{X: r.X + 3, Y: r.Y + 2, Button: tea.MouseWheelUp})
	app.paintShellFrame()
	if screen.ScrollModel().AutoScroll() {
		t.Fatal("scroll up did not disarm follow")
	}
	// Locate native painted control instead of duplicating its geometry formula.
	buttonX, buttonY := -1, -1
	for y := r.Y; y < r.Y+r.H; y++ {
		row := shellchat.PlainRow(app.mainSurf.Buffer(), y, app.mainSurf.Buffer().Width())
		if index := strings.Index(row, "[ ↓ Latest ]"); index >= 0 {
			buttonX = rendering.VisibleWidth(row[:index]) + 2
			buttonY = y
			break
		}
	}
	if buttonX < 0 {
		t.Fatal("Latest absent from scrolled-up hydrated pane")
	}
	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: buttonX, Y: buttonY, Button: tea.MouseLeft})
	_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{X: buttonX, Y: buttonY, Button: tea.MouseLeft})
	app.paintShellFrame()
	if !strings.Contains(chatAreaText(app), "ACTUAL HYDRATED TAIL") || !screen.ScrollModel().AutoScroll() {
		t.Fatal("Latest did not materialize and paint actual tail")
	}
	app.appendAgentMessage("owner", types.ChatMessage{InternalID: "stream-append", Role: types.RoleAssistant, Content: "SUBSEQUENT STREAM TAIL"})
	app.paintShellFrame()
	if !strings.Contains(chatAreaText(app), "SUBSEQUENT STREAM TAIL") || !screen.ScrollModel().AutoScroll() {
		t.Fatal("append lost following")
	}
	if app.Input.Value() != "draft must survive Latest" {
		t.Fatal("Latest consumed composer draft")
	}
	if otherScreen.ScrollModel().Offset() != otherOffset || app.Win.GroupActiveTab(otherGroup).ID != otherSelected {
		t.Fatal("Latest changed neighboring pane")
	}
}

func TestLatestRemainsPaintedAcrossLiveTicks(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	app.Tree.Visible = false
	app.AddOrchestrator("owner")
	app.ws.AgentMgr().Get("owner").State.Running = true
	for i := range 12 {
		app.appendAgentMessage("owner", types.ChatMessage{Role: types.RoleAssistant, Content: fmt.Sprintf("### Accepted result %d\n\nChecked state ownership and retained completed work. The draft stays untouched while browsing the plan and older messages.", i+1), Time: time.Now()})
	}
	_, _ = app.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	app.paintShellFrame()
	r, _ := app.rootHost.ScreenRect(app.chatScreen.ID())
	for range 7 {
		_, _ = app.Update(tea.MouseWheelMsg{X: r.X + 20, Y: r.Y + 8, Button: tea.MouseWheelUp})
		app.paintShellFrame()
	}
	for tick := range 20 {
		_, _ = app.Update(types.TickMsg(time.Now().Add(time.Duration(tick) * time.Second)))
		app.paintShellFrame()
		if !bufferContainsPlain(app.mainSurf.Buffer(), "Latest") {
			s := app.chatScreen.ScrollModel()
			t.Fatalf("Latest lost on tick %d: offset=%d max=%d content=%d viewport=%d auto=%v", tick, s.Offset(), s.MaxOffset(), s.Content(), s.Viewport(), s.AutoScroll())
		}
	}
}
