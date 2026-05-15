package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"crude/tui/components"
	"crude/tui/theme"
	"crude/tui/widgets"
)

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

	theme *theme.Theme
}

func NewSubPane(id string, paneType PaneType, title string, th *theme.Theme) *SubPane {
	ml := widgets.NewMessageList(th)
	return &SubPane{
		ID:        id,
		Title:     title,
		Type:      paneType,
		MsgList:   ml,
		Done:      false,
		StartTime: time.Now(),
		theme:     th,
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
		tokens = fmt.Sprintf(" ↓%d tokens", sp.TokenCount)
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

func (sp *SubPane) Render(width, maxH, spinFrame int) PaneRenderResult {
	if sp.Done {
		innerW := width - sp.theme.Spacing.PadLR*2
		if innerW < 10 {
			innerW = 10
		}
		summary := sp.renderSummaryLine(innerW)
		return PaneRenderResult{
			Lines: []string{"", summary},
		}
	}

	sp.MsgList.SetSize(width, maxH)
	sp.MsgList.SetChatAreaBounds(0, maxH)
	sp.MsgList.SetSpinFrame(spinFrame)
	sp.MsgList.SetActiveTyping(!sp.Done)
	sp.MsgList.SetPhase("")

	sbW := sp.MsgList.ScrollbarWidth()
	innerW := width - sp.theme.Spacing.PadLR*2 - sbW
	if innerW < 10 {
		innerW = 10
	}

	contentLines := sp.MsgList.Render(innerW)

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

	headerLine := sp.renderHeaderLine(innerW, sbW, spinFrame)

	lines := make([]string, 0, 1+visibleH+1)
	lines = append(lines, "")
	lines = append(lines, headerLine)
	for i := 0; i < visibleH; i++ {
		if i < len(contentLines) {
			lines = append(lines, contentLines[i])
		} else {
			lines = append(lines, "")
		}
	}

	return PaneRenderResult{
		Lines:         lines,
		PaneID:        sp.ID,
		ContentStart:  2,
		ContentHeight: visibleH,
	}
}

func (sp *SubPane) ScrollbarLineCount() int {
	if sp.Done || sp.MsgList == nil {
		return 0
	}
	return sp.MsgList.ContentHeight()
}

func (sp *SubPane) ScrollbarView() string {
	return sp.MsgList.ScrollbarView()
}

func (sp *SubPane) ScrollbarWidth() int {
	return sp.MsgList.ScrollbarWidth()
}

func (sp *SubPane) renderHeaderLine(innerW, sbW, spinFrame int) string {
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

	content := borderPrefix + styledTitle + " " + styledMeta
	visW := lipgloss.Width(content)
	padR := innerW - visW
	if padR < 0 {
		padR = 0
	}
	return content + strings.Repeat(" ", padR)
}

func (sp *SubPane) renderSummaryLine(innerW int) string {
	borderPrefix := lipgloss.NewStyle().Foreground(sp.theme.Colors.Border).Render("│ ")
	summary := sp.SummaryText()
	styled := lipgloss.NewStyle().Foreground(sp.theme.Colors.Muted).Render("● " + summary)
	full := borderPrefix + styled
	visW := lipgloss.Width(full)
	padR := innerW - visW
	if padR < 0 {
		padR = 0
	}
	return full + strings.Repeat(" ", padR)
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
		if !sp.Done && sp.MsgList != nil && sp.MsgList.ScrollbarWidth() > 0 {
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
