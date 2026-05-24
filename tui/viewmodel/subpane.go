package viewmodel

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/scrollbar"
	"crude/tui/theme"
	tuitools "crude/tui/tools"
	"crude/tui/widgets"
)

// PaneType distinguishes subpane variants. Only PaneAgent gets background color
// applied in view.go; PaneThinking (the thinking indicator) has no background.
type PaneType string

const (
	PaneThinking PaneType = "thinking"
	PaneAgent    PaneType = "agent"
)

type SubPane struct {
	ID         string
	Title      string
	Type       PaneType
	MsgList    *widgets.MessageList
	Done       bool
	StartTime  time.Time
	EndTime    time.Time
	TokenCount int

	compactSB         scrollbar.ScrollBar
	compactScrollY    int
	compactContentH   int
	compactVisibleH   int
	compactAutoScroll bool

	theme *theme.Theme
}

func NewSubPane(id string, paneType PaneType, title string, th *theme.Theme) *SubPane {
	ml := widgets.NewMessageList(th)
	return &SubPane{
		ID:                id,
		Title:             title,
		Type:              paneType,
		MsgList:           ml,
		Done:              false,
		StartTime:         time.Now(),
		compactSB:         scrollbar.New(0),
		compactAutoScroll: true,
		theme:             th,
	}
}

func (sp *SubPane) Elapsed() time.Duration {
	if sp.Done && !sp.EndTime.IsZero() {
		return sp.EndTime.Sub(sp.StartTime)
	}
	return time.Since(sp.StartTime)
}

func (sp *SubPane) Collapse() {
	if sp.Done {
		return
	}
	sp.Done = true
	sp.EndTime = time.Now()
}

func (sp *SubPane) SummaryText() string {
	dur := sp.Elapsed()
	tokens := ""
	if sp.TokenCount > 0 {
		tokens = fmt.Sprintf(" ↓%s", rendering.FormatTokens(sp.TokenCount))
	}
	verb := "Thinking"
	if sp.Done {
		verb = "Thinked"
	}
	if sp.Type == PaneAgent {
		verb = sp.Title
	}
	return fmt.Sprintf("%s for %.1fs%s", verb, dur.Seconds(), tokens)
}

type PaneRenderResult struct {
	Lines         []string
	PaneID        string
	ContentStart  int
	ContentHeight int
}

// Render produces the subpane lines to be embedded in the chat message list.
//
// Width contract: `width` is the CONTENT-ONLY width already subtracting all
// scrollbar columns and gaps (computed in renderItem as
// `width - 2*scrollbarWidth - 2`). The subpane must NOT subtract PadLR or
// scrollbar space itself — that's the caller's responsibility.
//
// Background color is NOT applied here. view.go applies it after composing
// the full line (content + padding + scrollbars) so the background spans
// edge-to-edge as a coherent block.
//
// IMPORTANT: We use runewidth.StringWidth(StripAnsi(s)) instead of
// lipgloss.Width(s) for measuring visible width. lipgloss.Width undercounts
// wide Unicode characters like │ and ● by 1 cell each, which caused overflow
// and "..." truncation artifacts.
func (sp *SubPane) Render(width, maxH, spinFrame int) PaneRenderResult {
	if width < 10 {
		width = 10
	}

	if sp.Done {
		summary := sp.renderSummaryLine(width, spinFrame)
		return PaneRenderResult{
			Lines:         []string{summary},
			PaneID:        sp.ID,
			ContentStart:  0,
			ContentHeight: 0,
		}
	}

	if sp.Type == PaneAgent {
		return sp.renderAgentCompact(width, maxH, spinFrame)
	}

	sp.MsgList.SetSize(width, maxH)
	sp.MsgList.SetChatAreaBounds(0, maxH)
	sp.MsgList.SetSpinFrame(spinFrame)
	sp.MsgList.SetActiveTyping(!sp.Done)
	sp.MsgList.SetPhase("")

	contentLines := sp.MsgList.Render(width)

	actualH := sp.MsgList.ContentHeight()
	visibleH := actualH
	if visibleH > maxH {
		visibleH = maxH
	}
	if visibleH > len(contentLines) {
		visibleH = len(contentLines)
	}

	if actualH <= maxH && actualH < len(contentLines) {
		topPad := len(contentLines) - actualH
		contentLines = contentLines[topPad:]
	}

	headerLine := sp.renderHeaderLine(width, spinFrame)

	lines := make([]string, 0, 1+visibleH)
	lines = append(lines, headerLine)
	for i := 0; i < visibleH; i++ {
		if i < len(contentLines) {
			lines = append(lines, contentLines[i])
		}
	}

	return PaneRenderResult{
		Lines:         lines,
		PaneID:        sp.ID,
		ContentStart:  1,
		ContentHeight: visibleH,
	}
}

