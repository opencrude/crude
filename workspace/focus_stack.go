package workspace

// FocusStack is a simple LIFO stack of focus targets. It is used by the
// window manager to remember which region had focus before a floating
// window was opened, so focus can be restored when the window closes.
type FocusStack struct {
	stack []FocusTarget
}

// NewFocusStack creates an empty focus stack.
func NewFocusStack() *FocusStack {
	return &FocusStack{}
}

// Push adds a target to the top of the stack.
func (s *FocusStack) Push(t FocusTarget) {
	s.stack = append(s.stack, t)
}

// Pop removes and returns the top target. Returns FocusNone if empty.
func (s *FocusStack) Pop() FocusTarget {
	if len(s.stack) == 0 {
		return FocusNone
	}
	top := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]
	return top
}

// Top returns the top target without removing it. Returns FocusNone if empty.
func (s *FocusStack) Top() FocusTarget {
	if len(s.stack) == 0 {
		return FocusNone
	}
	return s.stack[len(s.stack)-1]
}

// Clear empties the stack.
func (s *FocusStack) Clear() {
	s.stack = s.stack[:0]
}

// Len returns the number of targets in the stack.
func (s *FocusStack) Len() int {
	return len(s.stack)
}
