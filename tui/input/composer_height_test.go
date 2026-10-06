package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestComposerHeightStopsGrowingAndKeepsCompleteDraft(t *testing.T) {
	model := NewModel(theme.DefaultDark())
	model.SetWidth(80)
	for _, lines := range []int{1, 2, 4, 6, 12, 100} {
		draft := strings.Repeat("draft line\n", lines-1) + "DRAFT-END"
		model.SetInput(draft)
		want := 1 + min(lines, 6)
		if got := model.ShellHeight(80); got != want {
			t.Fatalf("%d draft lines request %d shell rows, want %d", lines, got, want)
		}
		model.LayoutShell(80, want)
		buf := surface.NewCellBuffer(80, want)
		model.PaintShell(buf, 0, 0, 80, want, ui.PaintContext{Focused: true})
		if model.Value() != draft || model.CursorFlat() != len([]rune(draft)) {
			t.Fatal("height limit changed the draft or cursor")
		}
		if text := composerRows(buf); !strings.Contains(text, "DRAFT-END") {
			t.Fatalf("clipped composer lost the visible caret end: %q", text)
		}
	}
}

func TestComposerScrollSurvivesUnchangedLayoutAndMapsPaintedText(t *testing.T) {
	model := NewModel(theme.DefaultDark())
	draft := "FIRST-ROW\none\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nFINAL-ROW"
	model.SetWidth(80)
	model.SetInput(draft)
	model.LayoutShell(80, 5)
	wheel := ui.MouseWheelEvent(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	model.HandleShell(ui.EventContext{LocalX: 3, LocalY: 3}, wheel)
	if model.input.ScrollOffset() == 0 {
		t.Fatal("fixture did not scroll away from the end")
	}
	offset := model.input.ScrollOffset()
	for frame := 0; frame < 2; frame++ {
		model.LayoutShell(80, 5)
		buf := surface.NewCellBuffer(80, 5)
		model.PaintShell(buf, 0, 0, 80, 5, ui.PaintContext{Focused: true})
		if model.input.ScrollOffset() != offset {
			t.Fatal("paint/layout snapped the user's scroll back to the cursor")
		}
		firstVisible := strings.Split(draft, "\n")[offset]
		if !strings.Contains(composerRows(buf), firstVisible) {
			t.Fatal("wheel changed the offset but not the painted content")
		}
		want := 0
		for _, line := range strings.Split(draft, "\n")[:offset] {
			want += len([]rune(line)) + 1
		}
		if pos, ok := model.MouseToInputPos(model.theme.Spacing.PadLR, 1); !ok || pos != want {
			t.Fatalf("scrolled hit points at %d, want painted source %d (ok=%v)", pos, want, ok)
		}
	}
}

func TestComposerResizeAndNavigationPreserveClippedContent(t *testing.T) {
	model := NewModel(theme.DefaultDark())
	model.SetInput(strings.Repeat("A paragraph that wraps while the composer remains bounded. ", 25) + "FINAL-ROW")
	for _, width := range []int{160, 60, 24, 80, 160} {
		height := model.ShellHeight(width)
		model.LayoutShell(width, height)
		if height > 7 || model.input.Bounds().W-model.input.PromptPrefixWidth() != model.InputTextWidth() {
			t.Fatal("height or measured/painted wrap widths diverged")
		}
		buf := surface.NewCellBuffer(width, height)
		model.PaintShell(buf, 0, 0, width, height, ui.PaintContext{Focused: true})
		if !strings.Contains(composerRows(buf), "FINAL-ROW") {
			t.Fatalf("width %d clipped the cursor's final row", width)
		}
	}
	original := model.Value()
	var event Event
	model, _, event = model.Update(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModCtrl}, false, false)
	if event != nil || model.CursorFlat() != 0 {
		t.Fatal("clipped document start navigation failed")
	}
	model.LayoutShell(80, model.ShellHeight(80))
	if model.input.ScrollOffset() != 0 {
		t.Fatal("document start did not reveal its visible row")
	}
	model, _, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl}, false, false)
	if model.Value() != original || model.CursorFlat() != len([]rune(original)) {
		t.Fatal("navigation trimmed draft or lost document end")
	}
}

func TestPastePromotionKeepsSmallListsButCondensesSubstantialOverflow(t *testing.T) {
	for _, test := range []struct {
		name, content string
		width         int
		attach        bool
	}{
		{"short paragraph", strings.Repeat("ordinary prose ", 35), 80, false},
		{"small tall checklist", strings.Repeat("- item\n", 24), 80, false},
		{"eight substantial lines", strings.Repeat("A complete diagnostic frame with context that is useful to inspect.\n", 8), 160, true},
		{"wide moderate paragraph", strings.Repeat("ordinary prose ", 80), 160, false},
		{"narrow long paragraph", strings.Repeat("ordinary prose ", 80), 80, true},
		{"tiny grid cannot promote a little text", strings.Repeat("ordinary prose ", 15), 16, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := NewModel(theme.DefaultDark())
			model.SetWidth(test.width)
			next, _, event := model.Update(tea.PasteMsg{Content: test.content}, false, false)
			_, attached := event.(PasteAttachmentEvent)
			if attached != test.attach {
				t.Fatalf("attached=%v want=%v; bytes=%d", attached, test.attach, len(test.content))
			}
			if !attached && next.Value() != test.content {
				t.Fatal("inline paste was not exact")
			}
		})
	}
}

func composerRows(buf *surface.CellBuffer) string {
	var text strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			text.WriteRune(buf.Get(x, y).Char)
		}
		text.WriteByte('\n')
	}
	return text.String()
}
