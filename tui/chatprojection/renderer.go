package chatprojection

import (
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"

	"crude/tui/perf"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/tools"
	"crude/tui/types"
	"crude/tui/ui"
)

var nextRendererSourceID atomic.Uint64

type SubPaneContentFn func(paneID string, width, maxH, spinFrame int, expanded bool) []SubPaneLine

type Renderer struct {
	vl         itemList
	extScroll  *ui.ScrollModel
	paneScroll *ui.ScrollModel
	sync       *MessageSync
	theme      *theme.Theme
	sourceID   uint64

	chatAreaTop    int
	chatAreaHeight int

	mdRenderer          *glamour.TermRenderer
	cachedWidth         int
	width               int
	height              int
	legacyModelFallback string
	username            string
	streaming           bool
	spinFrame           int
	activeTyping        bool
	phase               string

	lastScrollbarW   int
	subPaneContentFn SubPaneContentFn
	paneLineMap      []PaneLineInfo
	paneScrollbarW   int
	nativeCache      map[string][]PaintLine
	tokens           *ui.TokenSet
	// streamNative caches the bridged native rows for the completed portion of
	// a streaming assistant message, keyed by InternalID. streamNativeLen
	// records the StreamCompletedLen the cache was built from so it is only
	// re-bridged when the completed length changes (once per invalidation,
	// never per frame).
	streamNative    map[string][]PaintLine
	streamNativeLen map[string]int
	// thinkingBodyOnly is enabled for retained PaneThinking projections. The
	// parent pane owns lifecycle/disclosure chrome; this renderer supplies only
	// wrapped reasoning body rows and cursor/strike styling.
	thinkingBodyOnly bool
	// expanded records user-expanded messages; the absence of an ID means the
	// deterministic default applies (see disclosure.go). Disclosure is TUI
	// presentation state only — never persisted.
	expanded         map[string]bool
	activityMessages map[string]*types.ChatMessage   // transient batch children, keyed by stable call ID
	activityBatches  map[string][]*types.ChatMessage // rebuilt only when the canonical batch changes
	exportSnapshot   MessageRowsSnapshot
	exportRevision   uint64
	exportWidth      int
	exportScrollbarW int
	exportGeneration uint64
	exportScrollY    int
	exportViewportH  int
	lazyWidthReflow  bool
	frameSensitive   map[string]struct{}
}

type MessageRowsSnapshot struct {
	Rows          []MessageRow
	PaneLineMap   []PaneLineInfo
	ContentHeight int
	// SourceID identifies the retained Renderer that produced this snapshot.
	// Generation is renderer-local, so two session projections can both emit
	// generation one and still require the shared shell chat to replace rows.
	SourceID         uint64
	Generation       uint64
	ChangedIndices   []int
	StructureChanged bool
}

// SetThinkingBodyOnly switches retained thinking-pane projections between
// full chat blocks and body-only rows. It is presentation state owned by TUI.
func (r *Renderer) SetThinkingBodyOnly(v bool) {
	if r.thinkingBodyOnly == v {
		return
	}
	r.thinkingBodyOnly = v
	r.InvalidateAllMessages()
}

func NewRenderer(t *theme.Theme) *Renderer {
	r := &Renderer{
		theme:           t,
		sourceID:        nextRendererSourceID.Add(1),
		vl:              *newItemList(),
		sync:            NewMessageSync(),
		nativeCache:     make(map[string][]PaintLine),
		streamNative:    make(map[string][]PaintLine),
		streamNativeLen: make(map[string]int),
		expanded:        make(map[string]bool),
		frameSensitive:  make(map[string]struct{}),
	}
	r.tokens = ui.NewTokenSet(t)
	return r
}

func (r *Renderer) invalidateMessageCache(msg *types.ChatMessage) {
	if msg == nil {
		return
	}
	msg.CachedLines = nil
	delete(r.nativeCache, msg.InternalID)
	delete(r.streamNative, msg.InternalID)
	delete(r.streamNativeLen, msg.InternalID)
}

// streamNativeRows returns the bridged native rows for the completed portion
// of a streaming message, re-bridging only when StreamCompletedLen changes.
func (r *Renderer) streamNativeRows(msg *types.ChatMessage, tokens *ui.TokenSet) []PaintLine {
	if msg == nil {
		return nil
	}
	if cached, ok := r.streamNative[msg.InternalID]; ok && r.streamNativeLen[msg.InternalID] == msg.StreamCompletedLen {
		return cached
	}
	rows := BridgeAnsiLines(msg.StreamCompletedLines, tokens)
	r.streamNative[msg.InternalID] = rows
	r.streamNativeLen[msg.InternalID] = msg.StreamCompletedLen
	return rows
}

// BindScroll attaches the authoritative shellchat ScrollModel for main chat.
func (r *Renderer) BindScroll(scroll *ui.ScrollModel) {
	r.extScroll = scroll
}

// BindPaneScroll attaches the per-subpane ScrollModel for nested gutter geometry.
func (r *Renderer) BindPaneScroll(scroll *ui.ScrollModel) {
	r.paneScroll = scroll
}

// authoritativeScroll is the single scroll model all mutations must target.
// Main-chat export binds extScroll; nested/detached rendering uses paneScroll only.
func (r *Renderer) authoritativeScroll() *ui.ScrollModel {
	if r.extScroll != nil {
		return r.extScroll
	}
	return r.paneScroll
}

func (r *Renderer) autoScroll() bool {
	if scroll := r.authoritativeScroll(); scroll != nil {
		return scroll.AutoScroll()
	}
	return true
}

func (r *Renderer) setAutoScroll(v bool) {
	if scroll := r.authoritativeScroll(); scroll != nil {
		scroll.SetAutoScroll(v)
	}
}

// ScrollToTop initializes a newly hydrated review at its assignment. Live
// projections retain their existing scroll state across minimize/restore.
func (r *Renderer) ScrollToTop() {
	r.setAutoScroll(false)
	if scroll := r.authoritativeScroll(); scroll != nil {
		scroll.ScrollTo(0)
	}
	r.vl.ScrollTo(0)
}

func (r *Renderer) syncScrollToVL() {
	off := r.vl.ScrollY()
	auto := true
	if scroll := r.authoritativeScroll(); scroll != nil {
		off = scroll.Offset()
		auto = scroll.AutoScroll()
	}
	r.vl.ScrollTo(off)
	_ = auto
}

func (r *Renderer) syncScrollFromVL() {
	if scroll := r.authoritativeScroll(); scroll != nil {
		scroll.ScrollTo(r.vl.ScrollY())
	}
}

func (r *Renderer) effectiveScrollY() int {
	if scroll := r.authoritativeScroll(); scroll != nil {
		return scroll.Offset()
	}
	return r.vl.ScrollY()
}

func (r *Renderer) nextMsgID() string {
	return r.sync.NextMsgID()
}

func (r *Renderer) bumpVersion() {
	r.sync.BumpVersion()
}

func (r *Renderer) Version() uint64 {
	return r.sync.Version()
}

func (r *Renderer) SetSubPaneContentFn(fn SubPaneContentFn) {
	r.subPaneContentFn = fn
}

