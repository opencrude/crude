package widgets

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"crude/tui/theme"
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

func TestContextMenuLines_IncludesTitleAndSections(t *testing.T) {
	th := theme.DefaultDark()
	st := &ContextMenuState{
		Title: "Editor",
		Sections: []ContextMenuSection{
			{SectionLabel: "Code", Items: []ContextMenuItem{
				{Label: "Go to def"},
				{Label: "Find refs"},
			}},
		},
	}
	lines := ContextMenuLines(th, st, 0)
	// Chrome wraps the menu: top border + section label + 2 items +
	// bottom border = 5. The title is integrated into the top
	// border (not duplicated as a body row).
	assert.Len(t, lines, 5)
}

func TestContextMenuLines_DisabledItemRendered(t *testing.T) {
	th := theme.DefaultDark()
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "enabled"},
			{Label: "disabled", Disabled: true},
		}}},
	}
	lines := ContextMenuLines(th, st, 0)
	// Both items appear in the rendered output even when disabled.
	// Chrome wraps them: top border + 2 items + bottom border = 4.
	assert.Len(t, lines, 4)
}

// TestContextMenuLines_DetailNotTruncated is a regression test for
// the "Ctrl+V" → "Ctrl" truncation bug. The popup used to compute
// its width from label+detail but render with a hardcoded 18-char
// label padding, causing details to overflow the popup's clipping
// rectangle. Each rendered line should now contain the full detail.
func TestContextMenuLines_DetailNotTruncated(t *testing.T) {
	th := theme.DefaultDark()
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "Paste", Detail: "Ctrl+V"},
			{Label: "Find & replace", Detail: "Ctrl+H"},
		}}},
	}
	lines := ContextMenuLines(th, st, 0)
	// Chrome wraps the menu: top border + 2 items + bottom border = 4.
	assert.Len(t, lines, 4)
	plain1 := stripAnsi(lines[1])
	assert.Contains(t, plain1, "Paste", "first line missing label")
	assert.Contains(t, plain1, "Ctrl+V", "first line missing detail")
	plain2 := stripAnsi(lines[2])
	assert.Contains(t, plain2, "Find & replace", "second line missing label")
	assert.Contains(t, plain2, "Ctrl+H", "second line missing detail")
}

func TestContextMenuLines_HighlightedSpansFullWidth(t *testing.T) {
	th := theme.DefaultDark()
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "Paste", Detail: "Ctrl+V"},
		}}},
	}
	st.Build()
	st.Cursor = 0
	lines := ContextMenuLines(th, st, 0)
	// The highlighted item line (lines[1], after the top border)
	// should contain a 48;2 (bg RGB) sequence somewhere in its ANSI
	// codes. lipgloss emits \x1b[48;2;R;G;B m for backgrounds.
	assert.Contains(t, lines[1], "48;2", "highlighted line missing ANSI bg color")
}

func TestContextMenuLines_NoTitleInContent(t *testing.T) {
	// The title is integrated into the top border (not duplicated as
	// a body row). So the body has 1 item; the chrome adds 2 more
	// lines (top + bottom border) for a total of 3.
	th := theme.DefaultDark()
	st := &ContextMenuState{
		Title: "Editor",
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "Copy", Detail: "Ctrl+C"},
		}}},
	}
	lines := ContextMenuLines(th, st, 0)
	assert.Len(t, lines, 3) // top border + 1 item + bottom border
}

// stripAnsi removes ANSI escape codes from s. Good enough for
// assertion-level checks.
func stripAnsi(s string) string {
	var b []byte
	inEsc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			// ESC sequences end at the first letter (e.g. 'm', 'K', 'H').
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				inEsc = false
			}
			continue
		}
		b = append(b, c)
	}
	return string(b)
}

func TestContextMenuLines_DividerBetweenSections(t *testing.T) {
	th := theme.DefaultDark()
	st := &ContextMenuState{
		Sections: []ContextMenuSection{
			{SectionLabel: "A", Items: []ContextMenuItem{{Label: "a1"}}},
			{SectionLabel: "B", Items: []ContextMenuItem{{Label: "b1"}}},
		},
	}
	lines := ContextMenuLines(th, st, 0)
	// Chrome wraps the menu: top border + (2 section labels + 1
	// divider + 2 items) + bottom border = 7.
	assert.Len(t, lines, 7)
}

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
