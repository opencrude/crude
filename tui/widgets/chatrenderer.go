package widgets

import (
	"github.com/charmbracelet/glamour"

	"crude/tui/rendering"
)

func (m *ChatModel) MdRenderer() *glamour.TermRenderer {
	return m.makeMdRenderer()
}

func (m *ChatModel) makeMdRenderer() *glamour.TermRenderer {
	w := m.width
	if m.mdRenderer != nil && m.cachedWidth == w {
		return m.mdRenderer
	}
	r := rendering.MakeMdRenderer(w)
	if r != nil {
		m.mdRenderer = r
		m.cachedWidth = w
	}
	return r
}
