package viewmodel

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"crude/tui/rendering"
	"crude/tui/screens/termdock"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"
)

func TestTerminalDockShortWindowMatrix(t *testing.T) {
	app := newTestApp(t)
	app.width = 80
	screen := app.ensureTermScreen("compact-matrix", "compact")
	app.termDock.Mount("compact-matrix", screen)
	t.Cleanup(func() { app.dropTermScreen("compact-matrix") })

	// The dock hides when the usable band (between the header and the status
	// bar) cannot render a terminal. Derive the boundary from the current
	// frame geometry instead of hardcoding the legacy row offsets.
	minSafeHeight := app.chromeBottomRows() + app.termDockTopRow() + termdock.MinRenderableHeight
	for height := minSafeHeight - 1; height <= minSafeHeight+6; height++ {
		t.Run(strconv.Itoa(height), func(t *testing.T) {
			app.height = height
			app.termDockHeight = defaultTermDockHeight
			lay := app.computeContentLayout()

			if height < minSafeHeight {
				if !lay.TermDockRect.Empty() || app.termDockVisible() {
					t.Fatalf("height=%d: unsafe dock remained visible: %v", height, lay.TermDockRect)
				}
				return
			}
			dock := lay.TermDockRect
			if dock.Empty() {
				t.Fatalf("height=%d: usable dock was hidden", height)
			}
			if dock.Y < app.termDockTopRow() {
				t.Fatalf("height=%d: dock overlaps header: %v", height, dock)
			}
			if dock.Y+dock.H != height-app.chromeBottomRows() {
				t.Fatalf("height=%d: dock=%v does not end above status", height, dock)
			}
			if dock.H < termdock.MinRenderableHeight {
				t.Fatalf(
					"height=%d: dock height=%d leaves no usable terminal body; minimum=%d",
					height,
					dock.H,
					termdock.MinRenderableHeight,
				)
			}
			app.termDock.Layout(ui.LayoutContext{}, ui.Rect{W: dock.W, H: dock.H})
			if body := app.termDock.Active().Bounds(); body.H < workspace.MinTerminalRows {
				t.Fatalf("height=%d: terminal body=%v", height, body)
			}
		})
	}
}

func TestTerminalOutputMaterializesScreenBeforeExplicitRegistration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newTestApp(t)
	mgr := app.termMgr()
	info, err := mgr.Create(workspace.TerminalCreateOpts{
		Command: "/bin/cat",
		CWD:     t.TempDir(),
		Cols:    40,
		Rows:    10,
		Title:   "cat",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		app.dropTermScreen(info.ID)
		mgr.Close(info.ID)
	})
	if app.termScreens[info.ID] != nil {
		t.Fatal("fixture unexpectedly registered a terminal screen")
	}

	_ = app.handleTerminalOutput(workspace.TerminalOutputMsg{
		ID:   info.ID,
		Data: []byte("startup banner\r\n\x1b]2;ready\x07"),
	})
	screen := app.termScreens[info.ID]
	if screen == nil {
		t.Fatal("first output was dropped instead of materializing the screen")
	}
	if screen.Title() != "ready" {
		t.Fatalf("first output was not fed to the emulator: title=%q want ready", screen.Title())
	}
}

func TestFloatingTerminalBellUsesFrameAttentionIcon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	mgr := app.termMgr()
	info, err := mgr.Create(workspace.TerminalCreateOpts{
		Command: "/bin/cat",
		CWD:     t.TempDir(),
		Cols:    40,
		Rows:    10,
		Title:   "cat",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		app.discardTerminalUI()
		mgr.Close(info.ID)
	})

	app.promoteTerminal(info.ID)
	fw := app.overlayHost.Window(ui.TerminalOverlayID(info.ID))
	screen := app.termScreens[info.ID]
	if fw == nil || screen == nil {
		t.Fatal("floating terminal projection missing")
	}
	app.windowManager.ClearFocus()
	app.markTerminalBell(info.ID)
	if !screen.HasAttention() {
		t.Fatal("unfocused floating BEL did not record attention")
	}
	if !floatingTerminalFrameHasAttention(t, fw) {
		t.Fatal("unfocused floating BEL was not exposed in WindowFrame chrome")
	}

	app.windowManager.FocusWindow(fw.ID)
	app.tickTerminalCursors(time.Now())
	if screen.HasAttention() {
		t.Fatal("focusing the floating terminal did not clear attention")
	}
	if floatingTerminalFrameHasAttention(t, fw) {
		t.Fatal("focused floating terminal kept a stale attention icon")
	}
}

func TestTerminalManagerErrorsReachStatusBar(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*App)
		want string
	}{
		{
			name: "write",
			run:  func(app *App) { app.writeTerminal("missing", []byte("x")) },
			want: "Terminal write failed",
		},
		{
			name: "resize",
			run:  func(app *App) { _ = app.resizeTerminal("missing", 1, 1) },
			want: "Terminal resize failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp(t)
			app.StatusBar.SetWidth(120)
			tc.run(app)
			got := rendering.StripAnsi(app.StatusBar.View())
			if !strings.Contains(got, tc.want) {
				t.Fatalf("status=%q want substring %q", got, tc.want)
			}
		})
	}
}

func floatingTerminalFrameHasAttention(t *testing.T, window *wm.FloatingWindow) bool {
	t.Helper()
	frame, ok := window.Content.(*ui.WindowFrameWidget)
	if !ok || frame.Host() == nil {
		t.Fatalf("floating content has type %T", window.Content)
	}
	w, h := window.Size()
	frame.Host().Resize(w, h)
	buf := surface.NewCellBuffer(w, h)
	frame.Host().Paint(buf)
	for x := 0; x < w; x++ {
		if buf.Get(x, 0).Char == '•' {
			return true
		}
	}
	return false
}
