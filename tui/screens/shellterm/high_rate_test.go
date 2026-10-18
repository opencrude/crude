package shellterm

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"crude/terminalvt"
	"crude/tui/cell"
	"crude/tui/compositor"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

// Split at PTY-read boundaries, not VT-command boundaries: a movie-like child
// erases, hides the cursor and paints multiple rows inside one synchronized
// update. Every read may race a shell presentation.
func TestHighRateSynchronizedOutputNeverPresentsPartialFrame(t *testing.T) {
	e, err := terminalvt.New(terminalvt.Options{Cols: 40, Rows: 11})
	if errors.Is(err, terminalvt.ErrNoCGO) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	s := New(theme.DefaultDark(), "movie", "Movie")
	defer s.Close()
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 40, H: 11})
	s.SetTermFocused(true)
	var generation uint64
	paint := func(chunk string) (string, bool) {
		t.Helper()
		e.Feed([]byte(chunk))
		generation++
		grid, err := e.Rows(terminalvt.RowRequest{Start: -1, Count: 0})
		if err != nil {
			t.Fatal(err)
		}
		s.SetProjection(workspace.TerminalProjection{Info: workspace.TerminalInfo{ID: "movie", Alive: true}, Grid: grid, Generation: generation})
		buf := surface.NewCellBuffer(40, 11)
		s.Paint(buf, 0, 0, ui.PaintContext{})
		return compositor.GenerateANSI(buf), buf.Get(0, 0).Attr&cell.AttrReverse != 0
	}
	frame := func(n int) string {
		var out strings.Builder
		for y := 0; y < 11; y++ {
			fmt.Fprintf(&out, "\x1b[%d;1Hframe-%03d-row-%02d", y+1, n, y)
		}
		out.WriteString("\x1b[H\x1b[2 q\x1b[?25h")
		return out.String()
	}
	paint("\x1b[?1049h" + frame(0))
	partial, hidden, stale := 0, 0, 0
	for n := 1; n <= 120; n++ {
		for _, chunk := range []string{"\x1b[?2026h\x1b[?25l\x1b[2J", frame(n)[:20], frame(n)[20:]} {
			output, cursor := paint(chunk)
			if !strings.Contains(output, fmt.Sprintf("frame-%03d-row-10", n-1)) || strings.Contains(output, fmt.Sprintf("frame-%03d-row-", n)) {
				partial++
			}
			if !cursor {
				hidden++
			}
		}
		output, cursor := paint("\x1b[?2026l")
		if !strings.Contains(output, fmt.Sprintf("frame-%03d-row-10", n)) || !cursor {
			stale++
		}
	}
	if partial != 0 || hidden != 0 || stale != 0 {
		t.Fatalf("120 synchronized updates / 480 presentations: partial=%d cursor-hidden=%d stale-commit=%d", partial, hidden, stale)
	}
}

func TestSynchronizedTimeoutQueuesReadWithoutMoreOutput(t *testing.T) {
	s, _ := newTestScreen(t)
	p := s.projection
	p.Generation++
	p.Grid.Synchronized = true
	p.Grid.ReadSequence = 1
	s.SetProjection(p)
	due := s.syncRetryAt
	s.TakePendingCommands()
	s.TickCursor(due.Add(-time.Millisecond))
	if got := s.TakePendingCommands(); len(got) != 0 {
		t.Fatalf("premature timeout read=%+v", got)
	}
	// No output or layout events: the normal shell tick owns release admission.
	s.TickCursor(due)
	commands := s.TakePendingCommands()
	if len(commands) != 1 || commands[0].Kind != CommandReadRows || commands[0].Start != -1 {
		t.Fatalf("lost-reset retry=%+v", commands)
	}
	p.Grid.Synchronized = false
	p.Grid.ReadSequence++
	stale := p
	stale.Grid.ReadSequence--
	s.SetProjection(p) // background refresh beats the queued timeout response
	if s.SetRows(commands[0].Sequence, stale) || s.readPending {
		t.Fatal("late same-generation page applied or wedged the exact intent")
	}
	// A later timeout request is still admissible after the stale completion.
	p.Grid.Synchronized = true
	p.Grid.ReadSequence++
	s.SetProjection(p)
	s.TickCursor(s.syncRetryAt)
	commands = s.TakePendingCommands()
	if len(commands) != 1 {
		t.Fatalf("stale page blocked future timeout read: %+v", commands)
	}
	p.Grid.Synchronized = false
	p.Grid.ReadSequence++
	if !s.SetRows(commands[0].Sequence, p) || !s.syncRetryAt.IsZero() {
		t.Fatal("completed retry did not retire timeout state")
	}
	s.TickCursor(due.Add(2 * terminalvt.SynchronizedOutputTimeout))
	if got := s.TakePendingCommands(); len(got) != 0 {
		t.Fatalf("settled terminal still retries: %+v", got)
	}
}