func (r *Renderer) PaneLineMap() []PaneLineInfo {
	return r.paneLineMap
}

func (r *Renderer) SetPaneScrollbarWidth(w int) bool {
	w = max(0, w)
	if w != r.paneScrollbarW {
		if r.exportSnapshot.Generation != 0 {
			r.lazyWidthReflow = true
		}
		r.paneScrollbarW = w
		for _, msg := range r.sync.Messages {
			r.invalidateMessageCache(msg)
		}
		r.vl.InvalidateAll()
		return true
	}
	return false
}

func (r *Renderer) PaneScrollbarWidth() int {
	return r.paneScrollbarW
}

func (r *Renderer) SetTheme(t *theme.Theme) {
	if t != nil {
		r.theme = t
		if r.tokens == nil {
			r.tokens = ui.NewTokenSet(t)
		} else {
			r.tokens.Rebuild(t)
		}
		r.InvalidateAllMessages()
		r.mdRenderer = nil
	}
}

// Tokens returns the renderer's resolved TokenSet, rebuilt on theme changes.
// Used by the ANSI bridge to map known colours back to semantic roles.
func (r *Renderer) Tokens() *ui.TokenSet {
	if r == nil {
		return nil
	}
	if r.tokens == nil && r.theme != nil {
		r.tokens = ui.NewTokenSet(r.theme)
	}
	return r.tokens
}

func (r *Renderer) Theme() *theme.Theme {
	if r == nil {
		return nil
	}
	return r.theme
}

// SetLegacyModelFallback configures the label used only for messages written
// before durable per-turn attribution existed.
func (r *Renderer) SetLegacyModelFallback(name string) {
	if r.legacyModelFallback != name {
		r.legacyModelFallback = name
		r.InvalidateAllMessages()
	}
}

func (r *Renderer) SetUsername(name string) {
	if r.username != name {
		r.username = name
		r.InvalidateAllMessages()
	}
}

func (r *Renderer) SetSize(w, h int) {
	w = max(0, w)
	h = max(0, h)
	if r.width != w {
		if r.exportSnapshot.Generation != 0 {
			r.lazyWidthReflow = true
		}
		for i := range r.sync.Messages {
			r.sync.Messages[i].CachedLines = nil
			r.sync.Messages[i].StreamCompletedLines = nil
			r.sync.Messages[i].StreamCompletedLen = 0
		}
		r.streamNative = make(map[string][]PaintLine)
		r.streamNativeLen = make(map[string]int)
		r.mdRenderer = nil
		r.vl.InvalidateHeights()
		r.vl.InvalidateAll()
	}
	r.width = w
	r.height = h
	r.vl.SetViewport(r.chatAreaHeight)
}

func (r *Renderer) SetChatAreaBounds(top, height int) {
	r.chatAreaTop = top
	r.chatAreaHeight = height
	r.vl.SetViewport(height)
	if r.extScroll == nil && r.paneScroll != nil {
		r.paneScroll.SetViewport(height)
	}
}

func (r *Renderer) AppendMessage(msg types.ChatMessage) {
	r.invalidateStreamingCursorOwner()
	msgCopy := msg
	if msgCopy.InternalID == "" {
		msgCopy.InternalID = r.nextMsgID()
	}
	r.sync.MsgByID[msgCopy.InternalID] = &msgCopy
	if msgCopy.ToolID != "" {
		r.sync.MsgByID[msgCopy.ToolID] = &msgCopy
	}
	r.sync.Messages = append(r.sync.Messages, &msgCopy)
	r.bumpVersion()
	// Group membership can change when a neighboring thought or tool arrives.
	r.syncActivityItems([]string{msgCopy.InternalID})
}

func (r *Renderer) AppendToken(token string) {
	if len(r.sync.Messages) == 0 {
		return
	}
	last := r.sync.Messages[len(r.sync.Messages)-1]
	if last.Role != types.RoleAssistant {
		return
	}
	last.Content += token
	r.bumpVersion()
	last.CachedLines = nil
	if strings.Contains(token, "\n") {
		last.StreamCompletedLines = nil
		last.StreamCompletedLen = 0
	}
	r.vl.InvalidateItem(last.InternalID)
}

func (r *Renderer) AppendThinking(s string) {
	if len(r.sync.Messages) > 0 {
		last := r.sync.Messages[len(r.sync.Messages)-1]
		if last.Role == types.RoleThinking && last.ThinkingActive {
			last.Content += s
			r.bumpVersion()
			last.CachedLines = nil
			r.syncActivityItems([]string{last.InternalID})
			return
		}
	}
	for _, msg := range r.sync.Messages {
		if msg.ThinkingActive {
			msg.ThinkingActive = false
			r.bumpVersion()
			msg.CachedLines = nil
		}
	}
	r.invalidateStreamingCursorOwner()
	newMsg := &types.ChatMessage{
		InternalID:     r.nextMsgID(),
		Role:           types.RoleThinking,
		Content:        s,
		Time:           time.Now(),
		ThinkingActive: true,
	}
	r.sync.MsgByID[newMsg.InternalID] = newMsg
	r.sync.Messages = append(r.sync.Messages, newMsg)
	r.bumpVersion()
	r.syncActivityItems([]string{newMsg.InternalID})
}

func toIntField(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case int64:
		return int(n)
	}
	return 0
}

func (r *Renderer) findToolMsg(internalID, toolID string) *types.ChatMessage {
	if internalID != "" {
		if msg, ok := r.sync.MsgByID[internalID]; ok {
			return msg
		}
	}
	if toolID != "" {
		if msg, ok := r.sync.MsgByID[toolID]; ok {
			return msg
		}
	}
	return nil
}

