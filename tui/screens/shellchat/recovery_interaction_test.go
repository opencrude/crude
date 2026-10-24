package shellchat

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/recovery"
)

func TestRecoveryRetryClickDisclosureAndDragUseSharedHost(t *testing.T) {
	th := theme.DefaultDark()
	renderer := chatprojection.NewRenderer(th)
	renderer.SetSize(100, 20)
	renderer.SetChatAreaBounds(0, 20)
	status := recovery.Status{ID: "incident", AgentID: "owner", Generation: 7,
		State: recovery.Recovering, RetryAt: time.UnixMilli(30000), Detail: "actual diagnostic"}
	renderer.SyncMessages([]types.ChatMessage{{InternalID: status.ID, Role: types.RoleStatus, Recovery: &status}}, 1, 0)
	renderer.SetSpinFrame(1000)
	rows, _, _ := renderer.ExportMessageRows(100)
	screen := New(th)
	pad := th.Spacing.PadLR
	screen.SetMessages(rows, PaintParams{Compose: ComposeParams{InnerW: 96, PadLR: pad, Theme: th}})
	screen.SyncScrollMetrics(2, 2)
	host := ui.NewHost(th, screen)
	host.Resize(100, 2)
	host.Layout()
	var actionColumn int
	for _, span := range rows[0].Native[1].Spans {
		if span.Action != nil {
			break
		}
		actionColumn += rendering.VisibleWidth(span.Text)
	}
	actionColumn += pad + 2
	host.Handle(tea.MouseClickMsg{X: actionColumn, Y: 1, Button: tea.MouseLeft})
	click := host.Handle(tea.MouseReleaseMsg{X: actionColumn, Y: 1, Button: tea.MouseLeft})
	if click.Action == nil || click.Action.ID != chatprojection.ChatRecoveryRetryActionID {
		t.Fatalf("Retry now did not activate: %+v", click)
	}
	payload := click.Action.Payload.(chatprojection.ChatRecoveryRetryPayload)
	if payload.AgentID != "owner" || payload.IncidentID != "incident" || payload.Generation != 7 {
		t.Fatalf("Retry now lost exact incident identity: %+v", payload)
	}
	host.Handle(tea.MouseClickMsg{X: pad + 2, Y: 1, Button: tea.MouseLeft})
	disclose := host.Handle(tea.MouseReleaseMsg{X: pad + 2, Y: 1, Button: tea.MouseLeft})
	if disclose.Action == nil || disclose.Action.ID != chatprojection.ChatToggleActionID {
		t.Fatal("recovery header did not use ordinary disclosure")
	}
	host.Handle(tea.MouseClickMsg{X: actionColumn, Y: 1, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: actionColumn + 5, Y: 1, Button: tea.MouseLeft})
	drag := host.Handle(tea.MouseReleaseMsg{X: actionColumn + 5, Y: 1, Button: tea.MouseLeft})
	if drag.Action != nil || drag.Cmd == nil || screen.IsSelecting() {
		t.Fatal("dragging Retry now activated it or failed shared copy-on-release")
	}
	if !strings.Contains(rows[0].Lines[1], "retry in 29s") {
		t.Fatal("painted recovery lacks real countdown")
	}
}
