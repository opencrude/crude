package viewmodel

import (
	"strings"
	"testing"
	"time"

	"crude/tui/screens/shellchat"
	ttypes "crude/types"
)

func TestComputeContentLayout_AgentChatReservesPlaceholders(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.AddOrchestrator(
		"agent")

	app.appendActiveMessage(ttypes.ChatMessage{
		Role:    ttypes.RoleAssistant,
		Content: "UNIQUE_CHAT_MARKER_xyz",
		Time:    time.Unix(1, 0),
	})

	_ = app.computeContentLayout()
	if app.chatRect.Empty() {
		t.Fatal("expected chatRect to be set for agent chat")
	}
	if app.ChatAreaHeight < 1 {
		t.Fatal("expected ChatAreaHeight > 0")
	}
	if app.chatRect.H != app.ChatAreaHeight {
		t.Fatalf("chatRect.H=%d ChatAreaHeight=%d", app.chatRect.H, app.ChatAreaHeight)
	}
}

func TestRenderShell_PaintsNonEmptyMessages(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24

	app.AddOrchestrator(
		"agent")

	app.appendActiveMessage(ttypes.ChatMessage{
		Role:    ttypes.RoleAssistant,
		Content: "surface paint hello",
		Time:    time.Unix(1, 0),
	})

	app.paintShellFrame()

	if !app.chatHostVisible() {
		t.Fatal("expected chat slot active for agent chat")
	}
	buf := app.mainSurf.Buffer()
	if shellchat.NonEmptyCellCount(buf) == 0 {
		t.Fatal("expected non-empty message content on the shell surface")
	}
	found := false
	for y := app.chatRect.Y; y < app.chatRect.Y+app.chatRect.H && y < buf.Height(); y++ {
		row := shellchat.PlainRow(buf, y, buf.Width())
		if strings.Contains(row, "surface paint hello") || strings.Contains(row, "hello") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected message content painted on the shell surface")
	}
}
