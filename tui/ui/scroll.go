package ui

// ScrollbarWidth is the interactive scrollbar stroke. Product surfaces may
// reserve a separate blank gutter before it without changing hit geometry.
const ScrollbarWidth = 1

// ScrollModel tracks viewport offset for a 1-line-per-item collection.
type ScrollModel struct {
	offset     int
	viewport   int  // visible rows
	content    int  // total rows
	autoScroll bool // stick-to-bottom when content grows (chat gravity)
}

// NewScrollModel creates a scroll model. Auto-scroll is off by default;
// call SetAutoScroll(true) for chat-style stick-to-bottom.
func NewScrollModel() *ScrollModel {
	return &ScrollModel{}
}

// SetViewport sets visible height in rows.
func (s *ScrollModel) SetViewport(h int) {
	if h < 0 {
		h = 0
	}
	s.viewport = h
	s.Clamp()
}

// SetContent sets total content rows.
func (s *ScrollModel) SetContent(n int) {
	if n < 0 {
		n = 0
	}
	s.content = n
	s.Clamp()
}

// Offset returns the first visible row index.
func (s *ScrollModel) Offset() int { return s.offset }

// Viewport returns visible row count.
func (s *ScrollModel) Viewport() int { return s.viewport }

// Content returns total row count.
func (s *ScrollModel) Content() int { return s.content }

// MaxOffset is the maximum scroll offset.
func (s *ScrollModel) MaxOffset() int {
	m := s.content - s.viewport
	if m < 0 {
		return 0
	}
	return m
}

// Clamp keeps offset in range.
func (s *ScrollModel) Clamp() {
	if s.offset < 0 {
		s.offset = 0
	}
	max := s.MaxOffset()
	if s.offset > max {
		s.offset = max
	}
}

// ScrollBy adds delta to offset.
func (s *ScrollModel) ScrollBy(delta int) {
	s.offset += delta
	s.Clamp()
}

// ScrollTo sets absolute offset.
func (s *ScrollModel) ScrollTo(off int) {
	s.offset = off
	s.Clamp()
}

// EnsureVisible scrolls so idx is within the viewport.
func (s *ScrollModel) EnsureVisible(idx int) {
	if s.viewport <= 0 || s.content == 0 {
		return
	}
	if idx < 0 {
		return
	}
	if idx < s.offset {
		s.offset = idx
	} else if idx >= s.offset+s.viewport {
		s.offset = idx - s.viewport + 1
	}
	s.Clamp()
}

// ThumbBounds returns thumbTop and thumbHeight for a track of trackH rows.
// Matches tui/scrollbar math for migration compatibility.
func (s *ScrollModel) ThumbBounds(trackH int) (thumbTop, thumbH int) {
	if s.content <= 0 || s.viewport <= 0 || trackH <= 0 {
		return 0, trackH
	}
	maxScroll := s.MaxOffset()
	if maxScroll <= 0 {
		return 0, trackH
	}
	thumbH = s.viewport * trackH / s.content
	if thumbH < 1 {
		thumbH = 1
	}
	available := trackH - thumbH
	if available <= 0 {
		return 0, trackH
	}
	thumbTop = s.offset * available / maxScroll
	return thumbTop, thumbH
}

// AutoScroll reports whether stick-to-bottom is armed.
func (s *ScrollModel) AutoScroll() bool { return s.autoScroll }

// SetAutoScroll arms or disarms stick-to-bottom.
func (s *ScrollModel) SetAutoScroll(v bool) { s.autoScroll = v }

// NearBottom reports whether offset is within threshold rows of the bottom.
// When content fits in the viewport (MaxOffset == 0), this is always true.
func (s *ScrollModel) NearBottom(threshold int) bool {
	max := s.MaxOffset()
	if max <= 0 {
		return true
	}
	if threshold < 0 {
		threshold = 0
	}
	return s.offset >= max-threshold
}

// ShouldAutoScroll updates autoScroll from the current offset and returns whether
// the viewport should stick to the bottom. Near-bottom threshold is 5 rows,
// matching chatprojection wheelScroll for subpane migration compatibility.
func (s *ScrollModel) ShouldAutoScroll() bool {
	max := s.MaxOffset()
	if max <= 0 {
		s.autoScroll = true
		return true
	}
	nearBottom := s.offset >= max-5
	if s.autoScroll && nearBottom {
		return true
	}
	if !nearBottom {
		s.autoScroll = false
		return false
	}
	s.autoScroll = true
	return true
}

// StickToBottom scrolls to MaxOffset and arms auto-scroll.
func (s *ScrollModel) StickToBottom() {
	s.autoScroll = true
	s.ScrollTo(s.MaxOffset())
}

// ApplyAutoScroll scrolls to bottom when auto-scroll is armed.
// Call after content height changes (e.g. streaming chat rows).
// Does not re-evaluate near-bottom (use ShouldAutoScroll after user scroll).
func (s *ScrollModel) ApplyAutoScroll() {
	if s.autoScroll {
		s.ScrollTo(s.MaxOffset())
	}
}

// SyncScrollModel configures a ScrollModel for a 1-line-per-item viewport.
func SyncScrollModel(s *ScrollModel, content, viewport, offset int) *ScrollModel {
	if s == nil {
		s = NewScrollModel()
	}
	s.SetContent(content)
	s.SetViewport(viewport)
	s.ScrollTo(offset)
	return s
}
