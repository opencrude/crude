package viewmodel

import (
	"fmt"
	"strings"
	"testing"

	"crude/tui/chatprojection"
	"crude/tui/screens/shellchat"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/tui/wm"

	tea "charm.land/bubbletea/v2"
)

func installWheelProjection(s *shellchat.Screen, rootID uint64, outerOffset int) *ui.ScrollModel {
	lines := make([]string, 100)
	mapping := make([]chatprojection.PaneLineInfo, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("conversation-row-%03d", i)
	}
	pane := ui.NewScrollModel()
	pane.SetContent(50)
	pane.SetViewport(4)
	pane.ScrollTo(20)
	for i := 60; i < 64; i++ {
		lines[i] = fmt.Sprintf("nested-body-%d", i-60)
		mapping[i] = chatprojection.PaneLineInfo{PaneID: "nested", LineIdx: i - 60}
	}
	s.SetMessages([]chatprojection.MessageRow{{ID: "transcript", Lines: lines}}, shellchat.PaintParams{SourceID: rootID, Generation: 1, StructureChanged: true, PaneLineMap: mapping, Compose: shellchat.ComposeParams{InnerW: 60, PadLR: 1, NestingLevel: 1, MainSBVisible: true, PaneLineMap: mapping, PaneGutters: map[string]shellchat.PaneGutterState{"nested": {Scroll: pane, TrackH: 4, Visible: true}}}})
	s.ScrollModel().SetAutoScroll(false)
	s.ScrollModel().ScrollTo(outerOffset)
	return pane
}

func TestNestedWheelJourneyRootWindowModalAndFocus(t *testing.T) {
	app := newFullTestApp(t)
	root := shellchat.NewWithID(app.Theme, "wheel/root")
	windowChat := shellchat.NewWithID(app.Theme, "wheel/window")
	rootPane := installWheelProjection(root, 10, 64)
	windowPane := installWheelProjection(windowChat, 20, 58)
	app.rootHost = ui.NewHost(app.Theme, root)
	app.rootHost.Resize(100, 30)
	app.rootHost.Layout()
	app.rootHost.Focus(root.ID())
	app.width, app.height = 100, 30
	app.Tree.Visible = false
	frame := ui.NewWindowFrameWidget(app.Theme, "wheel/frame", ui.FrameWindow, "Chat window", windowChat, nil, false, 60, 14)
	fw := wm.NewFloatingWindow("wheel-window", "Chat window", frame, 60, 14, wm.WithDecoration(true), wm.WithFocusable(true), wm.WithMovable(true))
	fw.SetPosition(35, 10)
	app.windowManager.AddWindow(fw)
	frame.Host().Resize(60, 14)
	frame.Host().Layout()
	frame.Host().Focus(windowChat.ID())
	for i := 0; i < 8; i++ {
		app.dispatchMouseWheel(tea.MouseWheelMsg{X: 5, Y: 2, Button: tea.MouseWheelUp})
		app.rootHost.MarkLayout()
		app.rootHost.Layout()
	}
	if rootPane.Offset() != 20 || root.ScrollModel().Offset() >= 60 {
		t.Fatal("root conversation trapped by nested pane")
	}
	if windowPane.Offset() != 20 {
		t.Fatal("root wheel mutated unrelated window")
	}
	if app.windowManager.Focused() != fw || app.rootHost.Focused() != root.ID() {
		t.Fatal("wheel stole keyboard focus")
	}
	r, _ := frame.Host().ScreenRect(windowChat.ID())
	x, y := fw.Position()
	before := windowChat.ScrollModel().Offset()
	app.dispatchMouseWheel(tea.MouseWheelMsg{X: x + r.X + 5, Y: y + r.Y + 3, Button: tea.MouseWheelUp})
	if windowPane.Offset() != 19 || windowChat.ScrollModel().Offset() != before {
		t.Fatal("direct unfocused-window pane scrolling lost")
	}
	buf := surface.NewCellBuffer(60, 14)
	frame.Host().Paint(buf)
	if !strings.Contains(bufferPlain(buf), "nested-body") {
		t.Fatal("nested window no longer paints its content")
	}
	// An independent modal outside the wheel point shields both routes.
	modalBody := ui.NewLabel("wheel/modal", "Modal", app.Theme)
	modalFrame := ui.NewWindowFrameWidget(app.Theme, "wheel/modal-frame", ui.FrameDialog, "Modal", modalBody, nil, false, 20, 5)
	modal := wm.NewFloatingWindow("wheel-modal", "Modal", modalFrame, 20, 5, wm.WithModal(true))
	modal.SetPosition(70, 0)
	app.windowManager.AddWindow(modal)
	rootBefore, childBefore := root.ScrollModel().Offset(), windowPane.Offset()
	app.dispatchMouseWheel(tea.MouseWheelMsg{X: 5, Y: 2, Button: tea.MouseWheelUp})
	app.dispatchMouseWheel(tea.MouseWheelMsg{X: x + r.X + 5, Y: y + r.Y + 3, Button: tea.MouseWheelUp})
	if root.ScrollModel().Offset() != rootBefore || windowPane.Offset() != childBefore {
		t.Fatal("wheel crossed modal miss boundary")
	}
	app.windowManager.RemoveWindow(modal.ID)
	app.dispatchMouseWheel(tea.MouseWheelMsg{X: 5, Y: 2, Button: tea.MouseWheelUp})
	if root.ScrollModel().Offset() >= rootBefore {
		t.Fatal("modal close did not restore fresh outer route")
	}
}
