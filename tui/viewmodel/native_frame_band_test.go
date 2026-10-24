package viewmodel

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"

	"crude/terminalvt"
	"crude/tui/ui"
	"crude/workspace"
)

// Compare terminal escape output, not just UV's intended grid: terminal scrolling
// can modify rows which were unchanged in the application's latest cell frame.
func TestNativeFrameTerminalDockBand(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	app.Update(tea.WindowSizeMsg{Width: 160, Height: 45})
	app.Tree.Visible = false
	for _, name := range []string{"left.txt", "right.txt"} {
		if err := os.WriteFile(filepath.Join(ws.RootPath, name), []byte("CONTROL READY\n\nConnected editor and split\nRetained PTY\nSaved workspace settings\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	openEditorTabs(t, app, "left.txt")
	app.paintShellFrame()
	app.splitMainGroup(ui.Horizontal, false, 0)
	openEditorTabs(t, app, "right.txt")

	engine, err := terminalvt.New(terminalvt.Options{Cols: 160, Rows: 45})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	var output bytes.Buffer
	renderer := uv.NewTerminalRenderer(&output, []string{"TERM=alacritty"})
	renderer.SetColorProfile(colorprofile.TrueColor)
	renderer.SetFullscreen(true)
	renderer.SetRelativeCursor(false)
	renderer.SetScrollOptim(true)
	renderer.SetTabStops(-1)
	frame := uv.NewScreenBuffer(160, 45)
	present := func(stage string) {
		t.Helper()
		app.forceFrame()
		view := app.View()
		assertRegionFrame(t, app, view)
		want := uv.NewScreenBuffer(160, 45)
		view.Surface.Draw(want, want.Bounds())
		frame.Replace(want.Buffer)
		renderer.Render(frame.RenderBuffer)
		if err := renderer.Flush(); err != nil {
			t.Fatal(err)
		}
		wire := append([]byte(nil), output.Bytes()...)
		engine.Feed(wire)
		output.Reset()
		got, err := engine.Rows(terminalvt.RowRequest{Start: -1})
		if err != nil {
			t.Fatal(err)
		}
		for y, row := range got.Cells {
			for x, actual := range row {
				expected := want.CellAt(x, y)
				if strings.TrimSpace(actual.Text) != strings.TrimSpace(expected.Content) || !nativeFrameColorEqual(actual.BG, expected.Style.Bg) {
					t.Fatalf("%s native cell (%d,%d): got %+v want %+v; terminal bytes %q", stage, x, y, actual, expected, wire)
				}
			}
		}
	}
	present("editor")
	info := workspace.TerminalInfo{ID: "native-band-terminal", Title: "Terminal 1", Alive: true}
	for i, rows := range []int{12, 8, 0, 8, 0} {
		if rows == 0 {
			app.termDock.ClosePanel()
		} else {
			app.dockTerminalInfo(info)
			app.termDockHeight = rows
		}
		app.rootHost.MarkPaint()
		present(fmt.Sprintf("dock transition %d rows=%d", i, rows))
	}
}

func nativeFrameColorEqual(actual terminalvt.Color, expected uv.Color) bool {
	if expected == 0 {
		return !actual.Explicit
	}
	r, g, b, _ := expected.RGBA()
	return actual.Explicit && actual.R == uint8(r>>8) && actual.G == uint8(g>>8) && actual.B == uint8(b>>8)
}
