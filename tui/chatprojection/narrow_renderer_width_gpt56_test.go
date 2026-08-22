package chatprojection

import (
	"testing"

	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

func TestRendererDerivesNarrowWidthsFromActualBoundsGPT56(t *testing.T) {
	th := theme.DefaultDark()
	r := NewRenderer(th)
	padLR := th.Spacing.PadLR

	for _, width := range []int{-5, 0, 1, 3, 7, 20, 39} {
		r.SetSize(width, 10)
		wantWidth := max(0, width)
		wantInner := max(0, wantWidth-2*padLR)
		wantContent := max(0, wantInner-messageBodyChromeWidth)
		contentW, innerW := r.availableMessageWidths()
		if r.width != wantWidth {
			t.Fatalf("SetSize(%d) stored width=%d, want %d", width, r.width, wantWidth)
		}
		if innerW != wantInner || contentW != wantContent {
			t.Fatalf(
				"width=%d content/inner=(%d,%d), want (%d,%d)",
				width, contentW, innerW, wantContent, wantInner,
			)
		}
		if contentW < minimumMarkdownBodyWidthGPT56 && r.MdRenderer() != nil {
			t.Fatalf("width=%d content=%d unexpectedly enabled minimum-20 markdown renderer", width, contentW)
		}
	}
}

const minimumMarkdownBodyWidthGPT56 = 20

func TestSubPaneCallbackReceivesNoSyntheticNarrowWidthGPT56(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(3, 6)
	r.SetChatAreaBounds(0, 6)
	r.SetPaneScrollbarWidth(1)
	paneScroll := ui.NewScrollModel()
	paneScroll.SetContent(2)
	paneScroll.SetViewport(1)
	r.BindPaneScroll(paneScroll)
	gotWidth := -1
	r.SetSubPaneContentFn(func(_ string, width, _, _ int) []SubPaneLine {
		gotWidth = width
		return []SubPaneLine{{Text: "pane"}}
	})
	r.AppendMessage(types.ChatMessage{
		Role:      types.RoleAssistant,
		SubPaneID: "pane-1",
	})

	_, _, _ = r.ExportMessageRows(3)
	if gotWidth != 0 {
		t.Fatalf("three-column subpane callback width=%d, want 0 after pane/main scrollbar footprint", gotWidth)
	}
}
