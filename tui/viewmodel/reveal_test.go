package viewmodel

import (
	"testing"
	"time"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/widget"
	"crude/tui/wm"
)

// newRevealTestWindow builds a floating window whose body is a WindowFrame
// (the standard product popup shape), so reveal animations apply.
func newRevealTestWindow(t *testing.T, overlayID string) *wm.FloatingWindow {
	t.Helper()
	th := theme.DefaultDark()
	body := &revealTestBody{Base: ui.NewBase("reveal/body")}
	frame := ui.NewWindowFrameWidget(th, ui.NewID("reveal", "frame"), ui.FrameWindow, "Reveal", body, nil, true, 30, 12)
	fw := wm.NewFloatingWindow(overlayID, "", frame, 30, 12, wm.WithDecoration(false))
	return fw
}

type revealTestBody struct {
	ui.Base
}

func (b *revealTestBody) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (b *revealTestBody) Layout(_ ui.LayoutContext, r ui.Rect) { b.SetBounds(r) }

func (b *revealTestBody) Paint(buf *surface.CellBuffer, _, _ int, _ ui.PaintContext) {}

// plainWidgetBody is a bare widget.Widget (no Component interface) used as a
// non-framed FloatingWindow.Content in the no-op reveal test.
type plainWidgetBody struct{}

func (plainWidgetBody) Render(_ *surface.Surface, _ *widget.RenderContext) {}
func (plainWidgetBody) Measure(availW, availH int) (int, int)             { return availW, availH }

// TestReveal_BeginsAtZeroAndCompletes verifies the full lifecycle: begin
// collapses the frame content, ticks ramp it, and after the duration it is
// fully revealed and the anim is removed.
func TestReveal_BeginsAtZeroAndCompletes(t *testing.T) {
	app := newFullTestApp(t)
	fw := newRevealTestWindow(t, "reveal-test")
	// Register the window so advanceReveals can resolve the live frame
	// (mirrors overlayHost.Open in production).
	app.windowManager.AddWindow(fw)
	app.beginReveal(fw, "reveal-test")

	frame := frameOf(fw)
	if frame == nil {
		t.Fatal("frame not found")
	}
	if got := frame.Reveal(); got != 0 {
		t.Fatalf("reveal at begin=%v, want 0", got)
	}
	if _, ok := app.reveals["reveal-test"]; !ok {
		t.Fatal("reveal anim not registered")
	}

	// Mid-way: progress should be ~0.5 after half the duration.
	app.reveals["reveal-test"] = revealAnim{id: "reveal-test", startedAt: time.Now().Add(-revealDuration / 2)}
	app.advanceReveals(time.Now())
	mid := frame.Reveal()
	if mid < 0.3 || mid > 0.7 {
		t.Fatalf("mid reveal=%v, want ~0.5", mid)
	}

	// Past the duration: fully revealed and anim removed.
	app.reveals["reveal-test"] = revealAnim{id: "reveal-test", startedAt: time.Now().Add(-revealDuration * 2)}
	app.advanceReveals(time.Now())
	if got := frame.Reveal(); got != 1 {
		t.Fatalf("final reveal=%v, want 1", got)
	}
	if _, ok := app.reveals["reveal-test"]; ok {
		t.Fatal("reveal anim should be removed after completion")
	}
}

// TestReveal_NoWindowFrameIsNoop verifies windows without a WindowFrame body
// are untouched (no crash, no animation state).
func TestReveal_NoWindowFrameIsNoop(t *testing.T) {
	app := newFullTestApp(t)
	fw := wm.NewFloatingWindow("plain", "", plainWidgetBody{}, 10, 5, wm.WithDecoration(false))
	app.beginReveal(fw, "plain")

	if len(app.reveals) != 0 {
		t.Fatalf("no reveal should be registered for non-framed window, got %d", len(app.reveals))
	}
	// Advancing with nothing registered is a no-op.
	if app.advanceReveals(time.Now()) {
		t.Fatal("advance with no anims must return false")
	}
}

// TestReveal_StopsOnClose verifies stopReveal drops the animation.
func TestReveal_StopsOnClose(t *testing.T) {
	app := newFullTestApp(t)
	fw := newRevealTestWindow(t, "reveal-close")
	app.beginReveal(fw, "reveal-close")
	if len(app.reveals) != 1 {
		t.Fatalf("expected 1 anim, got %d", len(app.reveals))
	}
	app.stopReveal("reveal-close")
	if len(app.reveals) != 0 {
		t.Fatalf("expected 0 anims after stop, got %d", len(app.reveals))
	}
}

