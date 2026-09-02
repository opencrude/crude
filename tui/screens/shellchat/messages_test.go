package shellchat_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/screens/shellchat"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

func TestMessageSyncPreservesUnchangedCaches(t *testing.T) {
	store := chatprojection.NewMessageSync()
	messages := []types.ChatMessage{{InternalID: "domain-1", Role: types.RoleAssistant, Content: "hello"}}
	store.SyncMessages(messages, 1)
	store.Messages[0].CachedLines = []string{"rendered"}

	_, ok := store.SyncMessages(messages, 2)
	if !ok {
		t.Fatal("expected sync to apply")
	}
	if got := store.Messages[0].CachedLines; len(got) != 1 || got[0] != "rendered" {
		t.Fatalf("unchanged cache discarded: %#v", got)
	}
}

func TestMessageAtLocalYResolvesBottomGravityAndScroll(t *testing.T) {
	th := theme.DefaultDark()
	s := shellchat.New(th)
	rows := []chatprojection.MessageRow{{ID: "user-1", Lines: []string{"one", "two"}}, {ID: "assistant", Lines: []string{"three"}}}
	s.SetMessages(rows, shellchat.PaintParams{Compose: shellchat.ComposeParams{InnerW: 30, PadLR: th.Spacing.PadLR, Theme: th}})
	s.SyncScrollMetrics(6, 3)
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 34, H: 6})
	if _, ok := s.MessageAtLocalY(1); ok {
		t.Fatal("blank gravity row resolved to a message")
	}
	row, ok := s.MessageAtLocalY(3)
	if !ok || row.ID != "user-1" {
		t.Fatalf("row=%+v ok=%v", row, ok)
	}
	row, ok = s.MessageAtLocalY(5)
	if !ok || row.ID != "assistant" {
		t.Fatalf("row=%+v ok=%v", row, ok)
	}
}

func TestShellChatScrollModelStickToBottom(t *testing.T) {
	th := theme.DefaultDark()
	s := shellchat.New(th)
	scroll := s.ScrollModel()
	scroll.SetViewport(5)
	scroll.SetContent(20)
	scroll.SetAutoScroll(true)

	rows := make([]chatprojection.MessageRow, 5)
	for i := 0; i < 5; i++ {
		ch := string(rune('a' + i))
		rows[i] = chatprojection.MessageRow{
			ID:    ch,
			Lines: []string{ch},
			Native: []chatprojection.PaintLine{
				{Spans: []chatprojection.RowSpan{{Text: ch, Role: ui.ColorFg}}},
			},
		}
	}
	s.SetMessages(rows, shellchat.PaintParams{Compose: shellchat.ComposeParams{InnerW: 30, PadLR: th.Spacing.PadLR, Theme: th}})
	if scroll.Offset() != scroll.MaxOffset() {
		t.Fatalf("auto-scroll offset=%d want max=%d", scroll.Offset(), scroll.MaxOffset())
	}

	scroll.SetAutoScroll(false)
	scroll.ScrollTo(0)
	if handled := s.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnd})); !handled.Handled {
		t.Fatal("End key should be handled")
	}
	if !scroll.AutoScroll() || scroll.Offset() != scroll.MaxOffset() {
		t.Fatal("End should stick to bottom")
	}
}

func TestShellChatPgUpScrollsBoundModel(t *testing.T) {
	s := shellchat.New(theme.DefaultDark())
	scroll := s.ScrollModel()
	scroll.SetViewport(4)
	scroll.SetContent(20)
	scroll.StickToBottom()

	res := s.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyPgUp}))
	if !res.Handled {
		t.Fatal("PgUp should be handled")
	}
	if scroll.Offset() >= scroll.MaxOffset() {
		t.Fatalf("PgUp should scroll up from bottom, offset=%d max=%d", scroll.Offset(), scroll.MaxOffset())
	}
	if scroll.AutoScroll() {
		t.Fatal("PgUp should disarm auto-scroll")
	}
}
