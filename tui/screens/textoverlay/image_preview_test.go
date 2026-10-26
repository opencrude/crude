package textoverlay

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"

	"crude/tui/cell"
	"crude/tui/compositor"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/wm"
)

func TestImagePreviewGraphicsCompositesAsClippedCells(t *testing.T) {
	th := theme.DefaultDark()
	img := image.NewNRGBA(image.Rect(0, 0, 320, 160))
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, img); err != nil {
		t.Fatal(err)
	}
	screen := New(th)
	screen.OpenImage("image", "Actual image", img)
	resource := tea.NewKittyImage(pngData.Bytes())
	screen.SetImageResource(resource)
	screen.SetImageGraphics(true, 8, 16)
	frame := ui.NewWindowFrameWidget(th, "frame", ui.FrameWindow, screen.Title(), screen, nil, true, 42, 16)
	surf := surface.NewSurface("window", 42, 16, cell.TransparentBlank())
	frame.Render(surf, &wm.RenderContext{Theme: th})
	body, _ := frame.Host().ScreenRect(ImageID)
	placement := screen.ImagePlacement()
	// A 2:1 source at 1:2 cells fills 40 columns by 10 rows, not the
	// fallback's 40 sampled columns by 10 sampled rows of block glyphs.
	if placement.Image != resource || placement.Columns != 40 || placement.Rows != 10 {
		t.Fatalf("actual-pixel placement: %+v", placement)
	}
	count := 0
	var first cell.Cell
	for y := 0; y < surf.Height(); y++ {
		for x := 0; x < surf.Width(); x++ {
			c := surf.Buffer().Get(x, y)
			if c.Char == '▀' {
				t.Fatal("supported terminal still received a low-resolution approximation")
			}
			if c.Char != kitty.Placeholder {
				continue
			}
			if !body.Contains(x, y) || c.Width != 1 {
				t.Fatal("graphics cell escaped its one-cell body geometry")
			}
			if count == 0 {
				first = c
			}
			count++
		}
	}
	if count != 400 {
		t.Fatalf("graphics cells=%d, want 400", count)
	}
	if first.Content != string([]rune{kitty.Placeholder, kitty.Diacritic(0), kitty.Diacritic(0), kitty.Diacritic(int(resource.ID() >> 24))}) {
		t.Fatalf("first placeholder lacks explicit coordinates: %q", first.Content)
	}
	if first.FG != cell.NewTrueColor(uint8(resource.ID()>>16), uint8(resource.ID()>>8), uint8(resource.ID())) {
		t.Fatal("terminal image identity was not encoded in its placeholder")
	}
	// Nonzero parent origin, a clipped image edge, and a later opaque popup
	// all use the real surface compositor before crossing the UV boundary.
	c := compositor.NewCompositor(46, 18)
	surf.SetPosition(-2, 2)
	cover := surface.NewSurface("cover", 8, 4, cell.NewCell('X', cell.NoColor, cell.NoColor, 0))
	cover.SetPosition(8, 6)
	cover.SetZOrder(1)
	c.BeginFrame()
	c.Root().AddChild(surf)
	c.Root().AddChild(cover)
	c.Composite()
	out := uv.NewScreenBuffer(46, 18)
	c.PresentSurface().Draw(out, out.Bounds())
	visible := 0
	for y := 0; y < 18; y++ {
		for x := 0; x < 46; x++ {
			got := out.CellAt(x, y)
			if x >= 8 && x < 16 && y >= 6 && y < 10 && got.Content != "X" {
				t.Fatal("terminal image painted over an ordinary later popup")
			}
			if strings.HasPrefix(got.Content, string(kitty.Placeholder)) {
				visible++
				if got.Width != 1 || len([]rune(got.Content)) != 4 {
					t.Fatal("compositor lost image coordinates or cell width")
				}
			}
		}
	}
	if visible == 0 || visible >= count {
		t.Fatalf("clipping and occlusion did not affect image cells: %d", visible)
	}
	// Resizing keeps the immutable source and uses measured font geometry.
	screen.SetImageGraphics(true, 8, 24)
	frame.Host().Resize(22, 12)
	frame.Host().MarkLayout()
	frame.Host().Paint(surface.NewCellBuffer(22, 12))
	placement = screen.ImagePlacement()
	if placement.Image != resource || placement.Columns != 20 || placement.Rows != 4 {
		t.Fatalf("font/geometry change did not refit original source: %+v", placement)
	}
	screen.Close()
	if screen.ImagePlacement().Image != nil || screen.image.resource != nil || screen.image.placeholders != nil {
		t.Fatal("closed preview retained its terminal resources")
	}
}

