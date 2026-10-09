package viewmodel

import (
	"strings"
	"testing"
	"time"

	"crude/tui/settings"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/ui"
)

// setTestFrame writes the frame insets directly onto the app theme.
func setTestFrame(app *App, top, right, bottom, left int) {
	app.Theme.Spacing.Frame = ui.Insets{Top: top, Right: right, Bottom: bottom, Left: left}
}

// TestFrameInsetsShiftShellGeometry verifies the whole shell re-insets: the
// header/status/activity/sidebar slots move inside the frame, the content
// column narrows from both sides, and the chat area starts lower.
func TestFrameInsetsShiftShellGeometry(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	setTestFrame(app, 2, 3, 1, 4)
	lay := app.computeContentLayout()

	// Sidebar column starts after the left frame; content ends before right.
	if got := lay.SidebarW; got < 4 {
		t.Fatalf("SidebarW=%d must include the %d-column left frame", got, app.frameLeftCol())
	}
	if lay.ContentW != 100-lay.SidebarW-3 {
		t.Fatalf("ContentW=%d, want width minus left rail and 3 right-frame columns", lay.ContentW)
	}
	if lay.ChatAreaTop != app.chromeTopRow() {
		t.Fatalf("ChatAreaTop=%d, want chromeTopRow=%d", lay.ChatAreaTop, app.chromeTopRow())
	}
	if app.chromeTopRow() != 2 /* frame top */ +2 /* header */ {
		t.Fatalf("chromeTopRow=%d, want frameTop+2", app.chromeTopRow())
	}

	app.configureShell()
	header := app.shell.slots[shellHeaderSlot].rect
	if header.X != 4 || header.Y != 2 || header.W != 100-4-3 {
		t.Fatalf("header slot=%+v, want inset by frame left/top/right", header)
	}
	if got := app.chromeBottomRows(); got != 3 {
		t.Fatalf("bottom chrome=%d, want footer plus frame", got)
	}
	footer := app.shell.slots[shellFooterSlot].rect
	if footer.Y != 30-1-2 {
		t.Fatalf("footer slot Y=%d, want height-frameBottom-2", footer.Y)
	}
	activity := app.shell.slots[shellActivitySlot].rect
	if activity.X != 4 || activity.Y != 4 {
		t.Fatalf("activity slot=%+v, want X=frameLeft, Y=frameTop+2", activity)
	}
	if dock := app.termDockRect; !dock.Empty() && dock.W != lay.ContentW {
		t.Fatalf("term dock W=%d, want content width %d", dock.W, lay.ContentW)
	}
}

// TestFrameInsetsDefaultZero keeps the zero-padding contract: a theme straight
// from the factory renders edge to edge — the header content row IS row 0 and
// the main chrome starts right below the two header rows.
func TestFrameInsetsDefaultZero(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	lay := app.computeContentLayout()
	if app.frameTopRow() != 0 || app.frameBottomRow() != 0 || app.frameLeftCol() != 0 || app.frameRightCol() != 0 {
		t.Fatalf("default frame=%+v, want all zeros", app.frameInsets())
	}
	if lay.ChatAreaTop != 2 {
		t.Fatalf("ChatAreaTop=%d, want 2 (header rows 0-1, no forced pad)", lay.ChatAreaTop)
	}
	if lay.SidebarW <= 0 {
		t.Fatalf("SidebarW=%d must stay positive with zero frame", lay.SidebarW)
	}
}

// TestApplyFramePreviewLiveRegeometry verifies the settings preview re-insets
// the shell per keystroke without saving, and cancel restores the saved frame.
func TestApplyFramePreviewLiveReInsets(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	_ = app.computeContentLayout()
	before := app.computeLayout().ChatAreaTop

	five := 5
	app.applyFramePreview(settings.FramePreviewMsg{
		Top: &five, Bottom: &five, Left: &five, Right: &five,
	})
	if got := app.computeLayout().ChatAreaTop; got != before+5 {
		t.Fatalf("live preview ChatAreaTop=%d, want %d after top+5", got, before+5)
	}

	// Cancel restores the persisted frame.
	app.clearFramePreview()
	if got := app.computeLayout().ChatAreaTop; got != before {
		t.Fatalf("after cancel ChatAreaTop=%d, want restored %d", got, before)
	}
}

// TestApplyFramePreviewReplacesNotAdds guards the live-preview math: the
// settings form seeds rows with the PERSISTED values, so its message carries
// the complete frame. Editing one side must not double the untouched sides —
// the overlay replaces the persisted frame instead of stacking on it.
func TestApplyFramePreviewReplacesNotAdds(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	// Persisted frame: top=2, the rest 0 (the user's saved preference).
	setTestFrame(app, 2, 0, 0, 0)
	_ = app.computeContentLayout()

	// The settings model sends the full seeded frame: top stays 2, left is
	// edited to 4.
	two, four := 2, 4
	app.applyFramePreview(settings.FramePreviewMsg{
		Top: &two, Left: &four,
	})

	frame := app.frameInsets()
	if frame.Top != 2 {
		t.Fatalf("untouched top must not double: frame=%+v", frame)
	}
	if frame.Left != 4 {
		t.Fatalf("edited left must apply: frame=%+v", frame)
	}
	if frame.Bottom != 0 || frame.Right != 0 {
		t.Fatalf("untouched bottom/right must stay zero: frame=%+v", frame)
	}
}

