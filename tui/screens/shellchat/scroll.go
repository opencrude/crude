package shellchat

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
)

// handleScrollInput applies chat scroll keys/wheel to scroll. Returns true when
// the event was consumed. Text selection and main scrollbar drag are handled
// in Screen.Handle before this fallback runs.
func (s *Screen) handleScrollInput(evt ui.Event) bool {
	if s == nil || s.list == nil {
		return false
	}
	scroll := s.list.Scroll()
	switch evt.Kind {
	case ui.EventKey:
		switch evt.Key.Code {
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
	case ui.EventMouseWheel:
		switch evt.MouseWheel.Button {
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
