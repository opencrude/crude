// Package imageviewer provides a read-only, independently zoomable file pane.
// Callers decode images and prepare terminal resources off the UI goroutine.
package imageviewer

import (
	"fmt"
	"image"
	"math"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi/kitty"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/panelchrome"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	// CloseAction asks the shell to close this file tab. Screen never closes
	// resources itself, and Ctrl+W remains owned by the shell.
	CloseAction = "imageviewer.close"

	zoomOut = "imageviewer.zoom-out"
	zoomIn  = "imageviewer.zoom-in"
	fit     = "imageviewer.fit"

	// maxPixelScale is the largest magnification: one image pixel covers
	// 16 × 16 terminal pixels. More shows no further detail, only larger
	// blocks of one color, so zoom stops where every step still changes
	// what the user sees.
	maxPixelScale = 16
)

// Screen retains immutable source pixels, not a document editor. Image resources
// belong to the terminal writer; ImagePlacement only describes the current view.
type Screen struct {
	ui.Base
	tokens                *ui.TokenSet
	img                   image.Image
	resource              *tea.TerminalImage
	graphics              tea.ImageProtocol
	sixelPreview          bool
	cellWidth, cellHeight int
	zoom                  float64 // multiplier of the current fit scale
	minZoom, maxZoom      float64 // zoom range for the current image and viewport
	centerX, centerY      float64 // normalized source point at viewport center
	scale                 float64 // source pixels to terminal pixels
	viewport, virtual     ui.Rect
	visible               ui.Rect
	placement             tea.ImagePlacement
	placementGrid         ui.Rect  // parent-local cells of the full or cropped prototype
	placeholders          []string // visible cells only, never source-sized
	placeholderID         uint32
	placeholderRect       ui.Rect
	placeholderVirtual    ui.Rect
	emptyStatus           string
	status                string
	buttons               [3]*imageButton
	children              []ui.Component
	dragging              bool
	dragX, dragY          int
}

var _ ui.Component = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
var _ ui.PointerCaptureCanceller = (*Screen)(nil)

// NewWithID creates a pane with a stable, mount-unique component identity.
func NewWithID(th *theme.Theme, id ui.ComponentID) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	s := &Screen{Base: ui.NewBase(id), tokens: ui.NewTokenSet(th), graphics: tea.ImageNone, zoom: 1, centerX: .5, centerY: .5}
	for i, spec := range []struct{ segment, label, shortcut, icon, fallback, action string }{
		{"zoom-out", "Zoom out", "-", "\uf010", "−", zoomOut},
		{"zoom-in", "Zoom in", "+", "\uf00e", "+", zoomIn},
		{"fit", "Fit image", "0", "\uf065", "↔", fit},
	} {
		b := &imageButton{ViewerButton: panelchrome.NewViewerButton(ui.NewID(id, spec.segment), id, spec.action,
			spec.label, spec.shortcut, spec.icon, spec.fallback), owner: s}
		s.buttons[i] = b
		s.children = append(s.children, b)
	}
	s.SetTokens(s.tokens)
	return s
}

// SetImage replaces the immutable decoded image and its matching prepared PNG.
// Passing nil clears the pane and releases its references. Replacement resets
// zoom and pan. The caller must not mutate pixels after delivery.
func (s *Screen) SetImage(img image.Image, resource *tea.TerminalImage) {
	s.zoom, s.centerX, s.centerY = 1, .5, .5
	s.sixelPreview = false
	s.SetImageResidency(img, resource, "")
}

// SetImageResidency installs or drops the pixels of the same snapshot. A nil
// image keeps the pan and zoom anchor, and shows status until pixels return.
func (s *Screen) SetImageResidency(img image.Image, resource *tea.TerminalImage, status string) {
	if img == nil || img.Bounds().Empty() {
		img, resource = nil, nil
		s.sixelPreview = false
	}
	s.img, s.resource, s.emptyStatus = img, resource, status
	s.CancelPointerCapture()
	s.refresh()
}

