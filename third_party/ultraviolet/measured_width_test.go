package uv

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestStyledStringAcceptsMeasuredWidthBeyondFour(t *testing.T) {
	ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{Exact: map[string]int{"🧑‍🌾": 5}})
	t.Cleanup(func() { ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{}) })
	buffer := NewScreenBuffer(8, 1)
	buffer.Method = ansi.GraphemeWidth
	NewStyledString("🧑‍🌾x").Draw(buffer, Rect(0, 0, 8, 1))
	if primary := buffer.CellAt(0, 0); primary == nil || primary.Content != "🧑‍🌾" || primary.Width != 5 {
		t.Fatalf("measured primary = %#v", primary)
	}
	if following := buffer.CellAt(5, 0); following == nil || following.Content != "x" {
		t.Fatalf("following cell = %#v", following)
	}
}
