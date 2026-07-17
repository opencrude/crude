package widgets

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContextMenu_BuildFlattensSelectable(t *testing.T) {
	st := &ContextMenuState{
		Title: "Test",
		Sections: []ContextMenuSection{
			{SectionLabel: "A", Items: []ContextMenuItem{
				{Label: "a1"},
				{Label: "a2", Disabled: true},
				{Label: "a3"},
			}},
			{SectionLabel: "B", Items: []ContextMenuItem{
				{Label: "b1"},
			}},
		},
	}
	st.Build()
	// Disabled item is skipped.
	assert.Len(t, st.FlatItems, 3)
}

func TestContextMenu_NextWraps(t *testing.T) {
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "a"},
			{Label: "b"},
		}}},
	}
	st.Build()
	st.Cursor = 1
	st.Next()
	assert.Equal(t, 0, st.Cursor, "Next from last wraps to first")
}

func TestContextMenu_PrevWraps(t *testing.T) {
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "a"},
			{Label: "b"},
		}}},
	}
	st.Build()
	st.Cursor = 0
	st.Prev()
	assert.Equal(t, 1, st.Cursor, "Prev from first wraps to last")
}

func TestContextMenu_ActivateRunsAction(t *testing.T) {
	called := false
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "a", Action: func() { called = true }},
		}}},
	}
	st.Build()
	assert.True(t, st.Activate())
	assert.True(t, called)
}

func TestContextMenu_ActivateSkipsDisabled(t *testing.T) {
	called := false
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "a", Disabled: true, Action: func() { called = true }},
		}}},
	}
	st.Build()
	assert.False(t, st.Activate())
	assert.False(t, called)
}

func TestContextMenu_ActivateEmpty(t *testing.T) {
	st := &ContextMenuState{}
	st.Build()
	assert.False(t, st.Activate())
}

func TestQuickMenu(t *testing.T) {
	called := 0
	st := QuickMenu("Test",
		ContextMenuItem{Label: "a", Action: func() { called++ }},
		ContextMenuItem{Label: "b", Action: func() { called++ }},
	)
	assert.Equal(t, "Test", st.Title)
	assert.Len(t, st.Sections, 1)
	assert.Len(t, st.Sections[0].Items, 2)
	st.Activate()
	assert.Equal(t, 1, called)
}

// Visual contract tests migrated to BuildContextMenuFrame + cell-buffer
// assertions in contextmenu_frame_test.go:
//   - TestContextMenuLines_IncludesTitleAndSections → TestBuildContextMenuFrame_RendersItemsAndSections
//   - TestContextMenuLines_DisabledItemRendered → TestBuildContextMenuFrame_DisabledItemRendered
//   - TestContextMenuLines_DetailNotTruncated → TestBuildContextMenuFrame_DetailNotTruncated
//   - TestContextMenuLines_HighlightedSpansFullWidth → TestBuildContextMenuFrame_HighlightedHasBG
//   - TestContextMenuLines_NoTitleInContent → TestBuildContextMenuFrame_NoTitleInContent
//   - TestContextMenuLines_DividerBetweenSections → TestBuildContextMenuFrame_DividerBetweenSections
//
// Reveal animation tests remain in contextmenu_v2_test.go testing
// ContextMenuLinesReveal directly; the reveal feature is pending
// WindowFrame animation support (Phase 3+).

func TestContextMenuPopupWidth_Capped(t *testing.T) {
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "x"},
			{Label: "very long label that exceeds cap " + repeat("a", 100)},
		}}},
	}
	w := ContextMenuPopupWidth(st)
	assert.LessOrEqual(t, w, 60)
}

func TestContextMenuPopupHeight_AccountsForAllParts(t *testing.T) {
	st := &ContextMenuState{
		Title: "T",
		Sections: []ContextMenuSection{
			{SectionLabel: "A", Items: []ContextMenuItem{{Label: "a1"}, {Label: "a2"}}},
			{SectionLabel: "B", Items: []ContextMenuItem{{Label: "b1"}}},
		},
	}
	// 1 (sec A label) + 2 (items) + 1 (divider) + 1 (sec B label) +
	// 1 (item) + 2 (top + bottom border) = 8. No extra bottom pad.
	assert.Equal(t, 8, ContextMenuPopupHeight(st))
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
