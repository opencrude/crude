package widgets

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/fileio"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/widget"
)

// Visual thesis: Crude's industrial command surface, split into three calm
// horizontal bands: controls, virtual results, and a real editor preview.
// Content plan: query state -> paths and matching lines -> source context.
// Interaction thesis: selection previews instantly; Enter/double-click opens.

const findInFilesDebounce = 120 * time.Millisecond

// FindInFilesSearchRequestMsg is a UI intent emitted after debounce. The
// viewmodel routes it to workspace.ProjectSearchService; the widget never
// executes project I/O or owns cancellation.
type FindInFilesSearchRequestMsg struct {
	Owner      *FindInFiles
	Generation int
	Query      string
	Options    fileio.ProjectSearchOptions
}

// FindInFilesSearchCancelMsg asks the viewmodel to cancel workspace search.
type FindInFilesSearchCancelMsg struct{ Owner *FindInFiles }

type FindInFilesOpenMsg struct{ Match fileio.ProjectSearchMatch }
type FindInFilesCloseMsg struct{}

type FindInFiles struct {
	th    *theme.Theme
	root  string
	query *ui.TextEdit // shared single-line filter engine
	icons *filetree.IconResolver

	caseSensitive  bool
	regex          bool
	results        []fileio.ProjectSearchMatch
	selected       int
	scroll         int
	hovered        int
	hoveredControl int
	loading        bool
	err            string
	duration       time.Duration
	generation     int

	width, height  int
	resultY0       int
	resultH        int
	previewY       int
	previewH       int
	caseX0         int
	caseX1         int
	regexX0        int
	regexX1        int
	inputX         int
	inputStartRune int

	preview *editor.Editor
	scrollM *ui.ScrollModel

	lastClickIndex int
	lastClickAt    time.Time
	scrollDragging bool
}

func NewFindInFiles(th *theme.Theme, root string) *FindInFiles {
	icons := filetree.NewIconResolver()
	icons.ApplyTheme(&th.Colors.Tree)
	return &FindInFiles{
		th:             th,
		root:           root,
		query:          ui.NewTextEdit(),
		icons:          icons,
		selected:       -1,
		hovered:        -1,
		lastClickIndex: -1,
		scrollM:        ui.NewScrollModel(),
	}
}

func (m *FindInFiles) ensureQuery() *ui.TextEdit {
	if m.query == nil {
		m.query = ui.NewTextEdit()
	}
	return m.query
}

func (m *FindInFiles) Query() string                        { return m.ensureQuery().Value() }
func (m *FindInFiles) Results() []fileio.ProjectSearchMatch { return m.results }
func (m *FindInFiles) Selected() int                        { return m.selected }
func (m *FindInFiles) CaseSensitive() bool                  { return m.caseSensitive }
func (m *FindInFiles) Regex() bool                          { return m.regex }

func (m *FindInFiles) Close() {
	// Invalidate pending debounce messages. Workspace owns active search
	// cancellation and is called by the overlay OnClose path.
	m.generation++
	m.loading = false
}

func (m *FindInFiles) scheduleSearch() tea.Cmd {
	m.generation++
	gen := m.generation
	m.loading = strings.TrimSpace(m.Query()) != ""
	m.err = ""
	if strings.TrimSpace(m.Query()) == "" {
		m.results = nil
		m.selected = -1
		m.scroll = 0
		m.preview = nil
		m.loading = false
		return func() tea.Msg { return FindInFilesSearchCancelMsg{Owner: m} }
	}
	return tea.Tick(findInFilesDebounce, func(time.Time) tea.Msg {
		return FindInFilesSearchRequestMsg{
			Owner:      m,
			Generation: gen,
			Query:      m.Query(),
			Options: fileio.ProjectSearchOptions{
				CaseSensitive: m.caseSensitive,
				Regex:         m.regex,
			},
		}
	})
}

// AcceptsSearch reports whether a debounced request still belongs to the
// current query generation.
func (m *FindInFiles) AcceptsSearch(owner *FindInFiles, generation int) bool {
	return owner == m && generation == m.generation && strings.TrimSpace(m.Query()) != ""
}

