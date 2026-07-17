package ui

// SelectionModel is the shared cursor/selection model for collections.
// Indices refer to the filtered/visible item list, not the raw data source.
type SelectionModel struct {
	count int
	idx   int
	wrap  bool
}

// NewSelectionModel creates a selection model.
// wrap controls whether Up/Down wrap at edges (menus) or clamp (lists).
func NewSelectionModel(wrap bool) *SelectionModel {
	return &SelectionModel{idx: -1, wrap: wrap}
}

// SetCount updates item count and clamps the cursor.
func (s *SelectionModel) SetCount(n int) {
	if n < 0 {
		n = 0
	}
	s.count = n
	s.Clamp()
}

// Count returns item count.
func (s *SelectionModel) Count() int { return s.count }

// Index returns the selected index, or -1 if empty.
func (s *SelectionModel) Index() int { return s.idx }

// SetIndex selects idx if in range.
func (s *SelectionModel) SetIndex(idx int) bool {
	if s.count == 0 {
		s.idx = -1
		return false
	}
	if idx < 0 || idx >= s.count {
		return false
	}
	s.idx = idx
	return true
}

// ClearSelection clears the cursor (idx = -1) without changing count.
func (s *SelectionModel) ClearSelection() {
	s.idx = -1
}

// SetWrap enables wrap-around at edges.
func (s *SelectionModel) SetWrap(wrap bool) { s.wrap = wrap }

// Clamp ensures idx is valid.
func (s *SelectionModel) Clamp() {
	if s.count == 0 {
		s.idx = -1
		return
	}
	if s.idx < 0 {
		s.idx = 0
		return
	}
	if s.idx >= s.count {
		s.idx = s.count - 1
	}
}

// MoveUp moves the selection up.
func (s *SelectionModel) MoveUp() {
	if s.count == 0 {
		return
	}
	if s.idx < 0 {
		s.idx = 0
		return
	}
	if s.idx == 0 {
		if s.wrap {
			s.idx = s.count - 1
		}
		return
	}
	s.idx--
}

// MoveDown moves the selection down.
func (s *SelectionModel) MoveDown() {
	if s.count == 0 {
		return
	}
	if s.idx < 0 {
		s.idx = 0
		return
	}
	if s.idx >= s.count-1 {
		if s.wrap {
			s.idx = 0
		}
		return
	}
	s.idx++
}

// PageUp moves up by page size.
func (s *SelectionModel) PageUp(page int) {
	if page < 1 {
		page = 1
	}
	if s.count == 0 {
		return
	}
	s.idx -= page
	if s.idx < 0 {
		s.idx = 0
	}
}

// PageDown moves down by page size.
func (s *SelectionModel) PageDown(page int) {
	if page < 1 {
		page = 1
	}
	if s.count == 0 {
		return
	}
	s.idx += page
	if s.idx >= s.count {
		s.idx = s.count - 1
	}
}

// Home jumps to first item.
func (s *SelectionModel) Home() {
	if s.count == 0 {
		s.idx = -1
		return
	}
	s.idx = 0
}

// End jumps to last item.
func (s *SelectionModel) End() {
	if s.count == 0 {
		s.idx = -1
		return
	}
	s.idx = s.count - 1
}
