package wm

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"

	tea "charm.land/bubbletea/v2"
)

func TestNewWindowManager(t *testing.T) {
	wm := NewWindowManager()
	if wm == nil {
		t.Fatal("NewWindowManager returned nil")
	}
	if wm.Focused() != nil {
		t.Error("new manager should have no focused window")
	}
	if wm.TopWindow() != nil {
		t.Error("new manager should have no top window")
	}
	if len(wm.Windows()) != 0 {
		t.Errorf("Windows() count = %d, want 0", len(wm.Windows()))
	}
	if wm.IsDragging() {
		t.Error("new manager should not be dragging")
	}
}

func TestAddWindow(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("a", "A", testWidget{}, 20, 10)
	mgr.AddWindow(fw)

	if len(mgr.Windows()) != 1 {
		t.Errorf("window count = %d, want 1", len(mgr.Windows()))
	}
	if mgr.TopWindow() != fw {
		t.Error("TopWindow should be the added window")
	}
	if mgr.Focused() != fw {
		t.Error("Focused should be the added window")
	}
	if !fw.Focused() {
		t.Error("added window should be focused")
	}
}

func TestAddWindowNonFocusable(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("nf", "NF", testWidget{}, 20, 10,
		WithFocusable(false))
	mgr.AddWindow(fw)

	if mgr.Focused() != nil {
		t.Error("non-focusable window should not get focus")
	}
}

func TestRemoveWindow(t *testing.T) {
	mgr := NewWindowManager()
	fw1 := NewFloatingWindow("w1", "W1", testWidget{}, 20, 10)
	fw2 := NewFloatingWindow("w2", "W2", testWidget{}, 20, 10)
	mgr.AddWindow(fw1)
	mgr.AddWindow(fw2)

	mgr.RemoveWindow("w1")

	if mgr.Window("w1") != nil {
		t.Error("removed window should not be found")
	}
	if mgr.Focused() != fw2 {
		t.Error("focus should move to remaining window")
	}
}

func TestRemoveWindowLastWindow(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 20, 10)
	mgr.AddWindow(fw)

	mgr.RemoveWindow("w")

	if mgr.Focused() != nil {
		t.Error("focus should be nil after removing last window")
	}
	if mgr.TopWindow() != nil {
		t.Error("TopWindow should be nil after removing last window")
	}
}

func TestRemoveWindowClearsDragState(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("drag-win", "Drag", testWidget{}, 20, 10)
	mgr.AddWindow(fw)

	mgr.activeDrag = fw
	mgr.RemoveWindow("drag-win")

	if mgr.activeDrag != nil {
		t.Error("drag state should be cleared")
	}
}

func TestRemoveWindowClearsResizeState(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("resize-win", "Resize", testWidget{}, 20, 10)
	mgr.AddWindow(fw)

	mgr.activeResize = fw
	mgr.RemoveWindow("resize-win")

	if mgr.activeResize != nil {
		t.Error("resize state should be cleared")
	}
}

func TestWindowByID(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("find-me", "Find", testWidget{}, 20, 10)
	mgr.AddWindow(fw)

	if mgr.Window("find-me") != fw {
		t.Error("Window() should return the window by ID")
	}
	if mgr.Window("nonexistent") != nil {
		t.Error("Window() should return nil for unknown ID")
	}
}

func TestWindowsReturnsCopy(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 20, 10)
	mgr.AddWindow(fw)

	wins := mgr.Windows()
	if len(wins) != 1 {
		t.Fatalf("Windows() count = %d, want 1", len(wins))
	}
	// Modifying the returned slice should not affect the manager
	wins[0] = nil
	if mgr.Windows()[0] == nil {
		t.Error("modifying returned slice should not affect manager")
	}
}

func TestTopWindow(t *testing.T) {
	mgr := NewWindowManager()
	fw1 := NewFloatingWindow("w1", "W1", testWidget{}, 20, 10)
	fw2 := NewFloatingWindow("w2", "W2", testWidget{}, 20, 10)
	mgr.AddWindow(fw1)
	mgr.AddWindow(fw2)

	if mgr.TopWindow() != fw2 {
		t.Error("TopWindow should be the last added window")
	}
}