func (r *Renderer) UpdateToolStreaming(toolID, internalID, name string, data map[string]any) {
	target, _ := data["target"].(string)
	estTokens := toIntField(data["tool_est_tokens"])
	contentLines := toIntField(data["content_lines_int"])
	oldLines := toIntField(data["old_lines_int"])
	newLines := toIntField(data["new_lines_int"])

	previewPhase := types.ToolPreviewPreparing
	if target != "" || contentLines > 0 || oldLines > 0 || newLines > 0 || estTokens > 0 {
		previewPhase = types.ToolPreviewStreaming
	}

	if msg := r.findToolMsg(internalID, toolID); msg != nil {
		if msg.ToolStatus == "success" || msg.ToolStatus == "error" || msg.ToolStatus == types.ToolStatusCancelled {
		} else {
			if v, ok := data["content"]; ok {
				if s, ok := v.(string); ok {
					msg.Content = s
				}
			}
			if v, ok := data["tool_est_tokens"]; ok {
				msg.ToolEstTokens = toIntField(v)
			}
			if v, ok := data["lived_added"]; ok {
				msg.ToolLivedAdded = toIntField(v)
			}
			if v, ok := data["lived_removed"]; ok {
				msg.ToolLivedRemoved = toIntField(v)
			}
			if target != "" {
				msg.ToolProgressLabel = target
			}
			if contentLines > 0 {
				msg.ToolLivedAdded = contentLines
			}
			if oldLines > 0 {
				msg.ToolLivedRemoved = oldLines
			}
			if newLines > 0 {
				msg.ToolLivedAdded = newLines
			}
			msg.ToolPreviewPhase = previewPhase
			msg.ToolProgressPhase = "streaming"
			msg.ToolProgressMessage = ""
			r.bumpVersion()
			msg.CachedLines = nil
			if toolID != "" && msg.ToolID == "" {
				if existing, ok := r.sync.MsgByID[toolID]; !ok || existing == msg {
					msg.ToolID = toolID
					r.sync.MsgByID[toolID] = msg
				}
			}
			r.syncActivityItems([]string{msg.InternalID})
			return
		}
	}

	id := internalID
	if id == "" || r.sync.MsgByID[id] != nil {
		id = r.nextMsgID()
	}
	msg := &types.ChatMessage{
		InternalID:        id,
		Role:              types.RoleTool,
		Time:              time.Now(),
		ToolName:          name,
		ToolID:            toolID,
		ToolArgs:          "",
		ToolStatus:        "streaming",
		ToolIndex:         len(r.sync.Messages),
		ToolProgressPhase: "streaming",
		ToolPreviewPhase:  previewPhase,
		ToolProgressLabel: target,
		ToolEstTokens:     estTokens,
		ToolLivedAdded:    contentLines,
		ToolLivedRemoved:  oldLines,
		ToolStartTime:     time.Now(),
	}
	if newLines > 0 {
		msg.ToolLivedAdded = newLines
	}
	r.invalidateStreamingCursorOwner()
	r.sync.MsgByID[msg.InternalID] = msg
	if toolID != "" {
		r.sync.MsgByID[toolID] = msg
	}
	r.sync.Messages = append(r.sync.Messages, msg)
	r.bumpVersion()
	r.syncActivityItems([]string{msg.InternalID})
}

func (r *Renderer) UpdateToolCall(toolID, internalID, name, argsCompact string) {
	if msg := r.findToolMsg(internalID, toolID); msg != nil {
		if msg.ToolStatus == "success" || msg.ToolStatus == "error" || msg.ToolStatus == types.ToolStatusCancelled {
		} else {
			msg.ToolStatus = "calling"
			msg.ToolArgs = argsCompact
			msg.Content = name + " " + argsCompact
			msg.ToolPreviewPhase = types.ToolPreviewStreaming
			msg.ToolProgressPhase = ""
			msg.ToolProgressMessage = ""
			if msg.ToolProgressLabel == "" {
				msg.ToolProgressLabel = argsCompact
			}
			r.bumpVersion()
			msg.CachedLines = nil
			if toolID != "" && msg.ToolID == "" {
				if existing, ok := r.sync.MsgByID[toolID]; !ok || existing == msg {
					msg.ToolID = toolID
					r.sync.MsgByID[toolID] = msg
				}
			}
			r.syncActivityItems([]string{msg.InternalID})
			return
		}
	}

	id := internalID
	if id == "" || r.sync.MsgByID[id] != nil {
		id = r.nextMsgID()
	}
	msg := &types.ChatMessage{
		InternalID:        id,
		Role:              types.RoleTool,
		Content:           name + " " + argsCompact,
		Time:              time.Now(),
		ToolName:          name,
		ToolID:            toolID,
		ToolArgs:          argsCompact,
		ToolStatus:        "calling",
		ToolIndex:         len(r.sync.Messages),
		ToolPreviewPhase:  types.ToolPreviewStreaming,
		ToolProgressLabel: argsCompact,
		ToolStartTime:     time.Now(),
	}
	r.invalidateStreamingCursorOwner()
	r.sync.MsgByID[msg.InternalID] = msg
	if toolID != "" {
		r.sync.MsgByID[toolID] = msg
	}
	r.sync.Messages = append(r.sync.Messages, msg)
	r.bumpVersion()
	r.syncActivityItems([]string{msg.InternalID})
}

func (r *Renderer) UpdateToolProgress(toolID, internalID, name, phase, message, path string, linesAdded, linesRemoved, estTokens int) {
	msg := r.findToolMsg(internalID, toolID)
	if msg == nil {
		return
	}
	if msg.ToolStatus != "calling" && msg.ToolStatus != "streaming" && msg.ToolStatus != "success" {
		return
	}
	msg.ToolProgressPhase = phase
	msg.ToolProgressMessage = message
	if path != "" {
		msg.ToolProgressLabel = path
	}
	if toolID != "" && msg.ToolID == "" {
		if existing, ok := r.sync.MsgByID[toolID]; !ok || existing == msg {
			msg.ToolID = toolID
			r.sync.MsgByID[toolID] = msg
		}
	}
	switch phase {
	case "done":
		msg.ToolLivedAdded = linesAdded
		msg.ToolLivedRemoved = linesRemoved
		msg.ToolPreviewPhase = types.ToolPreviewDone
		if !msg.ToolStartTime.IsZero() {
			msg.ToolDuration = time.Since(msg.ToolStartTime)
		}
		if estTokens > 0 {
			msg.ToolEstTokens = estTokens
		}
	case "error":
		msg.ToolPreviewPhase = types.ToolPreviewError
		if linesAdded > 0 {
			msg.ToolLivedAdded = linesAdded
		}
		if linesRemoved > 0 {
			msg.ToolLivedRemoved = linesRemoved
		}
		if estTokens > 0 {
			msg.ToolEstTokens = estTokens
		}
	}
	r.bumpVersion()
	msg.CachedLines = nil
	r.syncActivityItems([]string{msg.InternalID})
}

func (r *Renderer) UpdateToolResult(toolID, internalID, name, result string, isError bool, estTokens, linesAdded, linesRemoved int) {
	msg := r.findToolMsg(internalID, toolID)
	if msg == nil {
		return
	}
	if msg.ToolStatus != "calling" && msg.ToolStatus != "streaming" {
		return
	}
	if isError {
		msg.ToolStatus = "error"
	} else {
		msg.ToolStatus = "success"
	}
	msg.ToolPreviewPhase = types.ToolPreviewDone
	msg.ToolProgressPhase = ""
	msg.ToolProgressMessage = ""
	if !msg.ToolStartTime.IsZero() {
		msg.ToolDuration = time.Since(msg.ToolStartTime)
	}
	if estTokens > 0 {
		msg.ToolEstTokens = estTokens
	}
	if linesAdded > 0 || linesRemoved > 0 {
		msg.ToolLivedAdded = linesAdded
		msg.ToolLivedRemoved = linesRemoved
	}
	msg.CachedLines = nil
	if result != "" {
		msg.Content = result
	} else {
		msg.Content = msg.ToolName + " " + msg.ToolArgs
	}
	r.bumpVersion()
	msg.CachedLines = nil
	r.syncActivityItems([]string{msg.InternalID})
}

// SetToolPermTag sets the PermTag on a tool message matched by toolID.
func (r *Renderer) SetToolPermTag(toolID, internalID, tag string) {
	msg := r.findToolMsg(internalID, toolID)
	if msg == nil {
		return
	}
	msg.PermTag = tag
	r.bumpVersion()
	msg.CachedLines = nil
	r.syncActivityItems([]string{msg.InternalID})
}

