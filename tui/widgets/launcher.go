package widgets

import (
	"fmt"
	"image/color"
	"math"
	"path/filepath"
	"strings"
	"time"

	"crude/sessiondb"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/theme"

	"charm.land/lipgloss/v2"
)

// FormatTurnCount formats a turn count for display.
func FormatTurnCount(turns int) string {
	if turns == 1 {
		return "1 turn"
	}
	return fmt.Sprintf("%d turns", turns)
}

// LauncherItemKind identifies a selectable row in the welcome launcher.
type LauncherItemKind int

const (
	LauncherItemNewSession LauncherItemKind = iota
	LauncherItemOpenFolder
	LauncherItemWorkspace
	LauncherItemSession
)

// WorkspaceEntry is a display row for a recent workspace.
type WorkspaceEntry struct {
	Name         string
	Path         string
	SessionCount int
}

// LauncherItem is one keyboard/mouse-selectable entry in the welcome view.
type LauncherItem struct {
	Kind      LauncherItemKind
	Workspace *WorkspaceEntry
	Session   *sessiondb.SessionMeta
}

// LauncherHit is a rectangular click target in content coordinates
// (x relative to inner content, y relative to first painted line).
type LauncherHit struct {
	X0, Y0, X1, Y1 int
	ItemIdx        int
}

// LauncherPaint is the fully rendered welcome view plus hit geometry.
// Hits cover full card bounds (not just title text). The workspace geometry
// describes the independently scrollable right-hand viewport.
type LauncherPaint struct {
	Lines                 []string
	Hits                  []LauncherHit
	LineToItem            []int // legacy Y-only map; -1 = non-selectable
	SelectedLine          int
	WorkspacePaneY0       int
	WorkspacePaneY1       int
	WorkspacePaneX0       int
	WorkspacePaneX1       int
	WorkspaceSearchY      int
	WorkspaceListY0       int
	WorkspaceListY1       int
	WorkspaceViewportH    int
	WorkspaceVisibleCount int
	WorkspaceScrollMax    int
}

// LauncherGroup is a date-grouped set of sessions (kept for grouping helpers).
type LauncherGroup struct {
	Label    string
	Sessions []sessiondb.SessionMeta
}

// BuildLauncherGroups groups sessions by date for display.
func BuildLauncherGroups(sessions []sessiondb.SessionMeta) []LauncherGroup {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := today.AddDate(0, 0, -1)

	var groups []LauncherGroup
	var currentKey string
	var currentLabel string
	var currentSessions []sessiondb.SessionMeta

	for _, s := range sessions {
		var key, label string
		if s.CreatedAt > 0 {
			t := time.Unix(0, s.CreatedAt)
			day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
			key = day.Format("2006-01-02")
			if day.Equal(today) {
				label = "today"
			} else if day.Equal(yesterday) {
				label = "yesterday"
			} else {
				label = day.Format("Mon Jan 2, 2006")
			}
		} else {
			key = "unknown"
			label = "unknown"
		}

		if key != currentKey {
			if len(currentSessions) > 0 {
				groups = append(groups, LauncherGroup{Label: currentLabel, Sessions: currentSessions})
			}
			currentKey = key
			currentLabel = label
			currentSessions = nil
		}
		currentSessions = append(currentSessions, s)
	}
	if len(currentSessions) > 0 {
		groups = append(groups, LauncherGroup{Label: currentLabel, Sessions: currentSessions})
	}
	return groups
}

// BuildLauncherItems builds the flat selectable list for the welcome screen.
// Order: New Session, New Workspace, workspaces...
// Sessions are not listed on the welcome screen (workspaces only on the right).
func BuildLauncherItems(workspaces []WorkspaceEntry, sessions []sessiondb.SessionMeta) []LauncherItem {
	_ = sessions
	items := make([]LauncherItem, 0, 2+len(workspaces))
	items = append(items,
		LauncherItem{Kind: LauncherItemNewSession},
		LauncherItem{Kind: LauncherItemOpenFolder},
	)
	for i := range workspaces {
		w := workspaces[i]
		wc := w
		items = append(items, LauncherItem{Kind: LauncherItemWorkspace, Workspace: &wc})
	}
	return items
}

