package viewmodel

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"

	"crude/agent/sessiondb"
	"crude/tui/ui"
	"crude/tui/wm"
)

// Wheel zoom is a discrete change: each step publishes the native image at
// the new zoom, and the terminal writer holds the current pixels until the new
// raster replaces them. Only a held pointer drag paints the cell preview.
func TestSixelFileWheelZoomsNativeImageAndDragPreviews(t *testing.T) {
	app, ws := newRealShellApp(t)
	paneJourneySize(app, 132, 40)
	app.terminalImages = terminalImageState{sixel: true, cellWidth: 8, cellHeight: 20}
	openImageFromTree(t, app, writeSizedImageFixture(t, ws.RootPath, "wheel-sixel.png", 800, 400))
	before := app.View()
	if len(before.Images) != 1 {
		t.Fatal("fixture did not start with native Sixel")
	}
	original := before.Images[0]
	rect, ok := app.screenRectOf(app.activeImageView().ID())
	if !ok {
		t.Fatal("file image was not mounted")
	}
	x, y := rect.X+rect.W/3, rect.Y+rect.H/2
	columns := original.Columns
	for range 2 {
		_, cmd := app.Update(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp})
		if cmd != nil {
			runAppCommand(t, app, cmd)
		}
		view := app.View()
		content := view.RenderContent()
		if len(view.Images) != 1 || view.Images[0].Image != original.Image || strings.ContainsRune(content, '▀') ||
			!strings.ContainsRune(content, kitty.Placeholder) {
			t.Fatal("wheel zoom replaced the native image with the cell preview")
		}
		if view.Images[0].Columns <= columns {
			t.Fatal("wheel input did not change the displayed image geometry")
		}
		columns = view.Images[0].Columns
	}
	settled := app.View()
	grid := uv.NewScreenBuffer(rect.X+rect.W, rect.Y+rect.H)
	settled.Surface.Draw(grid, settled.Surface.Bounds())
	anchor := []rune(grid.CellAt(x, y).Content)
	if len(anchor) != 4 || anchor[0] != kitty.Placeholder {
		t.Fatal("fixture did not place a native marker at the pan anchor")
	}
	column := 0
	for column < settled.Images[0].Columns && kitty.Diacritic(column) != anchor[2] {
		column++
	}
	if column < 12 || column == settled.Images[0].Columns {
		t.Fatal("fixture cannot test an unclamped pan at this anchor")
	}
	app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	last := ""
	for _, dx := range []int{4, 8, 12} {
		app.Update(tea.MouseMotionMsg{X: x + dx, Y: y + 1, Button: tea.MouseLeft})
		// Sample every motion, independent of whether their actual timestamps
		// fit the same ordinary 16 ms compositor frame.
		app.presentImmediateFrame()
		frame := app.View()
		text := frame.RenderContent()
		if len(frame.Images) != 0 || !strings.ContainsRune(text, '▀') || text == last {
			t.Fatal("captured pan did not paint the latest image as cells")
		}
		last = text
	}
	// A pause with the button down keeps the preview while the real image
	// pane still owns pointer capture.
	app.presentImmediateFrame()
	if held := app.View(); len(held.Images) != 0 || !strings.ContainsRune(held.RenderContent(), '▀') {
		t.Fatal("a pause during capture replaced the live preview with a native raster")
	}
	app.Update(tea.MouseReleaseMsg{X: x + 12, Y: y + 1, Button: tea.MouseLeft})
	panned := app.View()
	panned.Surface.Draw(grid, panned.Surface.Bounds())
	anchor = []rune(grid.CellAt(x, y).Content)
	if len(panned.Images) != 1 || panned.Images[0] != settled.Images[0] ||
		len(anchor) != 4 || anchor[0] != kitty.Placeholder || anchor[2] != kitty.Diacritic(column-12) {
		t.Fatal("release did not restore the original resource at the last captured pan")
	}
	_, close := app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	runAppCommand(t, app, close)
	if len(app.View().Images) != 0 || strings.ContainsRune(app.View().RenderContent(), kitty.Placeholder) {
		t.Fatal("closed image left a native placement")
	}
}

