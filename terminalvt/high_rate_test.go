//go:build cgo

package terminalvt

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSynchronizedOutputParserBoundariesAndLiveReplies(t *testing.T) {
	const begin, end = "\x1b[?2026h", "\x1b[?2026l"
	for split := 0; split <= len(begin); split++ {
		t.Run(fmt.Sprint(split), func(t *testing.T) {
			e := newTestEngine(t, Options{Cols: 20, Rows: 3})
			// No Rows call before the initial begin: the boundary, not the
			// last reader, must capture the completed frame.
			e.Feed([]byte("old\x1b[H\x1b[2 q" + begin[:split]))
			e.Feed([]byte(begin[split:] + "\x1b[?25l\x1b[2J\x1b[Hnew界\x1b[6n"))
			p, err := e.Rows(RowRequest{Start: -1, Count: 0})
			if err != nil || !p.Synchronized || !strings.HasPrefix(text(p), "old") || !p.Cursor.Visible || p.Cursor.Blink {
				t.Fatalf("held frame=%+v text=%q err=%v", p, text(p), err)
			}
			fx := e.Feed([]byte("\x1b[6n\x1b[?1004h"))
			if got := string(bytes.Join(fx.Replies, nil)); got != "\x1b[1;6R" || string(e.EncodeFocus(true)) != "\x1b[I" {
				t.Fatalf("synchronization delayed parser/replies/focus: %+v", fx)
			}
			// End and another begin in the SAME read must publish the
			// completed middle frame, not the old or partially erased one.
			e.Feed([]byte("\x1b[H\x1b[?25h" + end + begin + "\x1b[2J\x1b[Hnext"))
			q, err := e.Rows(RowRequest{Start: -1, Count: 0})
			if err != nil || !q.Synchronized || !strings.HasPrefix(text(q), "new界") || !q.Cursor.Visible || q.ReadSequence <= p.ReadSequence {
				t.Fatalf("coalesced boundary text=%q cursor=%+v err=%v", text(q), q.Cursor, err)
			}
			q.Cells[0][0].Text = "caller mutation"
			again, _ := e.Rows(RowRequest{Start: -1, Count: 0})
			if !strings.HasPrefix(text(again), "new界") {
				t.Fatal("completed render state aliases detached reader")
			}
			e.Feed([]byte(end[:split]))
			e.Feed([]byte(end[split:]))
			q, err = e.Rows(RowRequest{Start: -1, Count: 0})
			if err != nil || q.Synchronized || !strings.HasPrefix(text(q), "next") {
				t.Fatalf("commit=%q synchronized=%v err=%v", text(q), q.Synchronized, err)
			}
		})
	}
}

func TestSynchronizedOutputTimeoutResizeAndUnsynchronizedFrames(t *testing.T) {
	e := newTestEngine(t, Options{Cols: 20, Rows: 3})
	now := time.Unix(1, 0)
	e.now = func() time.Time { return now }
	e.Feed([]byte("old\x1b[H\x1b[?2026h\x1b[2Jnew"))
	now = now.Add(SynchronizedOutputTimeout - time.Nanosecond)
	p, err := e.Rows(RowRequest{Start: -1, Count: 0})
	if err != nil || !p.Synchronized || !strings.HasPrefix(text(p), "old") {
		t.Fatalf("early timeout=%q err=%v", text(p), err)
	}
	// No Feed after the lost reset: a later reader alone must release the hold.
	now = now.Add(time.Nanosecond)
	p, err = e.Rows(RowRequest{Start: -1, Count: 0})
	if err != nil || p.Synchronized || !strings.Contains(text(p), "new") {
		t.Fatalf("timeout=%q err=%v", text(p), err)
	}
	e.Feed([]byte("\x1b[?2026h\x1b[2J\x1b[Hresized"))
	if err := e.Resize(24, 4); err != nil {
		t.Fatal(err)
	}
	p, err = e.Rows(RowRequest{Start: -1, Count: 0})
	if err != nil || p.Synchronized || p.Cols != 24 || p.GridRows != 4 || !strings.HasPrefix(text(p), "resized") {
		t.Fatalf("resize projection=%+v err=%v", p, err)
	}
	e.Feed([]byte("\x1b[2J"))
	p, err = e.Rows(RowRequest{Start: -1, Count: 0})
	if err != nil || strings.TrimSpace(text(p)) != "" {
		t.Fatal("unsynchronized intentional erase was suppressed")
	}
	// OSC contents and cancelled CSI are not DEC 2026, despite their bytes.
	e.Feed([]byte("\x1b]2;?2026h\a\x1b[?2026\x18h\x1b[Hlive"))
	p, err = e.Rows(RowRequest{Start: -1, Count: 0})
	if err != nil || p.Synchronized || !strings.HasPrefix(text(p), "live") {
		t.Fatalf("non-mode payload changed publication: %q sync=%v err=%v", text(p), p.Synchronized, err)
	}
}

