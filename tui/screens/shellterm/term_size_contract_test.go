package shellterm

import (
	"errors"
	"testing"

	"crude/tui/ui"
	"crude/workspace"
)

func TestScreenTinyLayoutKeepsEmulatorAndPTYSizesInSync(t *testing.T) {
	s, _ := newTestScreen(t)
	resizes := 0
	gotCols, gotRows := 0, 0
	s.SetResize(func(cols, rows int) error {
		resizes++
		gotCols, gotRows = cols, rows
		return nil
	})

	s.Layout(ui.LayoutContext{}, ui.Rect{W: 2, H: 1})
	if s.cols != workspace.MinTerminalCols || s.rows != workspace.MinTerminalRows {
		t.Fatalf(
			"screen grid=%dx%d want shared minimum %dx%d",
			s.cols,
			s.rows,
			workspace.MinTerminalCols,
			workspace.MinTerminalRows,
		)
	}
	if s.vt.Cols() != workspace.MinTerminalCols || s.vt.Rows() != workspace.MinTerminalRows {
		t.Fatalf(
			"emulator grid=%dx%d want shared minimum %dx%d",
			s.vt.Cols(),
			s.vt.Rows(),
			workspace.MinTerminalCols,
			workspace.MinTerminalRows,
		)
	}
	if resizes != 1 || gotCols != s.vt.Cols() || gotRows != s.vt.Rows() {
		t.Fatalf(
			"PTY callback calls=%d size=%dx%d; emulator=%dx%d",
			resizes,
			gotCols,
			gotRows,
			s.vt.Cols(),
			s.vt.Rows(),
		)
	}

	// Another undersized layout normalizes to the same grid and must not send
	// a redundant PTY resize.
	s.Layout(ui.LayoutContext{}, ui.Rect{})
	if resizes != 1 {
		t.Fatalf("equivalent tiny layout sent %d PTY resizes; want 1", resizes)
	}
}

func TestScreenResizeFailureKeepsEmulatorAndPTYContractAligned(t *testing.T) {
	s, _ := newTestScreen(t)
	beforeCols, beforeRows := s.vt.Cols(), s.vt.Rows()
	sentinel := errors.New("pty resize failed")
	attempts := 0
	s.SetResize(func(int, int) error {
		attempts++
		return sentinel
	})

	s.Layout(ui.LayoutContext{}, ui.Rect{W: 20, H: 5})
	if attempts != 1 {
		t.Fatalf("resize attempts=%d want=1", attempts)
	}
	if s.cols != beforeCols || s.rows != beforeRows {
		t.Fatalf("screen committed failed resize %dx%d; want %dx%d", s.cols, s.rows, beforeCols, beforeRows)
	}
	if s.vt.Cols() != beforeCols || s.vt.Rows() != beforeRows {
		t.Fatalf(
			"emulator committed failed PTY resize %dx%d; want %dx%d",
			s.vt.Cols(),
			s.vt.Rows(),
			beforeCols,
			beforeRows,
		)
	}

	s.Layout(ui.LayoutContext{}, ui.Rect{W: 20, H: 5})
	if attempts != 2 {
		t.Fatalf("failed size was treated as committed; attempts=%d want=2", attempts)
	}
}