// ApplySearchResults applies domain search output to presentation state.
func (m *FindInFiles) ApplySearchResults(generation int, results []fileio.ProjectSearchMatch, err error, duration time.Duration) {
	if generation != m.generation {
		return
	}
	m.loading = false
	m.duration = duration
	if err != nil {
		m.err = err.Error()
		m.results = nil
		m.selected = -1
		m.preview = nil
		return
	}
	m.err = ""
	m.results = results
	m.scroll = 0
	m.hovered = -1
	m.lastClickIndex = -1
	m.lastClickAt = time.Time{}
	if len(m.results) == 0 {
		m.selected = -1
		m.preview = nil
		return
	}
	m.selected = 0
	m.rebuildPreview()
}

func (m *FindInFiles) rebuildPreview() {
	if m.selected < 0 || m.selected >= len(m.results) || m.results[m.selected].NameOnly {
		m.preview = nil
		return
	}
	result := m.results[m.selected]
	preview := editor.NewPreview(m.th, result.Path, result.Context, result.ContextStart)
	localLine := result.Line - result.ContextStart
	if localLine < 0 {
		localLine = 0
	}
	lineRunes := []rune(result.LineText)
	startCol := rendering.RuneIdxToCol(lineRunes, result.Column)
	endCol := rendering.RuneIdxToCol(lineRunes, max(result.EndColumn, result.Column+1))
	preview.SetSelection(localLine, startCol, localLine, max(endCol, startCol+1))
	m.preview = preview
}

func (m *FindInFiles) selectIndex(idx int) {
	if len(m.results) == 0 {
		m.selected = -1
		m.preview = nil
		return
	}
	idx = max(0, min(idx, len(m.results)-1))
	if idx == m.selected {
		return
	}
	m.selected = idx
	m.ensureSelectedVisible()
	m.rebuildPreview()
}

func (m *FindInFiles) ensureSelectedVisible() {
	if m.resultH <= 0 || m.selected < 0 {
		return
	}
	if m.selected < m.scroll {
		m.scroll = m.selected
	}
	if m.selected >= m.scroll+m.resultH {
		m.scroll = m.selected - m.resultH + 1
	}
	m.clampScroll()
}

func (m *FindInFiles) clampScroll() {
	maxScroll := len(m.results) - m.resultH
	if maxScroll < 0 {
		maxScroll = 0
	}
	m.scroll = max(0, min(m.scroll, maxScroll))
}

func (m *FindInFiles) HandleEvent(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.PasteMsg:
		m.ensureQuery().InsertRunes(msg.Content)
		return m.scheduleSearch()
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.MouseClickMsg:
		return m.handleClick(msg)
	case tea.MouseMotionMsg:
		m.handleMotion(msg)
	case tea.MouseReleaseMsg:
		m.scrollDragging = false
	case tea.MouseWheelMsg:
		m.handleWheel(msg)
	}
	return nil
}

func (m *FindInFiles) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEsc:
		return func() tea.Msg { return FindInFilesCloseMsg{} }
	case tea.KeyEnter:
		return m.openSelectedCmd()
	case tea.KeyUp:
		m.selectIndex(m.selected - 1)
		return nil
	case tea.KeyDown:
		m.selectIndex(m.selected + 1)
		return nil
	case tea.KeyPgUp:
		m.selectIndex(m.selected - max(1, m.resultH))
		return nil
	case tea.KeyPgDown:
		m.selectIndex(m.selected + max(1, m.resultH))
		return nil
	}
	if msg.String() == "ctrl+r" {
		m.regex = !m.regex
		return m.scheduleSearch()
	}
	ed := m.ensureQuery()
	before := ed.Value()
	if ui.HandleFilterKey(ed, msg) && ed.Value() != before {
		return m.scheduleSearch()
	}
	return nil
}

func (m *FindInFiles) openSelectedCmd() tea.Cmd {
	if m.selected < 0 || m.selected >= len(m.results) {
		return nil
	}
	match := m.results[m.selected]
	return func() tea.Msg { return FindInFilesOpenMsg{Match: match} }
}