// A pane small enough for quick terminal frames pans natively: every captured
// motion publishes the same resource at the new crop, never the cell preview.
func TestSixelFileSmallPanePansNatively(t *testing.T) {
	app, ws := newRealShellApp(t)
	paneJourneySize(app, 100, 30)
	app.terminalImages = terminalImageState{sixel: true, cellWidth: 6, cellHeight: 12}
	openImageFromTree(t, app, writeImageFixture(t, ws.RootPath, "pan-sixel.png"))
	if len(app.View().Images) != 1 {
		t.Fatal("fixture did not start with native Sixel")
	}
	rect, ok := app.screenRectOf(app.activeImageView().ID())
	if !ok || rect.W*6*rect.H*12 > sixelNativeMotionPixels {
		t.Fatalf("fixture pane %v is not small enough for native motion", rect)
	}
	x, y := rect.X+rect.W/2, rect.Y+rect.H/2
	for range 3 {
		_, cmd := app.Update(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp})
		if cmd != nil {
			runAppCommand(t, app, cmd)
		}
	}
	zoomed := app.View()
	if len(zoomed.Images) != 1 {
		t.Fatal("zoomed image is not native")
	}
	app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	last := zoomed.RenderContent()
	for _, dx := range []int{3, 6, 9} {
		app.Update(tea.MouseMotionMsg{X: x + dx, Y: y + 1, Button: tea.MouseLeft})
		app.presentImmediateFrame()
		frame := app.View()
		text := frame.RenderContent()
		if len(frame.Images) != 1 || frame.Images[0].Image != zoomed.Images[0].Image ||
			strings.ContainsRune(text, '▀') || text == last {
			t.Fatal("a small pane's pan did not stay native at the new crop")
		}
		last = text
	}
	app.Update(tea.MouseReleaseMsg{X: x + 9, Y: y + 1, Button: tea.MouseLeft})
	if released := app.View(); len(released.Images) != 1 || released.RenderContent() != last {
		t.Fatal("release changed the natively panned image")
	}
}

// Zooming past the 297-cell marker range keeps native Sixel pixels: the pane
// publishes a window into the virtual image instead of the reduced preview, and
// a small pan inside that window keeps the same placement.
func TestSixelFileHighZoomStaysNative(t *testing.T) {
	app, ws := newRealShellApp(t)
	paneJourneySize(app, 132, 40)
	app.terminalImages = terminalImageState{sixel: true, cellWidth: 8, cellHeight: 20}
	openImageFromTree(t, app, writeSizedImageFixture(t, ws.RootPath, "zoom-sixel.png", 800, 400))
	if len(app.View().Images) != 1 {
		t.Fatal("fixture did not start with native Sixel")
	}
	rect, _ := app.screenRectOf(app.activeImageView().ID())
	x, y := rect.X+rect.W/2, rect.Y+rect.H/2
	var placement tea.ImagePlacement
	for range 40 {
		_, cmd := app.Update(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp})
		if cmd != nil {
			runAppCommand(t, app, cmd)
		}
		view := app.View()
		if len(view.Images) != 1 || strings.Contains(view.RenderContent(), "Reduced detail") {
			t.Fatalf("zoom fell back to the cell preview: %+v", view.Images)
		}
		if placement = view.Images[0]; placement.VirtualColumns > 297 {
			break
		}
	}
	if placement.VirtualColumns <= 297 || placement.Columns > 297 || placement.SourceWidth != 0 {
		t.Fatalf("high zoom did not publish a window into the virtual image: %+v", placement)
	}
	before := app.View().RenderContent()
	app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	app.Update(tea.MouseMotionMsg{X: x + 3, Y: y, Button: tea.MouseLeft})
	app.Update(tea.MouseReleaseMsg{X: x + 3, Y: y, Button: tea.MouseLeft})
	panned := app.View()
	if len(panned.Images) != 1 || panned.Images[0] != placement || panned.RenderContent() == before {
		t.Fatalf("a small pan did not move inside the same window: %+v, was %+v", panned.Images, placement)
	}
}