// TestReveal_ClosedWindowDroppedMidReveal verifies that a reveal whose
// overlay was closed (window gone) is dropped without error.
func TestReveal_ClosedWindowDroppedMidReveal(t *testing.T) {
	app := newFullTestApp(t)
	fw := newRevealTestWindow(t, "reveal-gone")
	app.beginReveal(fw, "reveal-gone")
	// Simulate the overlay closing: remove from the WM (windowManager nil in
	// newFullTestApp? guard: beginReveal needs the frame only).
	if app.windowManager != nil {
		app.windowManager.RemoveWindow("reveal-gone")
	}
	app.advanceReveals(time.Now())
	if _, ok := app.reveals["reveal-gone"]; ok {
		t.Fatal("reveal for a closed overlay must be dropped")
	}
}

// TestReveal_OverlayOpenStartsAnimation verifies the end-to-end hook: opening
// an overlay through OverlayHost starts a reveal on the frame.
func TestReveal_OverlayOpenStartsAnimation(t *testing.T) {
	app := newFullTestApp(t)
	if app.overlayHost == nil {
		t.Fatal("precondition: overlayHost present")
	}
	app.registerOverlayHandlers()

	fw := newRevealTestWindow(t, "reveal-open")
	app.overlayHost.Open(ui.OverlayContextMenu, fw, ui.PopoverPolicy(true), nil)
	defer app.overlayHost.Close(ui.OverlayContextMenu)

	frame := frameOf(fw)
	if frame == nil {
		t.Fatal("frame not found")
	}
	if got := frame.Reveal(); got != 0 {
		t.Fatalf("reveal after open=%v, want 0 (animation started)", got)
	}
	if _, ok := app.reveals["context-menu"]; !ok {
		t.Fatal("overlay open must register a reveal anim")
	}
}

// TestReveal_ModalOverlaySkipsAnimation verifies modals appear instantly:
// their frames are NOT collapsed to reveal=0 on open.
func TestReveal_ModalOverlaySkipsAnimation(t *testing.T) {
	app := newFullTestApp(t)
	if app.overlayHost == nil {
		t.Fatal("precondition: overlayHost present")
	}
	app.registerOverlayHandlers()

	fw := newRevealTestWindow(t, "modal-test")
	app.overlayHost.Open(ui.OverlayDirtyClose, fw, ui.ModalDialogPolicy(), nil)
	defer app.overlayHost.Close(ui.OverlayDirtyClose)

	frame := frameOf(fw)
	if frame == nil {
		t.Fatal("frame not found")
	}
	if got := frame.Reveal(); got != 1 {
		t.Fatalf("modal reveal=%v, want 1 (no animation, full content)", got)
	}
	if _, ok := app.reveals["dirty-close-prompt"]; ok {
		t.Fatal("modal overlay must not register a reveal anim")
	}
}

// TestReveal_TypingPopupsSkipAnimation verifies completion/references/outline
// popups appear instantly (they re-open on every keystroke).
func TestReveal_TypingPopupsSkipAnimation(t *testing.T) {
	app := newFullTestApp(t)
	if app.overlayHost == nil {
		t.Fatal("precondition: overlayHost present")
	}
	app.registerOverlayHandlers()

	for _, tc := range []struct {
		id  ui.OverlayID
		pol ui.OverlayPolicy
	}{
		{ui.OverlayCompletion, ui.PopoverPolicy(true)},
		{ui.OverlayReferences, ui.PopoverPolicy(true)},
		{ui.OverlayOutline, ui.PopoverPolicy(true)},
	} {
		fw := newRevealTestWindow(t, string(tc.id))
		app.overlayHost.Open(tc.id, fw, tc.pol, nil)
		defer app.overlayHost.Close(tc.id)

		frame := frameOf(fw)
		if frame == nil {
			t.Fatalf("%s: frame not found", tc.id)
		}
		if got := frame.Reveal(); got != 1 {
			t.Fatalf("%s: reveal=%v, want 1 (typing popup must not animate)", tc.id, got)
		}
		if _, ok := app.reveals[string(tc.id)]; ok {
			t.Fatalf("%s: typing popup must not register a reveal anim", tc.id)
		}
	}
}