func (m *FindInFiles) handleClick(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft {
		return nil
	}
	if msg.Y == 0 {
		switch {
		case msg.X >= m.caseX0 && msg.X <= m.caseX1:
			m.caseSensitive = !m.caseSensitive
			return m.scheduleSearch()
		case msg.X >= m.regexX0 && msg.X <= m.regexX1:
			m.regex = !m.regex
			return m.scheduleSearch()
		case msg.X >= m.inputX:
			col := max(0, msg.X-m.inputX-2)
			if m.inputStartRune > 0 && col > 0 {
				col-- // leading ellipsis
			}
			ed := m.ensureQuery()
			runes := []rune(ed.Value())
			start := m.inputStartRune
			if start > len(runes) {
				start = len(runes)
			}
			visible := runes[start:]
			ed.SetCursor(start+rendering.ColToRuneIdx(visible, col), false)
			return nil
		}
	}
	if msg.X == m.width-2 && msg.Y >= m.resultY0 && msg.Y < m.resultY0+m.resultH && len(m.results) > m.resultH {
		m.scrollDragging = true
		m.scrollToResultRow(msg.Y)
		return nil
	}
	idx := m.resultIndexAt(msg.Y)
	if idx < 0 {
		return nil
	}
	m.selectIndex(idx)
	now := time.Now()
	if m.lastClickIndex == idx && now.Sub(m.lastClickAt) <= 400*time.Millisecond {
		m.lastClickAt = time.Time{}
		m.lastClickIndex = -1
		return m.openSelectedCmd()
	}
	m.lastClickIndex = idx
	m.lastClickAt = now
	return nil
}

func (m *FindInFiles) handleMotion(msg tea.MouseMotionMsg) {
	if m.scrollDragging {
		m.scrollToResultRow(msg.Y)
		return
	}
	if msg.Y == 0 {
		switch {
		case msg.X >= m.caseX0 && msg.X <= m.caseX1:
			m.hoveredControl = 1
		case msg.X >= m.regexX0 && msg.X <= m.regexX1:
			m.hoveredControl = 2
		default:
			m.hoveredControl = 0
		}
	} else {
		m.hoveredControl = 0
	}
	idx := m.resultIndexAt(msg.Y)
	if idx >= 0 {
		m.hovered = idx
	} else {
		m.hovered = -1
	}
}

func (m *FindInFiles) scrollToResultRow(y int) {
	if m.resultH <= 1 || len(m.results) <= m.resultH {
		return
	}
	row := max(0, min(y-m.resultY0, m.resultH-1))
	maxScroll := len(m.results) - m.resultH
	m.scroll = row * maxScroll / (m.resultH - 1)
}

func (m *FindInFiles) handleWheel(msg tea.MouseWheelMsg) {
	if msg.Y < m.resultY0 || msg.Y >= m.resultY0+m.resultH {
		return
	}
	if msg.Button == tea.MouseWheelUp {
		m.scroll -= 3
	} else if msg.Button == tea.MouseWheelDown {
		m.scroll += 3
	}
	m.clampScroll()
}

func (m *FindInFiles) resultIndexAt(y int) int {
	if y < m.resultY0 || y >= m.resultY0+m.resultH {
		return -1
	}
	idx := m.scroll + y - m.resultY0
	if idx < 0 || idx >= len(m.results) {
		return -1
	}
	return idx
}

func (m *FindInFiles) layout(width, height int) {
	m.width, m.height = width, height
	m.resultY0 = 2
	available := height - m.resultY0
	m.resultH = available * 42 / 100
	if m.resultH < 4 {
		m.resultH = min(4, max(1, available-1))
	}
	if m.resultH > available-4 {
		m.resultH = max(1, available-4)
	}
	m.previewY = m.resultY0 + m.resultH
	m.previewH = height - m.previewY - 1
	if m.previewH < 0 {
		m.previewH = 0
	}
	m.clampScroll()
}

