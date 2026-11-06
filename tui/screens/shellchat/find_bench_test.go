package shellchat

import (
	"fmt"
	"testing"
	"time"

	"crude/tui/chatprojection"
)

// denseChat builds a long conversation: rows of wrapped prose, one row in a
// hundred holding the word "needle". It stands in for weeks of history.
func denseChat(rows, linesPerRow int) []chatprojection.MessageRow {
	out := make([]chatprojection.MessageRow, rows)
	for i := range out {
		lines := make([]string, linesPerRow)
		for j := range lines {
			lines[j] = fmt.Sprintf("Message %d line %d keeps the renderer, the scroll model and the transcript in step", i, j)
			if i%100 == 7 && j == 1 {
				lines[j] = "this line holds the Needle that the reader wants to find among the history"
			}
		}
		out[i] = findRow(fmt.Sprint("row", i), uint64(i+1), lines...)
	}
	return out
}

// BenchmarkChatFindCount_20000Rows is the cost of counting a query over 20 000
// messages (80 000 lines), as when a long history is searched for the first
// time or the query changes.
func BenchmarkChatFindCount_20000Rows(b *testing.B) {
	rows := denseChat(20000, 4)
	f := chatFind{limit: time.Hour}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.setQuery(fmt.Sprintf("needle%d", i&1), FindOptions{}, -1)
		f.observe(rows, 1, false)
		if f.pending(len(rows)) {
			b.Fatal("count did not finish")
		}
	}
}

// BenchmarkChatFindStreamFrame_20000Rows is the per-frame cost of find while a
// reply streams into a long history: every frame replaces the last row, so
// find recounts one row and compares the stamps of the rest.
func BenchmarkChatFindStreamFrame_20000Rows(b *testing.B) {
	rows := denseChat(20000, 4)
	f := chatFind{limit: time.Hour}
	f.setQuery("needle", FindOptions{}, -1)
	f.observe(rows, 1, false)
	last := len(rows) - 1
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows[last] = findRow(rows[last].ID, uint64(100000+i), "the streamed reply mentions a needle", "and keeps growing")
		f.observe(rows, 1, true)
	}
}
