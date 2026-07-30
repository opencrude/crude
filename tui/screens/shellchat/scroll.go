package shellchat

import (
	tea "charm.land/bubbletea/v2"
)

// handleScrollInput applies chat scroll keys/wheel to scroll. Returns true when
// the message was consumed. Text selection and main scrollbar drag are handled
// in Screen.Handle before this fallback runs.
func (s *Screen) handleScrollInput(msg tea.Msg) bool {
	if s == nil || s.list == nil {
		return false
	}
	scroll := s.list.Scroll()
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		switch m.Code {
		case tea.KeyPgUp:
			scroll.ScrollBy(-scroll.Viewport())
			scroll.SetAutoScroll(false)
			return true
		case tea.KeyPgDown:
			scroll.ScrollBy(scroll.Viewport())
			if scroll.ShouldAutoScroll() {
				scroll.StickToBottom()
			}
			return true
		case tea.KeyHome:
			scroll.ScrollTo(0)
			scroll.SetAutoScroll(false)
			return true
		case tea.KeyEnd:
			scroll.StickToBottom()
			return true
		}
	case tea.MouseWheelMsg:
		switch m.Button {
		case tea.MouseWheelUp:
			n := s.wheel.WheelUp()
			if n > 0 {
				n = -n
			}
			scroll.ScrollBy(n)
			scroll.SetAutoScroll(false)
			return true
		case tea.MouseWheelDown:
			n := s.wheel.WheelDown()
			scroll.ScrollBy(n)
			if scroll.ShouldAutoScroll() {
				scroll.StickToBottom()
			}
			return true
		}
	}
	return false
}
