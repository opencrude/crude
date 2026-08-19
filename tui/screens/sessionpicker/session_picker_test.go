package sessionpicker

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestPaintGoldenCellBufferContract(t *testing.T) {
	now := time.Now().UnixNano()
	sessions := []sessiondb.SessionMeta{
		{ID: "a", ProjectName: "crude", Model: "gpt", TurnCount: 3, CreatedAt: now,
			FirstPrompt: sql.NullString{String: "fix the picker", Valid: true}},
	}
	screen, host := pickerHost(t, sessions)
	buf := paintHost(host, 72, 12)
	text := bufferText(buf)

	if nonEmpty := countNonEmptyCells(buf); nonEmpty == 0 {
		t.Fatal("session picker paint produced no non-empty cells")
	}
	for _, label := range []string{"Past sessions", "fix the picker", "3 turns"} {
		if !strings.Contains(text, label) {
			t.Fatalf("missing label %q:\n%s", label, text)
		}
	}
	if !rowHasSelectionHighlight(buf) {
		t.Fatalf("expected theme selection highlight:\n%s", text)
	}
	if _, ok := screen.Selected(); !ok {
		t.Fatal("expected initial selection")
	}
	// NOTE: no byte-identical golden here — the picker renders Today/
	// Yesterday date grouping from time.Now(), which is inherently
	// non-deterministic. The property checks above cover its content.
}

func countNonEmptyCells(buf *surface.CellBuffer) int {
	n := 0
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Char != 0 && c.Width != 0 {
				n++
			}
		}
	}
	return n
}

func TestSessionPickerFilterNarrowsList(t *testing.T) {
	now := time.Now().UnixNano()
	sessions := []sessiondb.SessionMeta{
		{ID: "a", ProjectName: "crude", Model: "gpt", TurnCount: 1, CreatedAt: now,
			FirstPrompt: sql.NullString{String: "alpha task", Valid: true}},
		{ID: "b", ProjectName: "crude", Model: "claude", TurnCount: 2, CreatedAt: now,
			FirstPrompt: sql.NullString{String: "beta work", Valid: true}},
	}
	screen, host := pickerHost(t, sessions)
	host.Handle(tea.KeyPressMsg{Code: '/', Text: "/"})
	host.Handle(tea.KeyPressMsg{Code: 'b', Text: "b"})
	host.Handle(tea.KeyPressMsg{Code: 'e', Text: "e"})
	host.Handle(tea.KeyPressMsg{Code: 't', Text: "t"})
	host.Handle(tea.KeyPressMsg{Code: 'a', Text: "a"})
	host.Layout()
	buf := paintHost(host, 72, 12)
	text := bufferText(buf)
	if !strings.Contains(text, "beta work") {
		t.Fatalf("filter should keep beta:\n%s", text)
	}
	if strings.Contains(text, "alpha task") {
		t.Fatalf("filter should hide alpha:\n%s", text)
	}
	_ = screen
}

func TestSessionPickerEmptyState(t *testing.T) {
	screen, host := pickerHost(t, nil)
	buf := paintHost(host, 72, 10)
	text := bufferText(buf)
	for _, want := range []string{
		"Past sessions",
		"No sessions yet",
		"Ctrl+Alt+S",
		"Enter resume",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("empty paint missing %q:\n%s", want, text)
		}
	}
	if _, ok := screen.Selected(); ok {
		t.Fatal("empty picker should have no selection")
	}
}

func TestSessionPickerGroupedSessionsAndHighlight(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	yesterday := today.AddDate(0, 0, -1)
	older := today.AddDate(0, 0, -5)

	sessions := []sessiondb.SessionMeta{
		{
			ID: "ses_today", ProjectName: "crude", Model: "gpt-test", TurnCount: 3,
			CreatedAt:   today.UnixNano(),
			FirstPrompt: sql.NullString{String: "fix the picker", Valid: true},
		},
		{
			ID: "ses_yest", ProjectName: "other", Model: "claude", TurnCount: 1,
			CreatedAt:   yesterday.UnixNano(),
			FirstPrompt: sql.NullString{String: "yesterday work", Valid: true},
		},
		{
			ID: "ses_old", ProjectName: "legacy", Model: "mini", TurnCount: 9,
			CreatedAt:   older.UnixNano(),
			FirstPrompt: sql.NullString{String: "old session", Valid: true},
		},
	}

	groups := BuildSessionGroups(sessions)
	if len(groups) != 3 {
		t.Fatalf("groups=%d, want 3", len(groups))
	}
	if groups[0].Label != "today" || groups[1].Label != "yesterday" {
		t.Fatalf("group labels=%q/%q", groups[0].Label, groups[1].Label)
	}

	screen, host := pickerHost(t, sessions)
	buf := paintHost(host, 72, 14)
	text := bufferText(buf)
	for _, want := range []string{
		"Past sessions (3)",
		"today",
		"yesterday",
		"fix the picker",
		"yesterday work",
		"old session",
		"3 turns",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("grouped paint missing %q:\n%s", want, text)
		}
	}

	// First selectable row is focused → highlight background differs from empty cells.
	if !rowHasSelectionHighlight(buf) {
		t.Fatalf("expected selection highlight on focused row:\n%s", text)
	}

	selected, ok := screen.Selected()
	if !ok || selected.ID != "ses_today" {
		t.Fatalf("selected=%#v ok=%v", selected, ok)
	}
}

