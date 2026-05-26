package widgets

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/rendering"
	"crude/tui/theme"
)

type FilePickerAction int

const (
	FilePickerNone FilePickerAction = iota
	FilePickerSelect
	FilePickerCancel
)

type FilePicker struct {
	th       *theme.Theme
	root     string
	current  string
	entries  []fileEntry
	selected int
	filter   string
	active   bool
}

type fileEntry struct {
	Name  string
	Path  string
	IsDir bool
}

func NewFilePicker(th *theme.Theme) FilePicker {
	return FilePicker{th: th}
}

func (m *FilePicker) Open(root string) {
	if root == "" {
		root, _ = os.Getwd()
	}
	abs, err := filepath.Abs(root)
	if err == nil {
		root = abs
	}
	m.root = root
	m.current = root
	m.selected = 0
	m.filter = ""
	m.active = true
	m.loadEntries()
}

func (m *FilePicker) Close() { m.active = false }
func (m FilePicker) IsOpen() bool { return m.active }

func (m FilePicker) Update(msg tea.Msg) (FilePicker, string, FilePickerAction) {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, "", FilePickerNone
	}
	switch keyMsg.Code {
	case tea.KeyEsc:
		return m, "", FilePickerCancel
	case tea.KeyUp:
		if m.selected > 0 {
			m.selected--
		}
	case tea.KeyDown:
		if m.selected < len(m.entries)-1 {
			m.selected++
		}
	case tea.KeyBackspace:
		if m.filter != "" {
			r := []rune(m.filter)
			m.filter = string(r[:len(r)-1])
		} else if m.current != m.root {
			m.current = filepath.Dir(m.current)
			m.selected = 0
			m.loadEntries()
		}
	case tea.KeyEnter:
		if len(m.entries) == 0 || m.selected < 0 || m.selected >= len(m.entries) {
			return m, "", FilePickerNone
		}
		entry := m.entries[m.selected]
		if entry.IsDir {
			m.current = entry.Path
			m.selected = 0
			m.filter = ""
			m.loadEntries()
			return m, "", FilePickerNone
		}
		return m, entry.Path, FilePickerSelect
	default:
		for _, r := range keyMsg.Text {
			if r >= 32 {
				m.filter += string(r)
			}
		}
	}
	return m, "", FilePickerNone
}

func (m FilePicker) View(width, height, spinFrame int) []string {
	boxW := width * 72 / 100
	if boxW > 100 {
		boxW = 100
	}
	if boxW < 32 {
		boxW = 32
	}
	innerW := boxW - 4
	bodyH := height*68/100 - 4
	if bodyH < 4 {
		bodyH = 4
	}
	entries := m.visibleEntries()
	start := 0
	if m.selected >= bodyH {
		start = m.selected - bodyH + 1
	}
	end := start + bodyH
	if end > len(entries) {
		end = len(entries)
	}
	var body []string
	pathLine := lipgloss.NewStyle().Foreground(m.th.Colors.Muted).Render(rendering.TruncateLine(m.current, innerW))
	body = append(body, pathLine)
	filter := "/ " + m.filter
	body = append(body, lipgloss.NewStyle().Foreground(m.th.Colors.Accent2).Render(rendering.TruncateLine(filter, innerW)))
	for i := start; i < end; i++ {
		entry := entries[i]
		icon := "  "
		if entry.IsDir {
			icon = "▸ "
		}
		name := icon + entry.Name
		if i == m.selected {
			name = lipgloss.NewStyle().Foreground(m.th.Colors.Fg).Background(m.th.Colors.Selection).Render(rendering.TruncateLine(name, innerW))
		} else {
			name = lipgloss.NewStyle().Foreground(m.th.Colors.Muted).Render(rendering.TruncateLine(name, innerW))
		}
		body = append(body, name)
	}
	return RenderFloatingBox(m.th, "Select file", body, "enter select · esc cancel · type filter", width, height)
}

// ViewInner renders the file picker content for embedding inside a container
// (e.g., a FloatingWindow). Unlike View(), which wraps content in a centered
// RenderFloatingBox with its own border, ViewInner renders the content directly
// to fill the available area. The container provides the border and title bar.
//
// Use View() for standalone rendering (with border/centering).
// Use ViewInner() for embedded rendering inside a container (without border).
func (m FilePicker) ViewInner(width, height, spinFrame int) []string {
	if width < 10 || height < 4 {
		return nil
	}

	innerW := width - 4
	if innerW < 1 {
		innerW = 1
	}
	bodyH := height - 1 // reserve 1 line for hint
	if bodyH < 4 {
		bodyH = 4
	}

	entries := m.visibleEntries()
	start := 0
	if m.selected >= bodyH {
		start = m.selected - bodyH + 1
	}
	end := start + bodyH
	if end > len(entries) {
		end = len(entries)
	}

	var body []string
	pathLine := lipgloss.NewStyle().Foreground(m.th.Colors.Muted).Render(rendering.TruncateLine(m.current, innerW))
	body = append(body, pathLine)
	filter := "/ " + m.filter
	body = append(body, lipgloss.NewStyle().Foreground(m.th.Colors.Accent2).Render(rendering.TruncateLine(filter, innerW)))
	for i := start; i < end; i++ {
		entry := entries[i]
		icon := "  "
		if entry.IsDir {
			icon = "▸ "
		}
		name := icon + entry.Name
		if i == m.selected {
			name = lipgloss.NewStyle().Foreground(m.th.Colors.Fg).Background(m.th.Colors.Selection).Render(rendering.TruncateLine(name, innerW))
		} else {
			name = lipgloss.NewStyle().Foreground(m.th.Colors.Muted).Render(rendering.TruncateLine(name, innerW))
		}
		body = append(body, name)
	}

	dim := lipgloss.NewStyle().Foreground(m.th.Colors.Muted)

	result := make([]string, 0, height)
	for _, line := range body {
		result = append(result, "  "+padRight(line, innerW)+"  ")
	}

	hint := "enter select · esc cancel · type filter"
	result = append(result, "  "+dim.Render(padRight(rendering.TruncateLine(hint, innerW), innerW))+"  ")

	// Fill remaining lines with blanks to cover the content area.
	for len(result) < height {
		result = append(result, strings.Repeat(" ", width))
	}

	return result
}

func (m *FilePicker) loadEntries() {
	entries, err := os.ReadDir(m.current)
	if err != nil {
		m.entries = nil
		return
	}
	m.entries = m.entries[:0]
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		m.entries = append(m.entries, fileEntry{Name: name, Path: filepath.Join(m.current, name), IsDir: entry.IsDir()})
	}
	sort.Slice(m.entries, func(i, j int) bool {
		if m.entries[i].IsDir != m.entries[j].IsDir {
			return m.entries[i].IsDir
		}
		return strings.ToLower(m.entries[i].Name) < strings.ToLower(m.entries[j].Name)
	})
}

func (m FilePicker) visibleEntries() []fileEntry {
	if m.filter == "" {
		return m.entries
	}
	filter := strings.ToLower(m.filter)
	var out []fileEntry
	for _, entry := range m.entries {
		if strings.Contains(strings.ToLower(entry.Name), filter) {
			out = append(out, entry)
		}
	}
	return out
}
