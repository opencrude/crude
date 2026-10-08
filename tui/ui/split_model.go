package ui

// ActionSplitResize is emitted by SplitDivider after a drag or keyboard
// nudge so parents that own outer geometry (the shell sidebar) can apply
// the new first-pane size.
const ActionSplitResize = "split.resize"

// SplitResize is the payload of ActionSplitResize.
type SplitResize struct {
	ID    ComponentID
	First int
}

// SplitModel is the shared size + drag state for a two-pane split. SplitPane
// uses it to lay out two children; product screens with custom multi-child
// columns bind a SplitDivider to the same model and read FirstSize/Resolve
// from Layout.
type SplitModel struct {
	dir         Direction // Horizontal = side-by-side, Vertical = stacked
	fraction    float64
	first       int // -1 = derive from fraction
	dividerSize int
	minFirst    int
	minSecond   int
	maxFirst    int // 0 = no extra cap beyond minSecond
	dragging    bool
	dragPointer int
	dragFirst   int
}

// NewSplitModel creates a split model with a 0.5 fraction and 1-cell divider.
func NewSplitModel(dir Direction) *SplitModel {
	return &SplitModel{
		dir:         dir,
		fraction:    0.5,
		first:       -1,
		dividerSize: 1,
	}
}

// Clone retains layout preferences without sharing pointer-drag state. It is
// used to resolve a non-mutating prospective pane layout before a tab drop.
func (m *SplitModel) Clone() *SplitModel {
	if m == nil {
		return nil
	}
	copy := *m
	copy.CancelDrag()
	return &copy
}

// Dir reports the split axis.
func (m *SplitModel) Dir() Direction {
	if m == nil {
		return Horizontal
	}
	return m.dir
}

// SetFraction sets the divider as a fraction of the main axis (clamped to
// 0..1) and clears any absolute first-pane size.
func (m *SplitModel) SetFraction(f float64) {
	if m == nil {
		return
	}
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	m.fraction = f
	m.first = -1
}

// SetFirstSize pins the first pane to an absolute cell count. Drag and
// keyboard resize use this path.
func (m *SplitModel) SetFirstSize(n int) {
	if m == nil {
		return
	}
	if n < 0 {
		n = 0
	}
	m.first = n
}

// HasFirstSize reports whether an absolute first-pane size is set (as
// opposed to a fraction that tracks container size).
func (m *SplitModel) HasFirstSize() bool {
	return m != nil && m.first >= 0
}

// FirstSize returns the last committed absolute first-pane size, or 0 when
// the model is still fraction-driven.
func (m *SplitModel) FirstSize() int {
	if m == nil || m.first < 0 {
		return 0
	}
	return m.first
}

// SetDividerSize sets the divider thickness in cells.
func (m *SplitModel) SetDividerSize(d int) {
	if m == nil {
		return
	}
	if d < 0 {
		d = 0
	}
	m.dividerSize = d
}

// DividerSize returns the configured divider thickness.
func (m *SplitModel) DividerSize() int {
	if m == nil {
		return 1
	}
	return m.dividerSize
}

// SetMinSizes sets the minimum first and second pane sizes. When the
// container is too small to honor both, Resolve shrinks them.
func (m *SplitModel) SetMinSizes(first, second int) {
	if m == nil {
		return
	}
	if first < 0 {
		first = 0
	}
	if second < 0 {
		second = 0
	}
	m.minFirst = first
	m.minSecond = second
}

// SetMaxFirst caps the first pane independently of the second-pane minimum.
// Zero means no extra cap.
func (m *SplitModel) SetMaxFirst(n int) {
	if m == nil {
		return
	}
	if n < 0 {
		n = 0
	}
	m.maxFirst = n
}

// Dragging reports an in-progress pointer drag.
func (m *SplitModel) Dragging() bool {
	return m != nil && m.dragging
}

// BeginDrag snapshots the pointer's main-axis position and the current
// first-pane size. pointer is a screen X (horizontal) or Y (vertical).
func (m *SplitModel) BeginDrag(pointer, currentFirst int) {
	if m == nil {
		return
	}
	m.dragging = true
	m.dragPointer = pointer
	m.dragFirst = currentFirst
	if m.dragFirst < 0 {
		m.dragFirst = 0
	}
}

// DragTo updates the absolute first-pane size from the pointer delta.
func (m *SplitModel) DragTo(pointer int) {
	if m == nil || !m.dragging {
		return
	}
	m.first = m.clampFirst(m.dragFirst + (pointer - m.dragPointer))
}

// EndDrag clears drag tracking. The last DragTo size stays committed.
func (m *SplitModel) EndDrag() {
	if m == nil {
		return
	}
	m.dragging = false
	m.dragPointer = 0
	m.dragFirst = 0
}

// CancelDrag is EndDrag; the last committed size is kept (same as scrollbar
// thumb cancel — the user already saw the live size).
func (m *SplitModel) CancelDrag() {
	m.EndDrag()
}

// NudgeFirst adjusts the absolute first-pane size by delta cells. Used by
// keyboard resize when the divider is focused.
func (m *SplitModel) NudgeFirst(delta, currentFirst int) {
	if m == nil || delta == 0 {
		return
	}
	if currentFirst < 0 {
		currentFirst = 0
	}
	m.first = m.clampFirst(currentFirst + delta)
}

func (m *SplitModel) clampFirst(first int) int {
	if first < m.minFirst {
		first = m.minFirst
	}
	if m.maxFirst > 0 && first > m.maxFirst {
		first = m.maxFirst
	}
	if first < 0 {
		first = 0
	}
	return first
}

// Resolve distributes mainTotal cells into first pane, divider, and second
// pane. It does not mutate the model (fraction-driven splits keep tracking
// the container). After a drag, the absolute first size is clamped into the
// current container.
func (m *SplitModel) Resolve(mainTotal int) (first, divider, second int) {
	if m == nil {
		return 0, 0, mainTotal
	}
	if mainTotal < 0 {
		mainTotal = 0
	}
	divider = m.dividerSize
	if divider < 0 {
		divider = 0
	}
	if divider > mainTotal {
		divider = mainTotal
	}
	remain := mainTotal - divider
	if remain < 0 {
		remain = 0
	}
	first = m.first
	if first < 0 {
		first = int(float64(mainTotal) * m.fraction)
	}
	if first < 0 {
		first = 0
	}
	if first > remain {
		first = remain
	}
	minFirst, minSecond := m.minFirst, m.minSecond
	if minFirst < 0 {
		minFirst = 0
	}
	if minSecond < 0 {
		minSecond = 0
	}
	if minFirst+minSecond <= remain {
		if first < minFirst {
			first = minFirst
		}
		if remain-first < minSecond {
			first = remain - minSecond
		}
	}
	if m.maxFirst > 0 && first > m.maxFirst {
		first = m.maxFirst
		if first > remain {
			first = remain
		}
		if minFirst+minSecond <= remain && remain-first < minSecond {
			first = remain - minSecond
		}
	}
	if first < 0 {
		first = 0
	}
	second = remain - first
	if second < 0 {
		second = 0
		first = remain
	}
	return first, divider, second
}

// PointerMain returns the main-axis screen coordinate for this split.
func (m *SplitModel) PointerMain(screenX, screenY int) int {
	if m != nil && m.dir == Vertical {
		return screenY
	}
	return screenX
}
