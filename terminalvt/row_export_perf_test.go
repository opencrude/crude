//go:build cgo

package terminalvt

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// Every cell changes on every frame, including foreground/background for the
// pixel workload. Feed is deliberately outside the measured export stage.
func rowExportFrame(cols, rows, phase int, rgb bool) []byte {
	var frame strings.Builder
	for y := 0; y < rows; y++ {
		fmt.Fprintf(&frame, "\x1b[%d;1H", y+1)
		for x := 0; x < cols; x++ {
			if rgb {
				fmt.Fprintf(&frame, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm", (x+phase)%256, (y+phase)%256, (x+y+phase)%256, (x+phase+1)%256, (y+phase+2)%256, (x+y+phase+3)%256)
			}
			frame.WriteByte(byte('A' + phase))
		}
	}
	return []byte(frame.String())
}

func BenchmarkNativeRowExport(b *testing.B) {
	for _, size := range [][2]int{{80, 24}, {200, 50}} {
		for _, rgb := range []bool{false, true} {
			b.Run(fmt.Sprintf("%dx%d/rgb_%t", size[0], size[1], rgb), func(b *testing.B) {
				e, err := New(Options{Cols: size[0], Rows: size[1]})
				if err != nil {
					b.Fatal(err)
				}
				defer e.Close()
				e.Feed([]byte("\x1b[?1049h"))
				frames := [2][]byte{rowExportFrame(size[0], size[1], 0, rgb), rowExportFrame(size[0], size[1], 1, rgb)}
				e.Feed(frames[1])
				if _, err := e.StreamRows(RowRequest{Start: -1}); err != nil {
					b.Fatal(err)
				}
				samples := make([]time.Duration, b.N)
				b.ReportAllocs()
				b.ResetTimer()
				for i := range b.N {
					b.StopTimer()
					e.Feed(frames[i%2])
					b.StartTimer()
					start := time.Now()
					p, err := e.StreamRows(RowRequest{Start: -1})
					samples[i] = time.Since(start)
					if err != nil || len(p.Cells) != size[1] || p.Cells[0][0].Text != string(byte('A'+i%2)) || p.Cells[size[1]-1][size[0]-1].Text != string(byte('A'+i%2)) {
						b.Fatalf("export failed/stale: %v", err)
					}
				}
				b.StopTimer()
				slices.Sort(samples)
				b.ReportMetric(float64(samples[min(len(samples)-1, len(samples)*99/100)].Nanoseconds()), "export-p99-ns")
			})
		}
	}
}

func TestNativeRowExportFidelityAndLifetime(t *testing.T) {
	for _, cols := range []int{80, 200} {
		t.Run(fmt.Sprint(cols), func(t *testing.T) {
			e := newTestEngine(t, Options{Cols: cols, Rows: 3})
			e.Feed([]byte("\x1b[?2027h\x1b[38;2;17;83;201m\x1b[48;2;44;55;66m\x1b[1;2;3;4;7;9mA界é👩‍💻\x18\x1b[0m"))
			p, err := e.StreamRows(RowRequest{Start: -1})
			if err != nil {
				t.Fatal(err)
			}
			// Independent literal expectations, not another consumer of the new
			// row helper: styles, spacer cells, UTF-8 and CAN normalization.
			wantTexts := []string{"A", "界", "", "é", "👩‍💻", "", "�"}
			wantWidths := []uint8{1, 2, 0, 1, 2, 0, 1}
			for x, want := range wantTexts {
				c := p.Cells[0][x]
				if c.Text != want || c.Width != wantWidths[x] || c.FG != (Color{true, 17, 83, 201}) || c.BG != (Color{true, 44, 55, 66}) || c.Style != (Style{true, true, true, true, true, true}) {
					t.Fatalf("cell %d=%+v", x, c)
				}
			}
			for _, c := range p.Cells[1] {
				if c != (Cell{Text: " ", Width: 1}) {
					t.Fatalf("blank cell=%+v", c)
				}
			}
			before := text(p)
			for phase := range 8 {
				e.Feed(rowExportFrame(cols, 3, phase, true))
				q, err := e.StreamRows(RowRequest{Start: -1})
				if err != nil || q.Cells[0][0].Text != string(byte('A'+phase)) || q.Cells[2][cols-1].FG != (Color{true, uint8((cols - 1 + phase) % 256), uint8(2 + phase), uint8((cols + 1 + phase) % 256)}) {
					t.Fatalf("stale row after feed phase=%d err=%v", phase, err)
				}
				if text(p) != before {
					t.Fatal("later row export mutated a published string")
				}
			}
			e.Close()
			if text(p) != before {
				t.Fatal("close invalidated detached strings")
			}
		})
	}
}

func TestNativeRowExportLongGraphemeAndWrap(t *testing.T) {
	e := newTestEngine(t, Options{Cols: 8, Rows: 3})
	// Above the former 64-byte UTF-8 scratch, below Ghostty's own codepoint cap.
	cluster := "e" + strings.Repeat("\u0301", 40)
	e.Feed([]byte(cluster + "1234567X"))
	p, err := e.StreamRows(RowRequest{Start: -1})
	if err != nil || p.Cells[0][0].Text != cluster || !p.Wrapped[0] || p.Cells[1][0].Text != "X" {
		t.Fatalf("grapheme/wrap lost: err=%v projection=%+v", err, p)
	}
	q, err := e.Rows(RowRequest{Start: -1})
	if err != nil || !reflect.DeepEqual(p.Cells, q.Cells) || !reflect.DeepEqual(p.Wrapped, q.Wrapped) {
		t.Fatal("history paging changed row content", err)
	}
	if len(e.rowCells) != 8 || len(e.rowText) != len(cluster)+7 {
		t.Fatalf("scratch is not bounded by actual row demand: cells=%d text=%d", len(e.rowCells), len(e.rowText))
	}
	// A later short row must not alter the prior cluster or retain a mutable
	// view of the oversized text scratch.
	e.Feed([]byte("\x1b[Hshort"))
	if _, err := e.StreamRows(RowRequest{Start: -1}); err != nil || p.Cells[0][0].Text != cluster {
		t.Fatal("growth scratch aliases old row", err)
	}
	if err := e.Resize(16, 2); err != nil {
		t.Fatal(err)
	}
	if len(e.rowCells) != 0 || len(e.rowText) != 0 {
		t.Fatal("resize retained obsolete-width scratch")
	}
	if _, err := e.StreamRows(RowRequest{Start: -1}); err != nil || len(e.rowCells) != 16 || len(e.rowText) != 16*4 {
		t.Fatal("resize did not derive scratch from new width", err)
	}
}

func TestNativeRowExportRejectsDimensionMismatch(t *testing.T) {
	for _, size := range [][2]int{{0, 3}, {MaxCols + 1, 3}, {8, MaxRows + 1}, {7, 3}, {9, 3}, {8, 2}, {8, 4}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			e := newTestEngine(t, Options{Cols: 8, Rows: 3})
			if _, err := e.StreamRows(RowRequest{Start: -1}); err != nil {
				t.Fatal(err)
			}
			// Exercise the native boundary's dimension checks independently of
			// Options/Resize validation, including a shorter actual native row.
			e.cols, e.rows = size[0], size[1]
			if _, _, err := e.viewportRows(); !errors.Is(err, ErrBounds) {
				t.Fatalf("invalid native row dimensions returned %v", err)
			}
		})
	}
}

