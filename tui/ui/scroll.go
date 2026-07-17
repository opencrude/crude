package ui

// ScrollModel tracks viewport offset for a 1-line-per-item collection.
type ScrollModel struct {
	offset    int
	viewport  int // visible rows
	content   int // total rows
}

// NewScrollModel creates a scroll model.
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
