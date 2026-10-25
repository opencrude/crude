package textoverlay

import (
	"image"
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/wm"
)

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