func TestSessionPickerKeyboardResumeAndCancel(t *testing.T) {
	now := time.Now().UnixNano()
	sessions := []sessiondb.SessionMeta{
		{ID: "a", ProjectName: "alpha", Model: "m", TurnCount: 1, CreatedAt: now,
			FirstPrompt: sql.NullString{String: "one", Valid: true}},
		{ID: "b", ProjectName: "beta", Model: "m", TurnCount: 2, CreatedAt: now,
			FirstPrompt: sql.NullString{String: "two", Valid: true}},
	}
	screen, host := pickerHost(t, sessions)

	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	selected, ok := screen.Selected()
	if !ok || selected.ID != "b" {
		t.Fatalf("after down selected=%#v", selected)
	}

	host.Handle(tea.KeyPressMsg{Code: 'k', Text: "k"})
	selected, ok = screen.Selected()
	if !ok || selected.ID != "a" {
		t.Fatalf("after k selected=%#v", selected)
	}

	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := commandMessage[SelectMsg](t, result)
	if msg.Target != screen || msg.Session.ID != "a" {
		t.Fatalf("resume message=%#v", msg)
	}

	screen, host = pickerHost(t, sessions)
	cancel := commandMessage[CancelMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}))
	if cancel.Target != screen {
		t.Fatalf("cancel target=%p want %p", cancel.Target, screen)
	}
}

func TestSessionPickerClickSelectsDoubleClickResumes(t *testing.T) {
	now := time.Now().UnixNano()
	sessions := []sessiondb.SessionMeta{
		{ID: "a", ProjectName: "alpha", Model: "m", TurnCount: 1, CreatedAt: now},
		{ID: "b", ProjectName: "beta", Model: "m", TurnCount: 2, CreatedAt: now},
	}
	screen, host := pickerHost(t, sessions)

	// List starts at Y=2 (title + blank). Second session is one header + two rows → Y=4.
	first := host.Handle(tea.MouseClickMsg{X: 2, Y: 4, Button: tea.MouseLeft})
	if first.Cmd != nil || first.Action != nil {
		t.Fatalf("first click should select only: %#v", first)
	}
	selected, ok := screen.Selected()
	if !ok || selected.ID != "b" {
		t.Fatalf("after click selected=%#v", selected)
	}

	second := host.Handle(tea.MouseClickMsg{X: 2, Y: 4, Button: tea.MouseLeft})
	msg := commandMessage[SelectMsg](t, second)
	if msg.Session.ID != "b" {
		t.Fatalf("double-click resume=%#v", msg)
	}
}

