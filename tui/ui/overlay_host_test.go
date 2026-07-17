package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/wm"
)

func newTestOverlayHost(t *testing.T) (*OverlayHost, *wm.WindowManager) {
	t.Helper()
	mgr := wm.NewWindowManager()
	return NewOverlayHost(mgr), mgr
}

func newStubWindow(id string, w, h int, opts ...wm.WindowOption) *wm.FloatingWindow {
	content := rendering.NewANSIAdapter(func() string { return "" })
	fw := wm.NewFloatingWindow(id, "", content, w, h, opts...)
	return fw
}

func TestOverlayHost_OpenAndClose(t *testing.T) {
	host, _ := newTestOverlayHost(t)
	closed := false
	fw := newStubWindow("test", 10, 3,
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideCloseRoute),
	)
	host.Open(OverlayContextMenu, fw, PopoverPolicy(true), func() { closed = true })
	if !host.IsOpen(OverlayContextMenu) {
		t.Fatal("expected overlay open")
	}
	if host.Window(OverlayContextMenu) == nil {
		t.Fatal("expected window")
	}
	host.Close(OverlayContextMenu)
	if host.IsOpen(OverlayContextMenu) {
		t.Fatal("expected overlay closed")
	}
	if !closed {
		t.Fatal("OnClose not called")
	}
}

func TestOverlayHost_CloseIsReentrantSafe(t *testing.T) {
	host, _ := newTestOverlayHost(t)
	calls := 0
	fw := newStubWindow(string(OverlayFilePicker), 10, 3, wm.WithFocusable(true))
	host.Open(OverlayFilePicker, fw, ModalDialogPolicy(), func() {
		calls++
		host.Close(OverlayFilePicker)
	})
	host.Close(OverlayFilePicker)
	if calls != 1 {
		t.Fatalf("reentrant OnClose calls=%d, want 1", calls)
	}
}

func TestOverlayHost_OpenReplacesExisting(t *testing.T) {
	host, _ := newTestOverlayHost(t)
	calls := 0
	fw1 := newStubWindow("test", 10, 3, wm.WithFocusable(true))
	fw2 := newStubWindow("test", 10, 3, wm.WithFocusable(true))
	host.Open(OverlayContextMenu, fw1, PopoverPolicy(true), func() { calls++ })
	host.Open(OverlayContextMenu, fw2, PopoverPolicy(true), func() { calls++ })
	if calls != 1 {
		t.Fatalf("expected 1 close call on replace, got %d", calls)
	}
	if !host.IsOpen(OverlayContextMenu) {
		t.Fatal("expected new overlay open")
	}
}

func TestOverlayHost_ClickOutsideIgnoreSuppresses(t *testing.T) {
	host, _ := newTestOverlayHost(t)
	fw := newStubWindow("test", 10, 3,
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	host.Open(OverlayDirtyClose, fw, ModalDialogPolicy(), nil)
	// Click at (50,50) — far outside the 10x3 window at (0,0).
	suppressed := host.HandleClickOutside(50, 50)
	if !suppressed {
		t.Fatal("modal overlay should suppress outside click")
	}
	if !host.IsOpen(OverlayDirtyClose) {
		t.Fatal("modal overlay should remain open")
	}
}

func TestOverlayHost_ClickOutsideClosesRoute(t *testing.T) {
	host, _ := newTestOverlayHost(t)
	fw := newStubWindow("test", 10, 3,
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideCloseRoute),
	)
	host.Open(OverlayContextMenu, fw, PopoverPolicy(true), nil)
	suppressed := host.HandleClickOutside(50, 50)
	if suppressed {
		t.Fatal("popover should NOT suppress outside click")
	}
	if host.IsOpen(OverlayContextMenu) {
		t.Fatal("popover should be closed by outside click")
	}
}

func TestOverlayHost_ClickInsideDoesNotClose(t *testing.T) {
	host, _ := newTestOverlayHost(t)
	fw := newStubWindow("test", 10, 3,
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideCloseRoute),
	)
	fw.SetPosition(0, 0)
	host.Open(OverlayContextMenu, fw, PopoverPolicy(true), nil)
	// Click inside the window (5,1).
	suppressed := host.HandleClickOutside(5, 1)
	if suppressed {
		t.Fatal("inside click should not be suppressed")
	}
	if !host.IsOpen(OverlayContextMenu) {
		t.Fatal("overlay should remain open for inside click")
	}
}