func TestSixelSplitterPreservesAnUnchangedImage(t *testing.T) {
	app, ws := newRealShellApp(t)
	paneJourneySize(app, 176, 48)
	app.terminalImages = terminalImageState{sixel: true, cellWidth: 9, cellHeight: 20}
	openImageFromTree(t, app, writeImageFixture(t, ws.RootPath, "split-sixel.png"))
	left := app.Win.ActiveGroup()
	app.splitEditor(ui.Horizontal, false)
	runAppCommand(t, app, app.drainLiveEditorCommands())
	right := app.Win.PaneRoot().Second.Group
	app.focusMainGroup(right)
	app.splitEditor(ui.Vertical, false)
	runAppCommand(t, app, app.drainLiveEditorCommands())
	app.presentImmediateFrame()
	before := app.View()
	if len(before.Images) != 3 {
		t.Fatalf("fixture did not mount three native placements: %d", len(before.Images))
	}
	stable := app.imageView(app.Win.GroupActiveTab(left).ID).ImagePlacement()
	node := app.Win.PaneRoot().Second
	rect, ok := app.rootHost.ScreenRect(app.mainPanes.dividers[node.ID].ID())
	if !ok {
		t.Fatal("nested split divider was not mounted")
	}
	x, y := rect.X+rect.W/2, rect.Y
	app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	for _, dy := range []int{2, 5, 3} {
		app.Update(tea.MouseMotionMsg{X: x, Y: y + dy, Button: tea.MouseLeft})
		app.presentImmediateFrame()
		view := app.View()
		actual, _ := app.rootHost.ScreenRect(app.mainPanes.dividers[node.ID].ID())
		if actual.Y != y+dy || len(view.Images) != 1 || view.Images[0] != stable ||
			!strings.ContainsRune(view.RenderContent(), '▀') {
			t.Fatalf("split did not follow the pointer with only affected previews: divider=%+v images=%d", actual, len(view.Images))
		}
	}
	app.Update(tea.MouseReleaseMsg{X: x, Y: y + 3, Button: tea.MouseLeft})
	if view := app.View(); len(view.Images) != 3 || strings.ContainsRune(view.RenderContent(), '▀') {
		t.Fatal("release did not restore both native split images")
	}
}

func openSixelMotionAttachment(t *testing.T, data []byte, mime string) (*App, *wm.FloatingWindow) {
	t.Helper()
	app := newTranscriptAttachmentApp(t)
	paneJourneySize(app, 176, 48)
	app.terminalImages = terminalImageState{started: true, sixel: true, cellWidth: 9, cellHeight: 20}
	ref, err := app.RefStore().CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedImage, Mime: mime, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	runAppCommand(t, app, app.beginReferencePreview(ref, true))
	fw := app.overlayHost.Window(ui.OverlayTextOverlay)
	fw.SetPosition(9, 6)
	fw.SetSize(112, 34)
	app.presentImmediateFrame()
	if v := app.View(); len(v.Images) != 1 || v.Images[0].Protocol != tea.ImageSixel {
		t.Fatal("fixture did not mount native Sixel attachment")
	}
	return app, fw
}