func (sp *SubPane) renderAgentCompact(width, maxH, spinFrame int) PaneRenderResult {
	headerLine := sp.renderHeaderLine(width, spinFrame)
	borderStyle := lipgloss.NewStyle().Foreground(sp.theme.Colors.Border)
	mutedStyle := lipgloss.NewStyle().Foreground(sp.theme.Colors.Muted)

	msgs := sp.MsgList.Messages()
	var allToolLines []string
	for _, msg := range msgs {
		if msg.Role != "tool" || msg.ToolName == "" {
			continue
		}
		phase := msg.ToolPreviewPhase
		label := tuitools.BuildToolTitle(*msg, phase)
		if label == "" {
			continue
		}

		var icon string
		switch {
		case msg.ToolStatus == "error" || phase == "error":
			icon = lipgloss.NewStyle().Foreground(lipgloss.Color("#f85149")).Render("✗")
		case phase == "done" || msg.ToolStatus == "success":
			icon = lipgloss.NewStyle().Foreground(lipgloss.Color("#3fb950")).Render("✓")
		case phase == "streaming" || msg.ToolStatus == "streaming" || msg.ToolStatus == "calling":
			icon = "●"
		default:
			icon = "○"
		}

		line := borderStyle.Render("│ ") + icon + " " + label
		visW := rendering.VisibleWidth(line)
		padR := width - visW
		if padR < 0 {
			padR = 0
		}
		line = line + strings.Repeat(" ", padR)
		allToolLines = append(allToolLines, line)
	}

	visibleH := maxH - 1
	if visibleH < 1 {
		visibleH = 1
	}

	sp.compactContentH = len(allToolLines)
	sp.compactVisibleH = visibleH

	maxScroll := sp.compactContentH - visibleH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if sp.compactAutoScroll {
		sp.compactScrollY = maxScroll
	}
	if sp.compactScrollY > maxScroll {
		sp.compactScrollY = maxScroll
	}
	if sp.compactScrollY < 0 {
		sp.compactScrollY = 0
	}

	var toolLines []string
	if len(allToolLines) == 0 {
		thinking := "Thinking..."
		if sp.MsgList.ActiveThinkingLineCount(width) == 0 {
			thinking = "Working..."
		}
		line := borderStyle.Render("│ ") + mutedStyle.Render("● "+thinking)
		visW := rendering.VisibleWidth(line)
		padR := width - visW
		if padR < 0 {
			padR = 0
		}
		toolLines = append(toolLines, line+strings.Repeat(" ", padR))
	} else {
		start := sp.compactScrollY
		end := start + visibleH
		if end > len(allToolLines) {
			end = len(allToolLines)
		}
		toolLines = allToolLines[start:end]
	}

	sp.compactSB.SetTrackHeight(visibleH)
	sp.compactSB.SetScrollInfo(sp.compactContentH, visibleH, sp.compactScrollY)

	lines := make([]string, 0, 1+len(toolLines))
	lines = append(lines, headerLine)
	lines = append(lines, toolLines...)

	return PaneRenderResult{
		Lines:         lines,
		PaneID:        sp.ID,
		ContentStart:  1,
		ContentHeight: len(toolLines),
	}
}

func (sp *SubPane) ScrollbarLineCount() int {
	if sp.Done || sp.MsgList == nil {
		return 0
	}
	if sp.Type == PaneAgent {
		return sp.compactContentH
	}
	return sp.MsgList.ContentHeight()
}

func (sp *SubPane) ScrollbarView() string {
	if sp.Type == PaneAgent && !sp.Done {
		return sp.compactSB.View()
	}
	return sp.MsgList.ScrollbarView()
}

func (sp *SubPane) ScrollbarWidth() int {
	if sp.Type == PaneAgent && !sp.Done {
		if sp.compactSB.IsVisible() && sp.compactContentH > 0 {
			return scrollbar.ScrollbarWidth
		}
		return 0
	}
	return sp.MsgList.ScrollbarWidth()
}

