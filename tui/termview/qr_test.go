//go:build cgo

package termview

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"crude/terminalvt"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestExpoHalfBlockQRSurvivesFragmentedVTAndPaint(t *testing.T) {
	var fixture struct {
		Extent int
		Matrix []string
		Output string
	}
	data, err := os.ReadFile("testdata/expo_halfblock.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	engine, err := terminalvt.New(terminalvt.Options{Cols: 32, Rows: 18})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	// Each UTF-8 code unit can arrive in a separate ConPTY/SSH read.
	for _, b := range []byte(strings.ReplaceAll(fixture.Output, "\n", "\r\n")) {
		engine.Feed([]byte{b})
	}
	grid, err := engine.Rows(terminalvt.RowRequest{Start: -1, Count: 0})
	if err != nil {
		t.Fatal(err)
	}
	var view View
	view.Install(grid, 1)
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		buf := surface.NewCellBuffer(32, 18)
		view.Paint(buf, 0, 0, 32, 18, ui.NewTokenSet(th), false, false)
		// Reconstruct every module from the painted cell, not the source text.
		// toqr uses 1=black. Expo paints the inverse glyph over the background.
		for row, bits := range fixture.Matrix {
			for col, bit := range bits {
				c := buf.Get(col+1, row/2+1)
				if c.Width != 1 || c.FG == c.BG {
					t.Fatalf("QR geometry/contrast lost at %d,%d: %+v", col, row, c)
				}
				var top, bottom bool
				switch c.Content {
				case "█":
					top, bottom = true, true
				case "▀":
					top = true
				case "▄":
					bottom = true
				case " ":
				default:
					t.Fatalf("QR glyph was lost at %d,%d: %q", col, row, c.Content)
				}
				ink := top
				if row%2 != 0 {
					ink = bottom
				}
				if ink != (bit == '0') {
					t.Fatalf("paint changed QR module at %d,%d", col, row)
				}
			}
		}
		if len(fixture.Matrix) != fixture.Extent {
			t.Fatal("invalid QR fixture")
		}
	}
}