// RenderWelcomeLauncher paints the welcome screen that fills height.
//
//	[ full-width New Session card — hero ]
//	[ left: New Workspace + shortcuts | right: searchable workspace viewport ]
//
// No in-content footer; navigation help lives in the persistent bottom status row.
// listScroll is the first visible line in the right workspaces pane.
func RenderWelcomeLauncher(
	cwdPath string,
	items []LauncherItem,
	selected int,
	listScroll int,
	filterActive bool,
	filterText string,
	th *theme.Theme,
	width, height int,
	spinFrame int,
	idleSeconds float64,
) LauncherPaint {
	if width < 40 {
		width = 40
	}
	if height < 12 {
		height = 12
	}

	var newSessionIdx, openFolderIdx = -1, -1
	var wsIdx []int
	for i, it := range items {
		switch it.Kind {
		case LauncherItemNewSession:
			newSessionIdx = i
		case LauncherItemOpenFolder:
			openFolderIdx = i
		case LauncherItemWorkspace:
			wsIdx = append(wsIdx, i)
		}
	}

	lines := make([]string, 0, height)
	lineToItem := make([]int, 0, height)
	var hits []LauncherHit
	selectedLine := 0

	// ── Full-width New Session hero ────────────────────────────────────
	if newSessionIdx >= 0 {
		path := cwdPath
		if rendering.VisibleWidth(path) > width-10 {
			path = rendering.TruncateLine(path, width-10)
		}
		block := renderActionCard(
			selected == newSessionIdx,
			sparkle(spinFrame/10000)+" New Session",
			path,
			"enter ↵",
			th, width, spinFrame, idleSeconds, true,
		)
		y0 := len(lines)
		for _, ln := range block {
			lines = append(lines, padLine(ln, width))
			lineToItem = append(lineToItem, newSessionIdx)
		}
		hits = append(hits, LauncherHit{X0: 0, Y0: y0, X1: width - 1, Y1: len(lines) - 1, ItemIdx: newSessionIdx})
		if selected == newSessionIdx {
			selectedLine = y0
		}
	}
	lines = append(lines, padLine("", width))
	lineToItem = append(lineToItem, -1)

	// ── Two-column body ────────────────────────────────────────────────
	gap := 3
	leftW := width * 40 / 100
	if leftW < 24 {
		leftW = 24
	}
	if leftW > 40 {
		leftW = 40
	}
	rightW := width - leftW - gap
	if rightW < 22 {
		rightW = 22
		leftW = width - rightW - gap
	}
	rightX0 := leftW + gap

	bodyH := height - len(lines)
	if bodyH < 6 {
		bodyH = 6
	}

	left := buildWelcomeLeftBody(items, openFolderIdx, selected, leftW, bodyH, spinFrame, idleSeconds, th)
	right, rightHits, rightMeta := buildWelcomeRightWorkspaces(
		items, wsIdx, selected, listScroll, rightW, bodyH, filterActive, filterText, th,
	)
	bodyY0 := len(lines)

	for row := 0; row < bodyH; row++ {
		l := ""
		if row < len(left.lines) {
			l = left.lines[row]
		}
		l = padLine(l, leftW)
		r := ""
		if row < len(right) {
			r = right[row]
		}
		r = padLine(r, rightW)
		lines = append(lines, padLine(l+th.Styles.DimLabel.Render(" │ ")+r, width))
		li := -1
		if row < len(left.lineItem) {
			li = left.lineItem[row]
		}
		lineToItem = append(lineToItem, li)
	}

	for _, h := range left.hits {
		hh := h
		hh.Y0 += bodyY0
		hh.Y1 += bodyY0
		hits = append(hits, hh)
		if hh.ItemIdx == selected {
			selectedLine = hh.Y0
		}
	}
	for _, h := range rightHits {
		hh := h
		hh.X0 += rightX0
		hh.X1 += rightX0
		hh.Y0 += bodyY0
		hh.Y1 += bodyY0
		hits = append(hits, hh)
		if hh.ItemIdx == selected {
			selectedLine = hh.Y0
		}
	}

	// No content footer; the app status surface owns help text.
	for len(lines) < height {
		lines = append(lines, padLine("", width))
		lineToItem = append(lineToItem, -1)
	}
	if len(lines) > height {
		lines = lines[:height]
		lineToItem = lineToItem[:height]
	}

	return LauncherPaint{
		Lines:                 lines,
		Hits:                  hits,
		LineToItem:            lineToItem,
		SelectedLine:          selectedLine,
		WorkspacePaneY0:       bodyY0,
		WorkspacePaneY1:       bodyY0 + bodyH - 1,
		WorkspacePaneX0:       rightX0,
		WorkspacePaneX1:       rightX0 + rightW - 1,
		WorkspaceSearchY:      bodyY0,
		WorkspaceListY0:       bodyY0 + rightMeta.listY0,
		WorkspaceListY1:       bodyY0 + rightMeta.listY1,
		WorkspaceViewportH:    rightMeta.viewportH,
		WorkspaceVisibleCount: rightMeta.visibleCount,
		WorkspaceScrollMax:    rightMeta.scrollMax,
	}
}