func (m *FindInFiles) View(width, height, spinFrame int) string {
	_ = spinFrame
	m.layout(width, height)
	bg := lipgloss.NewStyle().Background(m.th.Colors.SubPaneBg)
	muted := lipgloss.NewStyle().Foreground(m.th.Colors.Muted).Background(m.th.Colors.SubPaneBg)
	rule := lipgloss.NewStyle().Foreground(m.th.Colors.PopupDivider).Background(m.th.Colors.SubPaneBg)
	pad := func(line string) string {
		line = rendering.TruncateLine(line, width)
		if n := width - rendering.VisibleWidth(line); n > 0 {
			line += bg.Render(strings.Repeat(" ", n))
		}
		return line
	}

	caseLabel := renderFindToggle("Cc", m.caseSensitive, m.hoveredControl == 1, m.th)
	regexLabel := renderFindToggle(".*", m.regex, m.hoveredControl == 2, m.th)
	m.caseX0 = 1
	m.caseX1 = m.caseX0 + rendering.VisibleWidth(caseLabel) - 1
	m.regexX0 = m.caseX1 + 2
	m.regexX1 = m.regexX0 + rendering.VisibleWidth(regexLabel) - 1
	m.inputX = m.regexX1 + 2
	status := m.searchStatus()
	statusW := rendering.VisibleWidth(status)
	inputW := width - m.inputX - statusW - 2
	if inputW < 12 {
		inputW = 12
	}
	inputView := m.renderQueryInput(inputW)
	searchLine := bg.Render(" ") + caseLabel + bg.Render(" ") + regexLabel + bg.Render(" ") + inputView
	gap := width - rendering.VisibleWidth(searchLine) - statusW - 1
	if gap < 1 {
		gap = 1
	}
	searchLine += bg.Render(strings.Repeat(" ", gap)) + status + bg.Render(" ")

	lines := []string{pad(searchLine), pad(rule.Render(strings.Repeat("─", width)))}
	showScrollbar := len(m.results) > m.resultH
	rowW := width
	if showScrollbar {
		rowW -= 2 // scrollbar + passive resize-edge column
	}
	m.scrollM = ui.SyncScrollModel(m.scrollM, len(m.results), m.resultH, m.scroll)
	end := min(len(m.results), m.scroll+m.resultH)
	for i := m.scroll; i < end; i++ {
		line := m.renderResult(i, rowW)
		if showScrollbar {
			line += ui.ScrollbarCellANSI(m.th, m.scrollM, i-m.scroll) + " "
		}
		lines = append(lines, pad(line))
	}
	if len(m.results) == 0 && m.resultH > 0 {
		message := "Type to search this workspace"
		if m.loading {
			message = "Searching recursively..."
		} else if m.err != "" {
			message = m.err
		} else if m.Query() != "" {
			message = "No matches"
		}
		style := muted
		if m.err != "" {
			style = lipgloss.NewStyle().Foreground(m.th.Colors.Error).Background(m.th.Colors.SubPaneBg)
		}
		lines = append(lines, pad(style.Render("  "+message)))
	}
	for len(lines) < m.resultY0+m.resultH {
		lines = append(lines, pad(""))
	}

	previewLabel := " Preview "
	if m.selected >= 0 && m.selected < len(m.results) && !m.results[m.selected].NameOnly {
		selected := m.results[m.selected]
		previewLabel = fmt.Sprintf(" %s:%d ", selected.RelativePath, selected.Line)
	}
	label := muted.Render(rendering.TruncateLine(previewLabel, max(1, width-2)))
	lineW := width - rendering.VisibleWidth(label)
	lines = append(lines, pad(label+rule.Render(strings.Repeat("─", max(0, lineW)))))
	if m.preview != nil && m.previewH >= 4 {
		previewLines := m.preview.View(width, m.previewH, 0, 0)
		for _, line := range previewLines {
			lines = append(lines, pad(line))
		}
	}
	for len(lines) < height {
		lines = append(lines, pad(""))
	}
	return strings.Join(lines[:height], "\n")
}

func (m *FindInFiles) renderQueryInput(width int) string {
	ed := m.ensureQuery()
	// Track horizontal start for click-to-cursor (mirrors RenderFilterLine).
	runes := []rune(ed.Value())
	cursor := ed.Cursor()
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	available := width - 2
	if available < 2 {
		available = 2
	}
	cursorW := 1
	if cursor < len(runes) {
		cursorW = max(1, rendering.VisibleWidth(string(runes[cursor])))
	}
	start := 0
	for start < cursor && rendering.VisibleWidth(string(runes[start:cursor])) > available-cursorW {
		start++
	}
	m.inputStartRune = start
	return ui.RenderFilterLine(m.th, ed, ui.FilterPaintOptions{
		Prompt:      ">",
		Placeholder: "Search file names and contents",
		Width:       width,
		Focused:     true,
		BG:          m.th.Colors.SubPaneBg,
	})
}

