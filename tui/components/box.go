package components

import "charm.land/lipgloss/v2"

type Box struct {
	style lipgloss.Style
}

func NewBox() Box {
	return Box{}
}

func (b Box) Render(content string) string {
	return b.style.Render(content)
}