func (r *Renderer) MarkStreamingToolsCancelled() {
	var changed []string
	for _, msg := range r.sync.Messages {
		if msg.ToolStatus == "streaming" || msg.ToolStatus == "calling" {
			msg.ToolStatus = types.ToolStatusCancelled
			msg.ToolPreviewPhase = types.ToolPreviewDone
			msg.ToolProgressPhase = ""
			msg.ToolProgressMessage = ""
			msg.CachedLines = nil
			changed = append(changed, msg.InternalID)
		}
	}
	if len(changed) > 0 {
		r.bumpVersion()
		r.syncActivityItems(changed)
	}
}

func (r *Renderer) MarkAllThinkingDone() {
	var changed []string
	for _, msg := range r.sync.Messages {
		if msg.Role == types.RoleThinking {
			msg.ThinkingActive = false
			msg.ThinkingInterrupted = false
			msg.CachedLines = nil
			changed = append(changed, msg.InternalID)
		}
	}
	if len(changed) > 0 {
		r.bumpVersion()
		r.syncActivityItems(changed)
	}
}

func (r *Renderer) MarkAllThinkingInterrupted() {
	var changed []string
	for _, msg := range r.sync.Messages {
		if msg.Role == types.RoleThinking && msg.ThinkingActive {
			msg.ThinkingActive = false
			msg.ThinkingInterrupted = true
			msg.CachedLines = nil
			changed = append(changed, msg.InternalID)
		} else if msg.Role == types.RoleThinking {
			msg.ThinkingActive = false
			changed = append(changed, msg.InternalID)
		}
	}
	if len(changed) > 0 {
		r.bumpVersion()
		r.syncActivityItems(changed)
	}
}

func (r *Renderer) RemoveActiveThinking() string {
	var content string
	var keep []*types.ChatMessage
	for _, msg := range r.sync.Messages {
		if msg.Role == types.RoleThinking && msg.ThinkingActive {
			content = msg.Content
			delete(r.sync.MsgByID, msg.InternalID)
			continue
		}
		keep = append(keep, msg)
	}
	if len(keep) != len(r.sync.Messages) {
		r.sync.Messages = keep
		for _, msg := range r.sync.Messages {
			msg.CachedLines = nil
		}
		r.bumpVersion()
		r.RebuildItems()
	}
	return content
}

func (r *Renderer) ActiveThinkingLineCount(contentWidth int) int {
	for _, msg := range r.sync.Messages {
		if msg.Role == types.RoleThinking && msg.ThinkingActive {
			if msg.ThinkingDisplay.Kind.HasCompactPreview() {
				return 1
			}
			if msg.CachedLines != nil {
				return len(msg.CachedLines) + 1
			}
			return len(rendering.WrapText(msg.Content, contentWidth)) + 1
		}
	}
	return 0
}

func (r *Renderer) InvalidateCache() {
	perf.MarkCacheInvalidation()
	for _, msg := range r.sync.Messages {
		if msg.Role == types.RoleTool && (msg.ToolStatus == "streaming" || msg.ToolStatus == "calling") {
			msg.CachedLines = nil
			r.vl.InvalidateItem(msg.InternalID)
			if _, ok := r.vl.byID["activity:"+msg.InternalID]; ok {
				r.vl.InvalidateItem("activity:" + msg.InternalID)
			}
		}
		if msg.Role == types.RoleThinking && msg.ThinkingActive {
			msg.CachedLines = nil
			r.vl.InvalidateItem(msg.InternalID)
		}
		if msg.Role == types.RoleStatus && lifecycleMessageFrameSensitive(*msg) {
			msg.CachedLines = nil
			r.vl.InvalidateItem(msg.InternalID)
		}
	}
	// The trailing assistant message only needs per-tick invalidation while
	// its stream is live: mid-stream it is the streaming cursor (never cached
	// anyway), but once idle the unconditional drop here forced a full
	// re-render of the session's largest message on every tick, forever.
	// Active thinking rows are already covered by the loop above.
	if r.streaming && len(r.sync.Messages) > 0 {
		last := r.sync.Messages[len(r.sync.Messages)-1]
		if last.Role == types.RoleAssistant {
			last.CachedLines = nil
			r.vl.InvalidateItem(last.InternalID)
		}
	}
}

// InvalidateWidthProfile drops every width-derived row, wrap, and virtual-list
// measurement. Unlike InvalidateCache, this includes completed static messages.
func (r *Renderer) InvalidateWidthProfile() {
	for _, msg := range r.sync.Messages {
		msg.CachedLines = nil
		r.vl.InvalidateItem(msg.InternalID)
	}
}

func (r *Renderer) SetStreaming(s bool) {
	if r.streaming == s {
		return
	}
	if s {
		// Live streaming is expected work in the perf sentinel's eyes.
		perf.NoteActivity()
	}
	for i := len(r.sync.Messages) - 1; i >= 0; i-- {
		message := r.sync.Messages[i]
		if message.PendingBtw {
			continue
		}
		if message.Role == types.RoleAssistant {
			message.CachedLines = nil
			r.vl.InvalidateItem(message.InternalID)
		}
		break
	}
	r.streaming = s
}

func (r *Renderer) EndStreamingCursor() {
	r.SetStreaming(false)
}

func (r *Renderer) SetSpinFrame(f int) {
	if ui.ActivityDot(r.spinFrame) == ui.ActivityDot(f) {
		r.spinFrame = f
		return
	}
	r.spinFrame = f
	for id := range r.frameSensitive {
		if message := r.sync.MsgByID[id]; message != nil {
			message.CachedLines = nil
		}
		r.vl.InvalidateItem(id)
	}
}

func (r *Renderer) setFrameSensitive(id string, sensitive bool) {
	if sensitive {
		r.frameSensitive[id] = struct{}{}
	} else {
		delete(r.frameSensitive, id)
	}
}

func (r *Renderer) SetActiveTyping(v bool) {
	if r.activeTyping == v {
		return
	}
	r.activeTyping = v
	if id := r.streamingCursorOwnerID(); id != "" {
		if message := r.sync.MsgByID[id]; message != nil {
			message.CachedLines = nil
		}
		r.vl.InvalidateItem(id)
	}
}

func (r *Renderer) SetPhase(p string) {
	if r.phase == p {
		return
	}
	r.phase = p
	for _, message := range r.sync.Messages {
		if message != nil && message.Role == types.RoleTool && toolRowFrameSensitive(*message) {
			message.CachedLines = nil
			r.vl.InvalidateItem(message.InternalID)
		}
	}
}

func (r *Renderer) ScrollToBottom() {
	r.setAutoScroll(true)
	r.vl.ScrollToBottom()
	r.syncScrollFromVL()
}

func (r *Renderer) IsAtBottom() bool {
	if scroll := r.authoritativeScroll(); scroll != nil {
		return scroll.NearBottom(0)
	}
	return r.vl.IsAtBottom()
}

func (r *Renderer) Messages() []*types.ChatMessage {
	return r.sync.Messages
}

func (r *Renderer) SourceVersion() uint64 {
	return r.sync.SourceVersion()
}