func TestOverlayHost_KeepRouteStaysOpen(t *testing.T) {
	host, _ := newTestOverlayHost(t)
	fw := newStubWindow("test", 10, 3,
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	host.Open(OverlayFindReplace, fw, DefaultOverlayPolicy(), nil)
	suppressed := host.HandleClickOutside(50, 50)
	if suppressed {
		t.Fatal("keep-route should not suppress")
	}
	if !host.IsOpen(OverlayFindReplace) {
		t.Fatal("keep-route overlay should remain open")
	}
}

func TestOverlayHost_CloseAll(t *testing.T) {
	host, _ := newTestOverlayHost(t)
	calls := 0
	fw1 := newStubWindow("a", 10, 3, wm.WithFocusable(true))
	fw2 := newStubWindow("b", 10, 3, wm.WithFocusable(true))
	host.Open(OverlayContextMenu, fw1, PopoverPolicy(true), func() { calls++ })
	host.Open(OverlayFindReplace, fw2, DefaultOverlayPolicy(), func() { calls++ })
	host.CloseAll()
	if calls != 2 {
		t.Fatalf("expected 2 close calls, got %d", calls)
	}
	if host.IsOpen(OverlayContextMenu) || host.IsOpen(OverlayFindReplace) {
		t.Fatal("all overlays should be closed")
	}
}

func TestOverlayHost_ContentHandlerDispatch(t *testing.T) {
	host, _ := newTestOverlayHost(t)
	fw := newStubWindow(string(OverlayContextMenu), 10, 3, wm.WithFocusable(true))
	fw.SetPosition(2, 2)
	host.Open(OverlayContextMenu, fw, PopoverPolicy(true), nil)

	called := false
	host.RegisterContentHandler(OverlayContextMenu, func(h *OverlayHost, fw *wm.FloatingWindow, lx, ly int, msg tea.Msg) tea.Cmd {
		if lx != 3 || ly != 1 {
			t.Errorf("expected local (3,1), got (%d,%d)", lx, ly)
		}
		called = true
		return nil
	})
	host.DispatchContentClick(fw, tea.MouseClickMsg{X: 5, Y: 3, Button: tea.MouseLeft})
	if !called {
		t.Fatal("content handler not dispatched")
	}
}

func TestOverlayHost_HoverHandlerDispatch(t *testing.T) {
	host, _ := newTestOverlayHost(t)
	fw := newStubWindow(string(OverlayContextMenu), 10, 3, wm.WithFocusable(true))
	fw.SetPosition(0, 0)
	host.Open(OverlayContextMenu, fw, PopoverPolicy(true), nil)

	called := false
	host.RegisterHoverHandler(OverlayContextMenu, func(h *OverlayHost, fw *wm.FloatingWindow, lx, ly int, button tea.MouseButton) {
		called = true
	})
	host.DispatchContentHover(fw, 3, 1, tea.MouseNone)
	if !called {
		t.Fatal("hover handler not dispatched")
	}
}

func TestOverlayHost_Top(t *testing.T) {
	host, _ := newTestOverlayHost(t)
	fw1 := newStubWindow("a", 10, 3, wm.WithFocusable(true))
	fw2 := newStubWindow("b", 10, 3, wm.WithFocusable(true))
	host.Open(OverlayContextMenu, fw1, PopoverPolicy(true), nil)
	host.Open(OverlayFindReplace, fw2, DefaultOverlayPolicy(), nil)
	top := host.Top()
	if top == nil || top.ID != OverlayFindReplace {
		t.Fatalf("expected top=FindReplace, got %+v", top)
	}
	host.Close(OverlayFindReplace)
	top = host.Top()
	if top == nil || top.ID != OverlayContextMenu {
		t.Fatalf("expected top=ContextMenu after close, got %+v", top)
	}
}

func TestOverlayHost_GetNil(t *testing.T) {
	var host *OverlayHost
	if host.Get(OverlayContextMenu) != nil {
		t.Fatal("nil host should return nil")
	}
	if host.IsOpen(OverlayContextMenu) {
		t.Fatal("nil host should not be open")
	}
	if host.HandleClickOutside(0, 0) {
		t.Fatal("nil host should not suppress")
	}
	host.Close(OverlayContextMenu) // should not panic
	host.CloseAll()                // should not panic
}
