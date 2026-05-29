package widgets

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/rendering"
	"crude/tui/scrollbar"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/widget"
)

type FilePickerAction int

const (
	FilePickerNone FilePickerAction = iota
	FilePickerSelect
	FilePickerCancel
)

type FilePicker struct {
	th                  *theme.Theme
	root                string
	current             string
	entries             []fileEntry
	selected            int
	filter              string
	active              bool
	scrollbar           scrollbar.ScrollBar
	lastEntryPageSize   int // cached from last ViewInner render
}

type fileEntry struct {
	Name  string
	Path  string
	IsDir bool
}

func NewFilePicker(th *theme.Theme) FilePicker {
	return FilePicker{
		th:        th,
		scrollbar: scrollbar.New(0),
	}
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

func (m FilePicker) Update(msg tea.Msg) (FilePicker, string, FilePickerAction, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		entries := m.visibleEntries()
		switch msg.Button {
		case tea.MouseWheelUp:
			if m.selected > 0 {
				m.selected--
			}
		case tea.MouseWheelDown:
			if m.selected < len(entries)-1 {
				m.selected++
			}
		}
		// Update scrollbar scroll info based on current position
		entryPageSize := m.scrollPageSize()
		if entryPageSize < 1 {
			entryPageSize = 1
		}
		scrollStart := 0
		if m.selected >= entryPageSize {
			scrollStart = m.selected - entryPageSize + 1
		}
		cmd = m.scrollbar.SetScrollInfo(len(entries), entryPageSize, scrollStart)

	case tea.KeyPressMsg:
		switch msg.Code {
		case tea.KeyEsc:
			return m, "", FilePickerCancel, nil
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
				return m, "", FilePickerNone, nil
			}
			entry := m.entries[m.selected]
			if entry.IsDir {
				m.current = entry.Path
				m.selected = 0
				m.filter = ""
				m.loadEntries()
				return m, "", FilePickerNone, nil
			}
			return m, entry.Path, FilePickerSelect, nil
		default:
			for _, r := range msg.Text {
				if r >= 32 {
					m.filter += string(r)
				}
			}
		}

	case tea.MouseClickMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg:
		// Forward mouse events to the scrollbar for thumb dragging.
		// Translate mouse Y from screen-absolute to track-relative
		// coordinates, since the scrollbar expects Y relative to the
		// top of its track area (like MessageList does with chatAreaTop).
		var translatedMsg tea.Msg
		switch mm := msg.(type) {
		case tea.MouseClickMsg:
			mouse := mm.Mouse()
			translatedMsg = tea.MouseClickMsg{
				X:      mouse.X,
				Y:      mouse.Y - 2,
				Button: mm.Button,
			}
		case tea.MouseMotionMsg:
			mouse := mm.Mouse()
			translatedMsg = tea.MouseMotionMsg{
				X:      mouse.X,
				Y:      mouse.Y - 2,
				Button: mm.Button,
			}
		case tea.MouseReleaseMsg:
			mouse := mm.Mouse()
			translatedMsg = tea.MouseReleaseMsg{
				X:      mouse.X,
				Y:      mouse.Y - 2,
				Button: mm.Button,
			}
		}
		var sbCmd tea.Cmd
		m.scrollbar, sbCmd = m.scrollbar.Update(translatedMsg)
		if sbCmd != nil {
			cmd = sbCmd
		}
		// If the scrollbar is being dragged, read the drag-driven scroll
		// position and update the selection accordingly. Do NOT overwrite
		// scrollY with a selection-derived value (that snaps the thumb back).
		if m.scrollbar.IsDragging() {
			if m.scrollbar.ConsumeScrollChanged() {
				newScrollY := m.scrollbar.GetScrollY()
				// scrollY is the first visible entry index (scrollStart).
				// Set selected to the last visible entry in the range.
				entryPageSize := m.scrollPageSize()
				if entryPageSize < 1 {
					entryPageSize = 1
				}
				newSelected := newScrollY + entryPageSize - 1
				totalEntries := len(m.visibleEntries())
				if totalEntries > 0 {
					if newSelected < 0 {
						newSelected = 0
					}
					if newSelected >= totalEntries {
						newSelected = totalEntries - 1
					}
					m.selected = newSelected
				}
			}
			// Update scrollbar dimensions but preserve the drag-driven scrollY
			// so the thumb stays where the user dragged it.
			entries := m.visibleEntries()
			entryPageSize := m.scrollPageSize()
			if entryPageSize < 1 {
				entryPageSize = 1
			}
			if cmd == nil {
				cmd = m.scrollbar.SetScrollInfo(len(entries), entryPageSize, m.scrollbar.GetScrollY())
			}
		}
	}

	// Update scrollbar on any activity (skip during thumb drag — the
	// IsDragging block above already updates the scrollbar position
	// and preserves the drag-driven scrollY).
	if !m.scrollbar.IsDragging() {
		entries := m.visibleEntries()
		entryPageSize := m.scrollPageSize()
		if entryPageSize < 1 {
			entryPageSize = 1
		}
		scrollStart := 0
		if m.selected >= entryPageSize {
			scrollStart = m.selected - entryPageSize + 1
		}
		if cmd == nil {
			cmd = m.scrollbar.SetScrollInfo(len(entries), entryPageSize, scrollStart)
		}
	}

	return m, "", FilePickerNone, cmd
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
	entryPageSize := bodyH - 2 // body includes 2 header lines (path + filter)
	if entryPageSize < 1 {
		entryPageSize = 1
	}
	start := 0
	if m.selected >= entryPageSize {
		start = m.selected - entryPageSize + 1
	}
	end := start + entryPageSize
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
// ViewInner is also called by Render() (widget.Widget interface) to produce
// the cell content that is written to the surface.
//
// Use View() for standalone rendering (with border/centering).
// Use ViewInner() for embedded rendering inside a container (without border).
func (m *FilePicker) ViewInner(width, height, spinFrame int) []string {
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
	entryPageSize := bodyH - 2 // body includes 2 header lines (path + filter)
	if entryPageSize < 1 {
		entryPageSize = 1
	}
	m.lastEntryPageSize = entryPageSize

	start := 0
	if m.selected >= entryPageSize {
		start = m.selected - entryPageSize + 1
	}
	end := start + entryPageSize
	if end > len(entries) {
		end = len(entries)
	}

	// Update scrollbar state
	scrollStart := start
	m.scrollbar.SetTrackHeight(entryPageSize)
	// SetScrollInfo returns a tea.Cmd for fade timer; since ViewInner is a
	// render method (not an Update method), we call it only for the side
	// effect of updating the scrollbar's internal position. The cmd is
	// discarded here; the scrollbar visibility is maintained through
	// Update() calls.
	m.scrollbar.SetScrollInfo(len(entries), entryPageSize, scrollStart)

	// Account for scrollbar width in content area
	sbW := 0
	if m.scrollbar.IsVisible() && len(entries) > entryPageSize {
		sbW = scrollbar.ScrollbarWidth
	}
	contentW := innerW - sbW
	if contentW < 1 {
		contentW = 1
	}

	var body []string
	pathLine := lipgloss.NewStyle().Foreground(m.th.Colors.Muted).Render(rendering.TruncateLine(m.current, contentW))
	body = append(body, pathLine)
	filter := "/ " + m.filter
	body = append(body, lipgloss.NewStyle().Foreground(m.th.Colors.Accent2).Render(rendering.TruncateLine(filter, contentW)))
	for i := start; i < end; i++ {
		entry := entries[i]
		icon := "  "
		if entry.IsDir {
			icon = "▸ "
		}
		name := icon + entry.Name
		if i == m.selected {
			name = lipgloss.NewStyle().Foreground(m.th.Colors.Fg).Background(m.th.Colors.Selection).Render(rendering.TruncateLine(name, contentW))
		} else {
			name = lipgloss.NewStyle().Foreground(m.th.Colors.Muted).Render(rendering.TruncateLine(name, contentW))
		}
		body = append(body, name)
	}

	// Build scrollbar lines aligned with the entry area
	var sbLines []string
	if sbW > 0 {
		sbView := m.scrollbar.View()
		if sbView != "" {
			sbLines = strings.Split(sbView, "\n")
		}
	}

	dim := lipgloss.NewStyle().Foreground(m.th.Colors.Muted)

	result := make([]string, 0, height)
	for i, line := range body {
		padded := "  " + padRight(line, contentW) + "  "
		// Append scrollbar character for entry lines (i >= 2), otherwise blank
		if sbW > 0 {
			if i >= 2 && i-2 < len(sbLines) {
				padded = padded + sbLines[i-2]
			} else {
				padded = padded + strings.Repeat(" ", sbW)
			}
		}
		result = append(result, padded)
	}

	hint := "enter select · esc cancel · type filter"
	hintLine := "  " + dim.Render(padRight(rendering.TruncateLine(hint, contentW), contentW)) + "  "
	if sbW > 0 {
		hintLine = hintLine + strings.Repeat(" ", sbW)
	}
	result = append(result, hintLine)

	// Fill remaining lines with blanks to cover the content area.
	for len(result) < height {
		blank := strings.Repeat(" ", width)
		result = append(result, blank)
	}

	return result
}