func (m *FindInFiles) searchStatus() string {
	muted := lipgloss.NewStyle().Foreground(m.th.Colors.Muted).Background(m.th.Colors.SubPaneBg)
	if m.loading {
		return lipgloss.NewStyle().Foreground(m.th.Colors.Accent).Background(m.th.Colors.SubPaneBg).Render("searching")
	}
	if m.err != "" {
		return lipgloss.NewStyle().Foreground(m.th.Colors.Error).Background(m.th.Colors.SubPaneBg).Render("invalid query")
	}
	if m.Query() == "" {
		return muted.Render(filepath.Base(m.root))
	}
	files := make(map[string]struct{})
	for _, result := range m.results {
		files[result.Path] = struct{}{}
	}
	if m.duration > 0 {
		return muted.Render(fmt.Sprintf("%d results · %d files · %dms", len(m.results), len(files), m.duration.Milliseconds()))
	}
	return muted.Render(fmt.Sprintf("%d results · %d files", len(m.results), len(files)))
}

func (m *FindInFiles) renderResult(idx, width int) string {
	result := m.results[idx]
	selected := idx == m.selected
	hovered := idx == m.hovered && !selected
	bgColor := m.th.Colors.SubPaneBg
	if selected {
		bgColor = m.th.Colors.PopupSubtleBg
	} else if hovered {
		bgColor = m.th.Colors.Hover
	}
	base := lipgloss.NewStyle().Foreground(m.th.Colors.Fg).Background(bgColor)
	muted := lipgloss.NewStyle().Foreground(m.th.Colors.Muted).Background(bgColor)
	accent := lipgloss.NewStyle().Foreground(m.th.Colors.Accent).Background(bgColor).Bold(true)
	prefix := base.Render("  ")
	if selected {
		prefix = accent.Render("❯ ")
	}
	icon := m.icons.ResolveIcon(filepath.Base(result.Path), false, false, false)
	iconStyle := lipgloss.NewStyle().Foreground(icon.Color).Background(bgColor)
	path := renderFindMatches(result.RelativePath, result.PathMatches, base, accent)
	lineMeta := ""
	if !result.NameOnly {
		lineMeta = muted.Render(fmt.Sprintf(":%d", result.Line))
	}
	left := prefix + iconStyle.Render(icon.Icon) + base.Render(" ") + path + lineMeta
	remaining := width - rendering.VisibleWidth(left) - 2
	if remaining > 12 && !result.NameOnly {
		snippet := strings.TrimRight(result.LineText, "\r\n")
		snippet = rendering.TruncateLine(snippet, remaining-3)
		left += muted.Render("  ·  ") + renderFindMatches(snippet, result.TextMatches, muted, accent)
	}
	left = rendering.TruncateLine(left, width)
	if n := width - rendering.VisibleWidth(left); n > 0 {
		left += base.Render(strings.Repeat(" ", n))
	}
	return left
}

func renderFindToggle(label string, active, hovered bool, th *theme.Theme) string {
	style := lipgloss.NewStyle().Foreground(th.Colors.Muted).Background(th.Colors.SubPaneBg)
	if active {
		style = style.Foreground(th.Colors.Accent).Background(th.Colors.PopupSubtleBg).Bold(true)
	} else if hovered {
		style = style.Foreground(th.Colors.Fg).Background(th.Colors.Hover)
	}
	return style.Render(" " + label + " ")
}

func renderFindMatches(text string, positions []int, normal, matched lipgloss.Style) string {
	positionSet := make(map[int]struct{}, len(positions))
	for _, pos := range positions {
		positionSet[pos] = struct{}{}
	}
	var b strings.Builder
	for i, r := range []rune(text) {
		if _, ok := positionSet[i]; ok {
			b.WriteString(matched.Render(string(r)))
		} else {
			b.WriteString(normal.Render(string(r)))
		}
	}
	return b.String()
}

func (m *FindInFiles) Render(surf *surface.Surface, ctx *widget.RenderContext) {
	if surf.Width() < 10 || surf.Height() < 6 {
		return
	}
	rendering.RenderToSurface(surf, m.View(surf.Width(), surf.Height(), ctx.SpinFrame))
}

func (m *FindInFiles) Measure(availW, availH int) (int, int) {
	return availW, availH
}

var _ widget.Widget = (*FindInFiles)(nil)
var _ interface{ HandleEvent(tea.Msg) tea.Cmd } = (*FindInFiles)(nil)