func TestImagePreviewGraphicsFallsBackWithoutSupportAndRekeys(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.OpenImage("image", "Image", image.NewNRGBA(image.Rect(0, 0, 4, 2)))
	first := tea.NewKittyImage([]byte("immutable prepared PNG"))
	screen.SetImageResource(first)
	frame := ui.NewWindowFrameWidget(theme.DefaultDark(), "frame", ui.FrameWindow, screen.Title(), screen, nil, true, 22, 12)
	buf := surface.NewCellBuffer(22, 12)
	frame.Host().Paint(buf)
	if screen.ImagePlacement().Image != nil || !strings.ContainsRune(bufferText(buf), '▀') || strings.ContainsRune(bufferText(buf), kitty.Placeholder) {
		t.Fatal("unconfirmed terminal emitted graphics instead of the fallback")
	}
	screen.SetImageGraphics(true, 8, 16)
	frame.Host().MarkPaint()
	frame.Host().Paint(buf)
	old := append([]string(nil), screen.image.placeholders...)
	second := tea.NewKittyImage([]byte("another prepared PNG"))
	screen.SetImageResource(second)
	frame.Host().MarkPaint()
	frame.Host().Paint(buf)
	if screen.image.placeholderID != second.ID() || screen.ImagePlacement().Image != second || screen.image.placeholderID == first.ID() || len(old) == 0 {
		t.Fatal("same-sized replacement retained the previous terminal image identity")
	}
	screen.Open("text", "Edit", "TEXT ONLY")
	frame.Host().MarkLayout()
	frame.Host().Paint(buf)
	if screen.ImagePlacement().Image != nil || strings.ContainsRune(bufferText(buf), kitty.Placeholder) {
		t.Fatal("image resources leaked into the ordinary editable-text viewer")
	}
}

