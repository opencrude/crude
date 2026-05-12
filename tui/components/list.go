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

type ListConfig struct {
	ShowFilter bool
	ShowFooter bool
	ShowCount  bool
	Floating   bool
}

func defaultPanelConfig() ListConfig {
	return ListConfig{
		ShowFilter: true,
		ShowFooter: false,
		ShowCount:  true,
		Floating:   false,
	}
}

func defaultPopupConfig() ListConfig {
	return ListConfig{
		ShowFilter: false,
		ShowFooter: false,
		ShowCount:  false,
		Floating:   true,
	}
}

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
	config     ListConfig

	selectedID string

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
		if m.config.Floating {
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
		if m.config.ShowFilter && len(m.filter) > 0 {
			runes := []rune(m.filter)
			m.filter = string(runes[:len(runes)-1])
			m.applyFilter()
		}
	default:
		if m.config.ShowFilter {
			for _, r := range keyMsg.Runes {
				if unicode.IsPrint(r) {
					m.filter += string(r)
					m.applyFilter()
				}
			}
		}
	}

	return m, nil
}

func (m List) View(width int) string {
	if m.config.Floating {
		return m.viewPopup(width)
	}
	return m.viewPanel(width)
}

func (m List) viewPopup(width int) string {
	accent := m.theme.Styles.InputPrompt
	dim := m.theme.Styles.DimLabel
	highlight := m.theme.Styles.Highlight

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
		lines = append(lines, dim.Render(fmt.Sprintf("   ... %d above", aboveCount)))
	}
	for i := m.offset; i < end; i++ {
		item := m.filtered[i]
		if i == m.idx {
			marker := accent.Render("❯")
			label := highlight.Render(item.Label)
			detail := ""
			if item.Detail != "" {
				detail = " " + dim.Render(item.Detail)
			}
			lines = append(lines, " "+marker+" "+label+detail)
		} else {
			label := dim.Render(item.Label)
			detail := ""
			if item.Detail != "" {
				detail = " " + dim.Render(item.Detail)
			}
			lines = append(lines, "   "+label+detail)
		}
	}
	if belowCount > 0 {
		lines = append(lines, dim.Render(fmt.Sprintf("   ... %d below", belowCount)))
	}

	return strings.Join(lines, "\n")
}

