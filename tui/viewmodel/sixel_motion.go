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

// A Sixel move can resend hundreds of kilobytes even with a cached raster.
// Keep current cell frames throughout a captured drag, including pauses.
// Quiet time settles wheel and key bursts, not a held pointer.
const sixelMotionQuiet = 180 * time.Millisecond

type pixelMotionWakeMsg time.Time

type pixelMotionState struct {
	active []pixelMotionPreview
	probe  []pixelMotionSample // input scratch, not a second viewport
	split  []pixelMotionSample // before the next ordinary layout
	wake   bool                // at most one wake command
}

type pixelMotionTarget struct {
	file  *imageviewer.Screen
	group window.GroupID
	ref   *textoverlay.Screen
	epoch uint64
	frame *wm.FloatingWindow
}

type pixelMotionPreview struct {
	target pixelMotionTarget
	until  time.Time
	drag   bool
}

type pixelMotionSample struct {
	target   pixelMotionTarget
	viewport ui.Rect
	body     ui.Rect
}

type pixelMotionInput struct {
	moving pixelMotionTarget
	before ui.Rect
	split  *ui.SplitModel
	first  int
	drag   bool
}

// Observe the real input handlers, so no duplicated hit test or capture can
// suppress the wrong image. Neither mutation nor the shell frame gate waits
// for the native detail to return.
func (m *App) updateWithPixelMotion(msg tea.Msg) (tea.Model, tea.Cmd) {
	p := &m.pixelMotion
	if wake, ok := msg.(pixelMotionWakeMsg); ok {
		p.wake = false
		now := time.Time(wake)
		if m.finishPixelMotion(func(preview pixelMotionPreview) bool {
			return !preview.drag && !now.Before(preview.until)
		}) {
			m.presentImmediateFrame()
		}
		return m, m.schedulePixelMotionWake(now)
	}
	if m.terminalImages.protocol() != tea.ImageSixel && len(p.active) == 0 && len(p.split) == 0 {
		return m.update(msg)
	}

	gen := m.workspaceSwitchGen.Load()
	input := m.samplePixelMotionInput(msg)
	model, cmd := m.update(msg)
	if len(p.probe) == 0 && len(p.active) == 0 && len(p.split) == 0 && input.moving.frame == nil {
		return model, cmd
	}
	now := time.Now()
	cancel := gen != m.workspaceSwitchGen.Load() || m.terminalImages.protocol() != tea.ImageSixel
	switch msg.(type) {
	case tea.BlurMsg, tea.WindowSizeMsg, tea.ResumeMsg:
		cancel = true
	}
	release, released := msg.(tea.MouseReleaseMsg)
	endDrag := released && release.Button == tea.MouseLeft
	if !cancel && !endDrag {
		for _, active := range p.active {
			if active.drag {
				endDrag = !m.pixelMotionCaptureHeld()
				break
			}
		}
	}
	if cancel || endDrag {
		clear(p.split)
		p.split = p.split[:0]
		if m.finishPixelMotion(func(preview pixelMotionPreview) bool { return cancel || preview.drag }) {
			m.presentImmediateFrame()
		}
	}
	if !cancel {
		until := now.Add(sixelMotionQuiet)
		if input.moving.frame != nil && input.before != pixelMotionFrameRect(input.moving.frame) &&
			m.pixelMotionMounted(input.moving) {
			m.startPixelMotion(input.moving, until, true)
		}
		if input.split != nil && input.split.FirstSize() != input.first {
			// Compare the actual body rectangles after the existing layout.
			// Preserve the oldest baseline if several inputs precede one frame.
			if len(p.split) == 0 {
				p.split = append(p.split[:0], p.probe...)
			}
		} else if input.split == nil {
			for _, sample := range p.probe {
				if viewport, ok := sample.target.file.SixelMotionGeometry(); ok &&
					viewport != sample.viewport && m.pixelMotionMounted(sample.target) {
					m.startPixelMotion(sample.target, until, input.drag)
				}
			}
		}
	}
	clear(p.probe)
	p.probe = p.probe[:0]
	return model, tea.Batch(cmd, m.schedulePixelMotionWake(now))
}

func (m *App) pixelMotionCaptureHeld() bool {
	return m.rootHost != nil && m.rootHost.HasPointerCapture() ||
		m.windowManager != nil && m.windowManager.IsDragging() || m.capturingWindow() != nil
}

