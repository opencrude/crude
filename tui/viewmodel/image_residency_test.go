package viewmodel

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi/kitty"

	"crude/tui/window"
	"crude/types"
)

func TestImageWorkingSetManyTabs(t *testing.T) {
	app, ws := newRealShellApp(t)
	paneJourneySize(app, 100, 30)
	app.terminalImages = terminalImageState{supported: true, cellWidth: 8, cellHeight: 16}

	const count = 24
	const width, height = 2048, 1024
	// Tiny files that really decode to 8 MiB each. Their trailing data makes
	// them different source snapshots without making fixture encoding the load.
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{R: 160, G: 80, B: 40, A: 255}), image.Point{}, draw.Src)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, count)
	for i := range paths {
		paths[i] = filepath.Join(ws.RootPath, fmt.Sprintf("image-%02d.png", i))
		if err := os.WriteFile(paths[i], append(bytes.Clone(encoded.Bytes()), byte(i)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	tabs := make([]window.TabID, 0, count)
	var zoomStatus, panOrigin string
	for _, path := range paths {
		openImageFromTree(t, app, path)
		tab := app.Win.ActiveTab()
		app.presentImmediateFrame()
		frame := app.View()
		if !isImageTab(tab) || len(frame.Images) != 1 {
			t.Fatalf("opening %s did not show one image pane", path)
		}
		tabs = append(tabs, tab.ID)
		if len(tabs) == 1 {
			paneJourneyKey(app, '+', 0)
			unpanned := imageWorkingSetViewOrigin(t, app)
			paneJourneyKey(app, tea.KeyRight, 0)
			zoomStatus, panOrigin = app.activeImageView().Semantics().Value, imageWorkingSetViewOrigin(t, app)
			if unpanned == panOrigin {
				t.Fatal("the fixture did not pan the image")
			}
		}
	}
	elapsed := time.Since(start)
	runtime.GC()
	runtime.ReadMemStats(&after)
	resident, mounted := 0, 0
	var dormantBytes int64
	for _, id := range tabs {
		if screen := app.imageView(id); screen == nil {
			t.Fatalf("tab %d lost its retained view", id)
		} else if screen.ImagePlacement().Image != nil {
			mounted++
		}
		if view := app.mainPanes.views[id]; view.imageState.decoded.Pixels != nil {
			resident++
			if id != tabs[count-1] {
				dormantBytes += view.imageState.decoded.Bytes
			}
		}
	}
	t.Logf("%d sequential 2048x1024 tree opens: %s, %d decoded sources retained, %.1f MiB live Go heap added",
		count, elapsed.Round(time.Millisecond), resident, float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/(1<<20))
	// One selected image and no more than 64 MiB of dormant decoded pixels and
	// native PNG bytes. The hidden screens must release all native placements.
	if mounted != 1 || resident < 2 || resident > 9 || dormantBytes > 64<<20 {
		t.Fatalf("one visible pane: %d placements, %d resident images, %d dormant bytes", mounted, resident, dormantBytes)
	}
	first := app.mainPanes.views[tabs[0]]
	if first.imageState.decoded.Pixels != nil {
		t.Fatal("the oldest view should be cold before reopening it")
	}
	snapshot := first.imageState.snapshot.path
	writeImageFixture(t, ws.RootPath, filepath.Base(paths[0])) // Replace the project image with an 80×40 image.
	_, cmd := app.Update(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	app.presentImmediateFrame()
	if frame := app.View(); !strings.Contains(frame.RenderContent(), "Loading image…") || len(frame.Images) != 0 {
		t.Fatal("a cold tab must show loading without the old pane's placement")
	}
	runAppCommand(t, app, cmd)
	app.presentImmediateFrame()
	frame := app.View()
	if app.Win.ActiveTab().ID != tabs[0] || app.activeImageView() != first.image || len(frame.Images) != 1 ||
		app.activeImageView().Semantics().Value != zoomStatus || imageWorkingSetViewOrigin(t, app) != panOrigin {
		t.Fatalf("cold tab did not restore its original image and viewport: %s", app.activeImageView().Semantics().Value)
	}

	_, cmd = app.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	asyncIOStateFor(app).detachedCommands.Wait()
	if app.Win.TabByID(tabs[0]) != nil {
		t.Fatal("Ctrl+W did not close the image")
	}
	requireImageSnapshotRemoved(t, snapshot)
	openImageFromTree(t, app, paths[0])
	app.presentImmediateFrame()
	app.View()
	if got := app.activeImageView().Semantics().Value; !strings.Contains(got, "80 × 40") || !strings.Contains(got, "Fit") {
		t.Fatalf("opening after close did not read the new image at Fit: %q", got)
	}
}

func imageWorkingSetViewOrigin(t *testing.T, app *App) string {
	t.Helper()
	body := app.mainPanes.groups[app.Win.ActiveGroup()].body
	for y := body.Y; y < body.Y+body.H; y++ {
		for x := body.X; x < body.X+body.W; x++ {
			c := app.mainSurf.Buffer().Get(x, y)
			if c.Char == kitty.Placeholder {
				// Row and column are the first two combining marks, before
				// the terminal resource ID, which can change on re-decode.
				return string([]rune(c.Content)[:3])
			}
		}
	}
	t.Fatal("image placement has no painted cells")
	return ""
}

func requireImageSnapshotRemoved(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("private image snapshot %q was not removed: %v", path, err)
	}
}

func TestImageWorkingSetBatchDefersDormantPixelErrors(t *testing.T) {
	app, ws := newRealShellApp(t)
	first := writeImageFixture(t, ws.RootPath, "first.png")
	last := writeImageFixture(t, ws.RootPath, "last.png")
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	idat := bytes.Index(data, []byte("IDAT"))
	if idat < 0 || os.WriteFile(first, data[:idat+5], 0o600) != nil {
		t.Fatal("could not build a PNG with a valid header and truncated pixels")
	}
	runAppCommand(t, app, app.openPaneFiles([]string{first, last}, app.Win.ActiveGroup()))
	if app.Win.ActiveTab().Resource.ID != last || app.Win.TabByTitle(imageTabTitle(first)) == nil || app.imageResidency.warm.Len() != 0 {
		t.Fatal("the pane did not keep the dormant tab or loaded more than the selected image")
	}
	writeImageFixture(t, ws.RootPath, filepath.Base(first)) // The project file is now valid; the snapshot is not.
	_, cmd := app.Update(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	app.presentImmediateFrame()
	frame := app.View()
	if app.Win.ActiveTab().Resource.ID != first || !strings.Contains(frame.RenderContent(), "Could not load image. Reopen it.") ||
		len(frame.Images) != 0 || app.BufferMgr.Get(first) != nil {
		t.Fatal("the selected tab did not report the error in the original snapshot")
	}
}

func TestImageWorkingSetCloseAndWorkspaceRejectLateResults(t *testing.T) {
	app, ws := newRealShellApp(t)
	// This journey switches while an image result is undelivered, not while
	// Local History is writing. That separate owner correctly refuses a switch.
	defaults := ws.AppDefaults()
	defaults.LocalHistory = types.LocalHistorySettings{Enabled: "off"}
	ws.SetAppDefaults(defaults)
	path := writeImageFixture(t, ws.RootPath, "old.png")
	prepared := app.openPaneFiles([]string{path}, app.Win.ActiveGroup())().(paneFilesOpenedMsg)
	source := prepared.files[0].image
	_, cmd := app.Update(prepared)
	oldTab := app.Win.ActiveTab().ID
	ready := holdImageWorkingSetResult(t, cmd)

	_, cmd = app.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	asyncIOStateFor(app).detachedCommands.Wait()
	requireImageSnapshotRemoved(t, source.path)
	openImageFromTree(t, app, path)
	replacement := app.Win.ActiveTab().ID
	if replacement == oldTab || app.mainPanes.views[replacement].imageState.decoded.Pixels != nil {
		t.Fatal("the replacement used the still-undelivered load")
	}
	_, cmd = app.Update(ready)
	runAppCommand(t, app, cmd)
	replacementView := app.mainPanes.views[replacement]
	if replacementView.imageState.decoded.Pixels == nil || replacementView.imageState.snapshot == source {
		t.Fatal("a closed tab's result occupied the replacement instead of its own snapshot")
	}

	path = writeImageFixture(t, ws.RootPath, "undelivered.png")
	abandoned := app.openPaneFiles([]string{path}, app.Win.ActiveGroup())().(paneFilesOpenedMsg)
	target := t.TempDir()
	if err := app.SwitchProject(target); err != nil {
		t.Fatal(err)
	}
	asyncIOStateFor(app).detachedCommands.Wait()
	requireImageSnapshotRemoved(t, abandoned.files[0].image.path)
	requireImageSnapshotRemoved(t, replacementView.imageState.snapshot.path)
	_, cmd = app.Update(abandoned)
	runAppCommand(t, app, cmd)
	if app.Win.TabByTitle(imageTabTitle(path)) != nil {
		t.Fatal("the old workspace's undelivered open appeared in the replacement")
	}

	path = writeImageFixture(t, target, "not-delivered-before-stop.png")
	atStop := app.openPaneFiles([]string{path}, app.Win.ActiveGroup())().(paneFilesOpenedMsg)
	app.StopAsyncIO()
	requireImageSnapshotRemoved(t, atStop.files[0].image.path)
}

func holdImageWorkingSetResult(t *testing.T, cmd tea.Cmd) paneImageLoadedMsg {
	t.Helper()
	// Retain only the actual worker result, like a result waiting to be
	// delivered by Bubble Tea. Later input still uses the real Update route.
	cmds := []tea.Cmd{cmd}
	for len(cmds) > 0 {
		next := cmds[0]
		cmds = cmds[1:]
		if next == nil {
			continue
		}
		switch msg := next().(type) {
		case tea.BatchMsg:
			cmds = append(cmds, msg...)
		case paneImageLoadedMsg:
			if msg.err != nil || msg.decoded.Pixels == nil {
				t.Fatalf("could not hold a real decoded result: %v", msg.err)
			}
			return msg
		}
	}
	t.Fatal("selecting a cold image did not schedule a load")
	return paneImageLoadedMsg{}
}
