package window

import "testing"

func TestTabWidthsStayInsideNarrowAvailableWidth(t *testing.T) {
	for _, width := range []int{-4, 0, 1, 2, 5, 8, 12, 19} {
		w := newTestWindow()
		w.SetWidth(width)
		w.OpenTab("alpha", "", true)
		w.OpenTab("beta", "", true)
		w.OpenTab("gamma", "", true)

		if w.width < 0 {
			t.Fatalf("SetWidth(%d) retained negative width %d", width, w.width)
		}
		widths := w.computeTabWidths()
		if len(widths) != 3 {
			t.Fatalf("SetWidth(%d) returned %d widths, want 3", width, len(widths))
		}
		available := max(0, w.width-tabBarPadLeft-tabBarPadRight)
		tabSpace := max(0, available-(len(widths)-1)*tabGap)
		sum := 0
		for i, got := range widths {
			if got < 0 || got > available {
				t.Fatalf("SetWidth(%d) tab %d width=%d outside [0,%d]", width, i, got, available)
			}
			sum += got
		}
		if sum > tabSpace {
			t.Fatalf("SetWidth(%d) tab widths consume %d columns, only %d available after gaps", width, sum, tabSpace)
		}
		if hit := w.HitTest(w.width, 0); hit.TabIndex != -1 {
			t.Fatalf("SetWidth(%d) hit beyond actual width returned tab %d", width, hit.TabIndex)
		}
	}
}

func TestSingleTabShrinksBelowPreferredMinimum(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(5)
	w.OpenTab("alpha", "", true)
	widths := w.computeTabWidths()
	if len(widths) != 1 || widths[0] != 5 {
		t.Fatalf("single narrow tab widths=%v, want [5] (actual available width)", widths)
	}

	w.SetWidth(1)
	widths = w.computeTabWidths()
	if len(widths) != 1 || widths[0] != 1 {
		t.Fatalf("single one-column tab widths=%v, want [1]", widths)
	}
	if hit := w.HitTest(0, 0); hit.IsClose {
		t.Fatal("one-column tab must not expose a close hit region that cannot fit")
	}
}

func TestTabIdealWidthUsesCanonicalGraphemeColumns(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)
	w.OpenTab("界🧑‍💻", "", true)
	widths := w.computeTabWidths()
	// title=4 columns, pads=2, close=2, active marker reserve=2.
	if len(widths) != 1 || widths[0] != 10 {
		t.Fatalf("CJK/emoji tab width=%v, want [10]", widths)
	}
}
