package viewmodel

import (
	"time"

	"crude/tui/ui"
	"crude/tui/wm"
)

// revealDuration is the popup open slide-in duration. Short enough to feel
// instant, long enough to read as a deliberate reveal (the legacy
// ContextMenuLinesReveal used the same ~120ms feel).
const revealDuration = 120 * time.Millisecond

// revealAnim tracks one overlay's content-reveal animation. The frame's
// content area grows top-to-bottom from 0 to full over revealDuration.
type revealAnim struct {
	id        string
	startedAt time.Time
}

// frameOf returns the WindowFrame inside a floating window's content, or nil.
func frameOf(fw *wm.FloatingWindow) *ui.WindowFrame {
	if fw == nil || fw.Content == nil {
		return nil
	}
	if wf, ok := fw.Content.(*ui.WindowFrameWidget); ok {
		return wf.Frame()
	}
	return nil
}

// beginReveal starts a slide-in reveal for the overlay's WindowFrame (if it
// has one) and registers the animation so the per-frame tick advances it.
func (m *App) beginReveal(fw *wm.FloatingWindow, overlayID string) {
	if fw == nil {
		return
	}
	frame := frameOf(fw)
	if frame == nil {
		return
	}
	setWindowReveal(fw, 0)
	if m.reveals == nil {
		m.reveals = make(map[string]revealAnim)
	}
	m.reveals[overlayID] = revealAnim{id: overlayID, startedAt: time.Now()}
}

// advanceReveals updates every in-flight reveal to the current elapsed
// fraction. Returns true while any animation is still running. Called from
// the per-frame TickMsg handler.
func (m *App) advanceReveals(now time.Time) bool {
	if len(m.reveals) == 0 {
		return false
	}
	active := false
	for id, anim := range m.reveals {
		fw := m.windowManager.Window(id)
		if fw == nil {
			// Overlay closed mid-reveal: drop it.
			delete(m.reveals, id)
			continue
		}
		frame := frameOf(fw)
		if frame == nil {
			delete(m.reveals, id)
			continue
		}
		elapsed := now.Sub(anim.startedAt)
		if elapsed >= revealDuration {
			setWindowReveal(fw, 1)
			delete(m.reveals, id)
			continue
		}
		setWindowReveal(fw, float64(elapsed)/float64(revealDuration))
		active = true
	}
	return active
}

func setWindowReveal(fw *wm.FloatingWindow, progress float64) {
	widget, ok := fw.Content.(*ui.WindowFrameWidget)
	if !ok || widget.Frame().Reveal() == progress {
		return
	}
	widget.SetReveal(progress)
	// The final step also damages the window: a delayed first tick can jump
	// straight to fully revealed without ever returning an active animation.
	fw.MarkContentDirty()
}

// stopReveal removes any reveal for the overlay (used on close).
func (m *App) stopReveal(overlayID string) {
	delete(m.reveals, overlayID)
}
