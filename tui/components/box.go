package components

import "github.com/charmbracelet/lipgloss"

type Box struct {
	style lipgloss.Style
}

func NewBox() Box {
	return Box{}
}

func (b Box) Render(content string) string {
	return b.style.Render(content)
}
