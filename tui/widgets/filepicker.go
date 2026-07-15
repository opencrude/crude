package widgets

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/rendering"
	"crude/tui/scrollbar"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/widget"
)

// FilePickerAction is returned by Update to tell the caller what
// the user did. The popup dispatcher uses this to route the result
// (Select → file chosen, Cancel → close).
type FilePickerAction int

const (
	FilePickerNone FilePickerAction = iota
	FilePickerSelect
	FilePickerCancel
)

// FilePickerHitTest returns the index of the entry under the given
// popup-local (X, Y), or -1 if the click missed an entry (e.g. on
// the path/filter header lines, the scrollbar, or below the entry
// list). Used by the popup mouse dispatcher to wire up click-on-
// entry, matching the file tree's "click selects" behavior.
//
// The flat window content renders these lines in order:
//
//	localY 0: current path
//	localY 1: filter row
//	localY 2: divider
//	localY 3..3+entryPageSize-1: entry rows
//	localY last: hint line
func (m FilePicker) HitTest(localX, localY int) int {
	if !m.active {
		return -1
	}
	if localY < 3 {
		return -1
	}
	entryRow := localY - 3
	entries := m.visibleEntries()
	entryPageSize := m.lastEntryPageSize
	if entryPageSize < 1 {
		entryPageSize = 1
	}
	if entryRow >= entryPageSize {
		return -1
	}
	start := 0
	if m.selected >= entryPageSize {
		start = m.selected - entryPageSize + 1
	}
	idx := start + entryRow
	if idx < 0 || idx >= len(entries) {
		return -1
	}
	// Skip the click if it's on the scrollbar column. The scrollbar
	// takes the rightmost scrollbar.ScrollbarWidth cells of the
	// content area, cached during the last render.
	sbW := 0
	if m.scrollbar.IsVisible() && len(entries) > entryPageSize {
		sbW = scrollbar.ScrollbarWidth
	}
	if sbW > 0 && localX >= m.lastContentWidth {
		return -1
	}
	return idx
}

// FilePickerMode selects attach-file vs open-folder behavior.
type FilePickerMode int

const (
	// FilePickerModeFile: pick a file (attach reference). Enter on dir navigates; Enter on file selects.
	FilePickerModeFile FilePickerMode = iota
	// FilePickerModeFolder: pick a directory. Only dirs listed; Enter navigates; confirm opens current dir.
	FilePickerModeFolder
)

type FilePicker struct {
	th                *theme.Theme
	root              string
	current           string
	entries           []fileEntry
	selected          int
	filter            string
	active            bool
	mode              FilePickerMode
	scrollbar         scrollbar.ScrollBar
	lastEntryPageSize int // cached from last ViewInner render
	lastContentWidth  int
	lastHeight        int
	actionX0          int
	actionX1          int
	lastClickIdx      int // double-click to enter/select
	lastClickTime     time.Time
}

type fileEntry struct {
	Name  string
	Path  string
	IsDir bool
}

func NewFilePicker(th *theme.Theme) FilePicker {
	return FilePicker{
		th:           th,
		scrollbar:    scrollbar.New(0),
		lastClickIdx: -1,
	}
}

func (m *FilePicker) Open(root string) {
	m.open(root, FilePickerModeFile)
}

// OpenFolder opens the picker in folder-select mode (welcome "Open Folder").
func (m *FilePicker) OpenFolder(root string) {
	m.open(root, FilePickerModeFolder)
}

func (m *FilePicker) open(root string, mode FilePickerMode) {
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
	m.mode = mode
	m.lastClickIdx = -1
	m.loadEntries()
}

func (m FilePicker) Mode() FilePickerMode { return m.mode }

