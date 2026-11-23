package imageviewer

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"

	"crude/tui/cell"
	"crude/tui/compositor"
	"crude/tui/rendering"
	"crude/tui/screens/panelchrome"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func resource(t *testing.T, img image.Image) *tea.TerminalImage {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	result := tea.NewTerminalImage(data.Bytes(), img)
	if result == nil {
		t.Fatal("could not prepare image resource")
	}
	return result
}

func mount(w, h int) (*Screen, *ui.Host) {
	s := NewWithID(nil, "pane/image")
	host := ui.NewHost(nil, s)
	host.Resize(w, h)
	host.Layout()
	host.Focus(s.ID())
	return s, host
}

func paint(host *ui.Host, w, h int) *surface.CellBuffer {
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	return buf
}

func text(buf *surface.CellBuffer) string {
	var out strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Content != "" {
				out.WriteString(c.Content)
			} else if c.Char != 0 {
				out.WriteRune(c.Char)
			}
		}
		out.WriteByte('\n')
	}
	return out.String()
}

func key(host *ui.Host, code rune) ui.EventResult {
	return host.Handle(tea.KeyPressMsg{Code: code})
}

func TestGraphicsFitZoomCropAndResetThroughHost(t *testing.T) {
	s, host := mount(40, 21) // image viewport is 40 × 20
	img := image.NewNRGBA(image.Rect(0, 0, 320, 160))
	res := resource(t, img)
	s.SetImage(img, res)
	if !s.SetImageGraphics(tea.ImageKitty, 8, 16) || s.SetImageGraphics(tea.ImageKitty, 8, 16) {
		t.Fatal("capability change result is not stable")
	}
	buf := paint(host, 40, 21)
	if p := s.ImagePlacement(); p.Image != res || p.Columns != 40 || p.Rows != 10 {
		t.Fatalf("2:1 source must fit into 40 × 10 measured cells: %+v", p)
	}
	if buf.Get(0, 6).Char != kitty.Placeholder || buf.Get(0, 5).Char == kitty.Placeholder {
		t.Fatal("initial image is not vertically centered in the body")
	}
	if s.Status() != "320 × 160 · 100% · Fit" || strings.Contains(text(buf), "320 × 160") {
		t.Fatalf("dimensions, scale or zoom control absent:\n%s", text(buf))
	}
	for i := 0; i < 2; i++ {
		if !key(host, '+').Handled {
			t.Fatal("zoom shortcut not handled")
		}
	}
	buf = paint(host, 40, 21)
	if p := s.ImagePlacement(); p.Image != res || p.Columns != 80 || p.Rows != 20 {
		t.Fatalf("zoom must retain one full-sized placement, not a crop upload: %+v", p)
	}
	// At 2× fit, columns 20 through 59 are visible. Explicit coordinates
	// survive horizontal clipping; inferred neighboring coordinates do not.
	want := string([]rune{kitty.Placeholder, kitty.Diacritic(0), kitty.Diacritic(20), kitty.Diacritic(int(res.ID() >> 24))})
	if got := buf.Get(0, 1); got.Content != want || got.Width != 1 || got.FG != renderingIdentity(res.ID()) {
		t.Fatalf("first clipped placeholder=%+v, want content %q", got, want)
	}
	key(host, tea.KeyRight)
	buf = paint(host, 40, 21)
	want = string([]rune{kitty.Placeholder, kitty.Diacritic(0), kitty.Diacritic(23), kitty.Diacritic(int(res.ID() >> 24))})
	if buf.Get(0, 1).Content != want {
		t.Fatal("right arrow did not reveal pixels to the right")
	}
	for i := 0; i < 50; i++ {
		key(host, tea.KeyRight)
	}
	buf = paint(host, 40, 21)
	if got := []rune(buf.Get(39, 1).Content); len(got) != 4 || got[2] != kitty.Diacritic(79) {
		t.Fatal("pan did not clamp at final image column")
	}
	key(host, '0')
	if p := s.ImagePlacement(); p.Columns != 40 || p.Rows != 10 || s.centerX != .5 || s.zoom != 1 {
		t.Fatalf("fit did not reset pan and zoom: %+v", p)
	}
	s.SetImageGraphics(tea.ImageKitty, 8, 24)
	if p := s.ImagePlacement(); p.Columns != 40 || p.Rows != 7 {
		t.Fatalf("measured cell aspect did not refit: %+v", p)
	}
}