// SetImageGraphics sets measured cell pixels and confirmed protocol support.
// It reports whether inputs changed, so callers can invalidate their Host.
// Unknown cell geometry always uses the safe cell preview.
func (s *Screen) SetImageGraphics(protocol tea.ImageProtocol, cellWidth, cellHeight int) bool {
	cellWidth, cellHeight = max(0, cellWidth), max(0, cellHeight)
	if s.graphics == protocol && s.cellWidth == cellWidth && s.cellHeight == cellHeight {
		return false
	}
	s.graphics, s.cellWidth, s.cellHeight = protocol, cellWidth, cellHeight
	if protocol != tea.ImageSixel {
		s.sixelPreview = false
	}
	s.CancelPointerCapture()
	s.refresh()
	return true
}

// SixelMotionGeometry exposes the current virtual image to the shell's
// presentation policy. It includes the pan offset, unlike ImagePlacement.
func (s *Screen) SixelMotionGeometry() (ui.Rect, bool) {
	return s.virtual, s.graphics == tea.ImageSixel && s.resource.Supports(tea.ImageSixel) && !s.visible.Empty()
}

// SetSixelMotionPreview selects the existing cell painter without changing
// measured geometry, source identity, zoom or pan. The caller invalidates the
// mounted surface on a change.
func (s *Screen) SetSixelMotionPreview(preview bool) bool {
	if s.graphics != tea.ImageSixel || s.sixelPreview == preview {
		return false
	}
	s.sixelPreview = preview
	s.refresh()
	return true
}

// ImagePlacement describes the original resource at the current scale. Large
// virtual images use an exact, source-aligned crop when it fits the placeholder
// coordinate range. The screen never resamples or encodes pixels; the terminal
// writer prepares protocol-specific output from this immutable descriptor.
func (s *Screen) ImagePlacement() tea.ImagePlacement {
	return s.placement
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	if tokens == nil {
		return
	}
	s.tokens = tokens
	for _, b := range s.buttons {
		b.SetTokens(tokens)
	}
}

func (s *Screen) Children() []ui.Component { return s.children }

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	r.W, r.H = max(0, r.W), max(0, r.H)
	if r != s.Bounds() {
		s.CancelPointerCapture()
	}
	s.SetBounds(r)
	pad, inner := ui.ContentInset(r.W, s.tokens.PadLR())
	panelchrome.LayoutViewerActions(ctx, ui.Rect{X: pad, W: inner, H: r.H}, s.children)
	s.viewport = ui.Rect{Y: min(1, r.H), W: r.W, H: max(0, r.H-1)}
	s.refresh()
}

func (s *Screen) useGraphics() bool {
	return s.placement.Image != nil
}

// refreshPlacement keeps ordinary placements unchanged. A large prototype can
// be cropped only at boundaries that are BOTH whole source pixels and whole
// terminal cells. Expanding to those boundaries retains the same scale and
// subpixel pan phase, unlike rounding the viewport's source rectangle.
func (s *Screen) refreshPlacement() {
	s.placement, s.placementGrid = tea.ImagePlacement{}, ui.Rect{}
	if s.sixelPreview || !s.resource.Supports(s.graphics) || s.cellWidth <= 0 || s.cellHeight <= 0 || s.visible.Empty() {
		return
	}
	if s.graphics == tea.ImageSixel && !tea.SixelSizeSupported(s.visible.W, s.visible.H, s.cellWidth, s.cellHeight) {
		return
	}
	if s.virtual.W <= 297 && s.virtual.H <= 297 {
		s.placementGrid = s.virtual
		s.placement = tea.ImagePlacement{Image: s.resource, Columns: s.virtual.W, Rows: s.virtual.H}
		return
	}
	if s.visible.W > 297 || s.visible.H > 297 {
		return
	}
	if s.graphics == tea.ImageSixel {
		s.refreshSixelWindow()
		return
	}
	src := s.img.Bounds()
	// Kitty preserves aspect inside c/r, rather than stretching. A rounded full
	// grid may therefore include letterboxing. Do not reinterpret that padding
	// as source pixels or invent a different aspect for the crop.
	if float64(s.virtual.W)*float64(s.cellWidth)*float64(src.Dy()) !=
		float64(s.virtual.H)*float64(s.cellHeight)*float64(src.Dx()) {
		return
	}
	axis := func(source, extent, offset, visible int) (start, cells, pixelStart, pixels int) {
		common := gcd(source, extent)
		cellStep, pixelStep := extent/common, source/common
		first := offset / cellStep
		last := (offset + visible + cellStep - 1) / cellStep
		return first * cellStep, (last - first) * cellStep, first * pixelStep, (last - first) * pixelStep
	}
	x, w, sx, sw := axis(src.Dx(), s.virtual.W, s.visible.X-s.virtual.X, s.visible.W)
	y, h, sy, sh := axis(src.Dy(), s.virtual.H, s.visible.Y-s.virtual.Y, s.visible.H)
	if w > 297 || h > 297 {
		// Some source/cell ratios cannot represent an exact crop within the
		// protocol's range. Keep the safe preview, never quantize pan or scale.
		return
	}
	s.placementGrid = ui.Rect{X: s.virtual.X + x, Y: s.virtual.Y + y, W: w, H: h}
	s.placement = tea.ImagePlacement{Image: s.resource, Columns: w, Rows: h,
		SourceX: sx, SourceY: sy, SourceWidth: sw, SourceHeight: sh}
}

