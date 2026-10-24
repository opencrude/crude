package viewmodel

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"crude/shellport"
	"crude/terminalvt"
	"crude/tui/cell"
	"crude/tui/compositor"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/screens/shellterm"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func regionTerminalFixture(t testing.TB, messages int) (*App, *streamTestService, workspace.TerminalInfo) {
	t.Helper()
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	// Zero start makes the header's diagnostic elapsed clock deterministic
	// across the independent paint (the header intentionally uses wall time).
	wsOf(app).StartTime = time.Time{}
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	app.AddOrchestrator("region-root")
	model := filetree.NewFileTreeModel(t.TempDir())
	app.Tree = NewTreeState(model, 24)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(model)
	for _, msg := range seedBudgetSession(messages, 0) {
		app.appendActiveMessage(msg)
	}
	app.paintShellFrame()
	settleHistory(t, app)
	service := &streamTestService{}
	app.ws = &streamTestWorkspace{Workspace: app.ws, terminal: service}
	info := workspace.TerminalInfo{ID: "region-terminal", Title: "Region terminal", Alive: true}
	app.dockTerminalInfo(info)
	app.forceFrame()
	app.View()
	return app, service, info
}

func regionTerminalFrame(service *streamTestService, info workspace.TerminalInfo, n int) shellport.TerminalFrameMsg {
	text := fmt.Sprintf("FRAME %04d", n)
	row := make([]terminalvt.Cell, 40)
	for i := range row {
		row[i] = terminalvt.Cell{Text: " ", Width: 1}
	}
	for i, c := range text {
		row[i].Text = string(c)
	}
	return shellport.TerminalFrameMsg{Service: service, ID: info.ID, Projection: workspace.TerminalProjection{
		Info: info, Generation: uint64(n + 1), Grid: terminalvt.Projection{Cols: 40, GridRows: 4, Total: 4, Cells: [][]terminalvt.Cell{row, row, row, row}, HistoryEpoch: 1},
	}}
}

// assertRegionFrame compares every direct output cell to an independently
// cleared and unconditionally painted Host, composed with the real overlays.
// It never takes the retained mainSurf as the reference source.
func assertRegionFrame(t *testing.T, app *App, view tea.View) {
	t.Helper()
	w, h := app.width, app.height
	got := uv.NewScreenBuffer(w, h)
	view.Surface.Draw(got, got.Bounds())
	bg := cell.BlankCell(rendering.ThemeColorToCell(app.Theme.Colors.Bg))
	refSurf := surface.NewSurface("reference", w, h, bg)
	host := ui.NewHost(app.Theme, app.shell)
	host.Resize(w, h)
	host.Layout()
	host.Focus(app.rootHost.Focused())
	host.SetHovered(app.rootHost.Hovered())
	host.SetFrame(app.SpinFrame)
	host.Paint(refSurf.Buffer())
	comp := compositor.NewCompositor(w, h)
	comp.BeginFrame()
	comp.Root().Buffer().Fill(bg)
	comp.Root().AddChild(refSurf)
	app.windowManager.Render(comp.Root(), app.renderCtx)
	comp.Composite()
	want := uv.NewScreenBuffer(w, h)
	comp.PresentSurface().Draw(want, want.Bounds())
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !got.CellAt(x, y).Equal(want.CellAt(x, y)) {
				t.Fatalf("frame cell (%d,%d): got %+v want %+v (paint %+v)", x, y, got.CellAt(x, y), want.CellAt(x, y), app.rootHost.LastPaintStats())
			}
		}
	}
}

func TestRegionTerminalPublicationRetainsDenseChat(t *testing.T) {
	app, service, info := regionTerminalFixture(t, 200)
	for n := 0; n < 12; n++ {
		app.frameGate.lastPresented = time.Time{}
		app.Update(regionTerminalFrame(service, info, n))
		view := app.View()
		stats := app.rootHost.LastPaintStats()
		if stats.Full || stats.Cells != app.termDockRect.W*app.termDockRect.H {
			t.Fatalf("terminal repainted siblings: %+v dock=%+v", stats, app.termDockRect)
		}
		if !strings.Contains(view.RenderContent(), fmt.Sprintf("FRAME %04d", n)) {
			t.Fatal("latest terminal publication not visible")
		}
		assertRegionFrame(t, app, view)
	}
}

