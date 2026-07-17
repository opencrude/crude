package widgets

import (
	"fmt"
	"image/color"
	"math"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"

	"crude/sessiondb"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
)

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
	shortcuts [][2]string,
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

	// Full-width New Session hero
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

	// Two-column body
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

	left := buildWelcomeLeftBody(items, openFolderIdx, selected, leftW, bodyH, spinFrame, idleSeconds, th, shortcuts)
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
		WorkspaceSearchY:      bodyY0 + 1,
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
	shortcuts [][2]string,
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
	for _, sc := range shortcuts {
		key := sc[0]
		gap := 14 - rendering.VisibleWidth(key)
		if gap < 2 {
			gap = 2
		}
		line := "  " + th.Styles.Body.Render(key) + strings.Repeat(" ", gap) + th.Styles.DimLabel.Render(sc[1])
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

	mutedStyle := lipgloss.NewStyle().Foreground(th.Colors.Muted)

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

	// Shared filter paint (TextEdit engine when active).
	var searchLine string
	if filterActive {
		ed := ui.NewTextEdit()
		ed.SetValue(filterText)
		// Cursor at end for display (launcher keeps engine in viewmodel; paint uses value).
		ed.SetCursor(len([]rune(filterText)), false)
		searchLine = ui.RenderFilterLine(th, ed, ui.FilterPaintOptions{
			Prompt:      "> ",
			Placeholder: "Search workspaces",
			Width:       rightW,
			Focused:     true,
			BG:          th.Colors.SubPaneBg,
		})
	} else {
		searchLine = mutedStyle.Render("> ") + mutedStyle.Render("Search workspaces")
	}
	push(searchLine)

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
		scrollM := ui.SyncScrollModel(nil, len(rows), meta.viewportH, listScroll)
		for i := listScroll; i < end; i++ {
			row := rows[i]
			y0 := len(lines)
			line := renderWorkspaceRow(selected == row.item, *row.ws, icons, th, rowW)
			if showScrollbar {
				line += ui.ScrollbarCellANSI(th, scrollM, i-listScroll)
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

func padStyledLine(line string, width int, style lipgloss.Style) string {
	if n := width - rendering.VisibleWidth(line); n > 0 {
		line += style.Render(strings.Repeat(" ", n))
	}
	return line
}

func sessionCountLabel(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
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
	return th.Styles.Header.Render(strings.Repeat("─", left) + content + strings.Repeat("─", right))
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
