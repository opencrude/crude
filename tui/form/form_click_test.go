package form

import (
	"testing"

	"crude/tui/theme"
)

// TestForm_HitTest exercises the form's hit-test for clicking on
// individual form items. The mouse dispatcher uses this to focus
// the clicked field (and put TextItems into edit mode).
func TestForm_HitTest(t *testing.T) {
	th := theme.DefaultDark()
	f := NewForm(th, "Title", "hint",
		NewTextItem(th, "Field A", "default-a", false),
		NewTextItem(th, "Field B", "default-b", false),
		NewTextItem(th, "Field C", "default-c", false),
	)

	// Form layout (from form.View):
	//   line 0: empty
	//   line 1: title
	//   line 2: separator
	//   line 3: first item
	//   line 4: second item
	//   line 5: third item
	cases := []struct {
		line     int
		wantItem int
	}{
		{0, -1}, // leading empty
		{1, -1}, // title
		{2, -1}, // separator
		{3, 0},  // first item
		{4, 1},  // second item
		{5, 2},  // third item
		{6, -1}, // out of range
	}
	for _, c := range cases {
		hit := f.HitTest(c.line)
		if hit.ItemIndex != c.wantItem {
			t.Errorf("HitTest(%d) = ItemIndex=%d, want %d", c.line, hit.ItemIndex, c.wantItem)
		}
	}
}

// TestForm_HitTest_WithButtons verifies the button-row hit-test
// returns InButtonRow=true and ItemIndex=-1 (since the caller
// does further X-based hit-test for the specific button).
func TestForm_HitTest_WithButtons(t *testing.T) {
	th := theme.DefaultDark()
	f := NewFormWithButtons(th, "Title", "hint",
		[]Button{
			{Label: "Save"},
			{Label: "Cancel"},
		},
		NewTextItem(th, "Field A", "x", false),
	)

	// Layout: line 0=empty, 1=title, 2=sep, 3=item, 4=blank, 5=buttons
	hit := f.HitTest(5)
	if !hit.InButtonRow {
		t.Error("expected InButtonRow=true on button row")
	}
	if hit.ItemIndex != -1 {
		t.Errorf("expected ItemIndex=-1 on button row, got %d", hit.ItemIndex)
	}

	// Clicking on the item row should still return the item.
	hit = f.HitTest(3)
	if hit.ItemIndex != 0 {
		t.Errorf("expected item 0 on line 3, got %d", hit.ItemIndex)
	}
	if hit.InButtonRow {
		t.Error("expected InButtonRow=false on item row")
	}
}

// TestForm_FocusItem_FromClick verifies the click-to-focus path
// sets focus and starts edit mode (via the caller, for TextItems).
func TestForm_FocusItem_FromClick(t *testing.T) {
	th := theme.DefaultDark()
	f := NewForm(th, "Title", "hint",
		NewTextItem(th, "A", "x", false),
		NewTextItem(th, "B", "y", false),
	)
	f.FocusItem(1)
	if f.FocusedItem() != 1 {
		t.Errorf("expected focused=1, got %d", f.FocusedItem())
	}
	ti := f.FocusedTextItem()
	if ti == nil {
		t.Fatal("expected TextItem at index 1")
	}
	if ti.Label() != "B" {
		t.Errorf("expected label 'B', got %q", ti.Label())
	}
}
