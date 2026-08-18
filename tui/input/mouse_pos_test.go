package input

import (
	"testing"

	"crude/tui/theme"
)

// TestMouseToInputPosUsesInputLocalGeometry locks the coordinate contract of
// the migrated mouse path: MouseToInputPos maps shellinput-LOCAL coordinates
// (relative to the input shell rect, as delivered by the Host hit tree)
// against the model's own painted rows — it must never depend on a screen Y or
// chat-area offset. Verified at narrow/standard/wide widths.
func TestMouseToInputPosUsesInputLocalGeometry(t *testing.T) {
	for _, tc := range []struct {
		name  string
		width int
	}{
		{"narrow", 10},
		{"standard", 80},
		{"wide", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(theme.DefaultDark())
			m.SetWidth(tc.width)
			m.SetInput("hi")
			padLR := m.theme.Spacing.PadLR
			firstRow := m.textAreaY()

			if pos, ok := m.MouseToInputPos(padLR, firstRow); !ok || pos != 0 {
				t.Fatalf("first row first col = %d ok=%v, want 0", pos, ok)
			}
			if pos, ok := m.MouseToInputPos(padLR+2, firstRow); !ok || pos != 2 {
				t.Fatalf("first row col 2 = %d ok=%v, want 2", pos, ok)
			}
			if _, ok := m.MouseToInputPos(0, firstRow-1); ok {
				t.Fatal("row above the text area must be outside")
			}
		})
	}
}

// TestMouseToInputPosWrappedRows verifies a click on the second visual row
// maps to the flat rune index past the first line.
func TestMouseToInputPosWrappedRows(t *testing.T) {
	m := NewModel(theme.DefaultDark())
	m.SetWidth(200)
	m.SetInput("alpha\nbeta")
	padLR := m.theme.Spacing.PadLR
	firstRow := m.textAreaY()
	want := len("alpha") + 1 + 2
	if pos, ok := m.MouseToInputPos(padLR+2, firstRow+1); !ok || pos != want {
		t.Fatalf("second row col 2 = %d ok=%v, want %d", pos, ok, want)
	}
}