func TestFocusWindow(t *testing.T) {
	mgr := NewWindowManager()
	fw1 := NewFloatingWindow("w1", "W1", testWidget{}, 20, 10)
	fw2 := NewFloatingWindow("w2", "W2", testWidget{}, 20, 10)
	mgr.AddWindow(fw1)
	mgr.AddWindow(fw2)

	mgr.FocusWindow("w1")
	if mgr.Focused() != fw1 {
		t.Error("Focused should be fw1")
	}
	if !fw1.Focused() {
		t.Error("fw1 should be focused")
	}
	if fw2.Focused() {
		t.Error("fw2 should not be focused")
	}
	if mgr.TopWindow() != fw1 {
		t.Error("TopWindow should be fw1 after focus")
	}
}

func TestFocusWindowNonExistent(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 20, 10)
	mgr.AddWindow(fw)

	mgr.FocusWindow("nope")
	if mgr.Focused() != fw {
		t.Error("focus should remain on existing window")
	}
}

func TestFocusNext(t *testing.T) {
	mgr := NewWindowManager()
	fw1 := NewFloatingWindow("w1", "W1", testWidget{}, 20, 10)
	fw2 := NewFloatingWindow("w2", "W2", testWidget{}, 20, 10)
	fw3 := NewFloatingWindow("w3", "W3", testWidget{}, 20, 10)
	mgr.AddWindow(fw1)
	mgr.AddWindow(fw2)
	mgr.AddWindow(fw3)

	// Focus is on fw3 (last added), zOrder = [fw1, fw2, fw3]
	mgr.FocusNext()
	if mgr.Focused() != fw1 {
		t.Error("FocusNext from last should go to first (cyclic)")
	}

	mgr.FocusNext()
	if mgr.Focused() != fw2 {
		t.Error("FocusNext should go from fw1 to fw2")
	}
}

func TestFocusPrev(t *testing.T) {
	mgr := NewWindowManager()
	fw1 := NewFloatingWindow("w1", "W1", testWidget{}, 20, 10)
	fw2 := NewFloatingWindow("w2", "W2", testWidget{}, 20, 10)
	fw3 := NewFloatingWindow("w3", "W3", testWidget{}, 20, 10)
	mgr.AddWindow(fw1)
	mgr.AddWindow(fw2)
	mgr.AddWindow(fw3)

	// Focus is on fw3, zOrder = [fw1, fw2, fw3]
	mgr.FocusPrev()
	// FocusWindow(fw2) moves fw2 to top: zOrder = [fw1, fw3, fw2]
	if mgr.Focused() != fw2 {
		t.Errorf("FocusPrev from last = %v, want fw2", mgr.Focused().ID)
	}

	mgr.FocusPrev()
	// FocusWindow(fw3) moves fw3 to top: zOrder = [fw1, fw2, fw3]
	if mgr.Focused() != fw3 {
		t.Errorf("FocusPrev second time = %v, want fw3", mgr.Focused().ID)
	}

	mgr.FocusPrev()
	// FocusWindow(fw2) moves fw2 to top: zOrder = [fw1, fw3, fw2]
	if mgr.Focused() != fw2 {
		t.Errorf("FocusPrev third time = %v, want fw2", mgr.Focused().ID)
	}
}

func TestFocusNextEmptyManager(t *testing.T) {
	mgr := NewWindowManager()
	mgr.FocusNext() // should not panic
}

func TestFocusPrevEmptyManager(t *testing.T) {
	mgr := NewWindowManager()
	mgr.FocusPrev() // should not panic
}