type welcomeCol struct {
	lines    []string
	lineItem []int
	hits     []LauncherHit
}

// buildWelcomeLeftBody: New Workspace card + shortcuts (New Session is full-width hero above).
func buildWelcomeLeftBody(
	items []LauncherItem,
	openFolderIdx, selected, leftW, bodyH, spinFrame int,
	idleSeconds float64,
	th *theme.Theme,
) welcomeCol {
	var col welcomeCol
	push := func(s string, item int) {
		col.lines = append(col.lines, padLine(s, leftW))
		col.lineItem = append(col.lineItem, item)
	}

	if openFolderIdx >= 0 {
		y0 := len(col.lines)
		block := renderActionCard(
			selected == openFolderIdx,
			"📁 New Workspace",
			"Open a folder as a workspace",
			"enter ↵",
			th, leftW, spinFrame, idleSeconds, false,
		)
		for _, ln := range block {
			push(ln, openFolderIdx)
		}
		col.hits = append(col.hits, LauncherHit{
			X0: 0, Y0: y0, X1: leftW - 1, Y1: len(col.lines) - 1, ItemIdx: openFolderIdx,
		})
		push("", -1)
	}

	push(sectionHeader("Shortcuts", leftW, th), -1)
	for _, sc := range welcomeShortcuts() {
		key := sc[0]
		for rendering.VisibleWidth(key) < 14 {
			key += " "
		}
		line := "  " + th.Styles.Body.Render(key) + th.Styles.DimLabel.Render(sc[1])
		push(line, -1)
	}

	for len(col.lines) < bodyH {
		push("", -1)
	}
	if len(col.lines) > bodyH {
		col.lines = col.lines[:bodyH]
		col.lineItem = col.lineItem[:bodyH]
	}
	return col
}

func welcomeShortcuts() [][2]string {
	return [][2]string{
		{"Ctrl+Shift+P", "Command palette"},
		{"Ctrl+B", "Toggle sidebar"},
		{"Ctrl+S", "Settings"},
		{"Ctrl+O", "Attach file"},
		{"Alt+C", "Composer"},
		{"Ctrl+Q", "Close tab"},
		{"Esc", "Cancel / dismiss"},
		{"/", "Search workspaces"},
		{"d", "Delete item"},
	}
}

type workspacePaneMeta struct {
	listY0, listY1 int
	viewportH      int
	visibleCount   int
	scrollMax      int
}