func TestRegionTerminalKeysRetainSiblingsAndPaintLocalState(t *testing.T) {
	app, service, info := regionTerminalFixture(t, 80)
	app.Update(regionTerminalFrame(service, info, 0))
	app.forceFrame()
	app.View()
	app.termDockFocused = true
	app.termScreens[info.ID].SetControlled(true)
	app.terminalLeases = map[string]workspace.TerminalLease{info.ID: {TerminalID: info.ID, Generation: 1}}
	for _, key := range []tea.KeyPressMsg{
		{Code: 'j', Text: "j"},
		{Code: 'a', Mod: tea.ModCtrl | tea.ModShift}, // local selection
		{Code: 'j', Text: "j"},                       // clears selection
	} {
		before := app.compositor.CurrentSurface().(uv.SnapshotSurface).SnapshotToken()
		app.Update(key)
		view := app.View()
		stats := app.rootHost.LastPaintStats()
		after := app.compositor.CurrentSurface().(uv.SnapshotSurface).SnapshotToken()
		// LastPaintStats describes the last actual paint; a retained publication
		// performs none and deliberately leaves that historical observation alone.
		if after != before && (stats.Full || stats.Cells > app.termDockRect.W*app.termDockRect.H) {
			t.Fatalf("terminal key repainted sibling regions: %+v", stats)
		}
		assertRegionFrame(t, app, view)
	}
	var keys []string
	for _, input := range service.inputs {
		if input.Kind == "key" {
			keys = append(keys, input.Key.Text)
		}
	}
	if strings.Join(keys, "") != "jj" {
		t.Fatalf("PTY input/selection routing changed: %+v", service.inputs)
	}
	// Control messages still carry a source invalidation, independent of the
	// immediate-frame clock. A title/grid change must remain visible.
	app.terminalCommand(shellterm.Command{ID: info.ID, Kind: shellterm.CommandKey, Key: terminalvt.Key{Code: 'q', Text: "q"}})
	app.Update(regionTerminalFrame(service, info, 1))
	app.frameGate.lastPresented = time.Time{}
	app.handleInteractiveFrame(interactiveFrameMsg{generation: app.frameGate.generation})
	view := app.View()
	if !strings.Contains(view.RenderContent(), "FRAME 0001") {
		t.Fatal("later authority update was hidden by keyboard retention")
	}
	assertRegionFrame(t, app, view)
}

func TestRegionTerminalInputWithoutLocalInkRetainsPublishedFrame(t *testing.T) {
	app, service, info := regionTerminalFixture(t, 0)
	app.Update(regionTerminalFrame(service, info, 0))
	app.termDockFocused = true
	app.termScreens[info.ID].SetControlled(true)
	app.terminalLeases = map[string]workspace.TerminalLease{info.ID: {TerminalID: info.ID, Generation: 1}}
	app.forceFrame()
	app.View()
	token := func() uv.SnapshotToken { return app.compositor.CurrentSurface().(uv.SnapshotSurface).SnapshotToken() }
	before := token()
	for range 3 {
		app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		view := app.View()
		if after := token(); after != before {
			t.Fatal("an admitted PTY key with no local visual change recomposed/copied the published frame")
		}
		assertRegionFrame(t, app, view)
	}
	app.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl | tea.ModShift})
	selected := app.View()
	if token() == before {
		t.Fatal("local selection failed to publish its changed ink")
	}
	assertRegionFrame(t, app, selected)
	app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	assertRegionFrame(t, app, app.View())
	before = token()
	app.Update(regionTerminalFrame(service, info, 1))
	app.frameGate.lastPresented = time.Time{}
	app.presentImmediateFrame()
	view := app.View()
	if token() == before || !strings.Contains(view.RenderContent(), "FRAME 0001") {
		t.Fatal("retention suppressed a later authoritative terminal update")
	}
	assertRegionFrame(t, app, view)
	var keys string
	for _, input := range service.inputs {
		if input.Kind == "key" {
			keys += input.Key.Text
		}
	}
	if keys != "jjjj" {
		t.Fatalf("frame retention delayed or duplicated input: %q", keys)
	}
	before = token()
	service.prediction = func() (workspace.TerminalProjection, bool) {
		p := regionTerminalFrame(service, info, 2).Projection
		return p, true
	}
	app.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	view = app.View()
	if token() == before || !strings.Contains(view.RenderContent(), "FRAME 0002") {
		t.Fatal("clean-frame retention hid immediate predictive local ink")
	}
	assertRegionFrame(t, app, view)
}

func BenchmarkRegionCleanTerminalKey(b *testing.B) {
	app, service, info := regionTerminalFixture(b, 0)
	app.Update(regionTerminalFrame(service, info, 0))
	app.termDockFocused = true
	app.termScreens[info.ID].SetControlled(true)
	app.terminalLeases = map[string]workspace.TerminalLease{info.ID: {TerminalID: info.ID, Generation: 1}}
	app.forceFrame()
	app.View()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		service.inputs = service.inputs[:0]
		app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		app.View()
	}
}

