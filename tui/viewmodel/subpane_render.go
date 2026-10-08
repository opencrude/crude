package viewmodel

import (
	"crude/tui/chatprojection"
	"crude/tui/screens/shellchat"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

// subPaneProjection is the TUI-owned rendering and projection state for one
// subpane. It owns the concrete *chatprojection.Renderer (the retained render
// cache + selection + scrollbar) and the compact-agent scroll state that
// the workspace used to own.
//
// The workspace owns the canonical Conversation data; this projection
// synchronizes its MessageList from workspace.SubPane.View() by stable
// message ID and Conversation version — the same pattern used for
// AgentState.Messages -> syncAgentMessages.
type subPaneProjection struct {
	paneID   string
	renderer *chatprojection.Renderer

	// paneType mirrors the workspace SubPane.Type. The projection needs it
	// to decide whether the compact scrollbar or the MessageList scrollbar
	// is the active one (for ScrollbarWidth/View/LineCount).
	paneType workspace.PaneType

	// compact-agent scroll state. The workspace SubPane no longer owns
	// scrollbar/cursor state; this projection does.
	scroll *ui.ScrollModel

	// Compact titles retain source/geometry caches; only active dots depend on phase.
	compactToolLines  []chatprojection.SubPaneLine
	compactCacheWidth int
	compactMsgVersion uint64
	compactMsgCount   int
	compactAnimating  bool
	compactDot        string
	// expanded mirrors the parent chat block's disclosure state so completed
	// panes retain their scrollbar when reopened.
	expanded bool

	source *workspace.Conversation
}

// subPaneProjectionRegistry owns the per-pane projections and the
// cross-pane aggregate operations that PaneManager used to expose.
type subPaneProjectionRegistry struct {
	projections map[string]*subPaneProjection
	theme       *theme.Theme
	// owners retain independent inline presentation; root projections remain main renderers.
	owners   map[string]*subPaneProjectionRegistry
	embedded bool
	username string
}

func newSubPaneProjectionRegistry(th *theme.Theme) *subPaneProjectionRegistry {
	return &subPaneProjectionRegistry{
		projections: make(map[string]*subPaneProjection),
		theme:       th,
	}
}

// ForOwner returns retained inline presentation state for one embedding main agent.
// It never replaces the root registry's canonical main-chat projection.
func (reg *subPaneProjectionRegistry) ForOwner(owner string) *subPaneProjectionRegistry {
	if reg.owners == nil {
		reg.owners = make(map[string]*subPaneProjectionRegistry)
	}
	if child := reg.owners[owner]; child != nil {
		return child
	}
	child := newSubPaneProjectionRegistry(reg.theme)
	child.embedded = true
	child.username = reg.username
	reg.owners[owner] = child
	return child
}

func (reg *subPaneProjectionRegistry) Theme() *theme.Theme { return reg.theme }

// SetTheme replaces the registry theme and pushes it to every existing
// chatprojection.Renderer so open subpanes do not keep a stale palette.
func (reg *subPaneProjectionRegistry) SetTheme(th *theme.Theme) {
	if reg == nil || th == nil {
		return
	}
	reg.theme = th
	for _, child := range reg.owners {
		child.SetTheme(th)
	}
	for _, p := range reg.projections {
		if p != nil && p.renderer != nil {
			p.renderer.SetTheme(th)
		}
	}
}

// Get returns the projection for paneID, creating it (with a fresh
// MessageList) if missing. paneType is recorded on first creation so the
// registry can later decide which scrollbar path is active. Callers must
// initialize SetLegacyModelFallback/SetUsername/SetSubPaneContentFn after first
// creation. On an existing projection, paneType is updated to the passed
// value (cheap) so the registry stays correct if a pane is re-created
// with a different type.
func (reg *subPaneProjectionRegistry) Get(paneID string, paneType workspace.PaneType) *subPaneProjection {
	if p, ok := reg.projections[paneID]; ok {
		p.paneType = paneType
		return p
	}
	p := &subPaneProjection{
		paneID:   paneID,
		paneType: paneType,
		renderer: chatprojection.NewRenderer(reg.theme),
		scroll:   ui.NewScrollModel(),
	}
	if paneType == workspace.PaneThinking {
		p.renderer.SetThinkingBodyOnly(true)
	}
	p.renderer.SetUsername(reg.username)
	p.scroll.SetAutoScroll(true)
	p.renderer.BindPaneScroll(p.scroll)
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
	for _, child := range reg.owners {
		child.Remove(paneID)
	}
	delete(reg.owners, paneID)
}

// SetUsername propagates the username to every projection's MessageList.
func (reg *subPaneProjectionRegistry) SetUsername(name string) {
	reg.username = name
	for _, child := range reg.owners {
		child.SetUsername(name)
	}
	for _, p := range reg.projections {
		p.renderer.SetUsername(name)
	}
}

func (reg *subPaneProjectionRegistry) InvalidateWidthProfile() {
	for _, child := range reg.owners {
		child.InvalidateWidthProfile()
	}
	for _, projection := range reg.projections {
		if projection != nil && projection.renderer != nil {
			projection.renderer.InvalidateWidthProfile()
		}
	}
}

// TickAll publishes the shared frame clock. Animated rows are deliberately
// uncached; static message caches remain source-version owned across ticks.
func (reg *subPaneProjectionRegistry) TickAll(spinFrame int) {
	for _, child := range reg.owners {
		child.TickAll(spinFrame)
	}
	for _, p := range reg.projections {
		p.renderer.SetSpinFrame(spinFrame)
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
			if p.scroll.MaxOffset() > 0 {
				return true
			}
			continue
		}
		if p.renderer.PaneScrollMaxOffset() > 0 {
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
	for _, child := range reg.owners {
		child.InvalidateSubPane(parentPaneID, subPaneID)
	}
	if p, ok := reg.projections[parentPaneID]; ok {
		p.renderer.InvalidateSubPane(subPaneID)
	}
}

// InvalidateAllMessages forwards to the projection's MessageList.
func (reg *subPaneProjectionRegistry) InvalidateAllMessages(paneID string) {
	for _, child := range reg.owners {
		child.InvalidateAllMessages(paneID)
	}
	if p, ok := reg.projections[paneID]; ok {
		p.renderer.InvalidateAllMessages()
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
	if p.source == conversation && p.renderer.SourceVersion() == conversation.Version() {
		return
	}
	messages, version := chatMessagesFromConversation(conversation)
	if p.source != conversation && p.renderer.SourceVersion() == version {
		p.renderer.SyncMessages(nil, 0)
	}
	p.renderer.SyncMessages(messages, version)
	p.source = conversation
	if scroll {
		p.renderer.ScrollToBottom()
	}
}

// ActiveThinkingLineCount forwards to the projection's MessageList.
func (reg *subPaneProjectionRegistry) ActiveThinkingLineCount(paneID string, contentWidth int) int {
	if p, ok := reg.projections[paneID]; ok {
		return p.renderer.ActiveThinkingLineCount(contentWidth)
	}
	return 0
}

// SetStreaming forwards to the projection's MessageList.
func (reg *subPaneProjectionRegistry) SetStreaming(paneID string, v bool) {
	for _, child := range reg.owners {
		child.SetStreaming(paneID, v)
	}
	if p, ok := reg.projections[paneID]; ok {
		p.renderer.SetStreaming(v)
	}
}

// SetActiveTyping forwards to the projection's MessageList.
func (reg *subPaneProjectionRegistry) SetActiveTyping(paneID string, v bool) {
	for _, child := range reg.owners {
		child.SetActiveTyping(paneID, v)
	}
	if p, ok := reg.projections[paneID]; ok {
		p.renderer.SetActiveTyping(v)
	}
}

// SetPaneScrollbarWidth forwards to the projection's MessageList.
func (reg *subPaneProjectionRegistry) SetPaneScrollbarWidth(paneID string, w int) {
	if p, ok := reg.projections[paneID]; ok {
		p.renderer.SetPaneScrollbarWidth(w)
	}
}

// PaneGutterState returns native gutter paint/input metadata for shellchat.
func (reg *subPaneProjectionRegistry) PaneGutterState(paneID string, sp *workspace.SubPane) shellchat.PaneGutterState {
	p, ok := reg.projections[paneID]
	if !ok || sp == nil || (sp.Done && !p.expanded) {
		return shellchat.PaneGutterState{}
	}
	if sp.Type == workspace.PaneAgent {
		return shellchat.PaneGutterState{
			Scroll:  p.scroll,
			TrackH:  p.scroll.Viewport(),
			Visible: p.scroll.MaxOffset() > 0,
		}
	}
	return shellchat.PaneGutterState{
		Scroll:  p.scroll,
		TrackH:  p.scroll.Viewport(),
		Visible: p.renderer.PaneScrollMaxOffset() > 0,
	}
}

// ScrollModel returns the per-pane scroll model.
func (reg *subPaneProjectionRegistry) ScrollModel(paneID string) *ui.ScrollModel {
	if p, ok := reg.projections[paneID]; ok {
		return p.scroll
	}
	return nil
}

// ScrollbarWidth returns the scrollbar column width for the pane.
func (reg *subPaneProjectionRegistry) ScrollbarWidth(paneID string, sp *workspace.SubPane) int {
	p, ok := reg.projections[paneID]
	if !ok {
		return 0
	}
	if sp.Done && !p.expanded {
		return 0
	}
	if sp.Type == workspace.PaneAgent {
		if p.scroll.MaxOffset() > 0 {
			return ui.ScrollbarWidth
		}
		return 0
	}
	if p.renderer.PaneScrollMaxOffset() > 0 {
		return ui.ScrollbarWidth
	}
	return 0
}

// ScrollbarLineCount returns the content height used by the scrollbar.
func (reg *subPaneProjectionRegistry) ScrollbarLineCount(paneID string, sp *workspace.SubPane) int {
	p, ok := reg.projections[paneID]
	if !ok {
		return 0
	}
	if sp.Done && !p.expanded {
		return 0
	}
	if sp.Type == workspace.PaneAgent {
		return p.scroll.Content()
	}
	return p.renderer.ContentHeight()
}

// BuildPaneGutters collects gutter state for all panes referenced in paneLineMap.
func (reg *subPaneProjectionRegistry) BuildPaneGutters(paneLineMap []chatprojection.PaneLineInfo, paneMgr *workspace.PaneManager) map[string]shellchat.PaneGutterState {
	out := make(map[string]shellchat.PaneGutterState)
	for _, info := range paneLineMap {
		if info.PaneID == "" {
			continue
		}
		if _, ok := out[info.PaneID]; ok {
			continue
		}
		sp := paneMgr.Get(info.PaneID)
		if sp == nil {
			continue
		}
		out[info.PaneID] = reg.PaneGutterState(info.PaneID, sp)
	}
	return out
}

// Render produces subpane rows for chatprojection embedding. Structured
// native rows are returned for compact headers/tool lines and thinking-pane
// MessageList content (which is already native from renderThinkingMessage).
// expanded is the chat-level disclosure state: a completed pane that is not
// expanded collapses to its one-line summary.
func (reg *subPaneProjectionRegistry) Render(paneID string, sp *workspace.SubPane, width, maxH, spinFrame int, expanded bool) []chatprojection.SubPaneLine {
	if width < 10 {
		width = 10
	}
	p := reg.Get(paneID, sp.Type)
	p.expanded = expanded
	if reg.embedded {
		reg.SyncFromWorkspace(paneID, sp.Messages, false)
	}
	view := sp.View()

	if sp.Done && !expanded {
		return []chatprojection.SubPaneLine{
			chatprojection.BuildSubPaneSummaryLine(view, width),
		}
	}

	if sp.Type == workspace.PaneAgent {
		return reg.renderAgentCompact(paneID, sp, view, width, maxH, spinFrame, expanded)
	}

	// PaneThinking: delegate to the MessageList projection, same as the
	// prior SubPane.Render path. Keep topPad/scroll arithmetic identical to
	// the string path; only the per-row carrier changes (NativeLine).
	p.renderer.SetSize(width, maxH)
	p.renderer.SetChatAreaBounds(0, maxH)
	p.renderer.SetSpinFrame(spinFrame)
	p.renderer.SetActiveTyping(!sp.Done)
	p.renderer.SetPhase("")

	contentLines, contentNative := p.renderer.RenderVisible(width)
	actualH := p.renderer.ContentHeight()
	visibleH := actualH
	if visibleH > maxH {
		visibleH = maxH
	}
	p.scroll.SetContent(actualH)
	p.scroll.SetViewport(visibleH)
	if p.scroll.AutoScroll() {
		p.scroll.StickToBottom()
	} else {
		p.scroll.Clamp()
	}
	if visibleH > len(contentLines) {
		visibleH = len(contentLines)
	}
	if actualH <= maxH && actualH < len(contentLines) {
		topPad := len(contentLines) - actualH
		contentLines = contentLines[topPad:]
		if topPad < len(contentNative) {
			contentNative = contentNative[topPad:]
		} else {
			contentNative = nil
		}
	}

	headerLine := chatprojection.BuildSubPaneHeaderLine(view.Header, width, spinFrame)
	lines := make([]chatprojection.SubPaneLine, 0, 1+visibleH)
	lines = append(lines, headerLine)
	for i := 0; i < visibleH; i++ {
		if i < len(contentLines) {
			if i < len(contentNative) && !contentNative[i].PreferANSI() {
				lines = append(lines, chatprojection.NativeLine(contentNative[i]))
			} else {
				// Fallback: thinking content is plain text (never Glamour),
				// so emit a native plain body line rather than an ANSI row.
				lines = append(lines, chatprojection.NativeLine(chatprojection.PaintLine{
					Spans: []chatprojection.RowSpan{{Text: contentLines[i], Role: ui.ColorFg}},
				}))
			}
		}
	}
	return lines
}

func (reg *subPaneProjectionRegistry) renderAgentCompact(paneID string, sp *workspace.SubPane, view workspace.SubPaneView, width, maxH, spinFrame int, expanded bool) []chatprojection.SubPaneLine {
	p := reg.Get(paneID, sp.Type)

	msgs := p.renderer.Messages()
	var allToolLines []chatprojection.SubPaneLine
	msgVersion := p.renderer.Version()
	dot := ui.ActivityDot(spinFrame)
	if p.compactToolLines != nil && p.compactMsgVersion == msgVersion && p.compactMsgCount == len(msgs) && p.compactCacheWidth == width && (!p.compactAnimating || p.compactDot == dot) {
		allToolLines = p.compactToolLines
	} else {
		allToolLines, p.compactAnimating = chatprojection.BuildSubPaneToolLines(msgs, width, spinFrame)
		p.compactDot = dot
		p.compactToolLines = allToolLines
		p.compactMsgVersion = msgVersion
		p.compactMsgCount = len(msgs)
		p.compactCacheWidth = width
	}

	visibleH := maxH - 1
	if visibleH < 1 {
		visibleH = 1
	}

	contentH := len(allToolLines)
	p.scroll.SetContent(contentH)
	p.scroll.SetViewport(visibleH)
	if p.scroll.AutoScroll() {
		p.scroll.StickToBottom()
	} else {
		p.scroll.Clamp()
	}

	var toolLines []chatprojection.SubPaneLine
	if len(allToolLines) == 0 {
		thinking := p.renderer.ActiveThinkingLineCount(width) > 0
		toolLines = append(toolLines, chatprojection.BuildCompactWorkingLine(thinking, width))
	} else {
		start := p.scroll.Offset()
		end := start + visibleH
		if end > len(allToolLines) {
			end = len(allToolLines)
		}
		toolLines = allToolLines[start:end]
	}

	headerLine := chatprojection.BuildSubPaneHeaderLine(view.Header, width, spinFrame)
	lines := make([]chatprojection.SubPaneLine, 0, 1+len(toolLines))
	lines = append(lines, headerLine)
	lines = append(lines, toolLines...)
	return lines
}

// renderSubPaneContentForApp is the SubPaneContentFn signature expected
// by MessageList.SetSubPaneContentFn. It is set on each projection's
// MessageList so the parent's render path still calls back into the
// registry.
func (m *App) renderSubPaneContentForApp(paneID string, width, maxH, spinFrame int, expanded bool) []chatprojection.SubPaneLine {
	return m.subPaneContentForRegistry(m.subPaneProjections)(paneID, width, maxH, spinFrame, expanded)
}

// subPaneContentForRegistry keeps nested rendering in the embedding main view.
func (m *App) subPaneContentForRegistry(reg *subPaneProjectionRegistry) chatprojection.SubPaneContentFn {
	return func(paneID string, width, maxH, spinFrame int, expanded bool) []chatprojection.SubPaneLine {
		sp := m.PaneMgr.Get(paneID)
		if sp == nil {
			return []chatprojection.SubPaneLine{
				chatprojection.NativeLine(chatprojection.PaintLine{Spans: []chatprojection.RowSpan{{Text: "", Role: ui.ColorFg}}}),
				chatprojection.NativeLine(chatprojection.PaintLine{Spans: []chatprojection.RowSpan{{Text: "● unknown pane", Role: ui.ColorMuted}}}),
			}
		}
		p := reg.Get(paneID, sp.Type)
		p.renderer.SetSubPaneContentFn(m.subPaneContentForRegistry(reg))
		if n := m.AgentMgr.Get(paneID); n != nil && n.State != nil {
			p.renderer.SetLegacyModelFallback(n.Model)
			p.renderer.SetStreaming(n.State.Streaming)
			p.renderer.SetActiveTyping(n.State.ComputeActiveTyping())
		}
		return reg.Render(paneID, sp, width, maxH, spinFrame, expanded)
	}
}