// Render implements widget.Widget. Calls ViewInner to get the rendered
// lines, joins them, and writes the cells into the surface.
func (m *FilePicker) Render(surf *surface.Surface, ctx *widget.RenderContext) {
	lines := m.ViewInner(surf.Width(), surf.Height(), ctx.SpinFrame)
	if len(lines) == 0 {
		return
	}
	result := strings.Join(lines, "\n")
	rendering.RenderToSurface(surf, result)
}

// Measure implements widget.Widget. Returns the full available space,
// indicating the FilePicker is willing to fill whatever space it is given.
func (m *FilePicker) Measure(availW, availH int) (int, int) {
	return availW, availH
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

// scrollPageSize returns the number of entry lines that fit in the visible area.
func (m FilePicker) scrollPageSize() int {
	if m.lastEntryPageSize <= 0 {
		return 10 // reasonable default before first render
	}
	return m.lastEntryPageSize
}

// ScrollbarView returns the rendered scrollbar string for display alongside
// the entry list.
func (m FilePicker) ScrollbarView() string {
	return m.scrollbar.View()
}

// ScrollbarWidth returns the width occupied by the scrollbar (0 if hidden, 1 if visible).
func (m FilePicker) ScrollbarWidth() int {
	if m.scrollbar.IsVisible() && len(m.entries) > 0 {
		return scrollbar.ScrollbarWidth
	}
	return 0
}

// UpdateScrollbar processes scrollbar-specific messages (fade/hide timers).
func (m *FilePicker) UpdateScrollbar(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.scrollbar, cmd = m.scrollbar.Update(msg)
	return cmd
}

// FilePickerSelectMsg is sent when the user selects a file in the picker.
type FilePickerSelectMsg struct{ Path string }

// FilePickerCancelMsg is sent when the user cancels the file picker.
type FilePickerCancelMsg struct{}

// HandleEvent processes an input event in the file picker and returns any
// resulting commands (including messages for select/cancel actions).
// This method implements the generic interactive-widget contract so the
// App does not need to know about FilePicker internals.
func (m *FilePicker) HandleEvent(msg tea.Msg) tea.Cmd {
	switch msg.(type) {
	case scrollbar.FadeTickMsg, scrollbar.HideTickMsg:
		return m.UpdateScrollbar(msg)
	}
	updated, path, action, cmd := m.Update(msg)
	*m = updated
	switch action {
	case FilePickerSelect:
		m.Close()
		selectCmd := func() tea.Msg { return FilePickerSelectMsg{Path: path} }
		if cmd != nil {
			return tea.Batch(cmd, selectCmd)
		}
		return selectCmd
	case FilePickerCancel:
		m.Close()
		cancelCmd := func() tea.Msg { return FilePickerCancelMsg{} }
		if cmd != nil {
			return tea.Batch(cmd, cancelCmd)
		}
		return cancelCmd
	}
	return cmd
}
