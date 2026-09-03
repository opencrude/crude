package viewmodel

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/workspace"
)

func TestPointerBurstCoalescesPresentationWithoutBufferingGeometry(t *testing.T) {
	app := newFullTestApp(t)
	model := filetree.NewFileTreeModel(t.TempDir())
	app.Tree = NewTreeState(model, 30)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(model)
	_ = app.View()
	dividerX := app.activityChromeWidth() + app.Tree.Width
	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: dividerX, Y: 8, Button: tea.MouseLeft})
	app.forceFrame()
	_ = app.View()
	baseFrames := app.frameGate.presented
	for i := 0; i < 1000; i++ {
		x := dividerX + (i % 20)
		_, _ = app.Update(tea.MouseMotionMsg{X: x, Y: 8, Button: tea.MouseLeft})
		_ = app.View()
	}
	if !app.frameGate.wakePending {
		t.Fatal("pointer burst did not retain one deadline wake")
	}
	if got := app.Tree.Width; got != 40 {
		t.Fatalf("latest clamped geometry=%d want 40; events were buffered or dropped", got)
	}
	if frames := app.frameGate.presented - baseFrames; frames > 8 {
		t.Fatalf("pointer burst presented %d full frames; expected coalescing", frames)
	}
	_, _ = app.Update(tea.MouseReleaseMsg{X: dividerX + 19, Y: 8, Button: tea.MouseLeft})
	_ = app.View()
	if app.rootHost.HasPointerCapture() {
		t.Fatal("release did not clear capture")
	}
	if app.Tree.Width != 40 {
		t.Fatalf("release changed final geometry to %d", app.Tree.Width)
	}
}

func TestFrameGateKeepsSingleWakeAndReleaseForcesFrame(t *testing.T) {
	app := newFullTestApp(t)
	app.cachedOutput = "cached"
	app.frameGate.lastPresented = time.Now()
	first := app.requestCoalescedFrame(time.Now())
	if first == nil || !app.frameGate.wakePending {
		t.Fatal("first deferred frame did not schedule wake")
	}
	generation := app.frameGate.generation
	if second := app.requestCoalescedFrame(time.Now()); second != nil || app.frameGate.generation != generation {
		t.Fatal("duplicate pointer wake scheduled")
	}
	app.forceFrame()
	if app.shouldDeferFrame() || !app.frameGate.due || app.frameGate.wakePending {
		t.Fatal("release/forced frame did not supersede deferred wake")
	}
}

func TestIdleCadenceNeverDropsBelowTwoFPS(t *testing.T) {
	if workspace.IdleTickInterval > 500*time.Millisecond {
		t.Fatalf("idle tick=%s is below 2 FPS", workspace.IdleTickInterval)
	}
	if workspace.IdleTickInterval < 250*time.Millisecond {
		t.Fatalf("idle tick=%s needlessly burns CPU", workspace.IdleTickInterval)
	}
}
