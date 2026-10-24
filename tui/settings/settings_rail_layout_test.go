package settings

import (
	"testing"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

// TestSettingsNetworkGroupIsLast pins the rail order: the application-owned
// Network group (Connection / Proxy / Certificates / Remote access) closes the rail, after
// every AI, Security, and IDE entry, rather than sitting between AI and
// Security.
func TestSettingsNetworkGroupIsLast(t *testing.T) {
	for _, scope := range []types.SettingsScope{types.SettingsScopeApplication, types.SettingsScopeWorkspace} {
		model := NewScoped(theme.DefaultDark(), types.AppSettings{}, nil, scope, types.AppSettings{}, types.WorkspaceOverrides{})
		items := model.CategoryItems()
		last := -1
		for index, item := range items {
			if !item.Header && !item.Spacer {
				last = index
			}
		}
		if last < 3 {
			t.Fatalf("scope %s produced too few category rows (%d)", scope, last+1)
		}
		// The final four category rows must be Network's pages.
		tail := []string{items[last-3].Label, items[last-2].Label, items[last-1].Label, items[last].Label}
		if want := []string{"Connection", "Proxy", "Certificates", "Remote access"}; !equalStrings(tail, want) {
			t.Fatalf("scope %s rail tail = %v, want Network last %v", scope, tail, want)
		}
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func newSettingsFrame(t *testing.T, cat, w, h int) (*Model, *WorkspaceSettingsSurface, *ui.Host) {
	t.Helper()
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{}, nil)
	model.SelectCategory(cat)
	root := NewSurface(th, model)
	root.refreshForm()
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", root, nil, true, w, h)
	root.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(w, h)
	host.Layout()
	return model, root, host
}

// TestSettingsRailDividerCarriesPopupBackground pins the fix for the rail rule
// that read as a background-less strip inside the settings window: every
// divider cell now carries the popup fill explicitly instead of inheriting
// from an unmounted surface below.
func TestSettingsRailDividerCarriesPopupBackground(t *testing.T) {
	popupBG := ui.NewTokenSet(theme.DefaultDark()).Color(ui.ColorPopupBg)
	for _, size := range []ui.Size{{W: 124, H: 30}, {W: 90, H: 24}, {W: 70, H: 18}} {
		_, _, host := newSettingsFrame(t, catLSP, size.W, size.H)
		bar, ok := host.ScreenRect(WorkspaceSettingsDividerID)
		if !ok || bar.Empty() {
			t.Fatalf("size %+v: settings divider not laid out", size)
		}
		buf := surface.NewCellBuffer(size.W, size.H)
		host.Paint(buf)
		for y := bar.Y; y < bar.Y+bar.H; y++ {
			for x := bar.X; x < bar.X+bar.W; x++ {
				if bg := buf.Get(x, y).BG; !bg.Equal(popupBG) {
					t.Fatalf("size %+v: divider cell (%d,%d) bg=%v want popup bg %v — rule has no explicit background", size, x, y, bg, popupBG)
				}
			}
		}
	}
}

// TestSettingsFormScrollbarLeavesContentClickable checks the shared invariant:
// the scrollbar's reserved gutter is one blank column between the form's last
// content column and the painted stroke, the stroke's own column is the bar,
// and neither the form's last column nor the blank gutter is captured by the
// scrollbar's pointer target.
func TestSettingsFormScrollbarLeavesContentClickable(t *testing.T) {
	for _, size := range []ui.Size{{W: 124, H: 30}, {W: 90, H: 18}, {W: 76, H: 16}} {
		_, root, host := newSettingsFrame(t, catLSP, size.W, size.H)
		bar := root.formScroll
		if bar == nil || !bar.Needed() {
			continue // content fits this size; nothing to assert
		}
		barRect, ok := host.ScreenRect(bar.ID())
		if !ok {
			t.Fatalf("size %+v: form scrollbar not mounted", size)
		}
		if barRect.W != ui.ScrollbarWidth {
			t.Fatalf("size %+v: form scrollbar width=%d want the painted stroke only (%d)", size, barRect.W, ui.ScrollbarWidth)
		}
		contentRect, _ := host.ScreenRect(WorkspaceSettingsFormID)
		if gap := barRect.X - (contentRect.X + contentRect.W); gap != ui.ScrollbarWidth {
			t.Fatalf("size %+v: %d empty columns between form and scrollbar, want %d (form=%+v bar=%+v)",
				size, gap, ui.ScrollbarWidth, contentRect, barRect)
		}
		probeY := barRect.Y + 1
		strokeX := barRect.X + barRect.W - 1
		if hit := host.HitTest(strokeX, probeY); hit != bar.ID() {
			t.Fatalf("size %+v: stroke column hit=%q want scrollbar %q", size, hit, bar.ID())
		}
		// The form's last content column and the reserved blank column before
		// the stroke must both stay off the scrollbar's pointer target.
		for _, x := range []int{contentRect.X + contentRect.W - 1, barRect.X - 1} {
			if hit := host.HitTest(x, probeY); hit == bar.ID() {
				t.Fatalf("size %+v: column %d landed on the scrollbar (form=%+v bar=%+v)", size, x, contentRect, barRect)
			}
		}
	}
}

// TestSettingsRailScrollbarLeavesRowsClickable does the same for the category
// rail scrollbar: the reserved column protects the list rows.
func TestSettingsRailScrollbarLeavesRowsClickable(t *testing.T) {
	_, root, host := newSettingsFrame(t, catLSP, 76, 14)
	bar := root.listScroll
	if bar == nil || !bar.Needed() {
		t.Fatal("rail should overflow a 14-row panel and mount its scrollbar")
	}
	barRect, ok := host.ScreenRect(bar.ID())
	if !ok {
		t.Fatal("rail scrollbar not mounted")
	}
	if barRect.W != ui.ScrollbarWidth {
		t.Fatalf("rail scrollbar width=%d want the painted stroke only (%d)", barRect.W, ui.ScrollbarWidth)
	}
	listRect, _ := host.ScreenRect(WorkspaceSettingsListID)
	if gap := barRect.X - (listRect.X + listRect.W); gap != ui.ScrollbarWidth {
		t.Fatalf("%d empty columns between rail list and scrollbar, want %d (list=%+v bar=%+v)", gap, ui.ScrollbarWidth, listRect, barRect)
	}
	probeY := barRect.Y + 1
	strokeX := barRect.X + barRect.W - 1
	if hit := host.HitTest(strokeX, probeY); hit != bar.ID() {
		t.Fatalf("stroke column hit=%q want rail scrollbar %q", hit, bar.ID())
	}
	for _, x := range []int{listRect.X + listRect.W - 1, barRect.X - 1} {
		if hit := host.HitTest(x, probeY); hit == bar.ID() {
			t.Fatalf("column %d landed on the rail scrollbar (list=%+v bar=%+v)", x, listRect, barRect)
		}
	}
}
