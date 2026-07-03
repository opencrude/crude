package viewmodel

type FocusStack struct {
	stack []FocusTarget
}

func NewFocusStack() *FocusStack {
	return &FocusStack{}
}

func (s *FocusStack) Push(t FocusTarget) {
	s.stack = append(s.stack, t)
}

func (s *FocusStack) Pop() FocusTarget {
	if len(s.stack) == 0 {
		return FocusNone
	}
	top := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]
	return top
}

func (s *FocusStack) Top() FocusTarget {
	if len(s.stack) == 0 {
		return FocusNone
	}
	return s.stack[len(s.stack)-1]
}

func (s *FocusStack) Clear() {
	s.stack = s.stack[:0]
}

func (s *FocusStack) Len() int {
	return len(s.stack)
}
