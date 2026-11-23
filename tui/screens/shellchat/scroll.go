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
	if s.document && evt.Kind == ui.EventKey && evt.Key.Mod == 0 {
		switch evt.Key.Code {
		case tea.KeyUp:
			scroll.ScrollBy(-1)
		case tea.KeyDown:
			scroll.ScrollBy(1)
		case tea.KeyPgUp:
			scroll.ScrollBy(-max(1, scroll.Viewport()-1))
		case tea.KeyPgDown, tea.KeySpace:
			scroll.ScrollBy(max(1, scroll.Viewport()-1))
		case tea.KeyHome:
			scroll.ScrollTo(0)
		case tea.KeyEnd:
			scroll.ScrollTo(max(0, scroll.Content()-scroll.Viewport()))
		default:
			return false
		}
		return true
	}
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
	}
	return false
}
