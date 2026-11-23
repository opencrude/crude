package viewmodel

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/imageviewer"
	"crude/tui/screens/termdock"
	"crude/tui/screens/textoverlay"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/tui/wm"
)

// A Sixel image that moves must be sent again at every new position. Moving a
// window or panning reuses the prepared raster, so those drags stay native
// while the visible raster is small enough for the terminal to accept a frame
// quickly (sixelNativeMotionPixels). A drag that resizes an image (window
// resize, divider drag) needs a new raster at every step; it, and a drag of a
// larger image, paints only the affected view's cell preview. Release or
// capture loss restores native pixels; the terminal writer keeps the preview
// on screen until the native raster replaces it. Single wheel or key steps
// stay native. A burst of steps in a large pane uses the same live preview
// until the input settles.
//
// Windows Terminal 1.25 on an i7-9850H accepted an 810×560 raster (523 kB) in
// 22 ms and a 1530×1360 raster (2.1 MB) in 82 ms per frame.
const sixelNativeMotionPixels = 640_000

// Wait two long native-frame periods before restoring detail. The first two
// steps stay native so an isolated wheel notch does not flash a cell preview.
const sixelBurstQuiet = 160 * time.Millisecond

type sixelBurst struct {
	target     pixelMotionTarget
	last       time.Time
	steps      int
	active     bool
	generation uint64
}

type sixelBurstSettledMsg struct{ generation uint64 }

type pixelMotionState struct {
	burst  sixelBurst
	active []pixelMotionTarget
	probe  []pixelMotionSample // input scratch, not a second viewport
	split  []pixelMotionSample // before the next ordinary layout
}

type pixelMotionTarget struct {
	file  *imageviewer.Screen
	group window.GroupID
	ref   *textoverlay.Screen
	epoch uint64
	frame *wm.FloatingWindow
}

type pixelMotionSample struct {
	target   pixelMotionTarget
	viewport ui.Rect
	body     ui.Rect
}

type pixelMotionInput struct {
	moving   pixelMotionTarget
	resizing bool
	before   ui.Rect
	split    *ui.SplitModel
	first    int
}

// Observe the real input handlers, so no duplicated hit test or capture can
// suppress the wrong image. Neither mutation nor the shell frame gate waits
// for the native detail to return.
func (m *App) updateWithPixelMotion(msg tea.Msg) (tea.Model, tea.Cmd) {
	p := &m.pixelMotion
	if settled, ok := msg.(sixelBurstSettledMsg); ok {
		return m, m.settleSixelBurst(settled, time.Now())
	}
	if m.terminalImages.protocol() != tea.ImageSixel && len(p.active) == 0 && len(p.split) == 0 && !p.burst.active {
		return m.update(msg)
	}

	gen := m.workspaceSwitchGen.Load()
	discrete := isSixelStep(msg)
	input := m.samplePixelMotionInput(msg)
	if discrete && len(p.active) == 0 && m.mainPanes != nil && m.windowManager != nil {
		m.samplePixelMotionFiles()
		for _, fw := range m.windowManager.Windows() {
			target := m.pixelMotionWindow(fw)
			if target.file != nil {
				if viewport, ok := target.file.SixelMotionGeometry(); ok && m.pixelMotionMounted(target) {
					p.probe = append(p.probe, pixelMotionSample{target: target, viewport: viewport})
				}
			}
		}
	}
	model, cmd := m.update(msg)
	if len(p.probe) == 0 && len(p.active) == 0 && len(p.split) == 0 && input.moving.frame == nil && p.burst.target.file == nil {
		return model, cmd
	}
	cancel := gen != m.workspaceSwitchGen.Load() || m.terminalImages.protocol() != tea.ImageSixel
	switch msg.(type) {
	case tea.BlurMsg, tea.WindowSizeMsg, tea.ResumeMsg:
		cancel = true
	case tea.MouseClickMsg:
		m.endSixelBurst()
	}
	if cancel || p.burst.target.file != nil && !m.pixelMotionMounted(p.burst.target) {
		m.endSixelBurst()
	}
	release, released := msg.(tea.MouseReleaseMsg)
	end := cancel || released && release.Button == tea.MouseLeft ||
		len(p.active) > 0 && !m.pixelMotionCaptureHeld()
	if end {
		clear(p.split)
		p.split = p.split[:0]
		if m.finishPixelMotion(func(pixelMotionTarget) bool { return true }) {
			m.presentImmediateFrame()
		}
	}
	if !cancel {
		if input.moving.frame != nil && input.before != pixelMotionFrameRect(input.moving.frame) &&
			m.pixelMotionMounted(input.moving) && (input.resizing || !m.sixelNativeMotion(input.moving)) {
			m.startPixelMotion(input.moving)
		}
		if input.split != nil && input.split.FirstSize() != input.first {
			// Compare the actual body rectangles after the existing layout.
			// Preserve the oldest baseline if several inputs precede one frame.
			if len(p.split) == 0 {
				p.split = append(p.split[:0], p.probe...)
			}
		} else if input.split == nil {
			// A captured pan crops the prepared raster.
			for _, sample := range p.probe {
				if viewport, ok := sample.target.file.SixelMotionGeometry(); ok &&
					viewport != sample.viewport && m.pixelMotionMounted(sample.target) && !m.sixelNativeMotion(sample.target) {
					if discrete {
						cmd = tea.Batch(cmd, m.noteSixelStep(sample.target, time.Now()))
					} else {
						m.startPixelMotion(sample.target)
					}
				}
			}
		}
	}
	clear(p.probe)
	p.probe = p.probe[:0]
	return model, cmd
}