func TestBringToTop(t *testing.T) {
	mgr := NewWindowManager()
	fw1 := NewFloatingWindow("w1", "W1", testWidget{}, 20, 10)
	fw2 := NewFloatingWindow("w2", "W2", testWidget{}, 20, 10)
	mgr.AddWindow(fw1)
	mgr.AddWindow(fw2)

	mgr.BringToTop("w1")
	if mgr.TopWindow() != fw1 {
		t.Error("TopWindow should be fw1 after BringToTop")
	}
}

func TestSendToBottom(t *testing.T) {
	mgr := NewWindowManager()
	fw1 := NewFloatingWindow("w1", "W1", testWidget{}, 20, 10)
	fw2 := NewFloatingWindow("w2", "W2", testWidget{}, 20, 10)
	mgr.AddWindow(fw1)
	mgr.AddWindow(fw2)

	mgr.SendToBottom("w2")
	if mgr.TopWindow() != fw1 {
		t.Error("TopWindow should be fw1 after SendToBottom")
	}
}

func TestSetBackground(t *testing.T) {
	mgr := NewWindowManager()
	bg := testWidget{}
	mgr.SetBackground(bg)
}

func TestFocused(t *testing.T) {
	mgr := NewWindowManager()
	if mgr.Focused() != nil {
		t.Error("Focused() should be nil initially")
	}
	fw := NewFloatingWindow("w", "W", testWidget{}, 20, 10)
	mgr.AddWindow(fw)
	if mgr.Focused() != fw {
		t.Error("Focused() should return added window")
	}
}

func TestIsDragging(t *testing.T) {
	mgr := NewWindowManager()
	if mgr.IsDragging() {
		t.Error("should not be dragging initially")
	}

	fw := NewFloatingWindow("w", "W", testWidget{}, 20, 10)
	mgr.AddWindow(fw)
	mgr.activeDrag = fw
	if !mgr.IsDragging() {
		t.Error("should be dragging when activeDrag is set")
	}

	mgr.activeDrag = nil
	mgr.activeResize = fw
	if !mgr.IsDragging() {
		t.Error("should be dragging when activeResize is set")
	}
}

func TestNeedsRender(t *testing.T) {
	mgr := NewWindowManager()
	if mgr.NeedsRender() {
		t.Error("NeedsRender should be false initially")
	}
	mgr.needsRender = true
	if !mgr.NeedsRender() {
		t.Error("NeedsRender should be true")
	}
	mgr.ClearNeedsRender()
	if mgr.NeedsRender() {
		t.Error("NeedsRender should be false after ClearNeedsRender")
	}
}

func TestHandleMouseCloseButton(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 10, 5)
	fw.SetPosition(0, 0)
	mgr.AddWindow(fw)

	// Click on close button (x=6 for bordered window with w=10: w-4..w-2 = 6..8)
	msg := tea.MouseClickMsg{X: 6, Y: 0}
	clicked := mgr.HandleMouse(msg)
	if clicked != fw {
		t.Error("close button click should return the window")
	}
}

func TestHandleMouseTitleBarDrag(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 20, 10)
	fw.SetPosition(5, 5)
	mgr.AddWindow(fw)

	// Click on title bar (row 0, between border chars: x=1 to x=18)
	msg := tea.MouseClickMsg{X: 7, Y: 5}
	clicked := mgr.HandleMouse(msg)
	if clicked != fw {
		t.Error("title bar click should return the window")
	}
	if !fw.IsDragging() {
		t.Error("window should be in drag state")
	}
	if mgr.activeDrag != fw {
		t.Error("activeDrag should be set")
	}
}