func TestSixelAttachmentResizeAndCloseDuringMotion(t *testing.T) {
	app, fw := openSixelMotionAttachment(t, attachmentImageBytes(t), "image/png")
	before := app.View().Images[0]
	x, y := fw.Position()
	w, h := fw.Size()
	x, y = x+w-1, y+h-1
	app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	var wake tea.Cmd
	for _, d := range []int{6, 10, 14} {
		_, cmd := app.Update(tea.MouseMotionMsg{X: x - d, Y: y - d, Button: tea.MouseLeft})
		wake = tea.Batch(wake, cmd)
		app.presentImmediateFrame()
		view := app.View()
		if actualW, actualH := fw.Size(); actualW != w-d || actualH != h-d ||
			len(view.Images) != 0 || !strings.ContainsRune(view.RenderContent(), '▀') {
			t.Fatalf("resize did not present the new frame and image preview: %dx%d", actualW, actualH)
		}
	}
	app.Update(tea.MouseReleaseMsg{X: x - 14, Y: y - 14, Button: tea.MouseLeft})
	after := app.View()
	if len(after.Images) != 1 || after.Images[0].Image != before.Image || after.Images[0].Rows >= before.Rows {
		t.Fatal("released attachment did not return to its original native pixels at the final size")
	}
	// Moving the smaller window reuses its prepared raster, so it stays native.
	x, y = fw.Position()
	app.Update(tea.MouseClickMsg{X: x + 6, Y: y, Button: tea.MouseLeft})
	_, move := app.Update(tea.MouseMotionMsg{X: x + 10, Y: y + 1, Button: tea.MouseLeft})
	app.presentImmediateFrame()
	moved := app.View()
	if movedX, _ := fw.Position(); movedX == x || len(moved.Images) != 1 || moved.Images[0] != after.Images[0] ||
		strings.ContainsRune(moved.RenderContent(), '▀') {
		t.Fatal("moving a small image window dropped its native pixels")
	}
	_, close := app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	runAppCommand(t, app, tea.Batch(wake, move, close))
	if app.overlayHost.IsOpen(ui.OverlayTextOverlay) || len(app.View().Images) != 0 ||
		app.Input.Value() != "KEEP MY COMPOSER DRAFT" {
		t.Fatal("a pending motion wake resurrected the closed attachment or consumed the draft")
	}
}

type pacedSixelModel struct {
	*App
	frame int
}

func (*pacedSixelModel) Init() tea.Cmd { return nil }

func (m *pacedSixelModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tea.MouseClickMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg:
		m.frame++
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}

func (m *pacedSixelModel) View() tea.View {
	v := m.App.View()
	// The marker and the physical cell diff count as one writer flush.
	v.WindowTitle = fmt.Sprintf("sixel-motion-frame-%d!", m.frame)
	return v
}

type pacedSixelWrite struct {
	data []byte
	at   time.Time
}

type pacedSixelSink struct {
	out   chan pacedSixelWrite
	loops chan tea.LoopSample
}

func (s *pacedSixelSink) Write(p []byte) (int, error) {
	if len(p) != 0 {
		at := time.Now()
		s.out <- pacedSixelWrite{bytes.Clone(p), at}
	}
	return len(p), nil
}

func (s *pacedSixelSink) ObserveLoop(sample tea.LoopSample) {
	if _, motion := sample.Msg.(tea.MouseMotionMsg); motion {
		s.loops <- sample
	}
}

func (*pacedSixelSink) ObserveFlush(tea.FlushSample) {}

