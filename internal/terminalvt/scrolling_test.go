//go:build cgo

package terminalvt

import (
	"fmt"
	"strings"
	"testing"
)

func TestAnchoredRowsFollowIdentityThroughEvictionAndRejectResetCoordinates(t *testing.T) {
	e := newTestEngine(t, Options{Cols: 24, Rows: 4, ScrollbackLines: 32})
	for i := 0; i < 40; i++ {
		e.Feed([]byte(fmt.Sprintf("identity-%04d\r\n", i)))
	}
	p, err := e.Rows(RowRequest{Start: 16})
	if err != nil {
		t.Fatal(err)
	}
	want := text(p)
	for i := 40; i < 52; i++ {
		e.Feed([]byte(fmt.Sprintf("identity-%04d\r\n", i)))
		q, err := e.Rows(RowRequest{Start: p.Start, HistoryEpoch: p.HistoryEpoch, HistoryBase: p.HistoryBase})
		if err != nil {
			t.Fatal(err)
		}
		if text(q) != want || q.HistoryEpoch != p.HistoryEpoch || q.HistoryBase <= p.HistoryBase || q.Start != p.Start-1 {
			t.Fatalf("anchor %d moved to wrong rows: before start=%d epoch=%d base=%d after start=%d epoch=%d base=%d before=%q after=%q", i, p.Start, p.HistoryEpoch, p.HistoryBase, q.Start, q.HistoryEpoch, q.HistoryBase, want, text(q))
		}
		p = q
	}
	// A request admitted before resize/reset may execute only afterwards. It
	// receives one whole current viewport with new identity, never a partial
	// selection page from unrelated coordinates or an unbounded retry signal.
	request := RowRequest{Start: p.Start, Count: 1, HistoryEpoch: p.HistoryEpoch, HistoryBase: p.HistoryBase}
	e.Feed([]byte("\x1bcnew-reset\r\nsecond"))
	q, err := e.Rows(request)
	if err != nil || q.HistoryEpoch == p.HistoryEpoch || len(q.Cells) != q.GridRows || !strings.Contains(text(q), "new-reset") {
		t.Fatalf("reset race page start=%d epoch=%d len=%d text=%q err=%v", q.Start, q.HistoryEpoch, len(q.Cells), text(q), err)
	}
	if err := e.Resize(16, 6); err != nil {
		t.Fatal(err)
	}
	r, err := e.Rows(request)
	if err != nil || r.HistoryEpoch == q.HistoryEpoch || len(r.Cells) != 6 || r.Cols != 16 {
		t.Fatalf("reflow race=%+v err=%v", r, err)
	}
}