func TestHandleMouseTitleActionCellsNeitherDragNorResize(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 20, 10)
	fw.SetPosition(5, 5)
	fw.SetTitleActionWidth(3)
	mgr.AddWindow(fw)

	// Reserved cells sit left of close: w=20, closable → local x 14..16.
	if !fw.IsOnTitleAction(5+14, 5) || !fw.IsOnTitleAction(5+16, 5) {
		t.Fatal("title-action region not where expected")
	}
	msg := tea.MouseClickMsg{X: 5 + 15, Y: 5}
	clicked := mgr.HandleMouse(msg)
	if clicked != fw {
		t.Fatal("title-action click should return the window for content dispatch")
	}
	if fw.IsDragging() || mgr.activeDrag == fw {
		t.Error("title-action click must not start a drag")
	}
	if fw.IsResizing() || mgr.activeResize == fw {
		t.Error("title-action click must not start a top-edge resize")
	}
	// Ordinary title cells still drag.
	msg = tea.MouseClickMsg{X: 5 + 7, Y: 5}
	if mgr.HandleMouse(msg) != fw || !fw.IsDragging() {
		t.Error("plain title bar click should still start a drag")
	}
}

func TestHandleMouseEdgeResize(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 20, 10)
	fw.SetPosition(5, 5)
	mgr.AddWindow(fw)

	// Click on bottom edge (y=9, x=10)
	msg := tea.MouseClickMsg{X: 10, Y: 14}
	clicked := mgr.HandleMouse(msg)
	if clicked != fw {
		t.Error("edge click should return the window")
	}
	if !fw.IsResizing() {
		t.Error("window should be in resize state")
	}
	if mgr.activeResize != fw {
		t.Error("activeResize should be set")
	}
}

func TestHandleMouseInsideFocuses(t *testing.T) {
	mgr := NewWindowManager()
	fw1 := NewFloatingWindow("w1", "W1", testWidget{}, 20, 10)
	fw2 := NewFloatingWindow("w2", "W2", testWidget{}, 20, 10)
	fw1.SetPosition(0, 0)
	fw2.SetPosition(5, 5)
	mgr.AddWindow(fw1)
	mgr.AddWindow(fw2)

	// Click inside fw1 (z-ordered below fw2, so click should hit fw2 first)
	// Click center of fw1, but fw2 is above
	msg := tea.MouseClickMsg{X: 10, Y: 10}
	clicked := mgr.HandleMouse(msg)
	_ = clicked
}

func TestHandleMouseOutsideUnfocuses(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 10, 5)
	fw.SetPosition(0, 0)
	mgr.AddWindow(fw)

	msg := tea.MouseClickMsg{X: 50, Y: 50}
	clicked := mgr.HandleMouse(msg)
	if clicked != nil {
		t.Error("click outside should return nil")
	}
	if mgr.Focused() != nil {
		t.Error("focus should be cleared")
	}
}

func TestHandleMouseBlockedByModal(t *testing.T) {
	mgr := NewWindowManager()
	fwNorm := NewFloatingWindow("norm", "Norm", testWidget{}, 20, 10)
	fwModal := NewFloatingWindow("modal", "Modal", testWidget{}, 20, 10,
		WithModal(true))
	// Position windows so the modal does NOT cover the normal window's area.
	fwNorm.SetPosition(0, 0)
	fwModal.SetPosition(25, 0)
	mgr.AddWindow(fwNorm)
	mgr.AddWindow(fwModal)

	// Click on the normal window (not on the modal). Should be blocked.
	msg := tea.MouseClickMsg{X: 10, Y: 5}
	clicked := mgr.HandleMouse(msg)
	if clicked != nil {
		t.Error("click on blocked window should return nil")
	}
}

func TestHandleMouseOnModalItself(t *testing.T) {
	mgr := NewWindowManager()
	fwNorm := NewFloatingWindow("norm", "Norm", testWidget{}, 20, 10)
	fwModal := NewFloatingWindow("modal", "Modal", testWidget{}, 20, 10,
		WithModal(true))
	fwNorm.SetPosition(0, 0)
	fwModal.SetPosition(0, 0)
	mgr.AddWindow(fwNorm)
	mgr.AddWindow(fwModal)

	// Click on the modal window itself (should be allowed)
	msg := tea.MouseClickMsg{X: 10, Y: 5}
	clicked := mgr.HandleMouse(msg)
	if clicked == nil {
		t.Error("click on modal itself should be handled")
	}
}

