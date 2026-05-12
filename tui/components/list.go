package components

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"crude/tui/rendering"
	"crude/tui/theme"
)

type ListItem struct {
	ID     string
	Label  string
	Detail string
	Icon   string
}

type ListRenderMode int

const (
	ListRenderPanel ListRenderMode = iota
	ListRenderPopup
)

type ListAction int

const (
	ListActionNone ListAction = iota
	ListActionSelect
	ListActionTyped
	ListActionCancel
)

type List struct {
	theme      *theme.Theme
	title      string
	items      []ListItem
	filtered   []ListItem
	filter     string
	idx        int
	offset     int
	pageSize   int
	fetching   bool
	fetchError string
	spinFrame  int

	renderMode ListRenderMode

	FilterMatchFn func(item ListItem, filter string) bool

	action ListAction
}

func NewList(t *theme.Theme, pageSize int) List {
	return List{theme: t, pageSize: pageSize}
}

func (m List) Update(msg tea.Msg) (List, tea.Cmd) {
	m.action = ListActionNone

	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch keyMsg.Type {
	case tea.KeyEsc:
		if m.renderMode == ListRenderPopup {
			m.action = ListActionCancel
			return m, nil
		}
		m.filter = ""
		m.filtered = nil
		m.idx = 0
		m.offset = 0
		return m, nil
	case tea.KeyEnter:
		if len(m.filtered) > 0 {
			m.action = ListActionSelect
		} else {
			m.action = ListActionTyped
		}
		return m, nil
	case tea.KeyTab:
		if len(m.filtered) > 0 {
			m.action = ListActionSelect
		} else {
			m.action = ListActionCancel
		}
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
		if m.renderMode == ListRenderPopup {
			m.idx -= m.pageSize
			if m.idx < 0 {
				m.idx = 0
			}
			m.clampOffset()
		} else {
			m.idx -= m.pageSize
			if m.idx < 0 {
				m.idx = 0
			}
			m.clampOffset()
		}
	case tea.KeyPgDown:
		if m.renderMode == ListRenderPopup {
			m.idx += m.pageSize
			if m.idx >= len(m.filtered) {
				m.idx = len(m.filtered) - 1
			}
			m.clampOffset()
		} else {
			m.idx += m.pageSize
			if m.idx >= len(m.filtered) {
				m.idx = len(m.filtered) - 1
			}
			m.clampOffset()
		}
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
	if m.renderMode == ListRenderPopup {
		return m.viewPopup(width)
	}
	return m.viewPanel(width)
}

func (m List) viewPopup(width int) string {
	dim := m.theme.Styles.DimLabel
	highlight := m.theme.Styles.Highlight
	accent := m.theme.Styles.InputPrompt

	if len(m.filtered) == 0 {
		return ""
	}

	end := m.offset + m.pageSize
	if end > len(m.filtered) {
		end = len(m.filtered)
	}
	aboveCount := m.offset
	belowCount := len(m.filtered) - end

	var lines []string
	if aboveCount > 0 {
		lines = append(lines, dim.Render(fmt.Sprintf("  ... %d above", aboveCount)))
	}
	for i := m.offset; i < end; i++ {
		item := m.filtered[i]
		marker := ""
		if i == m.idx {
			marker = accent.Render("❯")
		}
		label := fmt.Sprintf("%-16s", item.Label)
		if i == m.idx {
			label = highlight.Render(label)
		} else {
			label = dim.Render(label)
		}
		lines = append(lines, " "+marker+" "+label+" "+dim.Render(item.Detail))
	}
	if belowCount > 0 {
		lines = append(lines, dim.Render(fmt.Sprintf("  ... %d below", belowCount)))
	}

	return strings.Join(lines, "\n")
}

func (m List) viewPanel(width int) string {
	accent := m.theme.Colors.Accent
	dim := m.theme.Styles.DimLabel

	var b strings.Builder

	if m.title != "" {
		titleStyle := lipgloss.NewStyle().Foreground(accent).Bold(true)
		b.WriteString("   " + titleStyle.Render(m.title))
		if m.fetching {
			b.WriteString(dim.Render("  fetching models..."))
		} else if len(m.items) > 0 {
			b.WriteString(dim.Render(fmt.Sprintf("  %d/%d", len(m.filtered), len(m.items))))
		}
		b.WriteString("\n")
	}

	if m.fetchError != "" {
		b.WriteString("   " + m.theme.Styles.ErrorText.Render("✗ "+m.fetchError) + "\n")
	} else {
		searchPrompt := lipgloss.NewStyle().Foreground(accent).Render(" ❯ / ")
		searchText := lipgloss.NewStyle().Foreground(m.theme.Colors.Fg).Render(m.filter)
		cursor := " "
		if m.spinFrame%2 == 0 {
			cursor = lipgloss.NewStyle().Foreground(accent).Render("█")
		}
		b.WriteString(searchPrompt + searchText + cursor + "\n")
	}

	b.WriteString(dim.Render("  " + strings.Repeat("─", 52)) + "\n")

	if m.fetching {
		b.WriteString(dim.Render("  waiting for models...") + "\n")
	} else if len(m.filtered) == 0 {
		if m.filter != "" {
			b.WriteString(dim.Render("  no matches — press enter to use typed value") + "\n")
		} else if m.fetchError != "" {
		} else {
			b.WriteString(dim.Render("  no models available") + "\n")
		}
	} else {
		start := m.offset
		end := m.offset + m.pageSize
		if end > len(m.filtered) {
			end = len(m.filtered)
		}

		for i := start; i < end; i++ {
			item := m.filtered[i]

			lineWidth := width - 4
			detailW := runewidth.StringWidth(item.Detail)
			labelW := lineWidth - detailW - 2
			if labelW < 2 {
				labelW = 2
			}

			label := rendering.TruncateLine(item.Label, labelW)
			padding := lineWidth - runewidth.StringWidth(label)
			if padding < 0 {
				padding = 0
			}

			if i == m.idx {
				highlightStyle := lipgloss.NewStyle().Foreground(m.theme.Colors.Fg).Background(m.theme.Colors.Selection)
				detail := lipgloss.NewStyle().Foreground(m.theme.Colors.Muted).Render(item.Detail)
				b.WriteString(lipgloss.NewStyle().Foreground(accent).Render(" ❯ ") +
					highlightStyle.Render(fmt.Sprintf("%-*s", lineWidth-runewidth.StringWidth(item.Detail)-2, label)) +
					detail)
			} else {
				detail := lipgloss.NewStyle().Foreground(m.theme.Colors.Muted).Render(item.Detail)
				b.WriteString("   " + lipgloss.NewStyle().Foreground(m.theme.Colors.Fg).Render(label) +
					strings.Repeat(" ", padding) + detail)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString(dim.Render("  " + strings.Repeat("─", 52)) + "\n")
	footer := "↑↓ navigate · enter select · esc cancel"
	if !m.fetching && len(m.filtered) > 0 {
		end := m.offset + m.pageSize
		if end > len(m.filtered) {
			end = len(m.filtered)
		}
		if m.offset > 0 || end < len(m.filtered) {
			scrollHint := fmt.Sprintf("%d–%d of %d", m.offset+1, end, len(m.filtered))
			if m.offset > 0 {
				scrollHint += " · ↑pgup"
			}
			if end < len(m.filtered) {
				scrollHint += " · ↓pgdn"
			}
			footer = scrollHint + " · enter select · esc cancel"
		}
	}
	b.WriteString(dim.Render("  " + footer) + "\n")

	if m.fetching && m.filter != "" {
		b.WriteString(dim.Render("  enter will use: ") +
			lipgloss.NewStyle().Foreground(m.theme.Colors.Fg).Render(m.filter) + "\n")
	}

	return b.String()
}

func (m *List) SetRenderMode(mode ListRenderMode) {
	m.renderMode = mode
}

func (m *List) SetTitle(title string) {
	m.title = title
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

func (m *List) SetSpinFrame(frame int) {
	m.spinFrame = frame
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

func (m List) Action() ListAction {
	return m.action
}

func (m *List) applyFilter() {
	m.filtered = nil
	m.idx = 0
	m.offset = 0
	s := strings.ToLower(m.filter)
	for _, item := range m.items {
		if m.FilterMatchFn != nil {
			if m.FilterMatchFn(item, s) {
				m.filtered = append(m.filtered, item)
			}
		} else {
			if strings.Contains(strings.ToLower(item.ID), s) || strings.Contains(strings.ToLower(item.Label), s) {
				m.filtered = append(m.filtered, item)
			}
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