func TestRegionFrameRandomProductEquivalence(t *testing.T) {
	app, service, info := regionTerminalFixture(t, 80)
	rng := rand.New(rand.NewSource(941))
	for n := 0; n < 80; n++ {
		app.frameGate.lastPresented = time.Time{}
		switch rng.Intn(9) {
		case 0, 1:
			app.Update(regionTerminalFrame(service, info, n))
		case 2:
			app.Update(types.TickMsg(time.Now()))
		case 3:
			app.Update(tea.MouseMotionMsg{X: rng.Intn(app.width), Y: rng.Intn(app.height)})
		case 4:
			if app.overlayHost.IsOpen(ui.TerminalOverlayID(info.ID)) {
				app.dockTerminalInfo(info)
			} else {
				app.floatTerminalInfo(info)
			}
			app.rootHost.MarkPaint() // the direct test intent bypasses Update
		case 5:
			if fw := app.overlayHost.Window(ui.TerminalOverlayID(info.ID)); fw != nil {
				fw.SetPosition(rng.Intn(12), rng.Intn(7))
			}
		case 6:
			app.Update(tea.WindowSizeMsg{Width: 80 + rng.Intn(50), Height: 30 + rng.Intn(12)})
		case 7:
			app.applyThemePreset([]string{"dark", "light"}[rng.Intn(2)])
		case 8:
			app.appendActiveMessage(types.ChatMessage{Role: types.RoleAssistant, Content: fmt.Sprintf("streamed message %d 界", n), InternalID: fmt.Sprint(n)})
			app.rootHost.MarkPaint() // same publication boundary as agent Update
		}
		view := app.View()
		assertRegionFrame(t, app, view)
	}
}

func TestRegionClockDoesNotPaintFileTree(t *testing.T) {
	app, _, _ := regionTerminalFixture(t, 0)
	app.Update(types.TickMsg(time.Now()))
	view := app.View()
	stats := app.rootHost.LastPaintStats()
	if stats.Full || stats.Cells >= app.width*app.height {
		t.Fatalf("clock still paints entire shell: %+v", stats)
	}
	assertRegionFrame(t, app, view)
}

func TestRegionEditorSelectionFullEquivalence(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	ws.StartTime = time.Time{}
	paneJourneySize(app, 120, 40)
	if err := os.WriteFile(filepath.Join(ws.RootPath, "region.txt"), []byte(strings.Repeat("selection alpha beta 界\n", 70)), 0600); err != nil {
		t.Fatal(err)
	}
	openEditorTabs(t, app, "region.txt")
	app.forceFrame()
	app.View()
	r := app.mainPanes.groups[app.Win.ActiveGroup()].editor
	for _, msg := range []tea.Msg{
		tea.MouseClickMsg{X: r.X + 12, Y: r.Y + 2, Button: tea.MouseLeft},
		tea.MouseMotionMsg{X: r.X + 20, Y: r.Y + 4, Button: tea.MouseLeft},
		tea.MouseReleaseMsg{X: r.X + 20, Y: r.Y + 4, Button: tea.MouseLeft},
	} {
		app.frameGate.lastPresented = time.Time{}
		app.Update(msg)
		assertRegionFrame(t, app, app.View())
	}
	if !app.EditorState.Editor.HasSelection() || app.rootHost.HasPointerCapture() {
		t.Fatal("selection or capture lifecycle lost")
	}
}

func BenchmarkRegionTerminalFrame(b *testing.B) {
	for _, messages := range []int{0, 500} {
		for _, full := range []bool{false, true} {
			b.Run(fmt.Sprintf("messages=%d/full=%v", messages, full), func(b *testing.B) {
				app, service, info := regionTerminalFixture(b, messages)
				frame := regionTerminalFrame(service, info, 0)
				var components, cells uint64
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; b.Loop(); i++ {
					frame.Projection.Generation = uint64(i + 1)
					frame.Projection.Grid.Cells[0][0].Text = string(rune('a' + i%26))
					app.frameGate.lastPresented = time.Time{}
					app.Update(frame)
					if full {
						app.rootHost.MarkPaint()
					}
					app.View()
					stats := app.rootHost.LastPaintStats()
					components += uint64(stats.Components)
					cells += uint64(stats.Cells)
				}
				b.ReportMetric(float64(components)/float64(b.N), "components/op")
				b.ReportMetric(float64(cells)/float64(b.N), "cleared-cells/op")
			})
		}
	}
}