func TestHandleMouseDuringDrag(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 10, 5)
	fw.SetPosition(0, 0)
	mgr.AddWindow(fw)

	// Start drag on title bar (row 0, x=5)
	msg1 := tea.MouseClickMsg{X: 5, Y: 0}
	mgr.HandleMouse(msg1)

	// Second click during drag — should not start a new drag
	msg2 := tea.MouseClickMsg{X: 8, Y: 0}
	clicked := mgr.HandleMouse(msg2)
	if clicked != fw {
		t.Error("click during drag should return the dragged window")
	}
}

func TestHandleMouseDuringDragCloseButton(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 10, 5)
	fw.SetPosition(0, 0)
	mgr.AddWindow(fw)

	// Start drag
	msg1 := tea.MouseClickMsg{X: 5, Y: 0}
	mgr.HandleMouse(msg1)

	// Click on close button (x=6 for bordered w=10)
	msg2 := tea.MouseClickMsg{X: 6, Y: 0}
	clicked := mgr.HandleMouse(msg2)
	if clicked != fw {
		t.Error("close button during drag should return window")
	}
}

func TestHandleMouseNonFocusable(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("nf", "NF", testWidget{}, 10, 5,
		WithFocusable(false))
	fw.SetPosition(0, 0)
	mgr.AddWindow(fw)

	msg := tea.MouseClickMsg{X: 5, Y: 2}
	clicked := mgr.HandleMouse(msg)
	if clicked != nil {
		t.Error("click on non-focusable window should return nil")
	}
}

func TestHandleMouseMotionDrag(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 10, 5)
	fw.SetPosition(5, 5)
	mgr.AddWindow(fw)

	// Start drag
	clickMsg := tea.MouseClickMsg{X: 7, Y: 5}
	mgr.HandleMouse(clickMsg)

	// Move mouse
	motionMsg := tea.MouseMotionMsg{X: 10, Y: 8, Button: tea.MouseLeft}
	moved := mgr.HandleMouseMotion(motionMsg)
	if moved != fw {
		t.Error("HandleMouseMotion during drag should return window")
	}
	x, y := fw.Position()
	if x != 8 || y != 8 {
		t.Errorf("position after drag move = (%d,%d), want (8,8)", x, y)
	}
	if !mgr.NeedsRender() {
		t.Error("NeedsRender should be set after drag")
	}
}

func TestHandleMouseMotionResize(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 10, 6)
	fw.SetPosition(5, 5)
	mgr.AddWindow(fw)

	// Start resize on bottom edge
	clickMsg := tea.MouseClickMsg{X: 7, Y: 10}
	mgr.HandleMouse(clickMsg)

	// Move mouse down
	motionMsg := tea.MouseMotionMsg{X: 7, Y: 12, Button: tea.MouseLeft}
	moved := mgr.HandleMouseMotion(motionMsg)
	if moved != fw {
		t.Error("HandleMouseMotion during resize should return window")
	}
	_, h := fw.Size()
	if h != 8 {
		t.Errorf("height after resize = %d, want 8", h)
	}
}

func TestHandleMouseMotionNoInteraction(t *testing.T) {
	mgr := NewWindowManager()
	motionMsg := tea.MouseMotionMsg{X: 10, Y: 10}
	moved := mgr.HandleMouseMotion(motionMsg)
	if moved != nil {
		t.Error("HandleMouseMotion without interaction should return nil")
	}
}

func TestHandleMouseMotionDragNoButton(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 10, 5)
	fw.SetPosition(0, 0)
	mgr.AddWindow(fw)

	// Start drag
	clickMsg := tea.MouseClickMsg{X: 5, Y: 0}
	mgr.HandleMouse(clickMsg)

	// Motion with no button pressed (DECSET 1003 mode)
	motionMsg := tea.MouseMotionMsg{X: 10, Y: 10, Button: tea.MouseNone}
	moved := mgr.HandleMouseMotion(motionMsg)
	if moved != nil {
		t.Error("motion without button should return nil")
	}
	if fw.IsDragging() {
		t.Error("drag should be stopped")
	}
}