// SyncMessages updates the retained TUI projection from workspace-owned
// message data. Stable IDs preserve unchanged render caches and scroll state.
func (r *Renderer) SyncMessages(messages []types.ChatMessage, sourceVersion uint64) {
	cursorOwnerID := r.streamingCursorOwnerID()
	result, ok := r.sync.SyncMessages(messages, sourceVersion)
	if !ok {
		return
	}

	if cursorOwnerID != "" && result.PureAppend {
		if previous := r.sync.MsgByID[cursorOwnerID]; previous != nil {
			previous.CachedLines = nil
			result.ChangedIDs = append(result.ChangedIDs, cursorOwnerID)
		}
	}
	r.syncActivityItems(result.ChangedIDs)
	r.pruneExpanded()
}

func (r *Renderer) RemoveMessage(idx int) {
	if idx < 0 || idx >= len(r.sync.Messages) {
		return
	}
	msg := r.sync.Messages[idx]
	delete(r.sync.MsgByID, msg.InternalID)
	r.sync.Messages = append(r.sync.Messages[:idx], r.sync.Messages[idx+1:]...)
	r.bumpVersion()
	r.RebuildItems()
	r.pruneExpanded()
}

func (r *Renderer) InvalidateItem(id string) {
	msg := r.sync.MsgByID[id]
	if msg == nil {
		msg = r.activityMessages[id]
	}
	if msg != nil {
		msg.CachedLines = nil
		r.vl.InvalidateItem(id)
	}
}

func (r *Renderer) Update(_ tea.Msg) (*Renderer, tea.Cmd) {
	return r, nil
}

// LastAssistantText returns the content of the most recent
// assistant message, or "" if there isn't one. Used by the
// right-click "Copy last assistant" item.
func (r *Renderer) LastAssistantText() string {
	for i := len(r.sync.Messages) - 1; i >= 0; i-- {
		msg := r.sync.Messages[i]
		if msg.Role == "assistant" && msg.Content != "" {
			return msg.Content
		}
	}
	return ""
}

// Render produces the visible chat lines for subpane projections.
func (r *Renderer) Render(width int) []string {
	lines, _ := r.RenderVisible(width)
	return lines
}

// RenderVisible produces parallel string and native rows for the current
// viewport (same gravity/scroll/pad arithmetic for both slices).
func (r *Renderer) RenderVisible(width int) ([]string, []PaintLine) {
	if r.chatAreaHeight <= 0 {
		return []string{}, nil
	}
	r.prepareRender(width)
	visible := r.vl.ExtractVisible()
	native := r.vl.ExtractVisibleNative()
	if r.extScroll == nil && r.paneScroll != nil {
		contentH := r.vl.TotalHeight()
		r.paneScroll.SetContent(contentH)
		r.paneScroll.SetViewport(r.vl.ViewportHeight())
	}
	r.buildPaneLineMap(visible)
	return visible, native
}

// ExportMessageRows returns the retained message projection for shellchat.
// Source invalidation materializes all required rows; width-only changes reflow
// viewport plus overscan and retain estimated offscreen heights.
func (r *Renderer) ExportMessageRows(width int) ([]MessageRow, []PaneLineInfo, int) {
	snapshot := r.ExportMessageRowsSnapshot(width)
	return snapshot.Rows, snapshot.PaneLineMap, snapshot.ContentHeight
}

func (r *Renderer) ExportMessageRowsSnapshot(width int) MessageRowsSnapshot {
	if r.chatAreaHeight <= 0 {
		return MessageRowsSnapshot{SourceID: r.sourceID}
	}
	scrollbarW := r.ScrollbarWidth()
	scrollY, viewportH := r.effectiveScrollY(), r.vl.ViewportHeight()
	if r.exportSnapshot.Generation != 0 && r.exportRevision == r.vl.Revision() && r.exportWidth == width && r.exportScrollbarW == scrollbarW && r.exportScrollY == scrollY && r.exportViewportH == viewportH {
		return r.exportSnapshot
	}
	structureChanged := r.exportSnapshot.Generation == 0 || r.exportWidth != width || r.exportScrollbarW != scrollbarW || len(r.exportSnapshot.Rows) != r.vl.ItemCount()
	dirty := make([]int, 0)
	oldHeights := make(map[int]int)
	for i := 0; i < r.vl.ItemCount(); i++ {
		item := r.vl.Item(i)
		if item != nil && (item.Dirty || item.Rendered == nil) {
			dirty = append(dirty, i)
			oldHeights[i] = item.Height
		}
	}
	r.prepareRender(width)
	if !r.lazyWidthReflow {
		r.vl.EnsureHeights(r.renderItem, width)
	}
	// Width-only virtualization materializes the visible range plus overscan;
	// offscreen dirty items retain previous/estimated heights until exposed.
	heightChanged := false
	paneChanged := false
	renderedDirty := dirty[:0]
	for _, i := range dirty {
		item := r.vl.Item(i)
		if item == nil || item.Dirty {
			continue
		}
		renderedDirty = append(renderedDirty, i)
		if oldHeights[i] != item.Height {
			heightChanged = true
		}
		if msg, ok := item.Data.(*types.ChatMessage); ok && msg != nil && msg.SubPaneID != "" {
			paneChanged = true
		}
	}
	r.vl.BuildIndex()
	if r.autoScroll() {
		r.vl.ScrollToBottom()
		r.syncScrollFromVL()
		if scroll := r.authoritativeScroll(); scroll != nil {
			scroll.ApplyAutoScroll()
		}
	} else {
		r.vl.ClampScroll()
	}
	structureChanged = structureChanged || heightChanged || paneChanged
	rows := r.exportSnapshot.Rows
	changedIndices := renderedDirty
	if structureChanged {
		rows = make([]MessageRow, r.vl.ItemCount())
		changedIndices = make([]int, r.vl.ItemCount())
		for i := range changedIndices {
			changedIndices[i] = i
		}
	}
	for _, i := range changedIndices {
		item := r.vl.Item(i)
		if item == nil {
			continue
		}
		rows[i] = r.messageRow(item)
	}
	paneMap := r.exportSnapshot.PaneLineMap
	if structureChanged {
		paneMap = r.buildGlobalPaneLineMap()
	}
	r.exportGeneration++
	r.exportRevision = r.vl.Revision()
	r.exportWidth = width
	r.exportScrollbarW = r.ScrollbarWidth()
	r.exportScrollY = r.effectiveScrollY()
	r.exportViewportH = r.vl.ViewportHeight()
	r.exportSnapshot = MessageRowsSnapshot{Rows: rows, PaneLineMap: paneMap, ContentHeight: r.vl.TotalHeight(), SourceID: r.sourceID, Generation: r.exportGeneration, ChangedIndices: changedIndices, StructureChanged: structureChanged}
	if r.lazyWidthReflow {
		remaining := false
		for i := 0; i < r.vl.ItemCount(); i++ {
			if item := r.vl.Item(i); item != nil && item.Dirty {
				remaining = true
				break
			}
		}
		r.lazyWidthReflow = remaining
	}
	return r.exportSnapshot
}

