package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/scrollbar"
	"crude/tui/theme"
	"crude/tui/widgets"
	"crude/workspace"
)

// subPaneProjection is the TUI-owned rendering and projection state for one
// subpane. It owns the concrete *widgets.MessageList (the retained render
// cache + selection + scrollbar) and the compact-agent scroll state that
// the workspace used to own.
//
// The workspace owns the canonical Conversation data; this projection
// synchronizes its MessageList from workspace.SubPane.View() by stable
// message ID and Conversation version — the same pattern used for
// AgentState.Messages -> syncAgentMessages.
type subPaneProjection struct {
	paneID  string
	msgList *widgets.MessageList

	// paneType mirrors the workspace SubPane.Type. The projection needs it
	// to decide whether the compact scrollbar or the MessageList scrollbar
	// is the active one (for ScrollbarWidth/View/LineCount).
	paneType workspace.PaneType
	done     bool

	// compact-agent scroll state. The workspace SubPane no longer owns
	// scrollbar/cursor state; this projection does.
	compactSB         scrollbar.ScrollBar
	compactScrollY    int
	compactContentH   int
	compactVisibleH   int
	compactAutoScroll bool

	// compact tool-line ANSI cache (keyed by source version + width + count).
	compactToolLines  []string
	compactCacheWidth int
	compactMsgVersion uint64
	compactMsgCount   int
}

// subPaneProjectionRegistry owns the per-pane projections and the
// cross-pane aggregate operations that PaneManager used to expose.
type subPaneProjectionRegistry struct {
	projections map[string]*subPaneProjection
	theme       *theme.Theme
}

func newSubPaneProjectionRegistry(th *theme.Theme) *subPaneProjectionRegistry {
	return &subPaneProjectionRegistry{
		projections: make(map[string]*subPaneProjection),
		theme:       th,
	}
}

func (reg *subPaneProjectionRegistry) Theme() *theme.Theme { return reg.theme }

// Get returns the projection for paneID, creating it (with a fresh
// MessageList) if missing. paneType is recorded on first creation so the
// registry can later decide which scrollbar path is active. Callers must
// initialize SetModelName/SetUsername/SetSubPaneContentFn after first
// creation. On an existing projection, paneType is updated to the passed
// value (cheap) so the registry stays correct if a pane is re-created
// with a different type.
func (reg *subPaneProjectionRegistry) Get(paneID string, paneType workspace.PaneType) *subPaneProjection {
	if p, ok := reg.projections[paneID]; ok {
		p.paneType = paneType
		return p
	}
	p := &subPaneProjection{
		paneID:            paneID,
		paneType:          paneType,
		msgList:           widgets.NewMessageList(reg.theme),
		compactSB:         scrollbar.New(0),
		compactAutoScroll: true,
	}
	reg.projections[paneID] = p
	return p
}

// GetExisting returns the projection only if it already exists.
func (reg *subPaneProjectionRegistry) GetExisting(paneID string) *subPaneProjection {
	return reg.projections[paneID]
}

// Has reports whether a projection exists for paneID.
func (reg *subPaneProjectionRegistry) Has(paneID string) bool {
	_, ok := reg.projections[paneID]
	return ok
}

// Remove deletes the projection for paneID. Safe to call when missing.
func (reg *subPaneProjectionRegistry) Remove(paneID string) {
	delete(reg.projections, paneID)
}

// SetModelName propagates the model name to every projection's MessageList.
func (reg *subPaneProjectionRegistry) SetModelName(name string) {
	for _, p := range reg.projections {
		p.msgList.SetModelName(name)
	}
}

// SetUsername propagates the username to every projection's MessageList.
func (reg *subPaneProjectionRegistry) SetUsername(name string) {
	for _, p := range reg.projections {
		p.msgList.SetUsername(name)
	}
}

// SetSize resizes every projection's MessageList. The max-height per pane
// matches the prior PaneManager.SetSize behavior (h/3 with a 5-row floor).
func (reg *subPaneProjectionRegistry) SetSize(w, h int) {
	maxH := h / 3
	if maxH < 5 {
		maxH = 5
	}
	for _, p := range reg.projections {
		p.msgList.SetSize(w, maxH)
	}
}

