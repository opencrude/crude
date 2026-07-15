package widgets

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	"crude/sessiondb"
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
// Hits cover full card bounds (not just title text). SessionScrollMax is
// the max first-visible index for the right-column session list.
type LauncherPaint struct {
	Lines            []string
	Hits             []LauncherHit
	LineToItem       []int // legacy Y-only map; -1 = non-selectable
	SelectedLine     int
	SessionPaneY0    int
	SessionPaneY1    int
	SessionPaneX0    int
	SessionScrollMax int
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
// Order: New Session, Open Folder, workspaces...
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
//   [ full-width New Session card — hero ]
//   [ left: Open Folder + shortcuts | right: recent workspaces ]
//
// No in-content footer — nav help lives in the status input row only.
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
	right, rightHits, scrollMax := buildWelcomeRightWorkspaces(
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

	// No content footer — status input owns help text.
	for len(lines) < height {
		lines = append(lines, padLine("", width))
		lineToItem = append(lineToItem, -1)
	}
	if len(lines) > height {
		lines = lines[:height]
		lineToItem = lineToItem[:height]
	}

	return LauncherPaint{
		Lines:            lines,
		Hits:             hits,
		LineToItem:       lineToItem,
		SelectedLine:     selectedLine,
		SessionPaneY0:    bodyY0,
		SessionPaneY1:    bodyY0 + bodyH - 1,
		SessionPaneX0:    rightX0,
		SessionScrollMax: scrollMax,
	}
}

type welcomeCol struct {
	lines    []string
	lineItem []int
	hits     []LauncherHit
}

// buildWelcomeLeftBody: Open Folder card + shortcuts (New Session is full-width hero above).
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
			"📁 Open Folder",
			"Browse and open a project",
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
		{"/", "Filter welcome"},
		{"d", "Delete item"},
	}
}

// buildWelcomeRightWorkspaces: scrollable recent workspaces on the right.
func buildWelcomeRightWorkspaces(
	items []LauncherItem,
	wsIdx []int,
	selected, listScroll, rightW, bodyH int,
	filterActive bool,
	filterText string,
	th *theme.Theme,
) (lines []string, hits []LauncherHit, scrollMax int) {
	push := func(s string) {
		lines = append(lines, padLine(s, rightW))
	}

	header := sectionHeader("Recent Workspaces", rightW, th)
	if filterActive {
		header = th.Styles.DimLabel.Render("filter ") + th.Styles.Body.Render(filterText+"▌")
	}
	push(header)

	listH := bodyH - 1
	if listH < 3 {
		listH = 3
	}

	type flat struct {
		line    string
		item    int
		isFirst bool
	}
	var flatRows []flat
	for _, i := range wsIdx {
		it := items[i]
		if it.Workspace == nil {
			continue
		}
		name := it.Workspace.Name
		if name == "" {
			name = "untitled"
		}
		path := it.Workspace.Path
		meta := sessionCountLabel(it.Workspace.SessionCount)
		block := renderCompactRow(selected == i, "▸ "+name+"  "+meta, path, th, rightW, 0, 0)
		for j, ln := range block {
			flatRows = append(flatRows, flat{line: ln, item: i, isFirst: j == 0})
		}
	}

	scrollMax = len(flatRows) - listH
	if scrollMax < 0 {
		scrollMax = 0
	}
	if listScroll > scrollMax {
		listScroll = scrollMax
	}
	if listScroll < 0 {
		listScroll = 0
	}

	if len(flatRows) == 0 {
		push(th.Styles.DimLabel.Render("  no other workspaces yet"))
	} else {
		end := listScroll + listH
		if end > len(flatRows) {
			end = len(flatRows)
		}
		for i := listScroll; i < end; i++ {
			fr := flatRows[i]
			y0 := len(lines)
			push(fr.line)
			if fr.item >= 0 && fr.isFirst {
				y1 := y0
				for k := i + 1; k < end && flatRows[k].item == fr.item && !flatRows[k].isFirst; k++ {
					y1++
				}
				hits = append(hits, LauncherHit{X0: 0, Y0: y0, X1: rightW - 1, Y1: y1, ItemIdx: fr.item})
			}
		}
	}

	for len(lines) < bodyH {
		push("")
	}
	if len(lines) > bodyH {
		lines = lines[:bodyH]
	}
	return lines, hits, scrollMax
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
		"Open Folder launches a browser; pick any project root.",
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
		mapped = selected + 1 // skip open folder
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