func (r *Renderer) messageRow(item *renderItem) MessageRow {
	subPaneID := ""
	contextBoundary := false
	toggleLine := -1
	toggleAny := false
	var leafToggles map[int]string
	if group, ok := item.Data.(*activityGroup); ok {
		toggleLine = 0
		toggleAny = r.expanded[group.id]
		leafToggles = group.leafRows
	} else if msg, ok := item.Data.(*types.ChatMessage); ok && msg != nil {
		subPaneID = msg.SubPaneID
		contextBoundary = msg.LifecyclePhase == "history_restored" || strings.HasPrefix(msg.LifecyclePhase, "compaction_")
		if r.hasDisclosure(msg) {
			toggleLine = 0
			toggleAny = r.disclosureState(msg) == DisclosureExpanded
		}
	}
	lines := item.Rendered
	if len(lines) == 0 {
		lines = []string{""}
	}
	native := item.Native
	if len(native) != len(lines) {
		native = nil
	}
	contextBoundaryStart := 0
	if contextBoundary && len(lines) > 1 && strings.TrimSpace(lines[0]) == "" && strings.TrimSpace(lines[1]) == "" {
		contextBoundaryStart = 1
	}
	if toggleLine >= 0 {
		// The leading spacer occupies row 0 when present; the disclosure
		// affordance lives on the message's first content row.
		if toggleLine < len(lines) && strings.TrimSpace(lines[0]) == "" {
			toggleLine = 1
		}
	}
	return MessageRow{
		ID:                   item.ID,
		Lines:                lines,
		Native:               native,
		SubPaneID:            subPaneID,
		ContextBoundary:      contextBoundary,
		ContextBoundaryStart: contextBoundaryStart,
		Toggle:               toggleLine >= 0,
		ToggleAny:            toggleAny,
		LeafToggles:          leafToggles,
		ToggleLine:           toggleLine,
	}
}

func (r *Renderer) prepareRender(width int) {
	sbW := r.ScrollbarWidth()
	if sbW != r.lastScrollbarW {
		if r.exportSnapshot.Generation != 0 {
			r.lazyWidthReflow = true
		}
		r.lastScrollbarW = sbW
		for _, msg := range r.sync.Messages {
			msg.CachedLines = nil
		}
		r.vl.InvalidateAll()
	}

	// The shellchat ScrollModel is authoritative. Apply the latest wheel/drag
	// offset before selecting the retained items that need rendering.
	r.syncScrollToVL()
	_ = r.vl.RenderVisible(r.renderItem, width)

	if r.extScroll == nil && r.paneScroll != nil {
		r.paneScroll.SetContent(r.vl.TotalHeight())
		r.paneScroll.SetViewport(r.vl.ViewportHeight())
	}

	if r.autoScroll() {
		r.vl.BuildIndex()
		r.vl.ScrollToBottom()
		r.syncScrollFromVL()
		if scroll := r.authoritativeScroll(); scroll != nil {
			scroll.ApplyAutoScroll()
		}
	} else if r.extScroll != nil {
		// Bound shellchat ScrollModel owns offset when disarmed; do not
		// mirror vl clamp back or stale vl geometry resets user scroll.
		r.vl.ClampScroll()
	} else {
		r.vl.ClampScroll()
		r.syncScrollFromVL()
	}
}

func (r *Renderer) streamingCursorOwnerID() string {
	for i := len(r.sync.Messages) - 1; i >= 0; i-- {
		message := r.sync.Messages[i]
		if message.PendingBtw {
			continue
		}
		if r.isStreamingCursor(message) {
			return message.InternalID
		}
		break
	}
	return ""
}

func (r *Renderer) invalidateStreamingCursorOwner() {
	id := r.streamingCursorOwnerID()
	if id == "" {
		return
	}
	if msg := r.sync.MsgByID[id]; msg != nil {
		msg.CachedLines = nil
	}
	r.vl.InvalidateItem(id)
}

func (r *Renderer) buildGlobalPaneLineMap() []PaneLineInfo {
	hasPane := false
	for _, msg := range r.sync.Messages {
		if msg != nil && msg.SubPaneID != "" {
			hasPane = true
			break
		}
	}
	if !hasPane {
		return nil
	}
	r.vl.BuildIndex()
	total := r.vl.TotalHeight()
	paneMap := make([]PaneLineInfo, total)
	if len(r.sync.Messages) == 0 || total == 0 {
		return paneMap
	}
	for _, msg := range r.sync.Messages {
		if msg == nil || msg.SubPaneID == "" {
			continue
		}
		itemIdx, found := r.vl.byID[msg.InternalID]
		if !found {
			continue
		}
		item := r.vl.Item(itemIdx)
		if item == nil {
			continue
		}
		start := r.vl.LineOfItem(itemIdx)
		for localLine := 1; localLine <= item.PaneRows && start+localLine < len(paneMap); localLine++ {
			paneMap[start+localLine] = PaneLineInfo{PaneID: msg.SubPaneID, LineIdx: localLine - 2}
		}
	}
	return paneMap
}

func (r *Renderer) buildPaneLineMap(visible []string) {
	r.paneLineMap = make([]PaneLineInfo, len(visible))
	if len(r.sync.Messages) == 0 {
		return
	}

	r.vl.BuildIndex()
	scrollY := r.effectiveScrollY()
	total := r.vl.TotalHeight()

	for i := range visible {
		var globalLine int
		if total <= r.chatAreaHeight {
			emptyTop := r.chatAreaHeight - total
			if i < emptyTop {
				continue
			}
			globalLine = i - emptyTop
		} else {
			globalLine = scrollY + i
			if globalLine >= total {
				continue
			}
		}

		itemIdx, localLine := r.vl.ItemAtLine(globalLine)
		item := r.vl.Item(itemIdx)
		if item == nil {
			continue
		}
		msg, ok := item.Data.(*types.ChatMessage)
		if !ok || msg.SubPaneID == "" {
			continue
		}

		// localLine 0 is the transcript-owned leading spacer. It never
		// inherits pane background, scrollbar paint, or nested wheel ownership.
		if localLine >= 1 && localLine <= item.PaneRows {
			r.paneLineMap[i] = PaneLineInfo{
				PaneID:  msg.SubPaneID,
				LineIdx: localLine - 2,
			}
		}
	}
}

// ScrollbarWidth returns reserved scrollbar columns for message layout width.
func (r *Renderer) ScrollbarWidth() int {
	r.vl.BuildIndex()
	w := r.paneScrollbarW
	if r.paneScroll != nil {
		if r.paneScroll.MaxOffset() > 0 {
			w += ui.ScrollbarWidth
		}
	} else if r.vl.MaxScroll() > 0 {
		w += ui.ScrollbarWidth
	}
	return w
}

// PaneScrollMaxOffset reports whether the thinking-pane scroll model overflows.
func (r *Renderer) PaneScrollMaxOffset() int {
	if r.paneScroll != nil {
		return r.paneScroll.MaxOffset()
	}
	r.vl.BuildIndex()
	if r.vl.MaxScroll() > 0 {
		return r.vl.MaxScroll()
	}
	return 0
}