func TestSixelAttachmentPacedDragThroughTerminalWriter(t *testing.T) {
	data, mime := attachmentImageBytes(t), "image/png"
	if photo := os.Getenv("CRUDE_SIXEL_PHOTO"); photo != "" {
		var err error
		data, err = os.ReadFile(photo)
		if err != nil {
			t.Fatal(err)
		}
		mime = "image/jpeg"
	}
	app, fw := openSixelMotionAttachment(t, data, mime)
	x, y := fw.Position()
	width, _ := fw.Size()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	sink := &pacedSixelSink{out: make(chan pacedSixelWrite, 128), loops: make(chan tea.LoopSample, 16)}
	program := tea.NewProgram(&pacedSixelModel{App: app}, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(sink),
		tea.WithWindowSize(app.width, app.height), tea.WithEnvironment([]string{"TERM=xterm-256color"}),
		tea.WithColorProfile(colorprofile.TrueColor), tea.WithObserver(sink), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() { _, err := program.Run(); done <- err }()
	defer func() {
		program.Quit()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()

	dcs := []byte("\x1bP0;1q")
	await := func(until func(pacedSixelWrite) bool) pacedSixelWrite {
		t.Helper()
		var received, longest int
		var first, last string
		for {
			select {
			case write := <-sink.out:
				received++
				longest = max(longest, len(write.data))
				last = fmt.Sprintf("%q", write.data[:min(70, len(write.data))])
				if received == 1 {
					first = last
				}
				if until(write) {
					return write
				}
			case <-ctx.Done():
				t.Fatalf("waiting for Sixel writer: %v; writes=%d largest=%d first=%s last=%s", ctx.Err(), received, longest, first, last)
			}
		}
	}
	native := func(w pacedSixelWrite) bool { return bytes.Contains(w.data, dcs) }
	open := await(native)
	t.Logf("stable native: %d writer bytes", len(open.data))
	program.Send(tea.MouseClickMsg{X: x + 6, Y: y, Button: tea.MouseLeft})
	await(func(w pacedSixelWrite) bool { return bytes.Contains(w.data, []byte("sixel-motion-frame-1!")) })

	pace := time.NewTicker(80 * time.Millisecond)
	defer pace.Stop()
	for i := range 5 {
		select {
		case <-pace.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		marker := []byte(fmt.Sprintf("sixel-motion-frame-%d!", i+2))
		started := time.Now()
		program.Send(tea.MouseMotionMsg{X: x + 7 + i, Y: y, Button: tea.MouseLeft})
		preview := await(func(w pacedSixelWrite) bool {
			if native(w) {
				t.Fatalf("intermediate motion %d sent native Sixel (%d writer bytes)", i, len(w.data))
			}
			return bytes.Contains(w.data, marker)
		})
		if !bytes.Contains(preview.data, []byte("▀")) {
			t.Fatalf("physical frame %d did not write any of the live image preview", i)
		}
		select {
		case loop := <-sink.loops:
			t.Logf("motion %d: Update %s, View+handoff %s, send-to-write %s, %d writer bytes, no DCS",
				i+1, loop.Update, loop.View+loop.Present, preview.at.Sub(started), len(preview.data))
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}

	program.Send(tea.MouseReleaseMsg{X: x + 11, Y: y, Button: tea.MouseLeft})
	settled := await(native)
	t.Logf("released native: %d writer bytes", len(settled.data))
	// Five moves translated the frame by five columns. Use the real close hit
	// target, not a test-only teardown or a fabricated View.
	program.Send(tea.MouseClickMsg{X: x + 5 + width - 2, Y: y, Button: tea.MouseLeft})
	await(func(w pacedSixelWrite) bool {
		if native(w) {
			t.Fatal("closing the window transmitted stale native Sixel")
		}
		return bytes.Contains(w.data, []byte("sixel-motion-frame-8!"))
	})
}

// A run of large wheel updates remains visible at its live geometry, does
// not enqueue native frames per step, and returns to native without another
// input after the last step.
func TestSixelFileWheelBurstSettlesAtLastZoom(t *testing.T) {
	app, ws := newRealShellApp(t)
	paneJourneySize(app, 160, 44)
	app.terminalImages = terminalImageState{sixel: true, cellWidth: 9, cellHeight: 20}
	openImageFromTree(t, app, writeSizedImageFixture(t, ws.RootPath, "burst.png", 1920, 1056))
	view := app.View()
	if len(view.Images) != 1 {
		t.Fatal("fixture is not using native Sixel")
	}
	original := view.Images[0].Image
	pane := app.activeImageView()
	r, ok := app.screenRectOf(pane.ID())
	if !ok {
		t.Fatal("image pane did not mount")
	}
	wheel := tea.MouseWheelMsg{X: r.X + r.W/2, Y: r.Y + r.H/2, Button: tea.MouseWheelUp}
	var cmds []tea.Cmd
	for i := range 7 {
		before, _ := pane.SixelMotionGeometry()
		_, cmd := app.Update(wheel)
		cmds = append(cmds, cmd)
		app.presentImmediateFrame()
		view = app.View()
		after, _ := pane.SixelMotionGeometry()
		if after.W <= before.W {
			t.Fatalf("wheel step %d did not change zoom", i+1)
		}
		if i >= 2 && (len(view.Images) != 0 || !strings.ContainsRune(view.RenderContent(), '▀')) {
			t.Fatalf("burst step %d did not show the live preview", i+1)
		}
	}
	geometry, _ := pane.SixelMotionGeometry()
	runAppCommand(t, app, tea.Batch(cmds...))
	view = app.View()
	if after, _ := pane.SixelMotionGeometry(); len(view.Images) != 1 || view.Images[0].Image != original || after != geometry {
		t.Fatal("after motion the native image did not settle at the last geometry")
	}
}