// TickAll invalidates caches and refreshes the spin frame on every
// projection's MessageList — the prior PaneManager.TickAll behavior.
func (reg *subPaneProjectionRegistry) TickAll(spinFrame int) {
	for _, p := range reg.projections {
		p.msgList.InvalidateCache()
		p.msgList.SetSpinFrame(spinFrame)
	}
}

// HasAnyScrollbarVisible reports whether any non-Done pane has a visible
// scrollbar (the prior PaneManager.HasAnyScrollbarVisible behavior).
func (reg *subPaneProjectionRegistry) HasAnyScrollbarVisible(done map[string]bool) bool {
	for id, p := range reg.projections {
		if done[id] {
			continue
		}
		if p.paneType == workspace.PaneAgent {
			if p.compactSB.IsVisible() && p.compactContentH > 0 {
				return true
			}
			continue
		}
		if p.msgList.ScrollbarWidth() > 0 {
			return true
		}
	}
	return false
}

// ScrollbarNestingLevel mirrors the prior PaneManager.ScrollbarNestingLevel.
// `done` is the workspace's per-pane Done map so the registry does not
// reach back into SubPane state.
func (reg *subPaneProjectionRegistry) ScrollbarNestingLevel(done map[string]bool) int {
	if reg.HasAnyScrollbarVisible(done) {
		return 1
	}
	return 0
}

// InvalidateSubPane forwards to the owning projection's MessageList.
func (reg *subPaneProjectionRegistry) InvalidateSubPane(parentPaneID, subPaneID string) {
	if p, ok := reg.projections[parentPaneID]; ok {
		p.msgList.InvalidateSubPane(subPaneID)
	}
}

// InvalidateAllMessages forwards to the projection's MessageList.
func (reg *subPaneProjectionRegistry) InvalidateAllMessages(paneID string) {
	if p, ok := reg.projections[paneID]; ok {
		p.msgList.InvalidateAllMessages()
	}
}

// SyncFromWorkspace projects the workspace Conversation into the
// projection's MessageList by stable ID/version. No-op when the
// MessageList is already at this Conversation version (preserves
// retained caches, selection, scroll).
func (reg *subPaneProjectionRegistry) SyncFromWorkspace(paneID string, conversation *workspace.Conversation, scroll bool) {
	p, ok := reg.projections[paneID]
	if !ok {
		return
	}
	if conversation == nil {
		return
	}
	if p.msgList.SourceVersion() == conversation.Version() {
		return
	}
	messages, version := chatMessagesFromConversation(conversation)
	p.msgList.SyncMessages(messages, version)
	if scroll {
		p.msgList.ScrollToBottom()
	}
}

// ActiveThinkingLineCount forwards to the projection's MessageList.
func (reg *subPaneProjectionRegistry) ActiveThinkingLineCount(paneID string, contentWidth int) int {
	if p, ok := reg.projections[paneID]; ok {
		return p.msgList.ActiveThinkingLineCount(contentWidth)
	}
	return 0
}

// SetStreaming forwards to the projection's MessageList.
func (reg *subPaneProjectionRegistry) SetStreaming(paneID string, v bool) {
	if p, ok := reg.projections[paneID]; ok {
		p.msgList.SetStreaming(v)
	}
}

// SetActiveTyping forwards to the projection's MessageList.
func (reg *subPaneProjectionRegistry) SetActiveTyping(paneID string, v bool) {
	if p, ok := reg.projections[paneID]; ok {
		p.msgList.SetActiveTyping(v)
	}
}

// SetPaneScrollbarWidth forwards to the projection's MessageList.
func (reg *subPaneProjectionRegistry) SetPaneScrollbarWidth(paneID string, w int) {
	if p, ok := reg.projections[paneID]; ok {
		p.msgList.SetPaneScrollbarWidth(w)
	}
}

// HandleScrollbarTick forwards a scrollbar tick to both the projection's
// MessageList and its compact scrollbar (for PaneAgent panes).
func (reg *subPaneProjectionRegistry) HandleScrollbarTick(paneID string, msg tea.Msg, isCompactActive bool) tea.Cmd {
	p, ok := reg.projections[paneID]
	if !ok {
		return nil
	}
	var cmd tea.Cmd
	if isCompactActive {
		newSB, tickCmd := p.compactSB.Update(msg)
		p.compactSB = newSB
		// compact scrollbar tick does not itself return a cmd today,
		// but keep the call shape for forward compatibility.
		_ = tickCmd
	} else {
		cmd = p.msgList.HandleScrollbarTick(msg)
	}
	return cmd
}