func (r *Renderer) renderItem(idx int, item *renderItem, width int) []string {
	if group, ok := item.Data.(*activityGroup); ok {
		lines, native := r.renderActivity(group)
		item.Native = native
		item.PaneRows = 0
		r.setFrameSensitive(group.id, group.active)
		return lines
	}
	msg, ok := item.Data.(*types.ChatMessage)
	if !ok {
		item.Native = nil
		item.PaneRows = 0
		return []string{""}
	}

	if msg.SubPaneID != "" && r.subPaneContentFn != nil {
		perf.MarkPaneRendered()
		maxH := r.chatAreaHeight / 3
		if maxH < 5 {
			maxH = 5
		}
		paneWidth := max(0, width-r.scrollbarFootprint())
		expanded := r.disclosureState(msg) == DisclosureExpanded
		paneLines := r.subPaneContentFn(msg.SubPaneID, paneWidth, maxH, r.spinFrame, expanded)
		frameSensitive := false
		result := make([]string, 0, len(paneLines)+1)
		native := make([]PaintLine, 0, len(paneLines)+1)
		item.PaneRows = 0
		result = append(result, "")
		native = append(native, buildPlainBodyLine(""))
		for _, sl := range paneLines {
			frameSensitive = frameSensitive || sl.FrameSensitive
			item.PaneRows++
			if sl.Native != nil {
				result = append(result, sl.PlainText())
				native = append(native, *sl.Native)
			} else {
				// SubPaneLine with only Text: bridge to native so no row ever
				// takes the ANSI paint path. Plain text becomes a ColorFg span.
				pl := BridgeAnsiLine(sl.Text, r.Tokens())
				result = append(result, pl.PlainText())
				native = append(native, pl)
			}
		}
		item.Native = native
		r.setFrameSensitive(msg.InternalID, frameSensitive)
		return result
	}
	item.PaneRows = 0

	isStreamingCursor := r.isStreamingCursor(msg)

	if r.sync.MsgByID[msg.InternalID] == msg && msg.CachedLines != nil && !isStreamingCursor && !msg.PendingBtw {
		result := make([]string, 0, len(msg.CachedLines)+1)
		native := make([]PaintLine, 0, len(msg.CachedLines)+1)
		result = append(result, "")
		native = append(native, buildPlainBodyLine(""))
		result = append(result, msg.CachedLines...)
		cachedNative := r.nativeCache[msg.InternalID]
		if len(cachedNative) == len(msg.CachedLines) {
			native = append(native, cachedNative...)
		} else {
			// Cached text without its exact parallel native rows is not a valid
			// cache entry. Drop both halves and rebuild through the normal styled
			// renderer; never synthesize colorless rows from cached text.
			msg.CachedLines = nil
			delete(r.nativeCache, msg.InternalID)
			lines, freshNative := r.renderSingleMessage(msg)
			item.Native = freshNative
			return lines
		}
		item.Native = native
		return result
	}

	lines, native := r.renderSingleMessage(msg)
	item.Native = native
	return lines
}

func (r *Renderer) isStreamingCursor(msg *types.ChatMessage) bool {
	if !r.isLastMessage(msg) {
		return false
	}
	switch {
	case msg.Role == types.RoleAssistant && r.streaming:
		return true
	case msg.Role == types.RoleThinking && msg.ThinkingActive:
		return true
	}
	return false
}

const messageBodyChromeWidth = 16

// availableMessageWidths derives every chat width from the current renderer
// bounds. Minimum readable widths are presentation policies, not geometry:
// a narrow terminal may yield zero content columns but never synthetic ones.
func (r *Renderer) availableMessageWidths() (contentWidth, innerW int) {
	padLR := max(0, r.theme.Spacing.PadLR)
	if r.thinkingBodyOnly {
		padLR = 0
	}
	innerW = max(0, r.width-padLR*2-r.scrollbarFootprint())
	if r.thinkingBodyOnly {
		// The parent pane owns chrome. Reserve only the body gutter so text
		// reaches one column before its own scrollbar footprint.
		return max(0, innerW-toolBodyGutterWidth), innerW
	}
	contentWidth = max(0, innerW-messageBodyChromeWidth)
	return contentWidth, innerW
}

// scrollbarFootprint is the shared horizontal contract for chat content:
// every visible scrollbar occupies one column, and the group has one blank
// separator immediately before the outermost (main chat) scrollbar.
func (r *Renderer) scrollbarFootprint() int {
	w := max(0, r.ScrollbarWidth())
	if w > 0 {
		w++
	}
	return w
}

func (r *Renderer) buildRenderContext(msg *types.ChatMessage, suppressHeader bool) renderContext {
	contentWidth, innerW := r.availableMessageWidths()
	// Tool result bodies (command output, grep, diffs) cap their height against
	// the visible chat viewport, not the full terminal height. When the chat
	// area fills the terminal the two are equal; in a floating/split layout the
	// chat area is the honest size of the scroll window the reader sees.
	chatH := r.chatAreaHeight
	if chatH <= 0 {
		chatH = r.height
	}
	return renderContext{
		th:                  r.theme,
		tokens:              r.Tokens(),
		contentWidth:        contentWidth,
		innerW:              innerW,
		width:               r.width,
		height:              r.height,
		chatHeight:          chatH,
		spinFrame:           r.spinFrame,
		activeTyping:        r.activeTyping,
		legacyModelFallback: r.legacyModelFallback,
		username:            r.username,
		streaming:           r.streaming,
		isLastMsg:           r.isLastMessage(msg),
		phase:               r.phase,
		mdRenderer:          r.MdRenderer(),
		disclosure:          r.disclosureState(msg),
		thinkingBodyOnly:    r.thinkingBodyOnly,
		planSuperseded:      r.planMessageSuperseded(msg),
	}
}

// planMessageSuperseded collapses historical snapshots when a newer plan
// mutation/snapshot exists for the same plan id. The underlying transcript
// remains intact and an explicit expansion still reveals the older snapshot.
func (r *Renderer) planMessageSuperseded(msg *types.ChatMessage) bool {
	if msg == nil || msg.Role != types.RoleTool || !tools.IsPlanTool(msg.ToolName) || msg.ToolStatus == "error" {
		return false
	}
	view, ok := parsePlanView(msg.Content)
	if !ok || view.ID == "" {
		return false
	}
	idx := r.messageIndexOf(msg)
	for i := idx + 1; i < len(r.sync.Messages); i++ {
		next := r.sync.Messages[i]
		if next == nil || next.Role != types.RoleTool || !tools.IsPlanTool(next.ToolName) || next.ToolStatus == "error" {
			continue
		}
		nextView, nextOK := parsePlanView(next.Content)
		if nextOK && nextView.ID == view.ID {
			return true
		}
	}
	return false
}