// renderHeaderLine produces the title bar ("│ ● agent-name 12s").
// It pads to exactly `width` visible cells using runewidth (not lipgloss.Width)
// to avoid overflow from wide Unicode chars like │ and ●.
func (sp *SubPane) renderHeaderLine(width, spinFrame int) string {
	borderPrefix := lipgloss.NewStyle().Foreground(sp.theme.Colors.Border).Render("│ ")
	var styledTitle string
	if !sp.Done {
		switch sp.Type {
		case PaneThinking:
			dotIdx := components.ThinkingDotIndex(spinFrame)
			dotColors := []string{"#f0883e", "#d0681e", "#b85010", "#d0681e"}
			dotColor := dotColors[dotIdx%len(dotColors)]
			dot := lipgloss.NewStyle().Foreground(lipgloss.Color(dotColor)).Render("● ")
			styledTitle = dot + lipgloss.NewStyle().Foreground(sp.theme.Colors.Accent).Bold(true).Render(sp.Title)
		default:
			styledTitle = lipgloss.NewStyle().Foreground(sp.theme.Colors.Accent).Bold(true).Render("● " + sp.Title)
		}
	} else {
		styledTitle = lipgloss.NewStyle().Foreground(sp.theme.Colors.Muted).Render("● " + sp.Title)
	}

	elapsed := sp.Elapsed()
	meta := fmt.Sprintf("%.0fs", elapsed.Seconds())
	styledMeta := lipgloss.NewStyle().Foreground(sp.theme.Colors.Muted).Render(meta)

	full := borderPrefix + styledTitle + " " + styledMeta
	visW := rendering.VisibleWidth(full)
	padR := width - visW
	if padR < 0 {
		padR = 0
	}
	full = full + strings.Repeat(" ", padR)
	return full
}

// renderSummaryLine produces the collapsed summary ("│ ● agent for 5.0s").
// Same width contract as renderHeaderLine — pads to `width` using runewidth.
func (sp *SubPane) renderSummaryLine(width, spinFrame int) string {
	borderPrefix := lipgloss.NewStyle().Foreground(sp.theme.Colors.Border).Render("│ ")
	summary := sp.SummaryText()
	styled := lipgloss.NewStyle().Foreground(sp.theme.Colors.Muted).Render("● " + summary)
	full := borderPrefix + styled
	visW := rendering.VisibleWidth(full)
	padR := width - visW
	if padR < 0 {
		padR = 0
	}
	full = full + strings.Repeat(" ", padR)
	return full
}

func (sp *SubPane) CanScrollCompact() bool {
	if sp.Done || sp.Type != PaneAgent {
		return false
	}
	return sp.compactContentH > sp.compactVisibleH && sp.compactVisibleH > 0
}

func (sp *SubPane) ScrollCompactUp() {
	sp.compactAutoScroll = false
	if sp.compactScrollY > 0 {
		sp.compactScrollY--
	}
}

func (sp *SubPane) ScrollCompactDown() {
	maxScroll := sp.compactContentH - sp.compactVisibleH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if sp.compactScrollY < maxScroll {
		sp.compactScrollY++
	}
	if sp.compactScrollY >= maxScroll {
		sp.compactAutoScroll = true
	}
}

func (sp *SubPane) HandleScrollbarTick(msg interface{}) {
	if sp.Type == PaneAgent && !sp.Done {
		newSB, _ := sp.compactSB.Update(msg)
		sp.compactSB = newSB
	}
}

type PaneManager struct {
	panes     map[string]*SubPane
	paneOrder []string
}

func NewPaneManager() *PaneManager {
	return &PaneManager{
		panes: make(map[string]*SubPane),
	}
}

func (pm *PaneManager) Get(id string) *SubPane {
	return pm.panes[id]
}

func (pm *PaneManager) GetOrCreate(id string, paneType PaneType, title string, th *theme.Theme) *SubPane {
	if p, ok := pm.panes[id]; ok {
		return p
	}
	sp := NewSubPane(id, paneType, title, th)
	pm.panes[id] = sp
	pm.paneOrder = append(pm.paneOrder, id)
	return sp
}

func (pm *PaneManager) Has(id string) bool {
	_, ok := pm.panes[id]
	return ok
}

func (pm *PaneManager) CollapsePane(id string) {
	if sp, ok := pm.panes[id]; ok {
		sp.Collapse()
	}
}

func (pm *PaneManager) TickAll(spinFrame int) {
	for _, sp := range pm.panes {
		if !sp.Done && sp.MsgList != nil {
			sp.MsgList.InvalidateCache()
			sp.MsgList.SetSpinFrame(spinFrame)
		}
	}
}

func (pm *PaneManager) SetSize(w, h int) {
	for _, sp := range pm.panes {
		if sp.MsgList != nil {
			maxH := h / 3
			if maxH < 5 {
				maxH = 5
			}
			sp.MsgList.SetSize(w, maxH)
		}
	}
}

func (pm *PaneManager) HasAnyActive() bool {
	for _, sp := range pm.panes {
		if !sp.Done {
			return true
		}
	}
	return false
}

func (pm *PaneManager) HasAnyScrollbarVisible() bool {
	for _, sp := range pm.panes {
		if !sp.Done && sp.ScrollbarWidth() > 0 {
			return true
		}
	}
	return false
}

func (pm *PaneManager) ScrollbarNestingLevel() int {
	if pm.HasAnyScrollbarVisible() {
		return 1
	}
	return 0
}
