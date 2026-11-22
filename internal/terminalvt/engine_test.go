//go:build cgo

package terminalvt

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func newTestEngine(t *testing.T, o Options) *Engine {
	t.Helper()
	e, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e
}
func text(p Projection) string {
	var b strings.Builder
	for _, row := range p.Cells {
		for _, c := range row {
			b.WriteString(c.Text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
func TestEngineNeutralEffectsAndGrid(t *testing.T) {
	e := newTestEngine(t, Options{Cols: 16, Rows: 4})
	fx := e.Feed([]byte("\x1b[31;1mA界e\u0301\x1b[0m\x1b]2;headless\a\x1b]7;file:///tmp\a\x1b]52;c;aGVsbG8=\a\x1b[?2004h\x1b[?1004h\x1b[6n"))
	if !fx.TitleChanged || fx.Title != "headless" || !fx.CWDChanged || fx.CWD != "file:///tmp" {
		t.Fatalf("effects=%+v", fx)
	}
	if len(fx.Clipboard) != 1 || string(fx.Clipboard[0]) != "hello" {
		t.Fatalf("clipboard=%q", fx.Clipboard)
	}
	if len(fx.Replies) != 1 || string(fx.Replies[0]) != "\x1b[1;5R" {
		t.Fatalf("replies=%q", fx.Replies)
	}
	p, err := e.Rows(RowRequest{Start: -1, Count: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Modes.BracketedPaste || !p.Modes.FocusReporting || p.Cursor.X != 4 {
		t.Fatalf("projection=%+v", p)
	}
	row := p.Cells[0]
	if row[0].Text != "A" || !row[0].Style.Bold || !row[0].FG.Explicit || row[1].Text != "界" || row[1].Width != 2 || row[2].Width != 0 || row[3].Text != "e\u0301" {
		t.Fatalf("cells=%+v", row[:5])
	}
	row[0].Text = "mutated"
	again, _ := e.Rows(RowRequest{Start: -1, Count: 4})
	if again.Cells[0][0].Text != "A" {
		t.Fatal("snapshot aliases engine")
	}
	if string(e.EncodeFocus(true)) != "\x1b[I" {
		t.Fatal("focus encoding")
	}
	if string(e.EncodeKey(Key{Code: KeyUp})) != "\x1b[A" {
		t.Fatal("key encoding")
	}
	if string(e.EncodePaste([]byte("paste"))) != "\x1b[200~paste\x1b[201~" {
		t.Fatal("paste encoding")
	}
	e.Feed([]byte("\x1b[?1000h\x1b[?1006h"))
	if string(e.EncodeMouse(Mouse{Action: MousePress, Button: MouseLeft, X: 2, Y: 1})) != "\x1b[<0;3;2M" {
		t.Fatal("mouse encoding")
	}
}
func TestEngineBoundedHistoryReadersAreIsolated(t *testing.T) {
	e := newTestEngine(t, Options{Cols: 12, Rows: 3, ScrollbackLines: 20})
	for i := 0; i < 50; i++ {
		e.Feed([]byte(fmt.Sprintf("row%02d\r\n", i)))
	}
	active, err := e.Rows(RowRequest{Start: -1, Count: 3})
	if err != nil {
		t.Fatal(err)
	}
	old, err := e.Rows(RowRequest{Start: 0, Count: 3})
	if err != nil {
		t.Fatal(err)
	}
	if old.Total > 23 || old.Total < 3 {
		t.Fatalf("unbounded history=%d", old.Total)
	}
	if text(old) == text(active) {
		t.Fatal("history reader returned active grid")
	}
	page, err := e.Rows(RowRequest{Start: 0, Count: old.Total})
	if err != nil || len(page.Cells) != old.Total {
		t.Fatalf("page=%d total=%d err=%v", len(page.Cells), old.Total, err)
	}
	again, _ := e.Rows(RowRequest{Start: -1, Count: 3})
	if text(again) != text(active) {
		t.Fatal("history reader changed active viewport")
	}
	fx := e.Feed([]byte("\x1b[6n"))
	if len(fx.Replies) != 1 {
		t.Fatal("reader disturbed query reply")
	}
	if _, err = e.Rows(RowRequest{Start: 0, Count: MaxSnapshotRows + 1}); !errors.Is(err, ErrBounds) {
		t.Fatal("snapshot cap")
	}
	if err = e.Resize(MaxCols+1, 3); !errors.Is(err, ErrBounds) {
		t.Fatal("grid cap")
	}
	e.Close()
	if _, err = e.Rows(RowRequest{Start: -1, Count: 1}); !errors.Is(err, ErrClosed) {
		t.Fatal("closed reader")
	}
}

func TestHistoryIdentityTracksEvictionNotIdenticalText(t *testing.T) {
	e := newTestEngine(t, Options{Cols: 8, Rows: 2, ScrollbackLines: 3})
	e.Feed([]byte("same\r\nsame\r\n"))
	p, err := e.Rows(RowRequest{Start: 0, Count: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Cells) != 2 {
		t.Fatal("zero count must default to grid height")
	}
	e.Feed([]byte("same\r\n"))
	q, err := e.Rows(RowRequest{Start: 0, Count: 0})
	if err != nil {
		t.Fatal(err)
	}
	if q.HistoryEpoch != p.HistoryEpoch {
		t.Fatal("ordinary output changed row identity")
	}
	if err := e.Resize(8, 2); err != nil {
		t.Fatal(err)
	}
	unchanged, err := e.Rows(RowRequest{Start: 0, Count: 0})
	if err != nil || unchanged.HistoryEpoch != q.HistoryEpoch {
		t.Fatal("same-size resize changed identity", err)
	}
	for i := 0; i < 800; i++ {
		e.Feed([]byte("same\r\n"))
	}
	r, err := e.Rows(RowRequest{Start: 0, Count: 0})
	if err != nil {
		t.Fatal(err)
	}
	if r.HistoryEpoch == q.HistoryEpoch && r.HistoryBase <= q.HistoryBase {
		t.Fatal("identical-text eviction did not advance row identity")
	}
	if err := e.Resize(12, 2); err != nil {
		t.Fatal(err)
	}
	s, err := e.Rows(RowRequest{Start: 0, Count: 0})
	if err != nil {
		t.Fatal(err)
	}
	if s.HistoryEpoch == r.HistoryEpoch {
		t.Fatal("reflow kept old identity")
	}
	e.Feed([]byte("\x1b[?1049h"))
	alt, err := e.Rows(RowRequest{Start: -1, Count: 0})
	if err != nil || alt.HistoryEpoch == s.HistoryEpoch {
		t.Fatal("screen switch kept old identity", err)
	}
	e.Feed([]byte("\x1b[?1049l\x1bc"))
	reset, err := e.Rows(RowRequest{Start: -1, Count: 0})
	if err != nil || reset.HistoryEpoch == alt.HistoryEpoch {
		t.Fatal("reset kept old identity", err)
	}
}