func (m List) viewPanel(width int) string {
	accent := m.theme.Colors.Accent
	dim := m.theme.Styles.DimLabel
	fg := m.theme.Colors.Fg
	muted := m.theme.Colors.Muted
	pad := m.theme.Spacing.PadLR
	indent := strings.Repeat(" ", pad)
	sepWidth := width - pad*2
	if sepWidth < 20 {
		sepWidth = 20
	}

	var b strings.Builder

	if m.title != "" {
		titleStyle := lipgloss.NewStyle().Foreground(accent).Bold(true)
		b.WriteString(indent + titleStyle.Render(m.title))
		b.WriteString("\n")
		b.WriteString(dim.Render(indent + strings.Repeat("─", sepWidth)) + "\n")
	}

	if m.fetchError != "" {
		b.WriteString(indent + m.theme.Styles.ErrorText.Render("✗ "+m.fetchError) + "\n")
	} else if m.config.ShowFilter {
		filterPrompt := lipgloss.NewStyle().Foreground(muted).Render("/ ")
		searchText := lipgloss.NewStyle().Foreground(fg).Render(m.filter)
		cursor := " "
		if m.spinFrame%2 == 0 {
			cursor = lipgloss.NewStyle().Foreground(accent).Render("█")
		}
		b.WriteString(indent + filterPrompt + searchText + cursor + "\n")
	}

	b.WriteString(dim.Render(indent + strings.Repeat("─", sepWidth)) + "\n")

	if m.fetching {
		spinner := spinnerPhases[m.spinFrame%len(spinnerPhases)]
		spinnerStyle := lipgloss.NewStyle().Foreground(accent)
		b.WriteString(indent + spinnerStyle.Render(spinner) + " " + dim.Render("waiting for models...") + "\n")
	} else if len(m.filtered) == 0 {
		if m.filter != "" {
			b.WriteString(dim.Render(indent + "no matches — press enter to use typed value") + "\n")
		} else if m.fetchError != "" {
		} else {
			b.WriteString(dim.Render(indent + "no models available") + "\n")
		}
	} else {
		start := m.offset
		end := m.offset + m.pageSize
		if end > len(m.filtered) {
			end = len(m.filtered)
		}

		lineWidth := width - pad*2 - 2

		maxLabelW := 0
		maxDetailW := 0
		for _, item := range m.filtered {
			lw := runewidth.StringWidth(item.Label)
			dw := runewidth.StringWidth(item.Detail)
			if lw > maxLabelW {
				maxLabelW = lw
			}
			if dw > maxDetailW {
				maxDetailW = dw
			}
		}

		labelColW := maxLabelW
		detailColW := maxDetailW
		gap := 2
		if labelColW+detailColW+gap > lineWidth {
			detailColW = lineWidth - labelColW - gap
			if detailColW < 0 {
				labelColW = lineWidth
				detailColW = 0
			}
		}

		for i := start; i < end; i++ {
			item := m.filtered[i]

			label := rendering.TruncateLine(item.Label, labelColW)
			labelPad := labelColW - runewidth.StringWidth(label)
			if labelPad < 0 {
				labelPad = 0
			}

			detailPart := ""
			if item.Detail != "" && detailColW > 0 {
				d := rendering.TruncateLine(item.Detail, detailColW)
				detailPart = lipgloss.NewStyle().Foreground(muted).Render(strings.Repeat(" ", labelPad+gap) + d)
			}

			isCursor := i == m.idx
			isSelected := m.selectedID != "" && item.ID == m.selectedID

			if isCursor {
				marker := lipgloss.NewStyle().Foreground(accent).Render("❯")
				labelRendered := lipgloss.NewStyle().Foreground(fg).Bold(true).Render(label)
				b.WriteString(indent + marker + " " + labelRendered + detailPart)
			} else if isSelected {
				marker := lipgloss.NewStyle().Foreground(muted).Render("❯")
				labelRendered := lipgloss.NewStyle().Foreground(fg).Render(label)
				b.WriteString(indent + marker + " " + labelRendered + detailPart)
			} else {
				labelRendered := lipgloss.NewStyle().Foreground(fg).Render(label)
				b.WriteString(indent + "  " + labelRendered + detailPart)
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

func (m *List) SetRenderMode(mode ListRenderMode) {
	m.renderMode = mode
	switch mode {
	case ListRenderPanel:
		m.config = defaultPanelConfig()
	case ListRenderPopup:
		m.config = defaultPopupConfig()
	}
}

func (m *List) SetConfig(cfg ListConfig) {
	m.config = cfg
}

func (m *List) SetTitle(title string) {
	m.title = title
}

func (m *List) SetItems(items []ListItem) {
	m.items = items
	m.filtered = items
	m.offset = 0
	m.idx = 0
	if m.selectedID != "" {
		for i, item := range items {
			if item.ID == m.selectedID {
				m.idx = i
				break
			}
		}
	}
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

func (m *List) SetSelectedID(id string) {
	m.selectedID = id
}

func (m List) SelectedID() string {
	return m.selectedID
}

func (m List) Hint() string {
	var h string
	if m.config.Floating {
		h = "↑↓ navigate · enter select · esc cancel"
	} else if m.fetching {
		h = "fetching... · esc cancel"
	} else if len(m.filtered) == 0 {
		if m.filter != "" {
			h = "no matches · enter: use typed value · esc cancel"
		} else {
			h = "esc cancel"
		}
	} else {
		end := m.offset + m.pageSize
		if end > len(m.filtered) {
			end = len(m.filtered)
		}
		scrollHint := ""
		if m.offset > 0 || end < len(m.filtered) {
			scrollHint = fmt.Sprintf("%d/%d · ", m.idx+1, len(m.filtered))
		}
		h = scrollHint + "↑↓ navigate · / filter · enter select · esc cancel"
	}
	if h != "" {
		h = "[ " + h + " ]"
	}
	return h
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
