package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

func BenchmarkDemoGalleryPaint_80x24(b *testing.B) {
	benchmarkDemoGalleryPaint(b, 80, 24)
}

func BenchmarkDemoGalleryPaint_120x40(b *testing.B) {
	benchmarkDemoGalleryPaint(b, 120, 40)
}

func benchmarkDemoGalleryPaint(b *testing.B, width, height int) {
	host := DemoGallery(theme.DefaultDark())
	host.Resize(width, height)
	buf := surface.NewCellBuffer(width, height)
	host.Paint(buf)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		host.Paint(buf)
	}
}

func BenchmarkDemoGalleryHandleKeyEditCycle(b *testing.B) {
	host := DemoGallery(theme.DefaultDark())
	host.Resize(80, 24)
	host.Focus(NewID(RootID, "tf-default"))
	insert := tea.KeyPressMsg{Code: 'a', Text: "a"}
	backspace := tea.KeyPressMsg{Code: tea.KeyBackspace}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		host.Handle(insert)
		host.Handle(backspace)
	}
}