func pickerItem(t *testing.T, screen *Screen, id string) ui.ListItem {
	t.Helper()
	for _, item := range screen.list.list.Items() {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("missing row %q", id)
	return ui.ListItem{}
}

func TestLiveRowsShowStatusAndSubAgents(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	now := time.Now().UnixNano()
	screen.Open([]sessiondb.SessionMeta{
		{ID: "root", ProjectName: "alpha", Model: "gpt-5", TurnCount: 3, CreatedAt: now},
		{ID: "old", ProjectName: "beta", Model: "gpt-5", TurnCount: 1, CreatedAt: now},
	})
	screen.SetLiveInfo(map[string]LiveInfo{
		"root": {Status: StatusWorking, SubAgents: 2},
	})

	root := pickerItem(t, screen, "root")
	isSpinner := false
	for _, frame := range spinnerFrames {
		if root.Icon == frame {
			isSpinner = true
		}
	}
	if !isSpinner {
		t.Fatalf("working session icon=%q, want a spinner frame", root.Icon)
	}
	for _, want := range []string{"2 sub-agents", "working", "gpt-5", "3 turns"} {
		if !strings.Contains(root.Detail, want) {
			t.Fatalf("working detail %q missing %q", root.Detail, want)
		}
	}

	old := pickerItem(t, screen, "old")
	if old.Icon != "○" {
		t.Fatalf("terminated session icon=%q, want ○", old.Icon)
	}
	if !strings.Contains(old.Detail, "terminated") {
		t.Fatalf("terminated detail %q missing status word", old.Detail)
	}

	// Minimized and idle get distinct static icons.
	screen.SetLiveInfo(map[string]LiveInfo{
		"root": {Status: StatusMinimized},
		"old":  {Status: StatusIdle},
	})
	if got := pickerItem(t, screen, "root").Icon; got != "▪" {
		t.Fatalf("minimized icon=%q, want ▪", got)
	}
	if got := pickerItem(t, screen, "old").Icon; got != "●" {
		t.Fatalf("idle icon=%q, want ●", got)
	}
}

func TestWorkingIconAnimatesAndSelectionSurvives(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	now := time.Now().UnixNano()
	screen.Open([]sessiondb.SessionMeta{
		{ID: "a", ProjectName: "alpha", TurnCount: 1, CreatedAt: now},
		{ID: "b", ProjectName: "beta", TurnCount: 2, CreatedAt: now},
		{ID: "c", ProjectName: "gamma", TurnCount: 3, CreatedAt: now},
	})
	screen.SetLiveInfo(map[string]LiveInfo{"a": {Status: StatusWorking}})
	if !screen.NeedsAnimation() {
		t.Fatal("a working session must request animation")
	}
	// Pin the user's selection on "c": tick-driven resyncs must not steal it.
	target := -1
	for i, item := range screen.list.list.Items() {
		if item.ID == "c" {
			target = i
		}
	}
	screen.list.list.SelectIndex(target)

	screen.SetSpinFrame(0)
	before := pickerItem(t, screen, "a").Icon
	screen.SetSpinFrame(300)
	after := pickerItem(t, screen, "a").Icon
	if before == after {
		t.Fatalf("working icon did not animate: %q", before)
	}
	if meta, ok := screen.Selected(); !ok || meta.ID != "c" {
		t.Fatalf("selection lost during spinner resync: %#v", meta)
	}
	// Non-working frames do nothing: no working session, no animation.
	screen.SetLiveInfo(map[string]LiveInfo{"a": {Status: StatusIdle}})
	if screen.NeedsAnimation() {
		t.Fatal("idle sessions must not request animation")
	}
	static := pickerItem(t, screen, "a").Icon
	screen.SetSpinFrame(999)
	if got := pickerItem(t, screen, "a").Icon; got != static {
		t.Fatalf("stopped session icon changed: %q → %q", static, got)
	}
}

func pickerHost(t *testing.T, sessions []sessiondb.SessionMeta) (*Screen, *ui.Host) {
	t.Helper()
	th := theme.DefaultDark()
	screen := New(th)
	screen.Open(sessions)
	component := ui.NewComponentWidget(th, screen, 72, 14)
	screen.SetFocusHandler(component.Focus)
	screen.FocusList()
	host := component.Host()
	host.Resize(72, 14)
	host.Layout()
	return screen, host
}

func paintHost(host *ui.Host, w, h int) *surface.CellBuffer {
	host.Resize(w, h)
	host.Layout()
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	return buf
}

func bufferText(buf *surface.CellBuffer) string {
	var b strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			r := buf.Get(x, y).Char
			if r == 0 {
				r = ' '
			}
			b.WriteRune(r)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func rowHasSelectionHighlight(buf *surface.CellBuffer) bool {
	tokens := ui.NewTokenSet(theme.DefaultDark())
	_, wantBG := tokens.ListRowColors(ui.StateFocused)
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if !c.BG.IsDefault() && c.BG == wantBG && c.Char != 0 && c.Char != ' ' {
				return true
			}
		}
	}
	return false
}

func commandMessage[T any](t *testing.T, result ui.EventResult) T {
	t.Helper()
	if result.Cmd == nil {
		t.Fatalf("result did not emit a command: %#v", result)
	}
	message, ok := result.Cmd().(T)
	if !ok {
		t.Fatalf("command message=%T (%#v)", result.Cmd(), result.Cmd())
	}
	return message
}