func TestHandleMouseMotionResizeNoButton(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 10, 6)
	fw.SetPosition(0, 0)
	mgr.AddWindow(fw)

	// Start resize on bottom edge
	clickMsg := tea.MouseClickMsg{X: 5, Y: 5}
	mgr.HandleMouse(clickMsg)

	motionMsg := tea.MouseMotionMsg{X: 10, Y: 10, Button: tea.MouseNone}
	moved := mgr.HandleMouseMotion(motionMsg)
	if moved != nil {
		t.Error("resize motion without button should return nil")
	}
	if fw.IsResizing() {
		t.Error("resize should be stopped")
	}
}

func TestHandleMouseRelease(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 10, 5)
	fw.SetPosition(0, 0)
	mgr.AddWindow(fw)

	// Start drag
	clickMsg := tea.MouseClickMsg{X: 5, Y: 0}
	mgr.HandleMouse(clickMsg)

	// Release
	releaseMsg := tea.MouseReleaseMsg{X: 5, Y: 0}
	released := mgr.HandleMouseRelease(releaseMsg)
	if released != fw {
		t.Error("HandleMouseRelease should return released window")
	}
	if fw.IsDragging() {
		t.Error("window should no longer be dragging")
	}
	if mgr.activeDrag != nil {
		t.Error("activeDrag should be nil")
	}
}

func TestHandleMouseReleaseEndsResize(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 10, 6)
	fw.SetPosition(0, 0)
	mgr.AddWindow(fw)

	// Start resize on bottom edge
	clickMsg := tea.MouseClickMsg{X: 5, Y: 5}
	mgr.HandleMouse(clickMsg)

	releaseMsg := tea.MouseReleaseMsg{X: 5, Y: 5}
	released := mgr.HandleMouseRelease(releaseMsg)
	if released != fw {
		t.Error("HandleMouseRelease should return resized window")
	}
	if fw.IsResizing() {
		t.Error("window should no longer be resizing")
	}
}

func TestHandleMouseReleaseNoop(t *testing.T) {
	mgr := NewWindowManager()
	releaseMsg := tea.MouseReleaseMsg{X: 0, Y: 0}
	released := mgr.HandleMouseRelease(releaseMsg)
	if released != nil {
		t.Error("release without drag should return nil")
	}
}

func TestHandleMouseWheel(t *testing.T) {
	mgr := NewWindowManager()
	fw1 := NewFloatingWindow("w1", "W1", testWidget{}, 10, 5)
	fw2 := NewFloatingWindow("w2", "W2", testWidget{}, 10, 5)
	fw1.SetPosition(0, 0)
	fw2.SetPosition(5, 5)
	mgr.AddWindow(fw1)
	mgr.AddWindow(fw2)

	// Wheel over fw2 (topmost)
	wheelMsg := tea.MouseWheelMsg{X: 7, Y: 7}
	hit := mgr.HandleMouseWheel(wheelMsg)
	if hit != fw2 {
		t.Error("wheel over fw2 should hit fw2")
	}

	// Wheel outside any window
	wheelMsg2 := tea.MouseWheelMsg{X: 50, Y: 50}
	hit2 := mgr.HandleMouseWheel(wheelMsg2)
	if hit2 != nil {
		t.Error("wheel outside should return nil")
	}
}

func TestHandleMouseWheelNonFocusable(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("nf", "NF", testWidget{}, 10, 5,
		WithFocusable(false))
	fw.SetPosition(0, 0)
	mgr.AddWindow(fw)

	wheelMsg := tea.MouseWheelMsg{X: 5, Y: 2}
	hit := mgr.HandleMouseWheel(wheelMsg)
	if hit != nil {
		t.Error("wheel on non-focusable should return nil")
	}
}