func TestSynchronizedHistoricalReadReturnsUsableCompletedGrid(t *testing.T) {
	e := newTestEngine(t, Options{Cols: 12, Rows: 2})
	e.Feed([]byte("history\r\nolder\r\nactive\r\nlast\x1b[?2026h\x1b[2J"))
	p, err := e.Rows(RowRequest{Start: 0, Count: 1})
	if err != nil || !p.Synchronized || len(p.Cells) != 2 || !strings.Contains(text(p), "active") || !strings.Contains(text(p), "last") {
		t.Fatalf("new viewer received blank/uncommitted history page: %+v text=%q err=%v", p, text(p), err)
	}
}

func TestSynchronizedByteChunksPreserveUnicodeAndScreenIdentity(t *testing.T) {
	e := newTestEngine(t, Options{Cols: 16, Rows: 3})
	const source = "\x1b[Hprimary\x1b[?2026h\x1b[?1049h\x1b[H👩‍💻界é\x1b[H\x1b[2 q"
	for _, value := range []byte(source) {
		e.Feed([]byte{value})
	}
	before, err := e.Rows(RowRequest{Start: -1, Count: 0})
	if err != nil || !before.Synchronized || before.Modes.AltScreen || !strings.HasPrefix(text(before), "primary") {
		t.Fatalf("screen transition leaked before commit: %+v err=%v", before, err)
	}
	e.Feed([]byte("\x1b[?2026l"))
	after, err := e.Rows(RowRequest{Start: -1, Count: 0})
	if err != nil || after.Synchronized || !after.Modes.AltScreen || after.HistoryEpoch == before.HistoryEpoch || !strings.HasPrefix(text(after), "👩‍💻界é") {
		t.Fatalf("byte chunks corrupted unicode/screen commit: text=%q modes=%+v epoch=%d err=%v", text(after), after.Modes, after.HistoryEpoch, err)
	}
	e.Feed([]byte("\x1b[?1049l"))
	p, err := e.Rows(RowRequest{Start: -1, Count: 0})
	if err != nil || p.Modes.AltScreen || !strings.HasPrefix(text(p), "primary") {
		t.Fatalf("primary was not restored: %q err=%v", text(p), err)
	}
}

// Keep the same raw bytes for before/after measurements. Mutation and detached
// row publication are separate stages; a fast Feed is not frame-latency proof.
func BenchmarkTerminalHighRate(b *testing.B) {
	for _, size := range [][2]int{{80, 24}, {160, 40}} {
		for _, synchronized := range []bool{false, true} {
			name := fmt.Sprintf("%dx%d/sync_%t", size[0], size[1], synchronized)
			var frame strings.Builder
			if synchronized {
				frame.WriteString("\x1b[?2026h")
			}
			frame.WriteString("\x1b[?25l\x1b[H")
			for row := 0; row < size[1]; row++ {
				fmt.Fprintf(&frame, "\x1b[%d;1H\x1b[38;2;%d;120;180m%s", row+1, row, strings.Repeat("#", size[0]))
			}
			frame.WriteString("\x1b[H\x1b[?25h")
			if synchronized {
				frame.WriteString("\x1b[?2026l")
			}
			data := []byte(frame.String())
			for _, publish := range []bool{false, true} {
				stage := "mutation"
				if publish {
					stage = "publication"
				}
				b.Run(name+"/"+stage, func(b *testing.B) {
					e, err := New(Options{Cols: size[0], Rows: size[1]})
					if err != nil {
						b.Fatal(err)
					}
					defer e.Close()
					e.Feed([]byte("\x1b[?1049h"))
					e.Feed(data)
					b.ReportAllocs()
					b.SetBytes(int64(len(data)))
					b.ResetTimer()
					for range b.N {
						e.Feed(data)
						if publish {
							if _, err := e.Rows(RowRequest{Start: -1, Count: 0}); err != nil {
								b.Fatal(err)
							}
						}
					}
				})
			}
		}
	}
}