// buildWelcomeRightWorkspaces renders a fixed search/header region and a
// virtual one-line workspace list. Only the result rows participate in scroll.
func buildWelcomeRightWorkspaces(
	items []LauncherItem,
	wsIdx []int,
	selected, listScroll, rightW, bodyH int,
	filterActive bool,
	filterText string,
	th *theme.Theme,
) (lines []string, hits []LauncherHit, meta workspacePaneMeta) {
	push := func(s string) {
		lines = append(lines, padLine(s, rightW))
	}

	searchStyle := lipgloss.NewStyle().Foreground(th.Colors.Fg)
	mutedStyle := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	accentStyle := lipgloss.NewStyle().Foreground(th.Colors.Accent).Bold(true)
	searchValue := mutedStyle.Render("Search workspaces")
	if filterActive {
		searchValue = searchStyle.Render(filterText) + accentStyle.Render("█")
	}
	push(mutedStyle.Render("> ") + searchValue)

	type workspaceRow struct {
		item int
		ws   *WorkspaceEntry
	}
	var rows []workspaceRow
	for _, i := range wsIdx {
		it := items[i]
		if it.Workspace == nil || !WorkspaceEntryMatches(*it.Workspace, filterText) {
			continue
		}
		rows = append(rows, workspaceRow{item: i, ws: it.Workspace})
	}

	countLabel := fmt.Sprintf("%d", len(rows))
	if len(rows) == 1 {
		countLabel += " workspace"
	} else {
		countLabel += " workspaces"
	}
	headerW := rightW - rendering.VisibleWidth(countLabel) - 1
	if headerW < 8 {
		headerW = 8
	}
	header := sectionHeader("Recent Workspaces", headerW, th) + " " + mutedStyle.Render(countLabel)
	push(header)

	meta.listY0 = 2
	meta.viewportH = bodyH - meta.listY0
	if meta.viewportH < 1 {
		meta.viewportH = 1
	}
	meta.listY1 = meta.listY0 + meta.viewportH - 1
	meta.visibleCount = len(rows)
	meta.scrollMax = len(rows) - meta.viewportH
	if meta.scrollMax < 0 {
		meta.scrollMax = 0
	}
	if listScroll < 0 {
		listScroll = 0
	}
	if listScroll > meta.scrollMax {
		listScroll = meta.scrollMax
	}
	showScrollbar := len(rows) > meta.viewportH
	rowW := rightW
	if showScrollbar {
		rowW--
	}
	icons := filetree.NewIconResolver()
	icons.ApplyTheme(&th.Colors.Tree)

	if len(rows) == 0 {
		empty := "No recent workspaces"
		if strings.TrimSpace(filterText) != "" {
			empty = "No workspaces match \"" + filterText + "\""
		}
		push(mutedStyle.Render("  " + rendering.TruncateLine(empty, rightW-2)))
	} else {
		end := listScroll + meta.viewportH
		if end > len(rows) {
			end = len(rows)
		}
		for i := listScroll; i < end; i++ {
			row := rows[i]
			y0 := len(lines)
			line := renderWorkspaceRow(selected == row.item, *row.ws, icons, th, rowW)
			if showScrollbar {
				line += renderWorkspaceScrollbarCell(i-listScroll, len(rows), meta.viewportH, listScroll, th)
			}
			push(line)
			hits = append(hits, LauncherHit{X0: 0, Y0: y0, X1: rowW - 1, Y1: y0, ItemIdx: row.item})
		}
	}

	for len(lines) < bodyH {
		push("")
	}
	if len(lines) > bodyH {
		lines = lines[:bodyH]
	}
	return lines, hits, meta
}

// WorkspaceEntryMatches applies the welcome search to workspace identity only.
func WorkspaceEntryMatches(ws WorkspaceEntry, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(ws.Name), query) ||
		strings.Contains(strings.ToLower(ws.Path), query)
}