func isSixelStep(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		return true
	case tea.KeyPressMsg:
		if msg.Mod == 0 {
			switch msg.Code {
			case '+', '=', '-', '0', tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight:
				return true
			}
		}
	}
	return false
}

func (m *App) noteSixelStep(target pixelMotionTarget, at time.Time) tea.Cmd {
	b := &m.pixelMotion.burst
	if b.target != target || at.Sub(b.last) >= sixelBurstQuiet && !b.active {
		m.endSixelBurst()
		b.target = target
	}
	b.last = at
	if b.active {
		return nil
	}
	b.steps++
	if b.steps < 3 {
		return nil
	}
	b.active = true
	m.setPixelMotionPreview(b.target, true)
	m.presentImmediateFrame()
	return sixelBurstTimer(b.generation, sixelBurstQuiet)
}

func sixelBurstTimer(generation uint64, delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg { return sixelBurstSettledMsg{generation} })
}

func (m *App) settleSixelBurst(msg sixelBurstSettledMsg, now time.Time) tea.Cmd {
	b := &m.pixelMotion.burst
	if !b.active || msg.generation != b.generation {
		return nil
	}
	if left := b.last.Add(sixelBurstQuiet).Sub(now); left > 0 {
		return sixelBurstTimer(b.generation, left)
	}
	m.endSixelBurst()
	return nil
}

func (m *App) endSixelBurst() {
	b := &m.pixelMotion.burst
	if b.active {
		m.setPixelMotionPreview(b.target, false)
		m.presentImmediateFrame()
	}
	*b = sixelBurst{generation: b.generation + 1}
}

func (m *App) pixelMotionCaptureHeld() bool {
	return m.rootHost != nil && m.rootHost.HasPointerCapture() ||
		m.windowManager != nil && m.windowManager.IsDragging() || m.capturingWindow() != nil
}

// Sample captured drags before input mutates the anchor or the pane geometry.
func (m *App) samplePixelMotionInput(msg tea.Msg) pixelMotionInput {
	p := &m.pixelMotion
	if _, ok := msg.(tea.MouseMotionMsg); !ok || m.windowManager == nil || m.rootHost == nil {
		return pixelMotionInput{}
	}
	if m.windowManager.IsDragging() {
		for _, fw := range m.windowManager.Windows() {
			if fw.IsDragging() || fw.IsResizing() {
				if target := m.pixelMotionWindow(fw); target.file != nil || target.ref != nil {
					return pixelMotionInput{moving: target, resizing: fw.IsResizing(), before: pixelMotionFrameRect(fw)}
				}
				break
			}
		}
		return pixelMotionInput{}
	}
	if owner, _, ok := m.rootHost.CapturedPointer(); ok {
		switch captured := m.rootHost.Component(owner).(type) {
		case *ui.SplitDivider:
			if captured.Model().Dragging() && (m.mainViewResizeActive() || owner == termdock.DividerID) {
				m.samplePixelMotionFiles()
				if len(p.probe) != 0 {
					return pixelMotionInput{split: captured.Model(), first: captured.Model().FirstSize()}
				}
			}
		case *imageviewer.Screen:
			m.samplePixelMotionFiles()
		}
		return pixelMotionInput{}
	}
	if fw := m.capturingWindow(); fw != nil {
		if target := m.pixelMotionWindow(fw); target.file != nil {
			owner, _, _ := windowContentHost(fw).CapturedPointer()
			if owner == target.file.ID() {
				if viewport, ok := target.file.SixelMotionGeometry(); ok {
					p.probe = append(p.probe, pixelMotionSample{target: target, viewport: viewport})
				}
			}
		}
	}
	return pixelMotionInput{}
}

// samplePixelMotionFiles records the docked image views a root capture can
// move. Floating windows are sampled through their own capture.
func (m *App) samplePixelMotionFiles() {
	p := m.mainPanes
	if p == nil || m.Win == nil {
		return
	}
	for id, group := range p.groups {
		if group.view == nil || group.view.image == nil || group.body.Empty() || p.windows[id] != nil {
			continue
		}
		if _, visible := p.placements[id]; !visible {
			continue
		}
		target := pixelMotionTarget{file: group.view.image, group: id}
		if viewport, ok := target.file.SixelMotionGeometry(); ok && m.pixelMotionMounted(target) {
			m.pixelMotion.probe = append(m.pixelMotion.probe, pixelMotionSample{target: target, viewport: viewport, body: group.body})
		}
	}
}