func TestIsBlockedByModal(t *testing.T) {
	mgr := NewWindowManager()
	fw1 := NewFloatingWindow("w1", "W1", testWidget{}, 20, 10)
	fw2 := NewFloatingWindow("w2", "W2", testWidget{}, 20, 10,
		WithModal(true))

	fw1.SetPosition(0, 0)
	fw2.SetPosition(0, 0)

	mgr.AddWindow(fw1)
	mgr.AddWindow(fw2)

	if !mgr.isBlockedByModal(fw1) {
		t.Error("fw1 should be blocked by modal fw2")
	}
	if mgr.isBlockedByModal(fw2) {
		t.Error("fw2 (modal itself) should not be blocked")
	}
}

func TestIsBlockedByModalInvisible(t *testing.T) {
	mgr := NewWindowManager()
	fw1 := NewFloatingWindow("w1", "W1", testWidget{}, 20, 10)
	fw2 := NewFloatingWindow("w2", "W2", testWidget{}, 20, 10,
		WithModal(true))
	fw2.SetVisible(false)

	fw1.SetPosition(0, 0)
	fw2.SetPosition(0, 0)

	mgr.AddWindow(fw1)
	mgr.AddWindow(fw2)

	if mgr.isBlockedByModal(fw1) {
		t.Error("fw1 should not be blocked by invisible modal")
	}
}

func TestIsBlockedByModalUnknown(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 10, 5)
	if mgr.isBlockedByModal(fw) {
		t.Error("unknown window should not be blocked")
	}
}

func TestRenderWithBackground(t *testing.T) {
	mgr := NewWindowManager()
	bg := testWidget{}
	mgr.SetBackground(bg)

	rootSurf := surface.NewSurface("root", 80, 24, cell.TransparentBlank())
	ctx := testContext()
	mgr.Render(rootSurf, ctx)
}

func TestRenderWithWindows(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 10, 5)
	fw.SetPosition(5, 5)
	mgr.AddWindow(fw)

	rootSurf := surface.NewSurface("root", 80, 24, cell.TransparentBlank())
	ctx := testContext()
	mgr.Render(rootSurf, ctx)

	// Window should have been rendered as a child of root
	children := rootSurf.Children()
	if len(children) == 0 {
		t.Fatal("no children added to root")
	}
	found := false
	for _, child := range children {
		if child == fw.Surface() {
			found = true
			break
		}
	}
	if !found {
		t.Error("window surface not found as child of root")
	}
}

func TestRenderInvisibleWindows(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 10, 5)
	fw.SetVisible(false)
	mgr.AddWindow(fw)

	rootSurf := surface.NewSurface("root", 80, 24, cell.TransparentBlank())
	ctx := testContext()
	mgr.Render(rootSurf, ctx)

	children := rootSurf.Children()
	for _, child := range children {
		if child == fw.Surface() {
			t.Error("invisible window should not be rendered")
		}
	}
}

func TestRenderMultipleWindowsZOrder(t *testing.T) {
	mgr := NewWindowManager()
	fw1 := NewFloatingWindow("w1", "W1", testWidget{}, 10, 5)
	fw2 := NewFloatingWindow("w2", "W2", testWidget{}, 10, 5)
	fw1.SetPosition(0, 0)
	fw2.SetPosition(5, 5)
	mgr.AddWindow(fw1)
	mgr.AddWindow(fw2)

	rootSurf := surface.NewSurface("root", 80, 24, cell.TransparentBlank())
	ctx := testContext()
	mgr.Render(rootSurf, ctx)

	children := rootSurf.Children()
	if len(children) < 2 {
		t.Fatal("expected at least 2 children")
	}
	// fw1 should have z-order 0, fw2 should have z-order 1
	surf1 := fw1.Surface()
	surf2 := fw2.Surface()
	for _, child := range children {
		if child == surf1 {
			if child != children[0] && child != children[1] {
				t.Error("fw1 surface not found at expected position")
			}
		}
		if child == surf2 {
			if child != children[0] && child != children[1] {
				t.Error("fw2 surface not found at expected position")
			}
		}
	}
}