func TestImagePreviewRendersPixelsInsideSharedFrameAndFooter(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		// Nonzero source bounds and two distinct half-cell colors catch source
		// origin mistakes and accidental metadata/text fallback.
		img := image.NewNRGBA(image.Rect(3, 5, 5, 7))
		for x := 3; x < 5; x++ {
			img.SetNRGBA(x, 5, color.NRGBA{R: 255, A: 255})
			img.SetNRGBA(x, 6, color.NRGBA{B: 255, A: 128})
		}
		screen := New(th)
		screen.OpenImage("image", "Pasted IMG1", img)
		frame := ui.NewWindowFrameWidget(th, "frame", ui.FrameWindow, screen.Title(), screen, nil, true, 4, 5)
		screen.SetFocusHandler(frame.Focus)
		screen.FocusInput()
		bg := ui.NewTokenSet(th).Color(ui.ColorPopupBg)
		for _, size := range []ui.Size{{W: 4, H: 5}, {W: 40, H: 12}, {W: 10, H: 3}, {W: 4, H: 5}} {
			surf := surface.NewSurface("window", size.W, size.H, cell.TransparentBlank())
			frame.Render(surf, &wm.RenderContext{Theme: th})
			body, _ := frame.Host().ScreenRect(ImageID)
			actions, _ := frame.Host().ScreenRect(ActionsID)
			if frame.Host().Component(InputID) != nil || body.Y+body.H != actions.Y || !screen.ReadOnly() {
				t.Fatalf("image did not replace text body or overlap-free geometry: %+v %+v", body, actions)
			}
			pixels := 0
			for y := 0; y < size.H; y++ {
				for x := 0; x < size.W; x++ {
					c := surf.Buffer().Get(x, y)
					if c.Char != '▀' {
						continue
					}
					pixels++
					if !body.Contains(x, y) || frame.Host().HitTest(x, y) != ImageID {
						t.Fatalf("pixel escaped image paint/hit bounds at %d,%d", x, y)
					}
					if size == (ui.Size{W: 4, H: 5}) && (c.FG != cell.NewTrueColor(255, 0, 0) || c.BG != bg.Blend(cell.NewTrueColor(0, 0, 255), float32(128)/255)) {
						t.Fatalf("pixel color or alpha lost: %+v", c)
					}
				}
			}
			if body.H > 0 && pixels == 0 {
				t.Fatal("image content contains no rendered pixels")
			}
			if body.H == 0 && pixels != 0 {
				t.Fatal("constrained image painted into footer")
			}
		}
		// A full-size preview has the ordinary Close-only footer, not Save/Cancel.
		frame.Host().Resize(40, 12)
		frame.Host().MarkLayout()
		buf := surface.NewCellBuffer(40, 12)
		frame.Host().Paint(buf)
		if painted := bufferText(buf); !strings.Contains(painted, "[ Close ]") || strings.Contains(painted, "Save") || strings.Contains(painted, "Cancel") {
			t.Fatalf("image footer decisions:\n%s", painted)
		}
		screen.FocusInput()
		if result := frame.Dispatch(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}); result.Cmd != nil {
			t.Fatal("image preview emitted save")
		}
		frame.Dispatch(tea.KeyPressMsg{Code: tea.KeyTab})
		frame.Dispatch(tea.KeyPressMsg{Code: tea.KeyUp})
		if frame.Host().Focused() != ImageID {
			t.Fatal("image footer did not return focus to image body")
		}
		_ = commandMessage[CancelMsg](t, frame.Dispatch(tea.KeyPressMsg{Code: tea.KeyEsc}))
	}
}

func TestImagePreviewAspectRatioAndTextModeTransitions(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	frame := ui.NewWindowFrameWidget(th, "frame", ui.FrameWindow, "Preview", screen, nil, true, 12, 8)
	for _, source := range []image.Rectangle{image.Rect(0, 0, 8, 2), image.Rect(0, 0, 2, 8), image.Rect(0, 0, 0, 0)} {
		screen.OpenImage("image", "Pasted IMG1", image.NewNRGBA(source))
		frame.Host().MarkLayout()
		buf := surface.NewCellBuffer(12, 8)
		frame.Host().Paint(buf)
		pixels := 0
		for _, c := range buf.Cells() {
			if c.Char == '▀' {
				pixels++
			}
		}
		// Body is 10 columns x 4 rows: wide image is 10 x 2 pixels (one
		// terminal row), tall is 2 x 8 pixels (four rows).
		want := 0
		if source.Dx() == 8 {
			want = 10
		} else if source.Dx() == 2 {
			want = 8
		}
		if pixels != want {
			t.Fatalf("source %v rendered %d cells, want %d", source, pixels, want)
		}
	}
	screen.Open("text", "Edit", "editable again")
	frame.Host().MarkLayout()
	buf := surface.NewCellBuffer(12, 8)
	frame.Host().Paint(buf)
	if frame.Host().Component(ImageID) != nil || screen.ReadOnly() || strings.ContainsRune(bufferText(buf), '▀') {
		t.Fatal("image persisted after returning to editable text")
	}
	screen.OpenImage("image", "Pasted IMG1", image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	screen.Close()
	if screen.IsOpen() || screen.image.image != nil || screen.Value() != "" {
		t.Fatal("closing retained image content")
	}
}