func (m *App) samplePixelMotionInput(msg tea.Msg) pixelMotionInput {
	p := &m.pixelMotion
	switch msg.(type) {
	case tea.MouseWheelMsg, tea.KeyPressMsg, tea.MouseClickMsg:
		// A wheel gesture may be retained by the Host, and the toolbar can
		// change zoom via either a key or click. Compare actual viewports.
		m.samplePixelMotionFiles(false)
	case tea.MouseMotionMsg:
		if m.windowManager == nil || m.rootHost == nil {
			break
		}
		if m.windowManager.IsDragging() {
			for _, fw := range m.windowManager.Windows() {
				if fw.IsDragging() || fw.IsResizing() {
					if target := m.pixelMotionWindow(fw); target.file != nil || target.ref != nil {
						return pixelMotionInput{moving: target, before: pixelMotionFrameRect(fw)}
					}
					break
				}
			}
			break
		}
		if owner, _, ok := m.rootHost.CapturedPointer(); ok {
			switch captured := m.rootHost.Component(owner).(type) {
			case *ui.SplitDivider:
				if captured.Model().Dragging() && (m.mainViewResizeActive() || owner == termdock.DividerID) {
					m.samplePixelMotionFiles(true)
					if len(p.probe) != 0 {
						return pixelMotionInput{split: captured.Model(), first: captured.Model().FirstSize(), drag: true}
					}
				}
			case *imageviewer.Screen:
				m.samplePixelMotionFiles(true)
				return pixelMotionInput{drag: true}
			}
			break
		}
		if fw := m.capturingWindow(); fw != nil {
			if target := m.pixelMotionWindow(fw); target.file != nil {
				owner, _, _ := windowContentHost(fw).CapturedPointer()
				if owner == target.file.ID() {
					if viewport, ok := target.file.SixelMotionGeometry(); ok {
						p.probe = append(p.probe, pixelMotionSample{target: target, viewport: viewport})
						return pixelMotionInput{drag: true}
					}
				}
			}
		}
	}
	return pixelMotionInput{}
}

func (m *App) samplePixelMotionFiles(dockedOnly bool) {
	p := m.mainPanes
	if p == nil || m.Win == nil {
		return
	}
	for id, group := range p.groups {
		if group.view == nil || group.view.image == nil || group.body.Empty() {
			continue
		}
		target := pixelMotionTarget{file: group.view.image, group: id}
		if pane := p.windows[id]; pane != nil {
			if dockedOnly || !pane.fw.Visible() {
				continue
			}
			target.frame = pane.fw
		} else if _, visible := p.placements[id]; !visible {
			continue
		}
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

func pixelMotionFrameRect(fw *wm.FloatingWindow) ui.Rect {
	x, y := fw.Position()
	w, h := fw.Size()
	return ui.Rect{X: x, Y: y, W: w, H: h}
}

// Split captures can include nested panes whose body did not move or resize.
// Only the existing shell layout can tell which image cells actually changed.
func (m *App) applyPixelMotionLayout() {
	p := &m.pixelMotion
	m.finishPixelMotion(func(preview pixelMotionPreview) bool { return !m.pixelMotionMounted(preview.target) })
	for _, sample := range p.split {
		if m.pixelMotionMounted(sample.target) {
			if group := m.mainPanes.groups[sample.target.group]; group != nil && group.body != sample.body {
				m.startPixelMotion(sample.target, time.Time{}, true)
			}
		}
	}
	clear(p.split)
	p.split = p.split[:0]
}

func (m *App) startPixelMotion(target pixelMotionTarget, until time.Time, drag bool) {
	m.setPixelMotionPreview(target, true)
	p := &m.pixelMotion
	for i := range p.active {
		if preview := &p.active[i]; preview.target == target {
			preview.until, preview.drag = until, drag || preview.drag
			return
		}
	}
	p.active = append(p.active, pixelMotionPreview{target: target, until: until, drag: drag})
}

func (m *App) finishPixelMotion(finished func(pixelMotionPreview) bool) bool {
	p := &m.pixelMotion
	old := p.active
	p.active = old[:0]
	for _, preview := range old {
		if finished(preview) {
			m.setPixelMotionPreview(preview.target, false)
		} else {
			p.active = append(p.active, preview)
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

func (m *App) schedulePixelMotionWake(now time.Time) tea.Cmd {
	p := &m.pixelMotion
	if p.wake {
		return nil
	}
	var earliest time.Time
	for _, preview := range p.active {
		if preview.drag {
			continue
		}
		if earliest.IsZero() || preview.until.Before(earliest) {
			earliest = preview.until
		}
	}
	if earliest.IsZero() {
		return nil
	}
	p.wake = true
	return tea.Tick(max(time.Millisecond, earliest.Sub(now)), func(at time.Time) tea.Msg { return pixelMotionWakeMsg(at) })
}