func TestFocusWindowBringsToTop(t *testing.T) {
	mgr := NewWindowManager()
	fw1 := NewFloatingWindow("w1", "W1", testWidget{}, 20, 10)
	fw2 := NewFloatingWindow("w2", "W2", testWidget{}, 20, 10)
	fw3 := NewFloatingWindow("w3", "W3", testWidget{}, 20, 10)
	mgr.AddWindow(fw1)
	mgr.AddWindow(fw2)
	mgr.AddWindow(fw3)

	// zOrder should be [fw1, fw2, fw3]
	mgr.FocusWindow("w1")
	// zOrder should now be [fw2, fw3, fw1]
	if mgr.TopWindow() != fw1 {
		t.Error("FocusWindow should bring window to top")
	}
}

func TestHandleMouseInsideWindowFocuses(t *testing.T) {
	mgr := NewWindowManager()
	fw1 := NewFloatingWindow("w1", "W1", testWidget{}, 20, 10)
	fw2 := NewFloatingWindow("w2", "W2", testWidget{}, 20, 10)
	fw1.SetPosition(0, 0)
	fw2.SetPosition(0, 0)
	mgr.AddWindow(fw1)
	mgr.AddWindow(fw2)

	// fw2 is on top. Click inside fw2 but on content area (not title bar, not edge)
	msg := tea.MouseClickMsg{X: 5, Y: 3}
	clicked := mgr.HandleMouse(msg)
	if clicked != fw2 {
		t.Error("click inside fw2 should return fw2")
	}
	if mgr.Focused() != fw2 {
		t.Error("fw2 should be focused")
	}

	// Now click inside fw1 — but fw2 is on top, so fw2 should still be hit
	msg2 := tea.MouseClickMsg{X: 5, Y: 3}
	clicked2 := mgr.HandleMouse(msg2)
	if clicked2 != fw2 {
		t.Error("click should hit fw2 (z-order top)")
	}
}

func TestHandleMouseWindowFrameEdgeClick(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 10, 6)
	fw.SetPosition(0, 0)
	mgr.AddWindow(fw)

	// Click on bottom edge
	msg := tea.MouseClickMsg{X: 5, Y: 5}
	clicked := mgr.HandleMouse(msg)
	if clicked != fw {
		t.Error("edge click on WindowFrame should return window")
	}
	if !fw.IsResizing() {
		t.Error("should start resize on edge click")
	}
}

func TestHandleMouseResizeEdgeTracking(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 10, 6)
	fw.SetPosition(10, 10)
	mgr.AddWindow(fw)

	// Click on the bottom WindowFrame edge.
	msg := tea.MouseClickMsg{X: 14, Y: 15}
	clicked := mgr.HandleMouse(msg)
	if clicked != fw {
		t.Error("bottom edge click should return window")
	}
	if fw.ResizeEdge() != EdgeBottom {
		t.Errorf("resize edge = %v, want EdgeBottom", fw.ResizeEdge())
	}
}

func TestHandleMouseNotMovableNoDrag(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 20, 10,
		WithMovable(false))
	fw.SetPosition(0, 0)
	mgr.AddWindow(fw)

	msg := tea.MouseClickMsg{X: 5, Y: 0}
	clicked := mgr.HandleMouse(msg)
	if clicked != fw {
		t.Error("click should return the window")
	}
	if fw.IsDragging() {
		t.Error("non-movable window should not start drag")
	}
}

func TestHandleMouseNotResizableNoResize(t *testing.T) {
	mgr := NewWindowManager()
	fw := NewFloatingWindow("w", "W", testWidget{}, 20, 10,
		WithResizable(false))
	fw.SetPosition(0, 0)
	mgr.AddWindow(fw)

	msg := tea.MouseClickMsg{X: 5, Y: 9}
	clicked := mgr.HandleMouse(msg)
	if clicked != fw {
		t.Error("click should return the window (inside, not edge)")
	}
	if fw.IsResizing() {
		t.Error("non-resizable window should not start resize")
	}
}