func TestNativeRowExportWideRightEdgeAndDirtyReuse(t *testing.T) {
	e := newTestEngine(t, Options{Cols: 8, Rows: 3})
	e.Feed([]byte("1234567界"))
	p, err := e.StreamRows(RowRequest{Start: -1})
	if err != nil || !p.Wrapped[0] || p.Cells[0][7].Width != 0 || p.Cells[0][7].Text != "" || p.Cells[1][0].Width != 2 || p.Cells[1][0].Text != "界" || p.Cells[1][1].Width != 0 || p.Cells[1][1].Text != "" {
		t.Fatalf("right-edge head/tail spacer lost: %+v err=%v", p, err)
	}
	q, err := e.StreamRows(RowRequest{Start: -1})
	if err != nil || &q.Cells[0][0] != &p.Cells[0][0] || &q.Cells[1][0] != &p.Cells[1][0] {
		t.Fatal("unchanged immutable rows were re-exported", err)
	}
	e.Feed([]byte("\x1b[2;1HZ"))
	r, err := e.StreamRows(RowRequest{Start: -1})
	if err != nil || r.Cells[1][0].Text != "Z" || p.Cells[1][0].Text != "界" || &r.Cells[1][0] == &p.Cells[1][0] || &r.Cells[0][0] != &p.Cells[0][0] {
		t.Fatal("dirty export lost fresh/unchanged row identity", err)
	}
}

// This catches the original per-cell allocation regression with the real entry
// point and a fully dirty grid, rather than benchmarking an isolated converter.
func TestNativeRowExportAllocationsScaleWithRows(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {200, 50}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			e := newTestEngine(t, Options{Cols: size[0], Rows: size[1]})
			e.Feed([]byte("\x1b[?1049h"))
			frames := [2][]byte{rowExportFrame(size[0], size[1], 0, true), rowExportFrame(size[0], size[1], 1, true)}
			phase := 0
			allocs := testing.AllocsPerRun(10, func() {
				phase ^= 1
				e.Feed(frames[phase])
				p, err := e.StreamRows(RowRequest{Start: -1})
				if err != nil || p.Cells[0][0].Text != string(byte('A'+phase)) {
					t.Fatal("export failed or reused a stale grid", err)
				}
			})
			if allocs > float64(size[1]*6+32) {
				t.Fatalf("fully dirty export allocations=%g; must scale with rows, not cells", allocs)
			}
		})
	}
}