func renderWorkspaceRow(selected bool, ws WorkspaceEntry, icons *filetree.IconResolver, th *theme.Theme, width int) string {
	if width < 8 {
		return strings.Repeat(" ", max(0, width))
	}
	name := ws.Name
	if name == "" {
		name = filepath.Base(ws.Path)
	}
	if name == "" || name == "." {
		name = "untitled"
	}
	meta := sessionCountLabel(ws.SessionCount)
	icon := icons.ResolveIcon(filepath.Base(ws.Path), true, false, false)
	bg := th.Colors.Bg
	if selected {
		bg = th.Colors.Hover
	}
	base := lipgloss.NewStyle().Background(bg)
	accent := lipgloss.NewStyle().Foreground(th.Colors.Accent).Background(bg).Bold(true)
	iconStyle := lipgloss.NewStyle().Foreground(icon.Color).Background(bg)
	nameStyle := lipgloss.NewStyle().Foreground(th.Colors.Fg).Background(bg).Bold(true)
	pathStyle := lipgloss.NewStyle().Foreground(th.Colors.Muted).Background(bg)
	metaStyle := lipgloss.NewStyle().Foreground(th.Colors.Muted).Background(bg)

	prefix := base.Render("  ")
	if selected {
		prefix = accent.Render("❯ ")
	}
	fixedW := 2 + rendering.VisibleWidth(icon.Icon) + 1 + 1 + 2 + rendering.VisibleWidth(meta)
	available := width - fixedW
	if available < 2 {
		available = 2
	}
	nameW := rendering.VisibleWidth(name)
	maxNameW := max(8, width/4)
	if nameW > maxNameW {
		nameW = maxNameW
	}
	if nameW > available {
		nameW = available
	}
	displayName := rendering.TruncateLine(name, nameW)
	pathW := available - rendering.VisibleWidth(displayName) - 3
	path := ""
	if pathW > 4 {
		path = " · " + rendering.TruncateLine(ws.Path, pathW)
	}
	left := prefix + iconStyle.Render(icon.Icon) + base.Render(" ") + nameStyle.Render(displayName) + pathStyle.Render(path)
	gap := width - rendering.VisibleWidth(left) - rendering.VisibleWidth(meta) - 2
	if gap < 1 {
		gap = 1
	}
	line := left + base.Render(strings.Repeat(" ", gap)) + metaStyle.Render(meta) + base.Render("  ")
	return padStyledLine(line, width, base)
}

func renderWorkspaceScrollbarCell(row, total, visible, scroll int, th *theme.Theme) string {
	if total <= visible || visible <= 0 {
		return " "
	}
	thumbH := visible * visible / total
	if thumbH < 1 {
		thumbH = 1
	}
	track := visible - thumbH
	maxScroll := total - visible
	thumbTop := 0
	if track > 0 && maxScroll > 0 {
		thumbTop = scroll * track / maxScroll
	}
	if row >= thumbTop && row < thumbTop+thumbH {
		return lipgloss.NewStyle().Foreground(th.Colors.Accent).Render("┃")
	}
	return lipgloss.NewStyle().Foreground(th.Colors.Border).Render("│")
}

func padStyledLine(line string, width int, style lipgloss.Style) string {
	if n := width - rendering.VisibleWidth(line); n > 0 {
		line += style.Render(strings.Repeat(" ", n))
	}
	return line
}

// renderCompactRow: accent bar when selected — never a filled Selection bg
// (filled bg leaves black residual holes under ultraviolet reparse).
func renderCompactRow(selected bool, title, sub string, th *theme.Theme, width, spinFrame int, idleSeconds float64) []string {
	_ = spinFrame
	_ = idleSeconds
	bar := "  "
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(th.Colors.Fg)
	if selected {
		bar = lipgloss.NewStyle().Foreground(th.Colors.Accent).Render("▌ ")
		titleStyle = titleStyle.Foreground(th.Colors.Accent)
	}
	t := rendering.TruncateLine(title, width-4)
	s := rendering.TruncateLine(sub, width-4)
	line1 := bar + titleStyle.Render(t)
	line2 := "  " + th.Styles.DimLabel.Render(s)
	return []string{padLine(line1, width), padLine(line2, width)}
}

func renderSessionRow(selected bool, title, sub, meta string, th *theme.Theme, width int) []string {
	metaPart := ""
	if meta != "" {
		metaPart = "  " + meta
	}
	return renderCompactRow(selected, title+metaPart, sub, th, width, 0, 0)
}

func rotatingTip(spinFrame int) string {
	tips := []string{
		"Ctrl+Shift+P opens the command palette — quit lives there too.",
		"New Workspace opens any project folder in Crude.",
		"Sessions stay with their workspace — resume any thread.",
		"Ctrl+B toggles the file tree without losing focus.",
		"Esc cancels — it never quits the app.",
		"Hover a card, click anywhere on it to open.",
	}
	return tips[(spinFrame/8000)%len(tips)]
}