func TestIconToolbarAlignmentSemanticsAndTooltips(t *testing.T) {
	for _, nerd := range []bool{false, true} {
		for _, width := range []int{80, 24, 17, 15, 9, 2, 0} {
			t.Run(fmt.Sprintf("nerd=%t/width=%d", nerd, width), func(t *testing.T) {
				th := theme.DefaultDark()
				th.Tree.Config.UseNerdFont = &nerd
				s := NewWithID(th, "pane/image")
				host := ui.NewHost(th, s)
				host.Resize(width, 8)
				host.Layout()
				s.SetImage(image.NewNRGBA(image.Rect(0, 0, 20, 20)), nil)
				buf := paint(host, width, 8)
				wantIcons := []string{"−", "+", "↔"}
				if nerd {
					wantIcons = []string{"\uf010", "\uf00e", "\uf065"}
				}
				wantLabels := []string{"Zoom out", "Zoom in", "Fit image"}
				wantTips := []string{"Zoom out (-)", "Zoom in (+)", "Fit image (0)"}
				right := width
				if width >= 3 {
					right-- // Default content inset leaves one cell at either edge.
				}
				// "[ icon ]": a Nerd Font icon keeps one blank cell after it
				// for the ink terminals draw wider than one cell.
				full := 5
				if nerd {
					full = 6
				}
				// One blank cell separates the buttons; a short row drops the
				// gaps before it shrinks a button.
				inner := right - (width - right)
				gap := 1
				if inner < 3*full+2*gap {
					gap = 0
				}
				toolbar := 3*full + 2*gap
				start := max(width-right, right-toolbar)
				for i, button := range s.buttons {
					bounds := button.Bounds()
					if bounds.X != start || bounds.Y != 0 || bounds.X+bounds.W > width {
						t.Fatalf("control %d not packed at right edge: %+v, start=%d", i, bounds, start)
					}
					start += bounds.W + gap
					// At Fit a large image cannot zoom out; an empty viewport cannot zoom.
					wantDisabled := i == 0 || i == 1 && s.viewport.Empty()
					if sem := button.Semantics(); sem.Role != ui.RoleButton || sem.Label != wantLabels[i] || sem.Disabled != wantDisabled || sem.Description != wantTips[i] {
						t.Fatalf("icon control %d has no descriptive accessible name/shortcut: %+v", i, sem)
					}
					source, ok := ui.Component(button).(ui.TooltipSource)
					if !ok {
						t.Fatalf("control %d does not expose the shared tooltip interface", i)
					}
					tip, anchor, ok := source.Tooltip()
					if ok != !bounds.Empty() {
						t.Fatalf("tooltip visibility differs from control bounds: %+v, ok=%t", bounds, ok)
					}
					if !ok {
						continue
					}
					if tip != wantTips[i] || anchor != (ui.Rect{W: bounds.W, H: bounds.H}) {
						t.Fatalf("tooltip must describe action/shortcut in control-local bounds: %q %+v", tip, anchor)
					}
					if _, ok := host.Component(button.ID()).(ui.TooltipSource); !ok {
						t.Fatal("mounted pointer target does not expose the shared tooltip interface")
					}
					host.Handle(tea.MouseMotionMsg{X: bounds.X, Y: 0})
					if host.Hovered() != button.ID() {
						t.Fatal("tooltip control is not the actual pointer target")
					}
					if bounds.W >= rendering.VisibleWidth(wantIcons[i])+4 {
						assertBracketedIcon(t, buf, bounds, wantIcons[i], nerd && bounds.W == full)
					}
					if inner >= 3*full && bounds.W != full {
						t.Fatalf("control %d is not the full bracketed button: %+v", i, bounds)
					}
				}
				if start-gap != right {
					t.Fatalf("toolbar end=%d, want right rail %d", start-gap, right)
				}
				row := strings.Split(text(buf), "\n")[0]
				if strings.Contains(row, "Zoom") || strings.Contains(row, "Fit") {
					t.Fatalf("text labels remain in icon-only toolbar: %q", row)
				}
				for x := 0; x < max(0, right-toolbar); x++ {
					if host.HitTest(x, 0) != s.ID() || buf.Get(x, 0).Char != ' ' {
						t.Fatal("old left-side controls retained paint or hit targets")
					}
				}
				s.SetImage(nil, nil)
				for _, button := range s.buttons {
					if !button.Semantics().Disabled {
						t.Fatal("accessible disabled state lost when image cleared")
					}
				}
			})
		}
	}
}

// assertBracketedIcon checks "[ icon ]" with the icon's ink in the middle:
// the same number of blank cells between each bracket and the ink. inkCell
// is the blank cell a Nerd Font icon's wide ink covers.
func assertBracketedIcon(t *testing.T, buf *surface.CellBuffer, bounds ui.Rect, icon string, inkCell bool) {
	t.Helper()
	ink := rendering.VisibleWidth(icon)
	if inkCell {
		ink++
	}
	left, right := bounds.X, bounds.X+bounds.W-1
	if buf.Get(left, 0).Char != '[' || buf.Get(right, 0).Char != ']' {
		t.Fatalf("button %+v is not bracketed: %q", bounds, cellText(buf, bounds))
	}
	at := left + 2
	if got := buf.Get(at, 0); got.Content != icon && string(got.Char) != icon {
		t.Fatalf("button %+v painted %q, want icon %q after one blank cell", bounds, cellText(buf, bounds), icon)
	}
	for x := left + 1; x < right; x++ {
		if (x < at || x >= at+ink) && buf.Get(x, 0).Char != ' ' {
			t.Fatalf("button %+v has ink outside the icon: %q", bounds, cellText(buf, bounds))
		}
	}
	if before, after := at-left-1, right-(at+ink); before != after {
		t.Fatalf("icon is not in the middle of %q: %d blank cells before it, %d after", cellText(buf, bounds), before, after)
	}
}

func cellText(buf *surface.CellBuffer, r ui.Rect) string {
	var b strings.Builder
	for x := r.X; x < r.X+r.W; x++ {
		c := buf.Get(x, r.Y)
		if c.Content != "" {
			b.WriteString(c.Content)
		} else {
			b.WriteRune(c.Char)
		}
	}
	return b.String()
}

func TestToolbarFontPreferenceChangesWithoutChangingImageGeometry(t *testing.T) {
	th := theme.DefaultDark()
	s := NewWithID(th, "pane/image")
	host := ui.NewHost(th, s)
	host.Resize(80, 24)
	host.Layout()
	s.SetImage(image.NewNRGBA(image.Rect(0, 0, 320, 160)), nil)
	key(host, '+')
	beforeViewport, beforeVirtual, beforeVisible := s.viewport, s.virtual, s.visible
	disabled, enabled := false, true
	for _, preference := range []*bool{&disabled, &enabled, nil} {
		th = theme.DefaultLight()
		th.Tree.Config.UseNerdFont = preference
		host.SetTheme(th)
		buf := paint(host, 80, 24)
		icon := "−"
		if panelchrome.NerdFont(th) {
			icon = "\uf010"
		}
		if !strings.Contains(strings.Split(text(buf), "\n")[0], icon) {
			t.Fatalf("toolbar ignored font preference/detection: %q", text(buf))
		}
		if s.viewport != beforeViewport || s.virtual != beforeVirtual || s.visible != beforeVisible {
			t.Fatal("font preference changed image geometry")
		}
	}
}

