package editor

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// BenchmarkLinkedViewTyping types into one view of a dense document while
// other views of it are open. "mutation" is the edit and its publication to
// every view; "paint" also repaints every view, as a frame does when all views
// are visible. Cost per view must stay a small constant: views share the
// document, history, syntax and width model, so nothing scales with the
// document size times the number of views.
func BenchmarkLinkedViewTyping(b *testing.B) {
	for _, lines := range []int{200, 20_000} {
		for _, views := range []int{1, 4, 8} {
			for _, paint := range []bool{false, true} {
				stage := "mutation"
				if paint {
					stage = "paint"
				}
				b.Run(fmt.Sprintf("lines%d/views%d/%s", lines, views, stage), func(b *testing.B) {
					primary := NewPreview(theme.DefaultDark(), "linked.go", strings.Repeat("func example(a, b int) int { return a + b }\n", lines), 1)
					b.Cleanup(primary.Close)
					primary.readOnly = false
					all := []*Editor{primary}
					for len(all) < views {
						v := primary.NewLinkedView()
						b.Cleanup(v.Close)
						all = append(all, v)
					}
					// Every view looks at a different part of the file.
					for i, v := range all {
						v.SetCursorPosition(i*lines/views, 4)
						v.viewW, v.viewH = 120, 40
					}
					surf := surface.NewSurface("linked", 120, 40, cell.TransparentBlank())
					ctx := ui.PaintContext{Theme: primary.th, Focused: true}
					for _, v := range all {
						v.Render(surf, ctx)
					}
					b.ReportAllocs()
					b.ResetTimer()
					insert := true
					for b.Loop() {
						key := tea.KeyPressMsg{Code: tea.KeyBackspace}
						if insert {
							key = tea.KeyPressMsg{Code: 'x', Text: "x"}
						}
						primary.HandleEvent(key)
						insert = !insert
						if paint {
							for _, v := range all {
								v.Render(surf, ctx)
							}
						}
					}
					b.StopTimer()
					if got, want := all[len(all)-1].LineText(0), primary.LineText(0); got != want {
						b.Fatalf("views diverged: %q vs %q", got, want)
					}
				})
			}
		}
	}
}
