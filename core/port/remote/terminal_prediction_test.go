package remote

import (
	"crude/core/wire"
	"crude/terminalvt"
	"crude/workspace"
	"testing"
	"time"
)

func TestTerminalPredictionImmutableAndUnsafeModes(t *testing.T) {
	// mosh's adaptive thresholds act on SRTT/2: show above 60 ms SRTT until
	// 40 ms with nothing outstanding, underline above 160 ms until 100 ms.
	var policy terminalPredictionPolicy
	for _, step := range []struct {
		carrier    corewire.Carrier
		rtt        time.Duration
		active     bool
		show, flag bool
	}{
		{corewire.CarrierQUIC, 50 * time.Millisecond, false, false, false},
		{corewire.CarrierQUIC, 61 * time.Millisecond, false, true, false},
		{corewire.CarrierQUIC, 45 * time.Millisecond, false, true, false},
		{corewire.CarrierQUIC, 30 * time.Millisecond, true, true, false},
		{corewire.CarrierQUIC, 30 * time.Millisecond, false, false, false},
		{corewire.CarrierSSH, 170 * time.Millisecond, false, true, true},
		{corewire.CarrierSSH, 120 * time.Millisecond, false, true, true},
		{corewire.CarrierSSH, 100 * time.Millisecond, false, true, false},
		{corewire.CarrierLocal, time.Second, false, false, false},
		{"", time.Second, false, false, false},
	} {
		if got := policy.update(step.carrier, step.rtt, step.active); got != step.show || policy.flag != step.flag {
			t.Fatalf("%s at %v (active=%v): show=%v flag=%v, want %v %v", step.carrier, step.rtt, step.active, got, policy.flag, step.show, step.flag)
		}
	}
	row := make([]terminalvt.Cell, 8)
	for i := range row {
		row[i] = terminalvt.Cell{Text: " ", Width: 1}
	}
	p := workspace.TerminalProjection{Grid: terminalvt.Projection{Cols: 8, GridRows: 2, Total: 2, Cells: [][]terminalvt.Cell{row, row}, Cursor: terminalvt.Cursor{Visible: true}}}
	key := terminalvt.Key{Text: "q", Code: 'q'}
	next, ok := terminalPrediction(p, key, true)
	if !ok || next.Grid.Cells[0][0].Text != "q" || !next.Grid.Cells[0][0].Style.Underline || next.Grid.Cursor.X != 1 {
		t.Fatal("missing immediate provisional glyph")
	}
	if plain, ok := terminalPrediction(p, key, false); !ok || plain.Grid.Cells[0][0].Text != "q" || plain.Grid.Cells[0][0].Style.Underline {
		t.Fatal("unflagged prediction must not underline")
	}
	if p.Grid.Cells[0][0].Text != " " || p.Grid.Cells[0][0].Style.Underline {
		t.Fatal("prediction mutated authoritative cells")
	}
	for _, change := range []func(*workspace.TerminalProjection){
		func(p *workspace.TerminalProjection) { p.Grid.Synchronized = true },
		func(p *workspace.TerminalProjection) { p.Grid.Modes.AltScreen = true },
		func(p *workspace.TerminalProjection) { p.Grid.Modes.MouseTracking = true },
		func(p *workspace.TerminalProjection) { p.Grid.Cursor.Visible = false },
		func(p *workspace.TerminalProjection) { p.Grid.Total = 10 },
	} {
		q := p
		change(&q)
		if _, ok := terminalPrediction(q, key, true); ok {
			t.Fatal("unsafe prediction accepted")
		}
	}
	for _, key := range []terminalvt.Key{{Text: "界"}, {Text: "\n"}, {Text: "x", Mod: terminalvt.ModCtrl}} {
		if _, ok := terminalPrediction(p, key, true); ok {
			t.Fatal("unsafe key predicted")
		}
	}
}

func TestTerminalEchoKeepsOnlyMatchingUnconfirmedSuffix(t *testing.T) {
	row := make([]terminalvt.Cell, 12)
	for i := range row {
		row[i] = terminalvt.Cell{Text: " ", Width: 1}
	}
	row[0].Text = "$"
	base := workspace.TerminalProjection{Grid: terminalvt.Projection{Cols: 12, GridRows: 1, Total: 1, Cells: [][]terminalvt.Cell{row}, Cursor: terminalvt.Cursor{X: 1, Visible: true}}}
	predicted := base
	for _, key := range []terminalvt.Key{{Text: "a"}, {Text: "B", Mod: terminalvt.ModShift}, {Code: terminalvt.KeySpace}, {Text: "c"}} {
		var ok bool
		predicted, ok = terminalPrediction(predicted, key, true)
		if !ok {
			t.Fatal(key)
		}
	}
	next, _ := terminalPrediction(base, terminalvt.Key{Text: "a"}, false)
	suffix, matched, ok := reconcileEcho(base, predicted, next)
	if !ok || !matched || suffix.Grid.Cells[0][2].Text != "B" || suffix.Grid.Cursor.X != 5 || next.Grid.Cells[0][2].Text != " " {
		t.Fatal("older frame erased the suffix or speculation overwrote the authority")
	}
	mismatch, _ := terminalPrediction(base, terminalvt.Key{Text: "*"}, false)
	if _, _, ok := reconcileEcho(base, predicted, mismatch); ok {
		t.Fatal("masked remote input accepted as echo")
	}
}
