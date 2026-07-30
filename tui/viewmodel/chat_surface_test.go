package viewmodel

import (
	"strings"
	"testing"
	"time"

	"crude/provider"
	"crude/tui/cell"
	"crude/tui/screens/shellchat"
	"crude/tui/surface"
	ttypes "crude/tui/types"
	"crude/tui/ui"
)

func TestBuildMainView_AgentChatReservesPlaceholders(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.AddOrchestrator(
		"agent",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
	app.appendActiveMessage(ttypes.ChatMessage{
		Role:    ttypes.RoleAssistant,
		Content: "UNIQUE_CHAT_MARKER_xyz",
		Time:    time.Unix(1, 0),
	})

	out := app.buildMainView()
	if strings.Contains(out, "UNIQUE_CHAT_MARKER_xyz") {
		t.Fatal("buildMainView must not embed composed chat ANSI; expected blank placeholders")
	}
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

func TestRenderChatToSurface_PaintsNonEmptyMessages(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.chatScreen = shellchat.New(app.Theme)
	app.chatHost = ui.NewHost(app.Theme, app.chatScreen)
	app.mainChatSurf = surface.NewSurface("main-chat", 80, 20, cell.TransparentBlank())

	app.AddOrchestrator(
		"agent",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
	app.appendActiveMessage(ttypes.ChatMessage{
		Role:    ttypes.RoleAssistant,
		Content: "surface paint hello",
		Time:    time.Unix(1, 0),
	})

	_ = app.buildMainView()
	app.renderChatToSurface()

	if !app.mainChatSurf.Visible() {
		t.Fatal("expected mainChatSurf visible for agent chat")
	}
	buf := app.mainChatSurf.Buffer()
	if shellchat.NonEmptyCellCount(buf) == 0 {
		t.Fatal("expected non-empty message content on chat surface")
	}
	found := false
	for y := 0; y < buf.Height(); y++ {
		row := shellchat.PlainRow(buf, y, buf.Width())
		if strings.Contains(row, "surface paint hello") || strings.Contains(row, "hello") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected message content painted on chat surface")
	}
}

func stripANSIForTest(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !(s[j] >= '@' && s[j] <= '~') {
				j++
			}
			if j < len(s) {
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
