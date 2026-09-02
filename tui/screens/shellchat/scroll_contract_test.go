package shellchat

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/tui/ui"
)

// TestShellChatScrollHostContractTable documents keyboard/wheel routing through
// the shared ScrollModel and single native scrollbar path on shellchat.Screen.
func TestShellChatScrollHostContractTable(t *testing.T) {
	th := theme.DefaultDark()
	lines := make([]string, 15)
	for i := range lines {
		lines[i] = "scroll line"
	}
	rows := []chatprojection.MessageRow{{ID: "m1", Lines: lines}}

	s := New(th)
	s.SetMessages(rows, PaintParams{
		Compose: ComposeParams{
			InnerW:        30,
			PadLR:         th.Spacing.PadLR,
			Theme:         th,
			MainSBVisible: true,
		},
	})
	s.SyncScrollMetrics(5, len(lines))
	host := ui.NewHost(th, s)
	w := ContentSurfaceWidth(30, th.Spacing.PadLR, true)
	host.Resize(w, 5)
	host.Layout()

	scroll := s.ScrollModel()
	scroll.StickToBottom()
	if !scroll.AutoScroll() {
		t.Fatal("setup: auto-scroll should be armed")
	}

	host.Handle(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if scroll.AutoScroll() || scroll.Offset() >= scroll.MaxOffset() {
		t.Fatal("PgUp should disarm and scroll up")
	}

	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnd})
	if !scroll.AutoScroll() || scroll.Offset() != scroll.MaxOffset() {
		t.Fatal("End should stick to bottom")
	}

	host.Handle(tea.MouseWheelMsg{X: th.Spacing.PadLR + 2, Y: 2, Button: tea.MouseWheelUp})
	if scroll.AutoScroll() {
		t.Fatal("wheel up should disarm auto-scroll")
	}

	for i := 0; i < 20 && scroll.Offset() < scroll.MaxOffset(); i++ {
		host.Handle(tea.MouseWheelMsg{X: th.Spacing.PadLR + 2, Y: 2, Button: tea.MouseWheelDown})
	}
	if !scroll.AutoScroll() {
		t.Fatal("wheel down at bottom should re-arm auto-scroll")
	}

	sbX := s.scrollbar.Bounds().X
	thumbTop, _ := scroll.ThumbBounds(s.scrollbar.Bounds().H)
	host.Handle(tea.MouseClickMsg{X: sbX, Y: thumbTop, Button: tea.MouseLeft})
	if !s.IsScrollbarDragging() {
		t.Fatal("scrollbar click should start drag on shared ScrollModel path")
	}
	before := scroll.Offset()
	host.Handle(tea.MouseMotionMsg{X: sbX, Y: 0, Button: tea.MouseLeft})
	if scroll.Offset() == before && scroll.MaxOffset() > 1 {
		t.Fatal("scrollbar drag should move shared ScrollModel offset")
	}
	host.Handle(tea.MouseReleaseMsg{X: sbX, Y: 3, Button: tea.MouseLeft})
	if s.IsScrollbarDragging() {
		t.Fatal("release should end scrollbar drag")
	}
}

// TestShellChatDisarmedOffsetPreservationContract verifies paint-only VirtualList
// sync does not reset user scroll when auto-scroll is disarmed.
func TestShellChatDisarmedOffsetPreservationContract(t *testing.T) {
	th := theme.DefaultDark()
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "scroll line"
	}
	rows := []chatprojection.MessageRow{{ID: "m1", Lines: lines}}

	s := New(th)
	s.SetMessages(rows, PaintParams{
		Compose: ComposeParams{
			InnerW:        30,
			PadLR:         th.Spacing.PadLR,
			Theme:         th,
			MainSBVisible: true,
		},
	})
	host := ui.NewHost(th, s)
	w := ContentSurfaceWidth(30, th.Spacing.PadLR, true)
	host.Resize(w, 5)
	host.Layout()

	scroll := s.ScrollModel()
	scroll.StickToBottom()
	host.Handle(tea.MouseWheelMsg{X: th.Spacing.PadLR + 2, Y: 2, Button: tea.MouseWheelUp})
	wantOff := scroll.Offset()
	if scroll.AutoScroll() || wantOff == 0 || wantOff >= scroll.MaxOffset() {
		t.Fatalf("setup: need disarmed mid-scroll offset, off=%d max=%d auto=%v", wantOff, scroll.MaxOffset(), scroll.AutoScroll())
	}

	grown := append(lines, "appended line")
	s.SetMessages([]chatprojection.MessageRow{{ID: "m1", Lines: grown}}, PaintParams{
		Compose: ComposeParams{
			InnerW:        30,
			PadLR:         th.Spacing.PadLR,
			Theme:         th,
			MainSBVisible: true,
		},
	})
	host.Layout()
	if scroll.AutoScroll() {
		t.Fatal("append while disarmed must not re-arm auto-scroll")
	}
	if scroll.Offset() != wantOff {
		t.Fatalf("disarmed append should preserve offset=%d got=%d max=%d", wantOff, scroll.Offset(), scroll.MaxOffset())
	}

	scroll.StickToBottom()
	s.SetMessages([]chatprojection.MessageRow{{ID: "m1", Lines: append(grown, "another")}}, PaintParams{
		Compose: ComposeParams{
			InnerW:        30,
			PadLR:         th.Spacing.PadLR,
			Theme:         th,
			MainSBVisible: true,
		},
	})
	host.Layout()
	if scroll.Offset() != scroll.MaxOffset() || !scroll.AutoScroll() {
		t.Fatalf("armed append should stick to bottom: off=%d max=%d auto=%v", scroll.Offset(), scroll.MaxOffset(), scroll.AutoScroll())
	}
}

