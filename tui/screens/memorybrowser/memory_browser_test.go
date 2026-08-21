package memorybrowser

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/memory"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func testRecords() []memory.ScoredRecord {
	now := time.Now().UnixNano()
	return []memory.ScoredRecord{
		{Record: memory.Record{
			ID: "mem-1", Kind: memory.KindConvention, Title: "Validation gate",
			Content: "Run go test ./... and golangci-lint before handoff.",
			Pinned:  true, Importance: 80, UseCount: 4, Status: memory.StatusActive,
			Source: memory.SourceTool, Provenance: "session:s1 turn:3",
			UpdatedAt: now, CreatedAt: now,
		}},
		{Record: memory.Record{
			ID: "mem-2", Kind: memory.KindLesson, Title: "moveWordLeft climbs blank lines",
			Content: "A swallowed continue parks the cursor on blank lines.",
			Status:  memory.StatusActive, Importance: 55, Source: memory.SourceAuto,
			UpdatedAt: now, CreatedAt: now,
		}},
	}
}

func browserHost(t *testing.T, records []memory.ScoredRecord) (*Screen, *ui.Host) {
	t.Helper()
	th := theme.DefaultDark()
	screen := New(th)
	screen.SetSearchFn(func(query string) []memory.ScoredRecord {
		query = strings.ToLower(strings.TrimSpace(query))
		if query == "" {
			return records
		}
		var out []memory.ScoredRecord
		for _, record := range records {
			hay := strings.ToLower(record.Title + " " + record.Content)
			if strings.Contains(hay, query) {
				out = append(out, record)
			}
		}
		return out
	})
	screen.Open()
	component := ui.NewComponentWidget(th, screen, 78, 20)
	screen.SetFocusHandler(component.Focus)
	screen.FocusList()
	host := component.Host()
	host.Resize(78, 20)
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

func TestBrowserPaintShowsRecordsAndDetail(t *testing.T) {
	_, host := browserHost(t, testRecords())
	text := bufferText(paintHost(host, 78, 20))
	for _, want := range []string{
		"Workspace Memory (2)",
		"Validation gate",
		"moveWordLeft climbs blank lines",
		// Detail pane for the selected (first) record: content + provenance.
		"Run go test ./... and golangci-lint before handoff.",
		"session:s1 turn:3",
		"p pin  d forget",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("paint missing %q:\n%s", want, text)
		}
	}
}

func TestBrowserSearchNarrowsResults(t *testing.T) {
	screen, host := browserHost(t, testRecords())
	screen.FocusSearch()
	for _, r := range "cursor" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	host.Layout()
	text := bufferText(paintHost(host, 78, 20))
	if !strings.Contains(text, "moveWordLeft") {
		t.Fatalf("search should keep the lesson:\n%s", text)
	}
	if strings.Contains(text, "Validation gate") {
		t.Fatalf("search should hide the convention:\n%s", text)
	}
}

func TestBrowserPinForgetAndCancelMessages(t *testing.T) {
	screen, host := browserHost(t, testRecords())

	pin := commandMessage[PinMsg](t, host.Handle(tea.KeyPressMsg{Code: 'p', Text: "p"}))
	if pin.Target != screen || pin.ID != "mem-1" || pin.Pinned {
		// mem-1 is pinned; toggling asks for pinned=false.
		t.Fatalf("pin message=%#v", pin)
	}

	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	forget := commandMessage[ForgetMsg](t, host.Handle(tea.KeyPressMsg{Code: 'd', Text: "d"}))
	if forget.Target != screen || forget.ID != "mem-2" {
		t.Fatalf("forget message=%#v", forget)
	}

	cancel := commandMessage[CancelMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}))
	if cancel.Target != screen {
		t.Fatalf("cancel target mismatch")
	}
}

func TestBrowserEmptyState(t *testing.T) {
	_, host := browserHost(t, nil)
	text := bufferText(paintHost(host, 78, 14))
	if !strings.Contains(text, "No memories yet") {
		t.Fatalf("empty state missing:\n%s", text)
	}
}