func (r *Renderer) renderSingleMessage(msg *types.ChatMessage) ([]string, []PaintLine) {
	origMsg := msg
	sanitized := *msg
	sanitized.Content = rendering.SanitizeForTerminal(msg.Content)
	sanitized.ProviderID = rendering.SanitizeForTerminal(msg.ProviderID)
	sanitized.ModelID = rendering.SanitizeForTerminal(msg.ModelID)
	sanitized.ToolArgs = rendering.SanitizeForTerminal(msg.ToolArgs)
	sanitized.ToolProgressLabel = rendering.SanitizeForTerminal(msg.ToolProgressLabel)
	sanitized.LifecycleProvider = rendering.SanitizeForTerminal(msg.LifecycleProvider)
	msg = &sanitized

	msgIdx := r.messageIndexOf(origMsg)
	suppressHeader := r.shouldSuppressHeader(msgIdx)

	ctx := r.buildRenderContext(origMsg, suppressHeader)
	if r.streaming && r.isLastMessage(origMsg) && origMsg.Role == types.RoleAssistant {
		ctx.streamNative = r.streamNativeRows(origMsg, r.Tokens())
	}
	ml := renderMessageContent(*msg, ctx, suppressHeader)

	cursorIdx := -1
	if ctx.isLastMsg {
		switch {
		case msg.Role == types.RoleAssistant && r.streaming:
			cursorIdx = len(ml.lines) - 1
		case msg.Role == types.RoleThinking && msg.ThinkingActive && !msg.ThinkingDisplay.Kind.HasCompactPreview():
			cursorIdx = len(ml.lines) - 1
		}
	}

	isStreamingCursor := cursorIdx >= 0
	if isStreamingCursor {
		ml = ml.withCursor(cursorIdx, r.spinFrame, r.activeTyping)
	}
	r.setFrameSensitive(origMsg.InternalID, ml.frameSensitive)

	// Every transcript item owns exactly one leading spacer, including panes
	// and activity groups. No geometry-dependent trailing-separator handoff.
	lines := []string{""}
	native := []PaintLine{buildPlainBodyLine("")}
	lines = append(lines, ml.lines...)
	native = append(native, ml.native...)

	// Work accounting lives here (not at the renderItem caller) so the role
	// is known: status rows re-render every tick by design and are counted
	// separately from the glamour-class re-renders the budgets hunt.
	if origMsg.Role == types.RoleStatus {
		perf.MarkStatusRerender()
	} else {
		perf.MarkItemRendered(len(lines))
	}

	// Persist the render cache on the retained message (r.sync.Messages), not
	// the sanitized copy: the copy is discarded on return, and writing to it
	// left every message permanently cache-less, so itemList.EnsureHeights
	// re-rendered the whole session every frame. Role/PendingBtw are identical
	// on both, so only the write target changes.
	if r.sync.MsgByID[origMsg.InternalID] == origMsg && !isStreamingCursor && origMsg.Role != types.RoleStatus && !origMsg.PendingBtw {
		origMsg.CachedLines = make([]string, len(ml.lines))
		copy(origMsg.CachedLines, ml.lines)
		r.nativeCache[origMsg.InternalID] = append([]PaintLine(nil), ml.native...)
	}

	return lines, native
}

func (r *Renderer) isLastMessage(msg *types.ChatMessage) bool {
	if len(r.sync.Messages) == 0 {
		return false
	}
	if r.sync.Messages[len(r.sync.Messages)-1] == msg {
		return true
	}
	if !r.streaming || msg.Role != types.RoleAssistant {
		return false
	}
	for i := len(r.sync.Messages) - 1; i >= 0; i-- {
		candidate := r.sync.Messages[i]
		if candidate.PendingBtw {
			continue
		}
		return candidate == msg
	}
	return false
}

func messageParty(role string) string {
	switch role {
	case types.RoleUser:
		return "user"
	case types.RoleAssistant, types.RoleThinking, types.RoleStatus:
		return "agent"
	case types.RoleAssignment:
		return "assignment"
	default:
		return ""
	}
}

func (r *Renderer) shouldSuppressHeader(msgIdx int) bool {
	if msgIdx <= 0 {
		return false
	}
	current := r.sync.Messages[msgIdx]
	currentParty := messageParty(current.Role)
	if currentParty == "" {
		return false
	}
	for i := msgIdx - 1; i >= 0; i-- {
		previous := r.sync.Messages[i]
		if current.Role == types.RoleAssistant {
			switch previous.Role {
			case types.RoleThinking, types.RoleStatus:
				return false
			case types.RoleAssistant:
				if current.TurnGeneration > 0 && previous.TurnGeneration > 0 {
					return current.TurnGeneration == previous.TurnGeneration
				}
			}
		}
		prevParty := messageParty(previous.Role)
		if prevParty == currentParty {
			return true
		}
		if prevParty != "" {
			return false
		}
	}
	return false
}

func (r *Renderer) messageIndexOf(target *types.ChatMessage) int {
	for i, m := range r.sync.Messages {
		if m == target {
			return i
		}
	}
	return -1
}

func (r *Renderer) MdRenderer() *glamour.TermRenderer {
	return r.makeMdRenderer()
}

func (r *Renderer) makeMdRenderer() *glamour.TermRenderer {
	contentWidth, _ := r.availableMessageWidths()
	// Glamour's shared renderer intentionally has a 20-column readable-body
	// minimum. Below that policy threshold, use the plain wrapping path so a
	// narrow chat does not reintroduce a synthetic 20-column composition.
	const minimumMarkdownContentWidth = 20
	if contentWidth < minimumMarkdownContentWidth {
		r.mdRenderer = nil
		r.cachedWidth = contentWidth
		return nil
	}
	if r.mdRenderer != nil && r.cachedWidth == contentWidth {
		return r.mdRenderer
	}
	// MakeMdRenderer subtracts four padding columns plus the same 16-column
	// message chrome. Re-add those fixed columns after deriving the bounded
	// content width so its word-wrap width equals contentWidth exactly.
	md := rendering.MakeMdRenderer(contentWidth+4+messageBodyChromeWidth, r.theme)
	if md != nil {
		r.mdRenderer = md
		r.cachedWidth = contentWidth
	}
	return md
}

func (r *Renderer) RebuildItems() {
	r.lazyWidthReflow = false
	// Explicit rebuilds repair missing native halves as well as source changes.
	for _, msg := range r.sync.Messages {
		if msg.CachedLines != nil && len(r.nativeCache[msg.InternalID]) != len(msg.CachedLines) {
			msg.CachedLines = nil
		}
	}
	r.syncActivityItems(nil)
}

func (r *Renderer) InvalidateAllMessages() {
	r.lazyWidthReflow = false
	perf.MarkCacheInvalidation()
	for i := range r.sync.Messages {
		r.invalidateMessageCache(r.sync.Messages[i])
	}
	for _, batch := range r.activityBatches {
		for _, msg := range batch {
			r.invalidateMessageCache(msg)
		}
	}
	r.vl.InvalidateHeights()
	r.vl.InvalidateAll()
}

func (r *Renderer) InvalidateSubPane(paneID string) {
	for _, msg := range r.sync.Messages {
		if msg.SubPaneID == paneID {
			msg.CachedLines = nil
			r.vl.InvalidateItem(msg.InternalID)
			return
		}
	}
}

func (r *Renderer) HasVisiblePaneScrollbar() bool {
	for _, info := range r.paneLineMap {
		if info.PaneID != "" {
			return true
		}
	}
	return false
}

func (r *Renderer) CanScroll() bool {
	r.vl.BuildIndex()
	return r.vl.MaxScroll() > 0
}

func (r *Renderer) ContentHeight() int {
	r.vl.BuildIndex()
	return r.vl.TotalHeight()
}
