package viewmodel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"crude/terminalvt"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// FuzzAppFrameState drives the product Update/View boundary, not PaintRegions
// directly. The oracle in assertRegionFrame independently clears and fully
// Host.Paint's the current shell, then composes its real floating windows.
// Byte zero chooses empty/dense history; the remaining bytes are at most thirty
// actions (low value modulo 14) with a bounded geometry/variant parameter.
func FuzzAppFrameState(f *testing.F) {
	for _, seed := range [][]byte{
		{0},
		{1},
		{0, 7, 8},    // Last overlay unmount must not retain its completed ink.
		{0, 5, 7, 8}, // Closing a menu restores floating-terminal focus ink.
		{0, 0, 1, 1, 13, 2, 1, 11, 0, 13},
		{1, 0, 1, 2, 13, 1, 11, 0, 3, 12, 26},
		{0, 5, 0, 6, 20, 4, 5, 13, 0},
		{1, 7, 8, 13, 0, 1},
		{0, 7, 6, 20, 8, 13},
		{1, 4, 18, 32, 46, 9, 23, 10, 24, 5, 0, 5},
		{1, 5, 7, 6, 8, 5, 13, 11, 2, 1, 3},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		app, service, info := regionTerminalFixture(t, int(data[0]%2)*80)
		app.termScreens[info.ID].SetControlled(true)
		app.terminalLeases = map[string]workspace.TerminalLease{
			info.ID: {TerminalID: info.ID, Generation: 1},
		}
		var expectedKeys []terminalvt.Key
		publication := 0
		// Each call allocates a fresh publication; no row accepted by the
		// immutable terminal path is ever mutated by a subsequent action.
		publish := func(cursor bool) string {
			publication++
			msg := regionTerminalFrame(service, info, publication)
			msg.Projection.Grid.Cursor = terminalvt.Cursor{X: 14, Y: 1, Visible: cursor, Blink: cursor}
			app.Update(msg)
			return fmt.Sprintf("FRAME %04d", publication)
		}
		// Only the presentation clock is moved: do not forceFrame/MarkPaint
		// here, which would hide missing source invalidation in later actions.
		app.frameGate.lastPresented = time.Time{}
		publish(false)
		assertRegionFrame(t, app, app.View())
		check := func(label string) tea.View {
			t.Helper()
			view := app.View()
			if view.Surface == nil {
				t.Fatalf("%s: missing direct-cell surface", label)
			}
			assertRegionFrame(t, app, view)
			var got []terminalvt.Key
			for _, input := range service.inputs {
				if input.Kind == "key" {
					if input.Lease.TerminalID != info.ID {
						t.Fatalf("%s: input reached wrong terminal: %+v", label, input)
					}
					got = append(got, input.Key)
				}
			}
			if len(got) != len(expectedKeys) {
				t.Fatalf("%s: admitted keys=%+v want %+v", label, got, expectedKeys)
			}
			for i := range got {
				if got[i] != expectedKeys[i] {
					t.Fatalf("%s: key %d=%+v want %+v", label, i, got[i], expectedKeys[i])
				}
			}
			return view
		}
		for step, action := range data[1:min(len(data), 31)] {
			kind, variant := int(action%14), int(action/14)
			label := fmt.Sprintf("step=%d action=%d variant=%d", step, kind, variant)
			t.Log(label)
			app.frameGate.lastPresented = time.Time{}
			menuOpen := app.overlayHost.IsOpen(ui.OverlayContextMenu)
			var wantText string
			switch kind {
			case 0: // Authoritative publication after idle, selection or prediction.
				wantText = publish(false)
			case 1, 2, 11:
				if menuOpen {
					break // Modal menu owns keys; do not count unadmitted input.
				}
				if !app.terminalOwnsKeys() {
					t.Fatalf("%s: terminal lost keyboard ownership", label)
				}
				if kind == 2 {
					app.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl | tea.ModShift})
					break // Local selection must never enter service.inputs.
				}
				key := rune('j' + variant%3)
				if kind == 11 {
					key = 'p'
					publication++
					p := regionTerminalFrame(service, info, publication).Projection
					service.prediction = func() (workspace.TerminalProjection, bool) { return p, true }
					wantText = fmt.Sprintf("FRAME %04d", publication)
				}
				expectedKeys = append(expectedKeys, terminalvt.Key{Code: key, Text: string(key)})
				app.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
				service.prediction = nil
			case 3:
				app.Update(types.TickMsg(time.UnixMilli(1_700_000_000_000 + int64(step)*400)))
			case 4:
				sizes := [][2]int{{80, 30}, {120, 40}, {64, 24}, {100, 32}, {48, 20}}
				size := sizes[variant%len(sizes)]
				app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			case 5:
				if app.overlayHost.IsOpen(ui.TerminalOverlayID(info.ID)) {
					app.dockTerminalInfo(info)
				} else {
					app.floatTerminalInfo(info)
				}
				// This direct placement intent bypasses Update's source owner.
				app.rootHost.MarkPaint()
			case 6:
				id := ui.TerminalOverlayID(info.ID)
				if menuOpen {
					id = ui.OverlayContextMenu
				}
				if fw := app.overlayHost.Window(id); fw != nil {
					fw.SetPosition(variant%13, variant%7)
				}
			case 7:
				app.openContextMenuState(tea.Mouse{X: 4 + variant, Y: 4 + variant%5}, &ui.ContextMenuState{
					Sections: []ui.ContextMenuSection{{Items: []ui.ContextMenuItem{{Label: "Frame fuzz overlay"}}}},
				})
				// Direct open bypasses the input event that owns shell focus ink.
				app.rootHost.MarkPaint()
			case 8:
				if menuOpen {
					app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
					if app.overlayHost.IsOpen(ui.OverlayContextMenu) {
						t.Fatalf("%s: Escape failed to unmount menu", label)
					}
				}
			case 9:
				app.ApplyThemePreset([]string{"dark", "light"}[variant%2])
			case 10:
				app.appendActiveMessage(types.ChatMessage{Role: types.RoleAssistant,
					Content: fmt.Sprintf("frame fuzz append %d: 界 é", step), InternalID: fmt.Sprintf("frame-fuzz-%d", step)})
				// Direct chat append bypasses the normal agent-message Update.
				app.rootHost.MarkPaint()
			case 12:
				publish(true)
				check(label + " cursor publication")
				// The product cursor owner accepts an explicit clock. Step both
				// phases without sleeps or relying on a wall-clock blink boundary.
				for _, ms := range []int64{4_000_000_000_000, 4_000_000_000_400} {
					app.tickTerminalCursors(time.UnixMilli(ms))
					// Direct clock dispatch bypasses TickMsg's dock invalidation;
					// tickTerminalCursors itself owns floating-window invalidation.
					app.rootHost.MarkComponentPaint(app.shell.termDock.ID())
					check(label + " blink")
				}
			case 13:
				before := app.compositor.CurrentSurface().(uv.SnapshotSurface).SnapshotToken()
				clean := !app.rootHost.NeedsPaint() && !app.windowManager.HasWindows()
				check(label + " idle")
				if clean && app.compositor.CurrentSurface().(uv.SnapshotSurface).SnapshotToken() != before {
					t.Fatalf("%s: clean idle frame was republished", label)
				}
			}
			view := check(label)
			if wantText != "" && !menuOpen && !app.windowManager.HasWindows() && !strings.Contains(view.RenderContent(), wantText) {
				t.Fatalf("%s: latest terminal ink %q not visible", label, wantText)
			}
		}
	})
}