// TestShellChatViewportResizePreservesDisarmedOffset verifies shrink/resize does
// not reset user scroll when auto-scroll is disarmed (production paint path).
func TestShellChatViewportResizePreservesDisarmedOffset(t *testing.T) {
	th := theme.DefaultDark()
	lines := make([]string, 24)
	for i := range lines {
		lines[i] = "scroll line"
	}
	rows := []chatprojection.MessageRow{{ID: "m1", Lines: lines}}

	s := New(th)
	s.SetMessages(rows, PaintParams{
		Compose: ComposeParams{
			InnerW:        30,
			PadLR:         th.Spacing.PadLR,
			Theme:         th,
			MainSBVisible: true,
		},
	})
	host := ui.NewHost(th, s)
	w := ContentSurfaceWidth(30, th.Spacing.PadLR, true)
	host.Resize(w, 8)
	host.Layout()

	scroll := s.ScrollModel()
	scroll.StickToBottom()
	host.Handle(tea.MouseWheelMsg{X: th.Spacing.PadLR + 2, Y: 2, Button: tea.MouseWheelUp})
	wantOff := scroll.Offset()
	if scroll.AutoScroll() || wantOff == 0 {
		t.Fatalf("setup: need disarmed offset, off=%d auto=%v", wantOff, scroll.AutoScroll())
	}

	s.SyncScrollMetrics(5, len(lines))
	host.Resize(w, 5)
	host.Layout()
	if scroll.AutoScroll() {
		t.Fatal("viewport shrink while disarmed must not re-arm auto-scroll")
	}
	if scroll.Offset() != wantOff {
		t.Fatalf("viewport shrink should preserve offset=%d got=%d max=%d", wantOff, scroll.Offset(), scroll.MaxOffset())
	}
}

// TestShellChatContentShrinkPreservesDisarmedOffset verifies content shrink on
// the production SetMessages path preserves disarmed scroll offset.
func TestShellChatContentShrinkPreservesDisarmedOffset(t *testing.T) {
	th := theme.DefaultDark()
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = "scroll line"
	}
	s := New(th)
	s.SetMessages([]chatprojection.MessageRow{{ID: "m1", Lines: lines}}, PaintParams{
		Compose: ComposeParams{
			InnerW:        30,
			PadLR:         th.Spacing.PadLR,
			Theme:         th,
			MainSBVisible: true,
		},
	})
	host := ui.NewHost(th, s)
	w := ContentSurfaceWidth(30, th.Spacing.PadLR, true)
	host.Resize(w, 6)
	host.Layout()

	scroll := s.ScrollModel()
	scroll.StickToBottom()
	host.Handle(tea.MouseWheelMsg{X: th.Spacing.PadLR + 2, Y: 2, Button: tea.MouseWheelUp})
	wantOff := scroll.Offset()
	if scroll.AutoScroll() {
		t.Fatal("setup: need disarmed scroll")
	}

	shorter := lines[:20]
	s.SetMessages([]chatprojection.MessageRow{{ID: "m1", Lines: shorter}}, PaintParams{
		Compose: ComposeParams{
			InnerW:        30,
			PadLR:         th.Spacing.PadLR,
			Theme:         th,
			MainSBVisible: true,
		},
	})
	s.SyncScrollMetrics(6, len(shorter))
	host.Layout()
	if scroll.AutoScroll() {
		t.Fatal("content shrink while disarmed must not re-arm auto-scroll")
	}
	if scroll.Offset() > scroll.MaxOffset() {
		t.Fatalf("content shrink should clamp to max=%d got=%d", scroll.MaxOffset(), scroll.Offset())
	}
	if wantOff <= scroll.MaxOffset() && scroll.Offset() != wantOff {
		t.Fatalf("content shrink should preserve in-range offset=%d got=%d max=%d", wantOff, scroll.Offset(), scroll.MaxOffset())
	}
}