func TestRightAlignedIconToolbarActionsThroughHost(t *testing.T) {
	for _, nerd := range []bool{false, true} {
		for _, width := range []int{80, 24, 17} {
			t.Run(fmt.Sprintf("nerd=%t/width=%d", nerd, width), func(t *testing.T) {
				th := theme.DefaultDark()
				th.Tree.Config.UseNerdFont = &nerd
				s := NewWithID(th, "pane/image")
				host := ui.NewHost(th, s)
				host.Resize(width, 8)
				host.Layout()
				host.Focus(s.ID())
				s.SetImage(image.NewNRGBA(image.Rect(0, 0, 20, 20)), nil)
				// Old left-side toolbar space must not zoom or start a pan.
				host.Handle(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
				host.Handle(tea.MouseReleaseMsg{X: 0, Y: 0, Button: tea.MouseLeft})
				if s.zoom != 1 || s.dragging {
					t.Fatal("empty toolbar space changed the image")
				}
				for i, wantZoom := range []float64{1, math.Sqrt2, 1} {
					if i != 1 {
						key(host, '+') // Zoom out and Fit must undo a real zoom.
					}
					bounds := s.buttons[i].Bounds()
					x := bounds.X + bounds.W/2
					before := s.zoom
					host.Handle(tea.MouseClickMsg{X: x, Y: 0, Button: tea.MouseRight})
					if s.zoom != before {
						t.Fatal("non-left pointer button activated image control")
					}
					host.Handle(tea.MouseClickMsg{X: x, Y: 0, Button: tea.MouseLeft})
					host.Handle(tea.MouseReleaseMsg{X: x, Y: 0, Button: tea.MouseLeft})
					if math.Abs(s.zoom-wantZoom) > 1e-8 || host.Focused() != s.ID() {
						t.Fatalf("right-aligned control %d failed actual click/focus: zoom=%g (before=%g), focus=%s",
							i, s.zoom, before, host.Focused())
					}
					host.Handle(tea.MouseMotionMsg{X: 0, Y: 0})
					if b := paint(host, width, 8); b.Get(x, 0).BG != s.tokens.Color(ui.ColorBg) || s.buttons[i].Semantics().Selected {
						t.Fatalf("momentary action %d retained a highlight after click", i)
					}
				}
				// Every tooltip's advertised key acts with native toolbar focus.
				for _, step := range []struct {
					key  rune
					want float64
				}{{'+', math.Sqrt2}, {'-', 1}, {'0', 1}} {
					if !key(host, step.key).Handled || math.Abs(s.zoom-step.want) > 1e-8 {
						t.Fatalf("advertised shortcut %q did not act", step.key)
					}
				}
				host.Focus(s.buttons[1].ID())
				key(host, tea.KeySpace)
				if math.Abs(s.zoom-math.Sqrt2) > 1e-8 {
					t.Fatal("native Space activation failed")
				}
				key(host, tea.KeyEnter)
				if math.Abs(s.zoom-2) > 1e-8 {
					t.Fatal("native Enter activation failed")
				}
				host.Focus(s.ID())
				for _, id := range []ui.ComponentID{s.buttons[2].ID(), s.buttons[1].ID(), s.buttons[0].ID(), s.ID()} {
					host.Handle(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
					if host.Focused() != id {
						t.Fatalf("reverse focus order=%s, want %s", host.Focused(), id)
					}
				}
			})
		}
	}
}

func renderingIdentity(id uint32) cell.Color {
	return cell.NewTrueColor(uint8(id>>16), uint8(id>>8), uint8(id))
}

func TestNativeButtonsKeyboardAndReadOnlyBehavior(t *testing.T) {
	s, host := mount(80, 20)
	img := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	s.SetImage(img, nil)
	buf := paint(host, 80, 20)
	if strings.Contains(text(buf), "zoom") || s.viewport.Y+s.viewport.H != 20 {
		t.Fatal("image still paints a hint or reserves a footer row")
	}
	in := s.buttons[1].Bounds()
	host.Handle(tea.MouseClickMsg{X: in.X + 2, Y: 0, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: in.X + 2, Y: 0, Button: tea.MouseLeft})
	if math.Abs(s.zoom-math.Sqrt2) > 1e-8 {
		t.Fatal("native zoom button action did not bubble to image owner")
	}
	if host.Focused() != s.ID() {
		t.Fatal("click did not return keys to the image")
	}
	key(host, '-')
	if math.Abs(s.zoom-1) > 1e-8 {
		t.Fatal("image keyboard shortcuts failed with toolbar focus")
	}
	key(host, tea.KeyTab)
	key(host, tea.KeyTab)
	key(host, tea.KeyEnter) // keyboard focus still activates a native button
	if s.zoom <= 1 {
		t.Fatal("focused native button did not activate")
	}
	f := s.buttons[2].Bounds()
	host.Handle(tea.MouseClickMsg{X: f.X + 2, Y: 0, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: f.X + 2, Y: 0, Button: tea.MouseLeft})
	if s.zoom != 1 {
		t.Fatal("Fit button did not reset")
	}
	host.Focus(s.ID())
	for _, id := range []ui.ComponentID{s.buttons[0].ID(), s.buttons[1].ID(), s.buttons[2].ID(), s.ID()} {
		key(host, tea.KeyTab)
		if host.Focused() != id {
			t.Fatalf("Tab focus=%s, want %s", host.Focused(), id)
		}
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if host.Focused() != s.buttons[2].ID() {
		t.Fatal("Shift+Tab did not reach Fit from image body")
	}
	closeResult := key(host, tea.KeyEsc)
	if closeResult.Action == nil || closeResult.Action.ID != CloseAction || s.img != img {
		t.Fatal("Esc did not emit close intent without destroying the view")
	}
	ctrlW := host.Handle(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	if ctrlW.Handled || ctrlW.Action != nil {
		t.Fatal("image pane consumed shell-owned Ctrl+W")
	}
	host.Focus(s.ID())
	for _, msg := range []tea.Msg{
		tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl},
		tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl},
		tea.KeyPressMsg{Code: tea.KeyBackspace},
		tea.PasteMsg{Content: "not image edits"},
	} {
		result := host.Handle(msg)
		if result.Action != nil || result.Cmd != nil || s.img != img || s.zoom != 1 {
			t.Fatalf("read-only input emitted an edit or command: %+v", result)
		}
	}
}

func TestDragWheelAndCaptureCancellation(t *testing.T) {
	s, host := mount(40, 21)
	s.SetImage(image.NewNRGBA(image.Rect(0, 0, 80, 80)), nil)
	for i := 0; i < 4; i++ {
		key(host, '+')
	}
	before := s.virtual
	host.Handle(tea.MouseClickMsg{X: 15, Y: 10, Button: tea.MouseLeft})
	if !host.HasPointerCapture() || !s.dragging {
		t.Fatal("body click did not establish native pointer capture")
	}
	host.Handle(tea.MouseMotionMsg{X: 20, Y: 12, Button: tea.MouseLeft})
	if s.virtual.X != before.X+5 || s.virtual.Y != before.Y+2 {
		t.Fatal("drag did not move the image with the pointer")
	}
	host.Handle(tea.MouseReleaseMsg{X: 100, Y: 100, Button: tea.MouseLeft})
	if host.HasPointerCapture() || s.dragging {
		t.Fatal("outside release retained capture")
	}
	beforeZoom := s.zoom
	host.Handle(tea.MouseWheelMsg{X: 15, Y: 10, Button: tea.MouseWheelDown})
	if s.zoom >= beforeZoom {
		t.Fatal("wheel down did not zoom out")
	}
	before = s.virtual
	host.Handle(tea.MouseWheelMsg{X: 15, Y: 10, Button: tea.MouseWheelUp, Mod: tea.ModShift})
	if s.virtual.X != before.X+3 {
		t.Fatal("Shift+wheel did not pan horizontally")
	}
	host.Handle(tea.MouseClickMsg{X: 15, Y: 10, Button: tea.MouseLeft})
	host.CancelPointerCapture()
	before = s.virtual
	host.Handle(tea.MouseMotionMsg{X: 20, Y: 15, Button: tea.MouseLeft})
	if s.dragging || s.virtual != before {
		t.Fatal("cancelled drag changed image geometry")
	}
}

func TestImageResidencyRestoresTheCurrentViewport(t *testing.T) {
	s, host := mount(50, 24)
	img := image.NewNRGBA(image.Rect(0, 0, 80, 80))
	res := resource(t, img)
	s.SetImageGraphics(tea.ImageSixel, 8, 16)
	s.SetImage(img, res)
	host.Handle(tea.MouseWheelMsg{X: 25, Y: 12, Button: tea.MouseWheelUp})
	host.Handle(tea.MouseWheelMsg{X: 25, Y: 12, Button: tea.MouseWheelUp})
	host.Handle(tea.MouseWheelMsg{X: 25, Y: 12, Button: tea.MouseWheelRight})
	before, placement := text(paint(host, 50, 24)), s.ImagePlacement()
	if placement.Image == nil || !strings.ContainsRune(before, kitty.Placeholder) {
		t.Fatal("fixture did not start with native pixels")
	}

	s.SetImageResidency(nil, nil, "Loading image…")
	if empty := text(paint(host, 50, 24)); s.ImagePlacement().Image != nil ||
		strings.ContainsRune(empty, kitty.Placeholder) || !strings.Contains(empty, "Loading image…") {
		t.Fatal("dropping pixels did not remove the placement and show its empty state")
	}
	s.SetImageResidency(img, res, "")
	if after := text(paint(host, 50, 24)); after != before || s.ImagePlacement() != placement {
		t.Fatal("the same snapshot returned with a different viewport")
	}
}

func TestWheelZoomAnchorsPointerAndIgnoresChrome(t *testing.T) {
	for _, graphics := range []bool{false, true} {
		s, host := mount(80, 41)
		img := image.NewNRGBA(image.Rect(0, 0, 640, 640))
		res := resource(t, img)
		s.SetImage(img, res)
		protocol := tea.ImageNone
		if graphics {
			protocol = tea.ImageKitty
		}
		s.SetImageGraphics(protocol, 8, 16)
		x, y := 52, 25
		sourceX := (float64(x-s.virtual.X) + .5) / float64(s.virtual.W)
		sourceY := (float64(y-s.virtual.Y) + .5) / float64(s.virtual.H)
		before := s.virtual
		host.Handle(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp})
		host.Handle(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp})
		if s.virtual.W != 2*before.W || s.virtual.H != 2*before.H {
			t.Fatalf("graphics=%v: two wheel-up steps did not double the image", graphics)
		}
		if math.Abs(float64(x-s.virtual.X)+.5-sourceX*float64(s.virtual.W)) > 1 ||
			math.Abs(float64(y-s.virtual.Y)+.5-sourceY*float64(s.virtual.H)) > 1 {
			t.Fatal("zoom moved the source point away from the pointer")
		}
		buf := paint(host, 80, 41)
		if graphics && (s.ImagePlacement().Image != res || !strings.ContainsRune(text(buf), kitty.Placeholder)) {
			t.Fatal("mouse zoom lost the native image")
		}
		if !graphics && !strings.ContainsRune(text(buf), '▀') {
			t.Fatal("mouse zoom lost the reduced-detail image")
		}
		zoom := s.zoom
		host.Handle(tea.MouseWheelMsg{X: 1, Y: 0, Button: tea.MouseWheelUp})
		host.Handle(tea.MouseWheelMsg{X: 1, Y: 42, Button: tea.MouseWheelUp})
		if s.zoom != zoom {
			t.Fatal("wheel over toolbar or outside the pane changed the image")
		}
		host.Handle(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
		host.Handle(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
		if math.Abs(s.zoom-1) > 1e-9 || s.virtual != before {
			t.Fatal("opposite wheel steps did not restore the fitted image")
		}
	}
}

func TestFallbackSamplesSourceAlphaNonzeroBoundsAndPan(t *testing.T) {
	s, host := mount(8, 5)
	img := image.NewNRGBA(image.Rect(3, 5, 11, 13))
	red, blue := color.NRGBA{R: 255, A: 255}, color.NRGBA{B: 255, A: 255}
	for y := 5; y < 13; y++ {
		for x := 3; x < 11; x++ {
			img.SetNRGBA(x, y, red)
			if x >= 7 {
				img.SetNRGBA(x, y, blue)
			}
		}
	}
	img.SetNRGBA(3, 5, color.NRGBA{})
	s.SetImage(img, nil)
	buf := paint(host, 8, 5)
	if s.ImagePlacement().Image != nil || strings.ContainsRune(text(buf), kitty.Placeholder) {
		t.Fatal("unsupported terminal received graphics")
	}
	if got := buf.Get(0, 1); got.Char != '▀' || got.FG != s.tokens.Color(ui.ColorBg) || got.BG.R != 255 {
		t.Fatalf("alpha/nonzero source origin lost: %+v", got)
	}
	if got := buf.Get(7, 1); got.FG.B != 255 {
		t.Fatalf("fallback did not show actual right-hand pixels: %+v", got)
	}
	for i := 0; i < 4; i++ {
		key(host, '+')
	}
	for i := 0; i < 20; i++ {
		key(host, tea.KeyRight)
	}
	buf = paint(host, 8, 5)
	for y := 1; y < 5; y++ {
		for x := 0; x < 8; x++ {
			if got := buf.Get(x, y); got.Char != '▀' || got.FG.B != 255 || got.BG.B != 255 {
				t.Fatalf("fallback crop did not reveal the blue image edge at %d,%d: %+v", x, y, got)
			}
		}
	}
}

func TestGraphicsCoordinateLimitUnknownMetricsAndReplacement(t *testing.T) {
	s, host := mount(297, 3)
	img := image.NewNRGBA(image.Rect(0, 0, 297, 4))
	first := resource(t, img)
	s.SetImage(img, first)
	s.SetImageGraphics(tea.ImageKitty, 1, 2)
	if p := s.ImagePlacement(); p.Columns != 297 || p.Rows != 2 {
		t.Fatalf("coordinate limit should include 297 cells: %+v", p)
	}
	buf := paint(host, 297, 3)
	last := []rune(buf.Get(296, 1).Content)
	if len(last) != 4 || last[2] != kitty.Diacritic(296) {
		t.Fatal("final valid coordinate was not encoded")
	}
	key(host, '+')
	buf = paint(host, 297, 3)
	if s.ImagePlacement().Image != nil || strings.ContainsRune(text(buf), kitty.Placeholder) || !strings.ContainsRune(text(buf), '▀') {
		t.Fatal("unrepresentable crop emitted invalid graphics instead of fallback")
	}
	key(host, '0')
	second := resource(t, img)
	s.SetImage(img, second)
	buf = paint(host, 297, 3)
	if s.ImagePlacement().Image != second || buf.Get(0, 1).FG != renderingIdentity(second.ID()) {
		t.Fatal("same-sized replacement retained stale terminal identity")
	}
	s.SetImageGraphics(tea.ImageKitty, 0, 0)
	buf = paint(host, 297, 3)
	if s.ImagePlacement().Image != nil || strings.ContainsRune(text(buf), kitty.Placeholder) {
		t.Fatal("unknown cell metrics did not fall back safely")
	}
	s.SetImage(nil, nil)
	buf = paint(host, 297, 3)
	if s.resource != nil || s.placeholders != nil || strings.ContainsRune(text(buf), '▀') || !strings.Contains(text(buf), "No image") {
		t.Fatal("cleared pane retained image state")
	}
}

func TestGraphicsRowLimitAndResize(t *testing.T) {
	s, host := mount(4, 298)
	img := image.NewNRGBA(image.Rect(0, 0, 4, 594))
	s.SetImage(img, resource(t, img))
	s.SetImageGraphics(tea.ImageKitty, 1, 2)
	if p := s.ImagePlacement(); p.Rows != 297 || p.Columns != 4 {
		t.Fatalf("last valid row geometry: %+v", p)
	}
	buf := paint(host, 4, 298)
	last := []rune(buf.Get(0, 297).Content)
	if len(last) != 4 || last[1] != kitty.Diacritic(296) {
		t.Fatal("final valid row was not encoded")
	}
	key(host, '+')
	buf = paint(host, 4, 298)
	if s.ImagePlacement().Image != nil || strings.ContainsRune(text(buf), kitty.Placeholder) {
		t.Fatal("unrepresentable row crop did not use safe fallback")
	}
	key(host, '0')
	host.Resize(2, 99)
	host.Layout()
	if p := s.ImagePlacement(); p.Rows != 98 || p.Columns != 2 {
		t.Fatalf("fit did not recompute after resize: %+v", p)
	}
}

type insetRoot struct {
	ui.Base
	child *Screen
}

func (r *insetRoot) Children() []ui.Component { return []ui.Component{r.child} }
func (r *insetRoot) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	r.SetBounds(bounds)
	r.child.Layout(ctx, ui.Rect{X: 3, Y: 2, W: max(0, bounds.W-6), H: max(0, bounds.H-4)})
}

func TestTinyAndParentLocalBounds(t *testing.T) {
	for w := 0; w <= 35; w++ {
		for h := 0; h <= 7; h++ {
			s := NewWithID(nil, "root/image")
			s.SetImage(image.NewNRGBA(image.Rect(0, 0, 20, 10)), nil)
			root := &insetRoot{Base: ui.NewBase("root"), child: s}
			host := ui.NewHost(nil, root)
			host.Resize(w+6, h+4)
			host.Layout()
			buf := paint(host, w+6, h+4)
			for y := 0; y < h+4; y++ {
				for x := 0; x < w+6; x++ {
					if !s.Bounds().Contains(x, y) && buf.Get(x, y).Char == '▀' {
						t.Fatalf("%dx%d: image escaped parent-local bounds at %d,%d", w, h, x, y)
					}
					if buf.Get(x, y).Char == '▀' && host.HitTest(x, y) != s.ID() {
						t.Fatalf("%dx%d: image paint/hit bounds differ", w, h)
					}
				}
			}
			for _, b := range s.buttons {
				r := b.Bounds()
				if r.X < 0 || r.W < 0 || r.X+r.W > w || r.H > h {
					t.Fatalf("%dx%d: toolbar escaped supplied bounds: %+v", w, h, r)
				}
			}
		}
	}
}

func TestGraphicsSurvivesCompositorClippingAndOcclusion(t *testing.T) {
	s, host := mount(40, 21)
	img := image.NewNRGBA(image.Rect(0, 0, 320, 160))
	s.SetImage(img, resource(t, img))
	s.SetImageGraphics(tea.ImageKitty, 8, 16)
	key(host, '+')
	key(host, '+')
	base := surface.NewSurface("image", 40, 22, cell.TransparentBlank())
	host.Paint(base.Buffer())
	base.SetPosition(-2, 1)
	c := compositor.NewCompositor(40, 24)
	cover := surface.NewSurface("cover", 8, 4, cell.NewCell('X', cell.NoColor, cell.NoColor, 0))
	cover.SetPosition(8, 6)
	cover.SetZOrder(1)
	c.BeginFrame()
	c.Root().AddChild(base)
	c.Root().AddChild(cover)
	c.Composite()
	out := uv.NewScreenBuffer(40, 24)
	c.PresentSurface().Draw(out, out.Bounds())
	if out.CellAt(8, 6).Content != "X" {
		t.Fatal("image painted over an opaque later surface")
	}
	got := []rune(out.CellAt(0, 2).Content)
	if len(got) != 4 || got[0] != kitty.Placeholder || got[1] != kitty.Diacritic(0) || got[2] != kitty.Diacritic(22) {
		t.Fatalf("compositor clipping lost original image coordinates: %q", string(got))
	}
}

func TestHighZoomKeepsOriginalSourceAndCellStepPan(t *testing.T) {
	s, host := mount(40, 21)
	// Nonzero Go bounds must not leak into PNG-relative source coordinates.
	img := image.NewNRGBA(image.Rect(3, 5, 323, 165))
	for y := 5; y < 165; y++ {
		for x := 3; x < 323; x++ {
			if x >= 163 {
				img.SetNRGBA(x, y, color.NRGBA{B: 255, A: 128})
			} else {
				img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
			}
		}
	}
	res := resource(t, img)
	s.SetImage(img, res)
	s.SetImageGraphics(tea.ImageKitty, 8, 16)
	for i := 0; i < 12; i++ {
		key(host, '+') // stops at the largest zoom, 16 terminal pixels per image pixel
	}
	if s.virtual.W != 640 || s.virtual.H != 160 {
		t.Fatalf("maximum zoom geometry=%+v", s.virtual)
	}
	check := func(wantX, wantY float64) {
		t.Helper()
		buf := paint(host, 40, 21)
		p := s.ImagePlacement()
		if p.Image != res || s.img != img || p.Columns > 297 || p.Rows > 297 ||
			p.SourceWidth <= 0 || p.SourceHeight <= 0 || p.SourceX < 0 || p.SourceY < 0 ||
			p.SourceX+p.SourceWidth > 320 || p.SourceY+p.SourceHeight > 160 {
			t.Fatalf("unbounded or replaced high-zoom source: %+v", p)
		}
		if strings.Contains(text(buf), "Reduced detail") || len(s.placeholders) != 40*20 {
			t.Fatal("representable high zoom did not use viewport-bounded graphics")
		}
		for y := 0; y < 20; y++ {
			for x := 0; x < 40; x++ {
				c := buf.Get(x, 1+y)
				row, col := 1+y-s.placementGrid.Y, x-s.placementGrid.X
				want := string([]rune{kitty.Placeholder, kitty.Diacritic(row), kitty.Diacritic(col), kitty.Diacritic(int(res.ID() >> 24))})
				if row < 0 || row >= p.Rows || col < 0 || col >= p.Columns || c.Content != want || c.FG != renderingIdentity(res.ID()) {
					t.Fatalf("invalid crop-relative placeholder %d,%d: %+v (placement %+v)", x, y, c, p)
				}
				// Decode the actual placement transform, not the crop helper.
				// The independently specified 16× geometry is 2 cells/source
				// pixel horizontally and 1 cell/source pixel vertically.
				gotX := float64(p.SourceX) + (float64(col)+.5)*float64(p.SourceWidth)/float64(p.Columns)
				gotY := float64(p.SourceY) + (float64(row)+.5)*float64(p.SourceHeight)/float64(p.Rows)
				if gotX != wantX+float64(x)/2 || gotY != wantY+float64(y) {
					t.Fatalf("source landmark jumped or changed scale at %d,%d: (%g,%g), want (%g,%g)",
						x, y, gotX, gotY, wantX+float64(x)/2, wantY+float64(y))
				}
			}
		}
	}
	check(150.25, 70.5)
	for step := 1; step <= 20; step++ {
		key(host, tea.KeyRight) // crosses source-aligned crop boundaries repeatedly
		key(host, tea.KeyDown)
		check(150.25+float64(step)*3/2, 70.5+float64(step)*2)
	}
	p := s.ImagePlacement()
	// The rightward pan now exposes the blue landmark, retaining its alpha.
	pixel := img.NRGBAAt(img.Bounds().Min.X+p.SourceX, img.Bounds().Min.Y+p.SourceY)
	if pixel.B != 255 || pixel.A != 128 {
		t.Fatalf("source crop did not reveal original transparent blue landmark: %+v", pixel)
	}
	for i := 0; i < 1000; i++ {
		key(host, tea.KeyRight)
		key(host, tea.KeyDown)
	}
	check(300.25, 140.5)
	for i := 0; i < 1000; i++ {
		key(host, tea.KeyLeft)
		key(host, tea.KeyUp)
	}
	check(.25, .5)
	key(host, '0')
	if p := s.ImagePlacement(); p != (tea.ImagePlacement{Image: res, Columns: 40, Rows: 10}) {
		t.Fatalf("Fit did not restore unchanged uncropped placement: %+v", p)
	}
}

func TestHighZoomResizeAndExactCropLimits(t *testing.T) {
	s, host := mount(40, 21)
	img := image.NewNRGBA(image.Rect(0, 0, 320, 160))
	res := resource(t, img)
	s.SetImage(img, res)
	s.SetImageGraphics(tea.ImageKitty, 8, 16)
	for i := 0; i < 6; i++ {
		key(host, '+') // 8× fit, 320 × 80 virtual grid
	}
	if p := s.ImagePlacement(); p.Image != res || p.Columns != 40 || p.Rows != 20 ||
		p.SourceX != 140 || p.SourceY != 60 || p.SourceWidth != 40 || p.SourceHeight != 40 {
		t.Fatalf("initial 8× crop=%+v", p)
	}
	host.Resize(80, 41)
	host.Layout()
	if p := s.ImagePlacement(); p.Image != res || p.Columns != 80 || p.Rows != 40 ||
		p.SourceX != 140 || p.SourceY != 60 || p.SourceWidth != 40 || p.SourceHeight != 40 {
		t.Fatalf("resize changed source center instead of fit-relative scale: %+v", p)
	}
	paint(host, 80, 41)
	host.Resize(298, 41)
	host.Layout()
	buf := paint(host, 298, 41)
	if s.ImagePlacement().Image != nil || strings.ContainsRune(text(buf), kitty.Placeholder) || !strings.ContainsRune(text(buf), '▀') {
		t.Fatal("viewport beyond diacritic range did not fall back")
	}
	host.Resize(80, 41)
	host.Layout()
	if s.ImagePlacement().Image != res {
		t.Fatal("resize into representable bounds did not restore original resource")
	}

	// A coprime source/cell ratio requires the entire 320-column prototype to
	// hit exact source pixel boundaries, despite a small visible viewport.
	odd := image.NewNRGBA(image.Rect(0, 0, 317, 317))
	s.SetImage(odd, resource(t, odd))
	host.Resize(40, 21)
	host.Layout()
	for i := 0; i < 6; i++ {
		key(host, '+')
	}
	if s.virtual.W != 320 || s.virtual.H != 160 || s.ImagePlacement().Image != nil {
		t.Fatal("unrepresentable source alignment rounded pan or scale")
	}
	// Rounded c/r aspect must not introduce different letterboxing in a crop.
	s.SetImage(img, res)
	for i := 0; i < 7; i++ {
		key(host, '+')
	}
	if s.virtual.W <= 297 || s.ImagePlacement().Image != nil {
		t.Fatal("nonexact aspect quantization was accepted for source crop")
	}
}

func TestHighZoomCropSurvivesCompositorOcclusion(t *testing.T) {
	s, host := mount(40, 21)
	img := image.NewNRGBA(image.Rect(0, 0, 320, 160))
	res := resource(t, img)
	s.SetImage(img, res)
	s.SetImageGraphics(tea.ImageKitty, 8, 16)
	for i := 0; i < 12; i++ {
		key(host, '+')
	}
	base := surface.NewSurface("image", 40, 22, cell.TransparentBlank())
	host.Paint(base.Buffer())
	base.SetPosition(-2, 1)
	cover := surface.NewSurface("cover", 8, 4, cell.NewCell('X', cell.NoColor, cell.NoColor, 0))
	cover.SetPosition(8, 6)
	cover.SetZOrder(1)
	c := compositor.NewCompositor(40, 24)
	c.BeginFrame()
	c.Root().AddChild(base)
	c.Root().AddChild(cover)
	c.Composite()
	out := uv.NewScreenBuffer(40, 24)
	c.PresentSurface().Draw(out, out.Bounds())
	if out.CellAt(8, 6).Content != "X" {
		t.Fatal("cropped image painted over the covering surface")
	}
	// Maximum zoom is 2 cells per source pixel across and 1 down; the crop
	// starts at the first visible cell, which the surface shift hides.
	want := string([]rune{kitty.Placeholder, kitty.Diacritic(0), kitty.Diacritic(2), kitty.Diacritic(int(res.ID() >> 24))})
	if got := out.CellAt(0, 2).Content; got != want {
		t.Fatalf("clipping lost crop-relative row/column: %q, want %q", got, want)
	}
}

// A huge procedural source catches accidental source-sized iteration/allocation
// without allocating a huge fixture. Paint may read only two pixels per cell.
type countedImage struct{ reads int }

func (*countedImage) ColorModel() color.Model { return color.NRGBAModel }
func (*countedImage) Bounds() image.Rectangle { return image.Rect(0, 0, 1<<24, 1<<24) }
func (i *countedImage) At(_, _ int) color.Color {
	i.reads++
	return color.NRGBA{R: 100, A: 255}
}

func TestPaintWorkIsViewportBoundedAndThemePropagates(t *testing.T) {
	s, host := mount(80, 24)
	img := &countedImage{}
	s.SetImage(img, nil)
	tokens := ui.NewTokenSet(theme.DefaultLight())
	s.SetTokens(tokens)
	buf := paint(host, 80, 24)
	if img.reads == 0 || img.reads > 2*80*22 {
		t.Fatalf("paint sampled by source size instead of viewport: %d", img.reads)
	}
	if buf.Get(79, 0).BG != tokens.Color(ui.ColorBg) {
		t.Fatal("screen fill did not follow supplied tokens")
	}
	// Native button backgrounds must follow the same shell token too.
	if buf.Get(0, 0).BG != tokens.Color(ui.ColorBg) {
		t.Fatal("toolbar retained popup or stale theme fill")
	}
}

func TestHighZoomGraphicsDoesNotReadOrReencodePixels(t *testing.T) {
	s, host := mount(80, 24)
	img := &countedImage{}
	// The decoded resource is owned by the loader. The screen must not read its
	// pixels to build a replacement resource, even for a very large source.
	res := tea.NewTerminalImage([]byte("loader-owned immutable PNG"), nil)
	s.SetImage(img, res)
	s.SetImageGraphics(tea.ImageKitty, 8, 16)
	for i := 0; i < 12; i++ {
		key(host, '+')
	}
	key(host, tea.KeyRight)
	key(host, tea.KeyDown)
	buf := paint(host, 80, 24)
	if s.ImagePlacement().Image != res || !strings.ContainsRune(text(buf), kitty.Placeholder) || img.reads != 0 {
		t.Fatalf("high-zoom graphics read pixels or replaced the loader resource: reads=%d placement=%+v", img.reads, s.ImagePlacement())
	}
}

// Zoom stops where a step no longer changes the picture. Past the range, a
// wheel or key changes neither the zoom number nor the image, and the way
// back to Fit is exactly as many steps as the way in.
func TestZoomStopsAtTheUsefulRange(t *testing.T) {
	s, host := mount(80, 41)
	img := image.NewNRGBA(image.Rect(0, 0, 1920, 1056))
	s.SetImageGraphics(tea.ImageSixel, 9, 20)
	s.SetImage(img, resource(t, img))
	x, y := 40, 20
	wheel := func(button tea.MouseButton) {
		host.Handle(tea.MouseWheelMsg{X: x, Y: y, Button: button})
	}
	steps := 0
	for ; steps < 100 && !s.buttons[1].Semantics().Disabled; steps++ {
		wheel(tea.MouseWheelUp)
	}
	if steps == 100 || math.Abs(s.scale-maxPixelScale) > 1e-9 || !strings.Contains(s.status, "Maximum zoom") {
		t.Fatalf("zoom in did not stop at the largest useful zoom: steps=%d scale=%g status=%q", steps, s.scale, s.status)
	}
	status, virtual, placement := s.status, s.virtual, s.ImagePlacement()
	for range 5 {
		wheel(tea.MouseWheelUp)
		key(host, '+')
	}
	if s.status != status || s.virtual != virtual || s.ImagePlacement() != placement {
		t.Fatalf("zoom past the limit changed the view: %q -> %q", status, s.status)
	}
	for i := range steps {
		if s.effectiveZoom() == 1 {
			t.Fatalf("returned to Fit after %d of %d steps", i, steps)
		}
		wheel(tea.MouseWheelDown)
	}
	if s.effectiveZoom() != 1 || !strings.Contains(s.status, "Fit") || !s.buttons[0].Semantics().Disabled {
		t.Fatalf("the same number of steps out did not return to Fit: zoom=%g status=%q", s.effectiveZoom(), s.status)
	}
	fitted := s.virtual
	wheel(tea.MouseWheelDown)
	key(host, '-')
	if s.virtual != fitted || s.effectiveZoom() != 1 {
		t.Fatal("a large image zoomed out past Fit")
	}

	// An image smaller than the pane can shrink to its actual size, and no
	// further. Fit already magnifies past the zoom limit, so it cannot grow.
	icon := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	s.SetImage(icon, resource(t, icon))
	if !s.buttons[1].Semantics().Disabled {
		t.Fatal("a magnified small image can still zoom in")
	}
	for range 20 {
		wheel(tea.MouseWheelDown)
	}
	if math.Abs(s.scale-1) > 1e-9 || !strings.Contains(s.status, "Actual size") || !s.buttons[0].Semantics().Disabled {
		t.Fatalf("small image did not stop at actual size: scale=%g status=%q", s.scale, s.status)
	}
}

// A resize that narrows the range shows the nearest zoom, and the requested
// zoom returns with the original geometry.
func TestZoomRequestSurvivesATemporaryResize(t *testing.T) {
	s, host := mount(80, 41)
	img := image.NewNRGBA(image.Rect(0, 0, 1920, 1056))
	s.SetImageGraphics(tea.ImageSixel, 9, 20)
	s.SetImage(img, resource(t, img))
	for range 4 {
		key(host, '+')
	}
	before, status := s.virtual, s.status
	host.Resize(300, 120)
	host.Layout()
	if s.scale > maxPixelScale+1e-9 {
		t.Fatalf("a larger pane exceeded the zoom limit: %g", s.scale)
	}
	host.Resize(80, 41)
	host.Layout()
	if s.virtual != before || s.status != status {
		t.Fatalf("resize lost the requested zoom: %+v %q, want %+v %q", s.virtual, s.status, before, status)
	}
}
