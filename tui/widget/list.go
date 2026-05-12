package widget

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

type ListItem struct {
	ID     string
	Label  string
	Detail string
}

type List struct {
	theme    *Theme
	items    []ListItem
	filtered []ListItem
	filter   string
	idx      int
	offset   int
	pageSize int
	fetching bool
	fetchError string
}

func NewList(t *Theme, pageSize int) List {
	return List{theme: t, pageSize: pageSize}
}

func (m List) Update(msg tea.Msg) (List, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch keyMsg.Type {
	case tea.KeyEsc:
		m.filter = ""
		m.filtered = nil
		m.idx = 0
		m.offset = 0
		return m, nil
	case tea.KeyEnter:
		return m, nil
	case tea.KeyUp:
		if len(m.filtered) > 0 {
			m.idx--
			if m.idx < 0 {
				m.idx = len(m.filtered) - 1
			}
			m.clampOffset()
		}
	case tea.KeyDown:
		if len(m.filtered) > 0 {
			m.idx++
			if m.idx >= len(m.filtered) {
				m.idx = 0
			}
			m.clampOffset()
		}
	case tea.KeyPgUp:
		m.idx -= m.pageSize
		if m.idx < 0 {
			m.idx = 0
		}
		m.clampOffset()
	case tea.KeyPgDown:
		m.idx += m.pageSize
		if m.idx >= len(m.filtered) {
			m.idx = len(m.filtered) - 1
		}
		m.clampOffset()
	case tea.KeyBackspace, tea.KeyDelete:
		if len(m.filter) > 0 {
			runes := []rune(m.filter)
			m.filter = string(runes[:len(runes)-1])
			m.applyFilter()
		}
	default:
		for _, r := range keyMsg.Runes {
			if unicode.IsPrint(r) {
				m.filter += string(r)
				m.applyFilter()
			}
		}
	}

	return m, nil
}

func (m List) View(width int) string {
	var b strings.Builder

	dim := m.theme.Styles.DimLabel
	highlight := m.theme.Styles.Highlight

	if m.fetching {
		b.WriteString(dim.Render("  fetching models..."))
		b.WriteString("\n")
	} else if m.fetchError != "" {
		b.WriteString(m.theme.Styles.ErrorText.Render("  " + m.fetchError))
		b.WriteString("\n")
	}

	visible := m.filtered
	totalDisplay := len(visible)
	start := m.offset
	end := m.offset + m.pageSize
	if end > totalDisplay {
		end = totalDisplay
	}

	for i := start; i < end; i++ {
		item := visible[i]

		lineWidth := width - 4
		detailW := runewidthStringWidth(item.Detail)
		labelW := lineWidth - detailW - 2
		if labelW < 2 {
			labelW = 2
		}

		label := TruncateLine(item.Label, labelW)

		padding := lineWidth - runewidthStringWidth(label)
		if padding < 0 {
			padding = 0
		}

		if i == m.idx {
			line := fmt.Sprintf("  ❯ %s%s%s", label, strings.Repeat(" ", padding), item.Detail)
			b.WriteString(highlight.Render(line))
		} else {
			b.WriteString(fmt.Sprintf("   %s%s%s", label, strings.Repeat(" ", padding), item.Detail))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func (m *List) SetItems(items []ListItem) {
	m.items = items
	m.filtered = items
	m.idx = 0
	m.offset = 0
}

func (m List) Items() []ListItem {
	return m.items
}

func (m *List) SetFetching(fetching bool) {
	m.fetching = fetching
}

func (m *List) SetFetchError(err string) {
	m.fetchError = err
}

func (m *List) SetFilter(s string) {
	m.filter = s
	m.applyFilter()
}

func (m List) Selected() (ListItem, bool) {
	if len(m.filtered) > 0 && m.idx >= 0 && m.idx < len(m.filtered) {
		return m.filtered[m.idx], true
	}
	return ListItem{}, false
}

func (m List) TypedValue() string {
	if len(m.filtered) == 0 {
		return m.filter
	}
	return ""
}

func (m *List) applyFilter() {
	m.filtered = nil
	m.idx = 0
	m.offset = 0
	s := strings.ToLower(m.filter)
	for _, item := range m.items {
		if strings.Contains(strings.ToLower(item.ID), s) || strings.Contains(strings.ToLower(item.Label), s) {
			m.filtered = append(m.filtered, item)
		}
	}
}

func (m *List) clampOffset() {
	if m.idx < m.offset {
		m.offset = m.idx
	}
	if m.idx >= m.offset+m.pageSize {
		m.offset = m.idx - m.pageSize + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func runewidthStringWidth(s string) int {
	// simple string width without ANSI
	w := 0
	for _, r := range s {
		if r == '\x1b' {
			break
		}
		w++
	}
	return w
}