func wrapPlain(s string, width int) []string {
	if width < 8 {
		width = 8
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	cur := words[0]
	for _, w := range words[1:] {
		if rendering.VisibleWidth(cur+" "+w) <= width {
			cur += " " + w
		} else {
			lines = append(lines, cur)
			cur = w
		}
	}
	lines = append(lines, cur)
	return lines
}

func sessionCountLabel(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}

func sessionDateLabel(s sessiondb.SessionMeta) string {
	if s.CreatedAt <= 0 {
		return "unknown"
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := today.AddDate(0, 0, -1)
	t := time.Unix(0, s.CreatedAt)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	if day.Equal(today) {
		return "today"
	}
	if day.Equal(yesterday) {
		return "yesterday"
	}
	return day.Format("Mon Jan 2, 2006")
}

func sessionCardText(s sessiondb.SessionMeta) (title, sub, meta string) {
	title = s.ProjectName
	if title == "" {
		title = "untitled"
	}
	sub = ""
	if s.FirstPrompt.Valid {
		sub = s.FirstPrompt.String
	}
	if sub == "" && s.LastPrompt.Valid {
		sub = s.LastPrompt.String
	}
	if sub == "" {
		sub = s.ProjectPath
	}
	meta = FormatTurnCount(s.TurnCount)
	if s.Model != "" {
		meta += " · " + s.Model
	}
	return title, sub, meta
}

func sectionHeader(label string, width int, th *theme.Theme) string {
	content := " " + strings.ToUpper(label) + " "
	cw := rendering.VisibleWidth(content)
	remain := width - cw
	if remain < 0 {
		remain = 0
	}
	left := 0
	right := remain
	return th.Styles.Separator.Render(strings.Repeat("─", left) + content + strings.Repeat("─", right))
}

func renderLauncherDateSep(label string, width int, th *theme.Theme) string {
	content := " " + label + " "
	cw := rendering.VisibleWidth(content)
	remain := width - cw
	if remain < 0 {
		remain = 0
	}
	left := remain / 2
	right := remain - left
	return th.Styles.Separator.Render(strings.Repeat("─", left) + content + strings.Repeat("─", right))
}

// renderActionCard draws a full-width bordered card (4 lines). Every line is
// part of the same hit target when mapped by the caller.
func renderActionCard(selected bool, title, tagline, hint string, th *theme.Theme, width, spinFrame int, idleSeconds float64, pulse bool) []string {
	boxW := width
	titleStyle := lipgloss.NewStyle().Bold(true)
	if selected {
		titleStyle = titleStyle.Foreground(th.Colors.Accent)
	} else {
		titleStyle = titleStyle.Foreground(th.Colors.Fg)
	}
	dim := th.Styles.DimLabel
	innerW := boxW - 4
	if innerW < 8 {
		innerW = 8
	}

	topLine := titleStyle.Render(title)
	tag := tagline
	if rendering.VisibleWidth(tag) > innerW-rendering.VisibleWidth(hint)-2 {
		tag = rendering.TruncateLine(tag, max(8, innerW-rendering.VisibleWidth(hint)-2))
	}
	tagW := rendering.VisibleWidth(tag)
	hintW := rendering.VisibleWidth(hint)
	spacerW := innerW - tagW - hintW
	if spacerW < 1 {
		spacerW = 1
	}
	middleLine := dim.Render(tag) + strings.Repeat(" ", spacerW) + dim.Render(hint)
	content := topLine + "\n" + middleLine

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(boxW)

	if selected && pulse && idleSeconds >= 5.0 {
		boxStyle = boxStyle.BorderForeground(pulseColor(spinFrame, th.Colors.Border, th.Colors.Accent))
	} else if selected {
		boxStyle = boxStyle.BorderForeground(th.Colors.Accent)
	} else {
		boxStyle = boxStyle.BorderForeground(th.Colors.Muted)
	}

	return strings.Split(boxStyle.Render(content), "\n")
}

// renderListCard draws a full-width bordered list row (title + subtitle + meta).
// Entire card bounds are one hit target.
func renderListCard(selected bool, title, subtitle, meta string, th *theme.Theme, width int) []string {
	boxW := width
	innerW := boxW - 4
	if innerW < 10 {
		innerW = 10
	}

	titleStyle := lipgloss.NewStyle().Bold(true)
	if selected {
		titleStyle = titleStyle.Foreground(th.Colors.Accent)
	} else {
		titleStyle = titleStyle.Foreground(th.Colors.Fg)
	}
	dim := th.Styles.DimLabel

	name := title
	if name == "" {
		name = "untitled"
	}
	metaStr := meta
	nameBudget := innerW - rendering.VisibleWidth(metaStr) - 2
	if nameBudget < 8 {
		nameBudget = 8
	}
	if rendering.VisibleWidth(name) > nameBudget {
		name = rendering.TruncateLine(name, nameBudget)
	}
	spacer := innerW - rendering.VisibleWidth(name) - rendering.VisibleWidth(metaStr)
	if spacer < 1 {
		spacer = 1
	}
	line1 := titleStyle.Render(name) + strings.Repeat(" ", spacer) + dim.Render(metaStr)

	sub := subtitle
	if rendering.VisibleWidth(sub) > innerW {
		sub = rendering.TruncateLine(sub, innerW)
	}
	line2 := dim.Render(sub)

	content := line1 + "\n" + line2
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(boxW)
	if selected {
		boxStyle = boxStyle.BorderForeground(th.Colors.Accent)
	} else {
		boxStyle = boxStyle.BorderForeground(th.Colors.Border)
	}
	return strings.Split(boxStyle.Render(content), "\n")
}

func padLine(line string, width int) string {
	lw := rendering.VisibleWidth(line)
	if lw < width {
		return line + strings.Repeat(" ", width-lw)
	}
	return line
}

var sparkleFrames = []string{"✨", "🌟", "✨", "💫", "✨", "⭐"}

func sparkle(frame int) string {
	return sparkleFrames[frame%len(sparkleFrames)]
}

func pulseColor(spinFrame int, c1, c2 color.Color) color.Color {
	r1, g1, b1, _ := c1.RGBA()
	r2, g2, b2, _ := c2.RGBA()
	phase := float64(spinFrame%3000) / 3000.0
	t := 0.5 - 0.5*math.Cos(2*math.Pi*phase)
	r := lerp(uint8(r1>>8), uint8(r2>>8), t)
	g := lerp(uint8(g1>>8), uint8(g2>>8), t)
	b := lerp(uint8(b1>>8), uint8(b2>>8), t)
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, b))
}