func (m *FilePicker) Close()      { m.active = false }
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
			if m.selected < len(m.visibleEntries())-1 {
				m.selected++
			}
		case tea.KeyBackspace:
			if m.filter != "" {
				r := []rune(m.filter)
				m.filter = string(r[:len(r)-1])
				m.selected = 0
			} else if m.mode == FilePickerModeFolder || m.current != m.root {
				parent := filepath.Dir(m.current)
				if parent == m.current {
					break
				}
				m.current = parent
				m.selected = 0
				m.loadEntries()
			}
		case tea.KeyEnter:
			// Folder mode: ctrl+o / empty list → confirm current dir.
			if m.mode == FilePickerModeFolder && (msg.String() == "ctrl+o" || len(m.visibleEntries()) == 0) {
				return m, m.current, FilePickerSelect, nil
			}
			entries := m.visibleEntries()
			if len(entries) == 0 || m.selected < 0 || m.selected >= len(entries) {
				if m.mode == FilePickerModeFolder {
					return m, m.current, FilePickerSelect, nil
				}
				return m, "", FilePickerNone, nil
			}
			entry := entries[m.selected]
			if entry.IsDir {
				m.current = entry.Path
				m.selected = 0
				m.filter = ""
				m.lastClickIdx = -1
				m.loadEntries()
				return m, "", FilePickerNone, nil
			}
			if m.mode == FilePickerModeFolder {
				// Should not list files in folder mode; treat as no-op.
				return m, "", FilePickerNone, nil
			}
			return m, entry.Path, FilePickerSelect, nil
		default:
			if msg.String() == "ctrl+o" && m.mode == FilePickerModeFolder {
				return m, m.current, FilePickerSelect, nil
			}
			for _, r := range msg.Text {
				if r >= 32 {
					m.filter += string(r)
					m.selected = 0
				}
			}
		}

	case tea.MouseClickMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg:
		// Handle entry clicks: if the user clicks on an entry row,
		// select it. A second click on the same row (or Enter)
		// activates it via the keyboard path. This matches the
		// file tree's "click selects" UX.
		if clickMsg, ok := msg.(tea.MouseClickMsg); ok {
			mouse := clickMsg.Mouse()
			if clickMsg.Button == tea.MouseLeft {
				if m.mode == FilePickerModeFolder && mouse.Y == m.lastHeight-1 && mouse.X >= m.actionX0 && mouse.X <= m.actionX1 {
					return m, m.current, FilePickerSelect, nil
				}
				idx := m.HitTest(mouse.X, mouse.Y)
				if idx >= 0 {
					entries := m.visibleEntries()
					// Double-click same row: enter dir or select file.
					if m.lastClickIdx == idx && time.Since(m.lastClickTime) <= 500*time.Millisecond && idx < len(entries) {
						m.lastClickIdx = -1
						e := entries[idx]
						if e.IsDir {
							m.current = e.Path
							m.selected = 0
							m.filter = ""
							m.loadEntries()
						} else if m.mode == FilePickerModeFile {
							return m, e.Path, FilePickerSelect, nil
						}
						return m, "", FilePickerNone, nil
					}
					m.selected = idx
					m.lastClickIdx = idx
					m.lastClickTime = time.Now()
					entryPageSize := m.scrollPageSize()
					if entryPageSize < 1 {
						entryPageSize = 1
					}
					scrollStart := 0
					if m.selected >= entryPageSize {
						scrollStart = m.selected - entryPageSize + 1
					}
					cmd = m.scrollbar.SetScrollInfo(len(entries), entryPageSize, scrollStart)
				}
			}
		}
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
				Y:      mouse.Y - 3,
				Button: mm.Button,
			}
		case tea.MouseMotionMsg:
			mouse := mm.Mouse()
			translatedMsg = tea.MouseMotionMsg{
				X:      mouse.X,
				Y:      mouse.Y - 3,
				Button: mm.Button,
			}
		case tea.MouseReleaseMsg:
			mouse := mm.Mouse()
			translatedMsg = tea.MouseReleaseMsg{
				X:      mouse.X,
				Y:      mouse.Y - 3,
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

// ViewInner renders the flat file-picker body. The FloatingWindow owns the
// draggable title bar; this widget deliberately draws no border or chrome.
func (m *FilePicker) ViewInner(width, height, spinFrame int) []string {
	if width < 10 || height < 5 {
		return nil
	}

	entries := m.visibleEntries()
	entryPageSize := height - 4 // location, filter, divider, and action row
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
	contentW := width - sbW
	if contentW < 1 {
		contentW = 1
	}
	m.lastContentWidth = contentW
	m.lastHeight = height

	bgStyle := lipgloss.NewStyle().Background(m.th.Colors.SubPaneBg)
	mutedStyle := lipgloss.NewStyle().Foreground(m.th.Colors.Muted).Background(m.th.Colors.SubPaneBg)
	labelStyle := lipgloss.NewStyle().Foreground(m.th.Colors.PopupTitleFocused).Background(m.th.Colors.SubPaneBg).Bold(true)
	filterStyle := lipgloss.NewStyle().Foreground(m.th.Colors.Fg).Background(m.th.Colors.SubPaneBg)
	entryStyle := lipgloss.NewStyle().Foreground(m.th.Colors.Fg).Background(m.th.Colors.SubPaneBg)
	dirStyle := lipgloss.NewStyle().Foreground(m.th.Colors.Accent2).Background(m.th.Colors.SubPaneBg)
	selectedStyle := lipgloss.NewStyle().
		Foreground(m.th.Colors.Fg).
		Background(m.th.Colors.PopupSubtleBg).
		Bold(true)
	dividerStyle := lipgloss.NewStyle().Foreground(m.th.Colors.PopupDivider).Background(m.th.Colors.SubPaneBg)

	padTo := func(s string, w int) string {
		if w < 1 {
			return ""
		}
		s = ansi.Truncate(s, w, "")
		if n := w - rendering.VisibleWidth(s); n > 0 {
			s += strings.Repeat(" ", n)
		}
		return s
	}

	var body []string
	pathLabel := "Location  "
	pathW := contentW - rendering.VisibleWidth(pathLabel)
	if pathW < 1 {
		pathW = 1
	}
	pathLine := labelStyle.Render(pathLabel) + mutedStyle.Render(ansi.Truncate(m.current, pathW, "..."))
	body = append(body, padTo(pathLine, contentW))
	filterValue := m.filter
	if filterValue == "" {
		filterValue = "Type to filter"
		filterValue = mutedStyle.Render(filterValue)
	} else {
		filterValue = filterStyle.Render(filterValue)
	}
	body = append(body, padTo(labelStyle.Render("Filter    ")+filterValue, contentW))
	body = append(body, dividerStyle.Render(strings.Repeat("─", contentW)))

	for i := start; i < end; i++ {
		entry := entries[i]
		marker := "  "
		name := entry.Name
		if entry.IsDir {
			if entry.Name == ".." {
				name = "../  Parent folder"
			} else {
				name += "/"
			}
		}
		if i == m.selected {
			marker = "› "
			name = selectedStyle.Render(padTo(marker+name, contentW))
		} else if entry.IsDir {
			name = padTo(marker+dirStyle.Render(name), contentW)
		} else {
			name = padTo(marker+entryStyle.Render(name), contentW)
		}
		body = append(body, name)
	}
	if len(entries) == 0 {
		empty := "No matching folders"
		if m.mode == FilePickerModeFile {
			empty = "No matching files"
		}
		body = append(body, mutedStyle.Render("  "+empty))
	}
	for len(body) < 3+entryPageSize {
		body = append(body, bgStyle.Render(strings.Repeat(" ", contentW)))
	}

	// Build scrollbar lines aligned with the entry area
	var sbLines []string
	if sbW > 0 {
		sbView := m.scrollbar.View()
		if sbView != "" {
			sbLines = strings.Split(sbView, "\n")
		}
	}

	result := make([]string, 0, height)
	for i, line := range body {
		padded := padTo(line, contentW)
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

	keyStyle := lipgloss.NewStyle().Foreground(m.th.Colors.Fg).Background(m.th.Colors.SubPaneBg).Bold(true)
	hintStyle := lipgloss.NewStyle().Foreground(m.th.Colors.Muted).Background(m.th.Colors.SubPaneBg)
	hintSegment := func(key, desc string) string {
		return keyStyle.Render(key) + hintStyle.Render(" "+desc)
	}
	var hint string
	if m.mode == FilePickerModeFolder {
		action := lipgloss.NewStyle().
			Foreground(m.th.Colors.PopupButtonFg).
			Background(m.th.Colors.PopupButtonBg).
			Bold(true).
			Render(" Open this folder ")
		m.actionX0 = 0
		m.actionX1 = rendering.VisibleWidth(action) - 1
		if m.actionX1 >= contentW {
			m.actionX1 = contentW - 1
		}
		hint = action + hintStyle.Render("  ") + strings.Join([]string{
			hintSegment("enter", "browse"),
			hintSegment("esc", "close"),
		}, hintStyle.Render("  ·  "))
	} else {
		m.actionX0, m.actionX1 = -1, -1
		hint = strings.Join([]string{
			hintSegment("enter", "select"),
			hintSegment("esc", "close"),
		}, hintStyle.Render("  ·  "))
	}
	hintLine := padTo(hint, contentW)
	if sbW > 0 {
		hintLine = hintLine + strings.Repeat(" ", sbW)
	}
	result = append(result, hintLine)

	for len(result) < height {
		result = append(result, bgStyle.Render(strings.Repeat(" ", width)))
	}
	return result[:height]
}

// Render implements widget.Widget. The window manager draws the flat,
// draggable title bar; this renders only the content surface.
func (m *FilePicker) Render(surf *surface.Surface, ctx *widget.RenderContext) {
	w, h := surf.Width(), surf.Height()
	if w < 10 || h < 4 {
		return
	}
	lines := m.ViewInner(w, h, ctx.SpinFrame)
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
	// Parent dir when not at filesystem root (folder mode always; file mode when not at open root).
	if filepath.Dir(m.current) != m.current {
		if m.mode == FilePickerModeFolder || m.current != m.root {
			m.entries = append(m.entries, fileEntry{Name: "..", Path: filepath.Dir(m.current), IsDir: true})
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		isDir := entry.IsDir()
		if m.mode == FilePickerModeFolder && !isDir {
			continue
		}
		m.entries = append(m.entries, fileEntry{Name: name, Path: filepath.Join(m.current, name), IsDir: isDir})
	}
	start := 0
	if len(m.entries) > 0 && m.entries[0].Name == ".." {
		start = 1
	}
	rest := m.entries[start:]
	sort.Slice(rest, func(i, j int) bool {
		if rest[i].IsDir != rest[j].IsDir {
			return rest[i].IsDir
		}
		return strings.ToLower(rest[i].Name) < strings.ToLower(rest[j].Name)
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

// FilePickerSelectMsg is sent when the user selects a file or folder.
// Folder is true when the picker was opened with OpenFolder.
type FilePickerSelectMsg struct {
	Path   string
	Folder bool
}

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
		folder := m.mode == FilePickerModeFolder
		m.Close()
		selectCmd := func() tea.Msg { return FilePickerSelectMsg{Path: path, Folder: folder} }
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