// CanScrollCompact reports whether the compact-agent scroll is active.
func (reg *subPaneProjectionRegistry) CanScrollCompact(paneID string) bool {
	p, ok := reg.projections[paneID]
	if !ok {
		return false
	}
	return p.compactContentH > p.compactVisibleH && p.compactVisibleH > 0
}

// ScrollCompactUp scrolls the compact-agent view up.
func (reg *subPaneProjectionRegistry) ScrollCompactUp(paneID string) {
	p, ok := reg.projections[paneID]
	if !ok {
		return
	}
	p.compactAutoScroll = false
	if p.compactScrollY > 0 {
		p.compactScrollY--
	}
}

// ScrollCompactDown scrolls the compact-agent view down.
func (reg *subPaneProjectionRegistry) ScrollCompactDown(paneID string) {
	p, ok := reg.projections[paneID]
	if !ok {
		return
	}
	maxScroll := p.compactContentH - p.compactVisibleH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if p.compactScrollY < maxScroll {
		p.compactScrollY++
	}
	if p.compactScrollY >= maxScroll {
		p.compactAutoScroll = true
	}
}

// ScrollbarView returns the scrollbar view for the pane. For PaneAgent
// panes that are not Done, the compact scrollbar view is returned;
// otherwise the MessageList scrollbar view is used.
func (reg *subPaneProjectionRegistry) ScrollbarView(paneID string, sp *workspace.SubPane) string {
	p, ok := reg.projections[paneID]
	if !ok {
		return ""
	}
	if sp.Type == workspace.PaneAgent && !sp.Done {
		return p.compactSB.View()
	}
	return p.msgList.ScrollbarView()
}

// ScrollbarWidth returns the scrollbar column width for the pane.
func (reg *subPaneProjectionRegistry) ScrollbarWidth(paneID string, sp *workspace.SubPane) int {
	p, ok := reg.projections[paneID]
	if !ok {
		return 0
	}
	if sp.Type == workspace.PaneAgent && !sp.Done {
		if p.compactSB.IsVisible() && p.compactContentH > 0 {
			return scrollbar.ScrollbarWidth
		}
		return 0
	}
	return p.msgList.ScrollbarWidth()
}

// ScrollbarLineCount returns the content height used by the scrollbar.
func (reg *subPaneProjectionRegistry) ScrollbarLineCount(paneID string, sp *workspace.SubPane) int {
	p, ok := reg.projections[paneID]
	if !ok {
		return 0
	}
	if sp.Done {
		return 0
	}
	if sp.Type == workspace.PaneAgent {
		return p.compactContentH
	}
	return p.msgList.ContentHeight()
}

// Render produces the subpane lines to be embedded in the chat message
// list. It reads the presentation-neutral SubPaneView from the workspace
// and applies the same width contract as the prior SubPane.Render: the
// caller already subtracted scrollbar columns and gaps; the projection
// must not subtract PadLR or scrollbar space itself.
func (reg *subPaneProjectionRegistry) Render(paneID string, sp *workspace.SubPane, width, maxH, spinFrame int) []string {
	if width < 10 {
		width = 10
	}
	view := sp.View()

	if sp.Done {
		return []string{renderSubPaneSummaryLine(reg.theme, view, width)}
	}

	if sp.Type == workspace.PaneAgent {
		return reg.renderAgentCompact(paneID, sp, view, width, maxH, spinFrame)
	}

	// PaneThinking: delegate to the MessageList projection, same as the
	// prior SubPane.Render path.
	p := reg.Get(paneID, sp.Type)
	p.msgList.SetSize(width, maxH)
	p.msgList.SetChatAreaBounds(0, maxH)
	p.msgList.SetSpinFrame(spinFrame)
	p.msgList.SetActiveTyping(!sp.Done)
	p.msgList.SetPhase("")

	contentLines := p.msgList.Render(width)
	actualH := p.msgList.ContentHeight()
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

	headerLine := renderSubPaneHeaderLine(reg.theme, view.Header, width, spinFrame)
	lines := make([]string, 0, 1+visibleH)
	lines = append(lines, headerLine)
	for i := 0; i < visibleH; i++ {
		if i < len(contentLines) {
			lines = append(lines, contentLines[i])
		}
	}
	return lines
}