// refreshSixelWindow places a window of up to 297 × 297 cells, the marker
// range, inside a larger virtual image. The terminal writer fits the image to
// the whole virtual grid, so the window keeps the exact scale and pan position
// that the Kitty source crop can represent only at whole source pixels. The
// window moves in steps of half its spare cells: a pan inside it changes only
// which cells are visible, so the writer crops the raster it already prepared.
func (s *Screen) refreshSixelWindow() {
	axis := func(virtualStart, virtualSize, visibleStart, visibleSize int) (start, size int) {
		size = min(virtualSize, 297)
		step := max(1, (size-visibleSize)/2)
		offset := (visibleStart - virtualStart) / step * step
		return virtualStart + min(max(0, offset), virtualSize-size), size
	}
	x, w := axis(s.virtual.X, s.virtual.W, s.visible.X, s.visible.W)
	y, h := axis(s.virtual.Y, s.virtual.H, s.visible.Y, s.visible.H)
	s.placementGrid = ui.Rect{X: x, Y: y, W: w, H: h}
	s.placement = tea.ImagePlacement{Image: s.resource, Columns: w, Rows: h,
		VirtualColumns: s.virtual.W, VirtualRows: s.virtual.H,
		OffsetColumn: x - s.virtual.X, OffsetRow: y - s.virtual.Y}
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// effectiveZoom is the requested zoom held inside the current range. The
// request itself survives a resize: a pane that is briefly too large or too
// small for it shows the nearest zoom it can and returns to the request.
func (s *Screen) effectiveZoom() float64 {
	return min(s.maxZoom, max(s.minZoom, s.zoom))
}

// zoomStep returns the next zoom level towards dir (+1 in, -1 out). Levels
// are Fit times powers of √2, so stepping back always returns to Fit; the
// range ends are levels too.
func (s *Screen) zoomStep(dir int) float64 {
	halfOctaves := 2 * math.Log2(s.effectiveZoom())
	if dir > 0 {
		halfOctaves = math.Floor(halfOctaves+1e-9) + 1
	} else {
		halfOctaves = math.Ceil(halfOctaves-1e-9) - 1
	}
	return min(s.maxZoom, max(s.minZoom, math.Exp2(halfOctaves/2)))
}

func (s *Screen) refreshViewport() {
	s.virtual, s.visible, s.scale = ui.Rect{}, ui.Rect{}, 0
	s.minZoom, s.maxZoom = 1, 1
	if s.img != nil && !s.img.Bounds().Empty() && !s.viewport.Empty() {
		src, v := s.img.Bounds(), s.viewport
		cw, ch := s.cellWidth, s.cellHeight
		if cw <= 0 || ch <= 0 {
			cw, ch = 1, 2
		}
		fitScale := math.Min(float64(v.W)*float64(cw)/float64(src.Dx()), float64(v.H)*float64(ch)/float64(src.Dy()))
		// Zoom out stops at Fit, or at actual size for an image smaller
		// than the pane. Zoom in stops at maxPixelScale, or at Fit when Fit
		// already magnifies more. Outside that range a step changes the
		// number but not the picture.
		s.minZoom = math.Min(1, 1/fitScale)
		s.maxZoom = math.Max(1, maxPixelScale/fitScale)
		s.scale = fitScale * s.effectiveZoom()
		w := max(1, int(math.Ceil(float64(src.Dx())*s.scale/float64(cw)-1e-9)))
		h := max(1, int(math.Ceil(float64(src.Dy())*s.scale/float64(ch)-1e-9)))
		axis := func(extent, view int, center *float64) int {
			if extent <= view {
				*center = .5
				return (view - extent) / 2
			}
			offset := min(max(0, *center*float64(extent)-float64(view)/2), float64(extent-view))
			// Keep the unrounded center authoritative. Writing the cell-rounded
			// position back makes repeated zoom steps drift away from the center.
			*center = (offset + float64(view)/2) / float64(extent)
			return -int(math.Round(offset))
		}
		s.virtual = ui.Rect{X: v.X + axis(w, v.W, &s.centerX), Y: v.Y + axis(h, v.H, &s.centerY), W: w, H: h}
		s.visible = intersect(s.virtual, v)
	}
}

// refresh derives bounded presentation state only on source or geometry changes.
// Zoom and drag never encode pixels, upload resources, or allocate by source size.
func (s *Screen) refresh() {
	s.refreshViewport()
	s.refreshPlacement()
	if s.placement.Image != nil && s.graphics == tea.ImageSixel {
		s.placement.Protocol = tea.ImageSixel
		s.placement.CellWidth, s.placement.CellHeight = s.cellWidth, s.cellHeight
	}
	empty := s.img == nil || s.img.Bounds().Empty()
	zoom := s.effectiveZoom()
	s.buttons[0].SetDisabled(empty || zoom <= s.minZoom)
	s.buttons[1].SetDisabled(empty || zoom >= s.maxZoom)
	s.buttons[2].SetDisabled(empty)
	s.status = "No image"
	if s.img == nil {
		if s.emptyStatus != "" {
			s.status = s.emptyStatus
		}
	} else {
		src := s.img.Bounds()
		s.status = fmt.Sprintf("%d × %d", src.Dx(), src.Dy())
		if s.scale > 0 {
			if s.cellWidth > 0 && s.cellHeight > 0 {
				s.status += fmt.Sprintf(" · %.0f%%", s.scale*100)
			} else {
				// Without measured cell pixels a source-pixel percentage would
				// be invented. State the known fit-relative zoom instead.
				s.status += fmt.Sprintf(" · %.0f%% of fit", zoom*100)
			}
			switch {
			case zoom == 1:
				s.status += " · Fit"
			case zoom >= s.maxZoom:
				s.status += " · Maximum zoom"
			case zoom <= s.minZoom && s.cellWidth > 0 && s.cellHeight > 0:
				s.status += " · Actual size"
			}
			if !s.useGraphics() && !s.sixelPreview {
				s.status += " · Reduced detail"
			}
		}
	}
	if !s.useGraphics() || s.visible.Empty() {
		if !s.sixelPreview {
			s.placeholders = nil
			s.placeholderID = 0
		}
		return
	}
	id := s.resource.ID()
	if s.placeholderID == id && s.placeholderRect == s.visible && s.placeholderVirtual == s.placementGrid {
		return
	}
	s.placeholderID, s.placeholderRect, s.placeholderVirtual = id, s.visible, s.placementGrid
	n := s.visible.W * s.visible.H
	if cap(s.placeholders) < n {
		s.placeholders = make([]string, n)
	} else {
		s.placeholders = s.placeholders[:n]
	}
	for y := 0; y < s.visible.H; y++ {
		for x := 0; x < s.visible.W; x++ {
			s.placeholders[y*s.visible.W+x] = string([]rune{
				kitty.Placeholder,
				kitty.Diacritic(s.visible.Y + y - s.placementGrid.Y),
				kitty.Diacritic(s.visible.X + x - s.placementGrid.X),
				kitty.Diacritic(int(id >> 24)),
			})
		}
	}
}

func intersect(a, b ui.Rect) ui.Rect {
	x, y := max(a.X, b.X), max(a.Y, b.Y)
	return ui.Rect{X: x, Y: y, W: max(0, min(a.X+a.W, b.X+b.W)-x), H: max(0, min(a.Y+a.H, b.Y+b.H)-y)}
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := s.Bounds()
	if r.Empty() {
		return
	}
	bg := s.tokens.Color(ui.ColorBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	ox, oy = ox+r.X, oy+r.Y
	if !s.visible.Empty() {
		if s.useGraphics() {
			fg := rendering.ImagePlaceholderColor(s.resource.ID())
			for y := 0; y < s.visible.H; y++ {
				for x := 0; x < s.visible.W; x++ {
					buf.Set(ox+s.visible.X+x, oy+s.visible.Y+y, cell.NewClusterCell(s.placeholders[y*s.visible.W+x], 1, fg, bg, 0))
				}
			}
		} else {
			s.paintPreview(buf, ox, oy, bg)
		}
	}
	if s.img == nil && !s.viewport.Empty() {
		pad, inner := ui.ContentInset(r.W, s.tokens.PadLR())
		buf.WriteString(ox+pad, oy+s.viewport.Y, rendering.TruncateVisual(s.status, inner, "…"), s.tokens.Color(ui.ColorMuted), bg, 0)
	}
}

func (s *Screen) paintPreview(buf *surface.CellBuffer, ox, oy int, bg cell.Color) {
	src := s.img.Bounds()
	// Sample only visible output cells, twice per cell. Nonzero image bounds
	// and alpha are preserved without a resized-image allocation.
	sample := func(x, halfY int) cell.Color {
		sx := min(src.Dx()-1, int((float64(x)+.5)*float64(src.Dx())/float64(s.virtual.W)))
		sy := min(src.Dy()-1, int((float64(halfY)+.5)*float64(src.Dy())/float64(s.virtual.H*2)))
		return rendering.ImagePixelToCell(s.img.At(src.Min.X+sx, src.Min.Y+sy), bg)
	}
	for y := s.visible.Y; y < s.visible.Y+s.visible.H; y++ {
		for x := s.visible.X; x < s.visible.X+s.visible.W; x++ {
			col, row := x-s.virtual.X, (y-s.virtual.Y)*2
			buf.Set(ox+x, oy+y, cell.NewCell('▀', sample(col, row), sample(col, row+1), 0))
		}
	}
}

func (s *Screen) changeZoom(dir int) bool {
	next := s.zoomStep(dir)
	if next == s.effectiveZoom() {
		return false
	}
	s.CancelPointerCapture()
	s.zoom = next
	s.refresh()
	return true
}

// zoomAt keeps the source point under the pointer in place, except where
// centering a small image or clamping at an edge takes precedence.
func (s *Screen) zoomAt(dir int, x, y int) bool {
	if s.virtual.Empty() {
		return false
	}
	next := s.zoomStep(dir)
	if next == s.effectiveZoom() {
		return false
	}
	sourceX := (float64(x-s.virtual.X) + .5) / float64(s.virtual.W)
	sourceY := (float64(y-s.virtual.Y) + .5) / float64(s.virtual.H)
	s.CancelPointerCapture()
	s.zoom = next
	s.refreshViewport()
	s.centerX = sourceX + (float64(s.viewport.W)/2-float64(x-s.viewport.X)-.5)/float64(s.virtual.W)
	s.centerY = sourceY + (float64(s.viewport.H)/2-float64(y-s.viewport.Y)-.5)/float64(s.virtual.H)
	s.refresh()
	return true
}

func (s *Screen) pan(dx, dy int) {
	if s.virtual.Empty() {
		return
	}
	s.centerX += float64(dx) / float64(s.virtual.W)
	s.centerY += float64(dy) / float64(s.virtual.H)
	s.refresh()
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case zoomOut, zoomIn:
		dir := 1
		if action.ID == zoomOut {
			dir = -1
		}
		return zoomResult(s.changeZoom(dir))
	case fit:
		s.CancelPointerCapture()
		s.zoom, s.centerX, s.centerY = 1, .5, .5
		s.refresh()
	default:
		return ui.EventResult{}
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
}

func zoomResult(changed bool) ui.EventResult {
	r := ui.EventResult{Handled: true}
	if changed {
		r.Invalidate = ui.InvalidatePaint
	}
	return r
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	handled := true
	switch evt.Kind {
	case ui.EventKey:
		s.CancelPointerCapture()
		switch evt.Key.String() {
		case "esc":
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: CloseAction}}
		case "tab", "shift+tab":
			index := 0
			if evt.Key.Mod.Contains(tea.ModShift) {
				index = len(s.buttons) - 1
			}
			return focusResult(s.buttons[index].ID())
		case "+", "=":
			return s.HandleAction(ctx, ui.Action{ID: zoomIn})
		case "-", "_":
			return s.HandleAction(ctx, ui.Action{ID: zoomOut})
		case "0":
			return s.HandleAction(ctx, ui.Action{ID: fit})
		case "left":
			s.pan(-3, 0)
		case "right":
			s.pan(3, 0)
		case "up":
			s.pan(0, -2)
		case "down":
			s.pan(0, 2)
		default:
			// Leave shell navigation and close chords to the parent. No key
			// is ever forwarded to a text editor by this component.
			handled = false
		}
	case ui.EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft || !s.viewport.Contains(ctx.LocalX, ctx.LocalY) || s.visible.Empty() {
			return ui.EventResult{}
		}
		s.dragging, s.dragX, s.dragY = true, ctx.LocalX, ctx.LocalY
	case ui.EventMouseMotion:
		if !s.dragging {
			return ui.EventResult{}
		}
		s.pan(s.dragX-ctx.LocalX, s.dragY-ctx.LocalY)
		s.dragX, s.dragY = ctx.LocalX, ctx.LocalY
	case ui.EventMouseRelease:
		if !s.dragging || evt.MouseRelease.Button != tea.MouseLeft {
			return ui.EventResult{}
		}
		s.CancelPointerCapture()
	case ui.EventMouseWheel:
		if !s.viewport.Contains(ctx.LocalX, ctx.LocalY) {
			return ui.EventResult{}
		}
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			if evt.MouseWheel.Mod.Contains(tea.ModShift) {
				s.pan(-3, 0)
			} else {
				return zoomResult(s.zoomAt(1, ctx.LocalX, ctx.LocalY))
			}
		case tea.MouseWheelDown:
			if evt.MouseWheel.Mod.Contains(tea.ModShift) {
				s.pan(3, 0)
			} else {
				return zoomResult(s.zoomAt(-1, ctx.LocalX, ctx.LocalY))
			}
		case tea.MouseWheelLeft:
			s.pan(-3, 0)
		case tea.MouseWheelRight:
			s.pan(3, 0)
		default:
			handled = false
		}
	case ui.EventCustom:
		if _, ok := evt.Custom.(tea.BlurMsg); ok {
			s.CancelPointerCapture()
		}
		handled = false
	default:
		handled = false
	}
	return ui.EventResult{Handled: handled, Invalidate: ui.InvalidatePaint}
}

func (s *Screen) CancelPointerCapture() { s.dragging = false }

func focusResult(id ui.ComponentID) ui.EventResult {
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: id},
		Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
}

// Status describes the current image for the shell header.
func (s *Screen) Status() string { return s.status }

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Image", Value: s.status,
		Description: "Read-only image. Wheel or plus and minus zoom. 0 fits. Arrows and drag pan. Shift+wheel pans horizontally."}
}

// Native buttons remain discoverable in the Host tree. Their ordinary
// activation bubbles to Screen; image navigation also works with toolbar focus.
type imageButton struct {
	*panelchrome.ViewerButton
	owner *Screen
}

func (b *imageButton) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		if evt.Key.Code == tea.KeyTab {
			for i, button := range b.owner.buttons {
				if button != b {
					continue
				}
				next := i + 1
				if evt.Key.Mod.Contains(tea.ModShift) {
					next = i - 1
				}
				if next < 0 || next >= len(b.owner.buttons) {
					return focusResult(b.owner.ID())
				}
				return focusResult(b.owner.buttons[next].ID())
			}
		}
		if result := b.owner.Handle(ctx, evt); result.Handled {
			return result
		}
	}
	return b.ViewerButton.Handle(ctx, evt)
}