// TestApplyFramePreviewBlankFieldMeansZero verifies a blanked form field
// previews as zero (what a save persists), not as the previously persisted
// value — preview and save must agree.
func TestApplyFramePreviewBlankFieldMeansZero(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	setTestFrame(app, 2, 0, 0, 0)

	// User clears the top field: the message carries nil for top and the
	// model's seeded values for the rest.
	zero := 0
	app.applyFramePreview(settings.FramePreviewMsg{
		Top: nil, Bottom: &zero, Left: &zero, Right: &zero,
	})
	if got := app.frameInsets().Top; got != 0 {
		t.Fatalf("blanked top must preview as 0, got %d", got)
	}
}

// TestHeaderRightRunStaysInsidePaddedSlot verifies the header's right-aligned
// clock/usage runs stop before the right frame: the projection width must
// mirror the slot (terminal width minus horizontal frame), not the terminal.
// The frame columns are background-painted blanks; the assertion is about
// visible glyphs, not background cells.
func TestHeaderRightRunStaysInsidePaddedSlot(t *testing.T) {
	app := newFullTestApp(t)
	app.ModelName = "some-model"
	wsOf(app).StartTime = time.Now().Add(-time.Minute)
	app.width, app.height = 100, 30
	setTestFrame(app, 0, 6, 0, 0)

	app.configureShell()
	app.configureHeaderScreen()
	app.paintShellFrame()

	buf := app.mainSurf.Buffer()
	rightEdge := 100 - 6 // first right-frame column
	for y := app.frameTopRow(); y < app.frameTopRow()+2; y++ {
		for x := rightEdge; x < buf.Width(); x++ {
			if c := buf.Get(x, y); !c.IsBlank() {
				t.Fatalf("header painted a glyph into the right frame at (%d,%d): %q", x, y, c.Char)
			}
		}
	}

	// And the clock must actually be painted inside the slot (guards against
	// the assertion passing because the header painted nothing at all).
	// The right-run ends just before the slot's right inner edge.
	clockVisible := false
	for y := app.frameTopRow(); y < app.frameTopRow()+2; y++ {
		for x := 0; x < rightEdge; x++ {
			if strings.ContainsRune("0123456789msdhs", buf.Get(x, y).Char) {
				clockVisible = true
			}
		}
	}
	if !clockVisible {
		t.Fatal("header clock/usage runs not painted inside the slot")
	}
}

// TestThemeFrameFromWorkspaceSettings verifies the persisted paddings land on
// the live theme frame during preset application (the settings-save path).
func TestThemeFrameFromWorkspaceSettings(t *testing.T) {
	app := newFullTestApp(t)
	two, three, one, four := 2, 3, 1, 4
	wsOf(app).SetAppDefaults(ttypes.AppSettings{
		Theme: ttypes.ThemeSettings{PadTop: &two, PadBottom: &three, PadLeft: &one, PadRight: &four},
	})
	app.ApplyThemePreset("dark")
	frame := app.Theme.Spacing.Frame
	if frame.Top != 2 || frame.Bottom != 3 || frame.Left != 1 || frame.Right != 4 {
		t.Fatalf("theme frame after preset=%+v, want persisted values", frame)
	}
	if got := app.computeContentLayout().ChatAreaTop; got != 2+2 {
		t.Fatalf("ChatAreaTop=%d, want frameTop(2)+header(2)", got)
	}
}

// TestThemeFrameDefaultsWhenUnset verifies an empty config leaves the theme's
// zero frame untouched — no padding is forced on anyone.
func TestThemeFrameDefaultsWhenUnset(t *testing.T) {
	app := newFullTestApp(t)
	app.ApplyThemePreset("dark")
	frame := app.Theme.Spacing.Frame
	if frame.Top != 0 || frame.Bottom != 0 || frame.Left != 0 || frame.Right != 0 {
		t.Fatalf("theme frame=%+v, want zero (padding is opt-in only)", frame)
	}
}

// TestFramePadClamped verifies insane values clamp into the 0..20 band so the
// shell can never be pushed off screen.
func TestFramePadClamped(t *testing.T) {
	app := newFullTestApp(t)
	huge := 100
	wsOf(app).SetAppDefaults(ttypes.AppSettings{
		Theme: ttypes.ThemeSettings{PadTop: &huge},
	})
	app.ApplyThemePreset("dark")
	if got := app.frameTopRow(); got != 20 {
		t.Fatalf("clamped top=%d, want 20", got)
	}
	if got := clampFramePad(-5); got != 0 {
		t.Fatalf("clampFramePad(-5)=%d, want 0", got)
	}
}

// TestThemeSpacingDefaults verifies the shared theme factory still carries the
// frame (zero insets) so every theme rebuild includes the field.
func TestThemeSpacingDefaults(t *testing.T) {
	th := theme.DefaultDark()
	if th.Spacing.Frame != (ui.Insets{}) {
		t.Fatalf("default frame=%+v, want zero insets", th.Spacing.Frame)
	}
}