// renderAgentCompact mirrors the prior SubPane.renderAgentCompact but
// reads from the workspace SubPaneView and writes to projection-owned
// scroll/cache state.
func (reg *subPaneProjectionRegistry) renderAgentCompact(paneID string, sp *workspace.SubPane, view workspace.SubPaneView, width, maxH, spinFrame int) []string {
	p := reg.Get(paneID, sp.Type)
	th := reg.theme
	borderStyle := lipgloss.NewStyle().Foreground(th.Colors.Border)
	mutedStyle := lipgloss.NewStyle().Foreground(th.Colors.Muted)

	msgs := p.msgList.Messages()
	var allToolLines []string
	msgVersion := p.msgList.Version()
	if p.compactToolLines != nil && p.compactMsgVersion == msgVersion && p.compactMsgCount == len(msgs) && p.compactCacheWidth == width {
		allToolLines = p.compactToolLines
	} else {
		allToolLines = renderSubPaneToolLines(th, view, width)
		p.compactToolLines = allToolLines
		p.compactMsgVersion = msgVersion
		p.compactMsgCount = len(msgs)
		p.compactCacheWidth = width
	}

	visibleH := maxH - 1
	if visibleH < 1 {
		visibleH = 1
	}

	p.compactContentH = len(allToolLines)
	p.compactVisibleH = visibleH

	maxScroll := p.compactContentH - visibleH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if p.compactAutoScroll {
		p.compactScrollY = maxScroll
	}
	if p.compactScrollY > maxScroll {
		p.compactScrollY = maxScroll
	}
	if p.compactScrollY < 0 {
		p.compactScrollY = 0
	}

	// Keep the workspace SubPane's compact scroll-intent fields mirrored,
	// so CanScrollCompact/ScrollCompact* on the workspace still observe the
	// same state. (These setters exist precisely for this boundary.)
	sp.SetCompactContentH(p.compactContentH)
	sp.SetCompactVisibleH(p.compactVisibleH)
	sp.SetCompactScrollY(p.compactScrollY)
	sp.SetCompactAutoScroll(p.compactAutoScroll)

	var toolLines []string
	if len(allToolLines) == 0 {
		thinking := "Thinking..."
		if p.msgList.ActiveThinkingLineCount(width) == 0 {
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
		start := p.compactScrollY
		end := start + visibleH
		if end > len(allToolLines) {
			end = len(allToolLines)
		}
		toolLines = allToolLines[start:end]
	}

	p.compactSB.SetTrackHeight(visibleH)
	p.compactSB.SetScrollInfo(p.compactContentH, visibleH, p.compactScrollY)

	headerLine := renderSubPaneHeaderLine(th, view.Header, width, spinFrame)
	lines := make([]string, 0, 1+len(toolLines))
	lines = append(lines, headerLine)
	lines = append(lines, toolLines...)
	return lines
}

// renderSubPaneHeaderLine renders the title bar ("│ ● agent-name 12s").
// Pads to exactly `width` visible cells using VisibleWidth (not
// lipgloss.Width) to avoid overflow from wide Unicode chars like │ and ●.
func renderSubPaneHeaderLine(th *theme.Theme, header workspace.SubPaneHeaderView, width, spinFrame int) string {
	borderPrefix := lipgloss.NewStyle().Foreground(th.Colors.Border).Render("│ ")
	var styledTitle string
	if !header.Done {
		switch header.Type {
		case workspace.PaneThinking:
			dotIdx := components.ThinkingDotIndex(spinFrame)
			// Pulse the thinking dot through four accent shades. The exact
			// hex cycle is historical; using the theme Accent + a brightness
			// ramp would be cleaner but is out of scope for the seam removal.
			// Keep the cycle but route via the theme Spinner.Grounding base
			// so a high-contrast theme can override it later.
			base := th.Colors.Spinner.Grounding
			if base == nil {
				base = th.Colors.Accent
			}
			_ = dotIdx
			dot := lipgloss.NewStyle().Foreground(base).Render("● ")
			styledTitle = dot + lipgloss.NewStyle().Foreground(th.Colors.Accent).Bold(true).Render(header.Title)
		default:
			styledTitle = lipgloss.NewStyle().Foreground(th.Colors.Accent).Bold(true).Render("● " + header.Title)
		}
	} else {
		styledTitle = lipgloss.NewStyle().Foreground(th.Colors.Muted).Render("● " + header.Title)
	}

	meta := fmt.Sprintf("%.0fs", header.Elapsed.Seconds())
	styledMeta := lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(meta)

	full := borderPrefix + styledTitle + " " + styledMeta
	visW := rendering.VisibleWidth(full)
	padR := width - visW
	if padR < 0 {
		padR = 0
	}
	return full + strings.Repeat(" ", padR)
}

// renderSubPaneSummaryLine renders the collapsed summary ("│ ● agent for 5.0s").
func renderSubPaneSummaryLine(th *theme.Theme, view workspace.SubPaneView, width int) string {
	borderPrefix := lipgloss.NewStyle().Foreground(th.Colors.Border).Render("│ ")
	summary := ""
	if view.Summary != nil {
		summary = view.Summary.Text
	}
	styled := lipgloss.NewStyle().Foreground(th.Colors.Muted).Render("● " + summary)
	full := borderPrefix + styled
	visW := rendering.VisibleWidth(full)
	padR := width - visW
	if padR < 0 {
		padR = 0
	}
	return full + strings.Repeat(" ", padR)
}

// renderSubPaneToolLines builds the ANSI tool-line rows from the
// presentation-neutral SubPaneView. Each row is padded to `width` visible
// cells.
func renderSubPaneToolLines(th *theme.Theme, view workspace.SubPaneView, width int) []string {
	borderStyle := lipgloss.NewStyle().Foreground(th.Colors.Border)
	var lines []string
	for _, tl := range view.ToolLines {
		if tl.Label == "" {
			continue
		}
		var icon string
		switch {
		case tl.Status == "error" || tl.Phase == "error":
			icon = lipgloss.NewStyle().Foreground(th.Colors.Error).Render("✗")
		case tl.Phase == "done" || tl.Status == "success":
			icon = lipgloss.NewStyle().Foreground(th.Colors.Success).Render("✓")
		case tl.Phase == "streaming" || tl.Status == "streaming" || tl.Status == "calling":
			icon = "●"
		default:
			icon = "○"
		}
		line := borderStyle.Render("│ ") + icon + " " + tl.Label
		visW := rendering.VisibleWidth(line)
		padR := width - visW
		if padR < 0 {
			padR = 0
		}
		lines = append(lines, line+strings.Repeat(" ", padR))
	}
	return lines
}

// renderSubPaneContentForApp is the SubPaneContentFn signature expected
// by MessageList.SetSubPaneContentFn. It is set on each projection's
// MessageList so the parent's render path still calls back into the
// registry.
func (m *App) renderSubPaneContentForApp(paneID string, width, maxH, spinFrame int) []string {
	sp := m.PaneMgr.Get(paneID)
	if sp == nil {
		return []string{"", "● unknown pane"}
	}
	if n := m.AgentMgr.Get(paneID); n != nil && n.State != nil {
		m.subPaneProjections.SetStreaming(paneID, n.State.Streaming)
		m.subPaneProjections.SetActiveTyping(paneID, n.State.ComputeActiveTyping())
	}
	return m.subPaneProjections.Render(paneID, sp, width, maxH, spinFrame)
}

// EnsureTime is a tiny helper so time is referenced even if other
// imports are pruned during incremental edits.
var _ = time.Now

// paneDoneMap builds the per-pane Done snapshot used by the registry's
// aggregate scrollbar queries (HasAnyScrollbarVisible / ScrollbarNestingLevel).
// The registry takes the done-map as a parameter so it never reaches back
// into SubPane state.
func (m *App) paneDoneMap() map[string]bool {
	done := make(map[string]bool, len(m.PaneMgr.Panes()))
	for id, sp := range m.PaneMgr.Panes() {
		done[id] = sp.Done
	}
	return done
}