func (m *App) pixelMotionWindow(fw *wm.FloatingWindow) pixelMotionTarget {
	if group, ok := paneGroupFromOverlay(ui.OverlayID(fw.ID)); ok && m.Win != nil {
		if tab := m.Win.GroupActiveTab(group); tab != nil {
			if file := m.imageView(tab.ID); file != nil {
				if _, ready := file.SixelMotionGeometry(); ready {
					return pixelMotionTarget{file: file, group: group, frame: fw}
				}
			}
		}
	} else if fw.ID == string(ui.OverlayTextOverlay) {
		if ref := m.sessionTextOverlay(); ref != nil && ref.HasSixelImage() {
			return pixelMotionTarget{ref: ref, epoch: ref.OpenEpoch(), frame: fw}
		}
	}
	return pixelMotionTarget{}
}

func (m *App) pixelMotionMounted(target pixelMotionTarget) bool {
	if target.frame != nil && (m.windowManager == nil || !target.frame.Visible() || m.windowManager.Window(target.frame.ID) != target.frame) {
		return false
	}
	if target.ref != nil {
		return target.ref == m.sessionTextOverlay() && target.ref.OpenEpoch() == target.epoch
	}
	if m.Win == nil || m.mainPanes == nil || m.Win.IsFloating(target.group) != (target.frame != nil) {
		return false
	}
	tab := m.Win.GroupActiveTab(target.group)
	if tab == nil || m.imageView(tab.ID) != target.file {
		return false
	}
	if target.frame == nil {
		_, visible := m.mainPanes.placements[target.group]
		return visible
	}
	return true
}

// sixelNativeMotion reports whether a drag of target can keep native pixels:
// the area it can show is small enough for the terminal to accept a frame
// quickly. The pane body or window bounds the visible raster.
func (m *App) sixelNativeMotion(target pixelMotionTarget) bool {
	cells := ui.Rect{}
	switch {
	case target.frame != nil:
		cells = pixelMotionFrameRect(target.frame)
	case m.mainPanes != nil:
		if group := m.mainPanes.groups[target.group]; group != nil {
			cells = group.body
		}
	}
	cw, ch := m.terminalImages.cellWidth, m.terminalImages.cellHeight
	return !cells.Empty() && cw > 0 && ch > 0 && cells.W*cw*cells.H*ch <= sixelNativeMotionPixels
}

func pixelMotionFrameRect(fw *wm.FloatingWindow) ui.Rect {
	x, y := fw.Position()
	w, h := fw.Size()
	return ui.Rect{X: x, Y: y, W: w, H: h}
}

// Split captures can include nested panes whose body did not move or resize.
// Only the existing shell layout can tell which image cells actually changed.
func (m *App) applyPixelMotionLayout() {
	p := &m.pixelMotion
	m.finishPixelMotion(func(target pixelMotionTarget) bool { return !m.pixelMotionMounted(target) })
	for _, sample := range p.split {
		if m.pixelMotionMounted(sample.target) {
			if group := m.mainPanes.groups[sample.target.group]; group != nil && group.body != sample.body {
				m.startPixelMotion(sample.target)
			}
		}
	}
	clear(p.split)
	p.split = p.split[:0]
}

func (m *App) startPixelMotion(target pixelMotionTarget) {
	m.setPixelMotionPreview(target, true)
	for _, active := range m.pixelMotion.active {
		if active == target {
			return
		}
	}
	m.pixelMotion.active = append(m.pixelMotion.active, target)
}

func (m *App) finishPixelMotion(finished func(pixelMotionTarget) bool) bool {
	p := &m.pixelMotion
	old := p.active
	p.active = old[:0]
	for _, target := range old {
		if finished(target) {
			m.setPixelMotionPreview(target, false)
		} else {
			p.active = append(p.active, target)
		}
	}
	clear(old[len(p.active):])
	return len(p.active) != len(old)
}

func (m *App) setPixelMotionPreview(target pixelMotionTarget, preview bool) {
	switch {
	case target.file != nil:
		if !target.file.SetSixelMotionPreview(preview) {
			return
		}
		if m.rootHost != nil && m.rootHost.Component(target.file.ID()) == target.file {
			m.rootHost.MarkComponentPaint(target.file.ID())
		}
	case target.ref != nil:
		if target.ref.OpenEpoch() != target.epoch || !target.ref.SetSixelMotionPreview(preview) {
			return
		}
	}
	if target.frame != nil {
		target.frame.MarkContentDirty()
	}
}