func lerp(a, b uint8, t float64) uint8 {
	return uint8(float64(a) + (float64(b)-float64(a))*t)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Legacy helpers kept for any external references during transition.

// RenderPrimaryEntry draws a bordered primary action box.
func RenderPrimaryEntry(selected bool, title, tagline, hint string, th *theme.Theme, width int, idleSeconds float64) []string {
	return renderActionCard(selected, title, tagline, hint, th, width, 0, idleSeconds, false)
}

// RenderNewSessionEntry is kept for compatibility with older call sites/tests.
func RenderNewSessionEntry(selected bool, th *theme.Theme, width int, spinFrame int, idleSeconds float64) []string {
	return renderActionCard(
		selected,
		sparkle(spinFrame/10000)+" New Session",
		"Start a blank canvas and explore new ideas...",
		"enter ↵",
		th, width, spinFrame, idleSeconds, true,
	)
}

// RenderLauncher is a thin wrapper that renders sessions-only (no workspaces).
// Prefer RenderWelcomeLauncher for the full welcome screen.
func RenderLauncher(sessions []sessiondb.SessionMeta, selected int, filterActive bool, filterText string, th *theme.Theme, width, height, spinFrame int, idleSeconds float64) []string {
	items := BuildLauncherItems(nil, sessions)
	mapped := selected
	if selected > 0 {
		mapped = selected + 1 // skip New Workspace
	}
	paint := RenderWelcomeLauncher("", items, mapped, 0, filterActive, filterText, th, width, height, spinFrame, idleSeconds)
	return paint.Lines
}

// RenderWorkspaceLauncher is retained for compatibility; prefer RenderWelcomeLauncher.
func RenderWorkspaceLauncher(cwdName, cwdPath string, workspaces []WorkspaceEntry, selected int, filterActive bool, filterText string, th *theme.Theme, width, height, spinFrame int, idleSeconds float64) []string {
	items := BuildLauncherItems(workspaces, nil)
	mapped := selected
	if selected > 0 {
		mapped = selected + 1
	}
	paint := RenderWelcomeLauncher(cwdPath, items, mapped, 0, filterActive, filterText, th, width, height, spinFrame, idleSeconds)
	return paint.Lines
}
