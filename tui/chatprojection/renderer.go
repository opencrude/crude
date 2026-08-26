package chatprojection

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"

	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/tools"
	"crude/tui/types"
	"crude/tui/ui"
)

type SubPaneContentFn func(paneID string, width, maxH, spinFrame int, expanded bool) []SubPaneLine

type Renderer struct {
	vl         itemList
	extScroll  *ui.ScrollModel
	paneScroll *ui.ScrollModel
	sync       *MessageSync
	theme      *theme.Theme

	chatAreaTop    int
	chatAreaHeight int

	mdRenderer   *glamour.TermRenderer
	cachedWidth  int
	width        int
	height       int
	modelName    string
	username     string
	streaming    bool
	spinFrame    int
	activeTyping bool
	phase        string

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
	expanded map[string]bool
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
		vl:              *newItemList(),
		sync:            NewMessageSync(),
		nativeCache:     make(map[string][]PaintLine),
		streamNative:    make(map[string][]PaintLine),
		streamNativeLen: make(map[string]int),
		expanded:        make(map[string]bool),
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
	var rows []PaintLine
	for _, line := range msg.StreamCompletedLines {
		rows = append(rows, BridgeAnsiLine(line, tokens))
	}
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

func (r *Renderer) SetModelName(name string) {
	if r.modelName != name {
		r.modelName = name
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
	item := r.messageToItem(len(r.sync.Messages) - 1)
	r.vl.AppendItem(item)
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
		if last.Role == types.RoleThinking {
			last.Content += s
			r.bumpVersion()
			last.CachedLines = nil
			r.vl.InvalidateItem(last.InternalID)
			return
		}
	}
	for _, msg := range r.sync.Messages {
		if msg.ThinkingActive {
			msg.ThinkingActive = false
			r.bumpVersion()
			msg.CachedLines = nil
			r.vl.InvalidateItem(msg.InternalID)
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
	item := r.messageToItem(len(r.sync.Messages) - 1)
	r.vl.AppendItem(item)
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
			r.vl.InvalidateItem(msg.InternalID)
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
	item := r.messageToItem(len(r.sync.Messages) - 1)
	r.vl.AppendItem(item)
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
			r.vl.InvalidateItem(msg.InternalID)
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
	item := r.messageToItem(len(r.sync.Messages) - 1)
	r.vl.AppendItem(item)
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
	r.vl.InvalidateItem(msg.InternalID)
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
	r.vl.InvalidateItem(msg.InternalID)
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
	r.vl.InvalidateItem(msg.InternalID)
}

func (r *Renderer) MarkStreamingToolsCancelled() {
	changed := false
	for _, msg := range r.sync.Messages {
		if msg.ToolStatus == "streaming" || msg.ToolStatus == "calling" {
			msg.ToolStatus = types.ToolStatusCancelled
			msg.ToolPreviewPhase = types.ToolPreviewDone
			msg.ToolProgressPhase = ""
			msg.ToolProgressMessage = ""
			msg.CachedLines = nil
			r.vl.InvalidateItem(msg.InternalID)
			changed = true
		}
	}
	if changed {
		r.bumpVersion()
	}
}

func (r *Renderer) MarkAllThinkingDone() {
	changed := false
	for _, msg := range r.sync.Messages {
		if msg.Role == types.RoleThinking {
			msg.ThinkingActive = false
			msg.ThinkingInterrupted = false
			msg.CachedLines = nil
			r.vl.InvalidateItem(msg.InternalID)
			changed = true
		}
	}
	if changed {
		r.bumpVersion()
	}
}

func (r *Renderer) MarkAllThinkingInterrupted() {
	changed := false
	for _, msg := range r.sync.Messages {
		if msg.Role == types.RoleThinking && msg.ThinkingActive {
			msg.ThinkingActive = false
			msg.ThinkingInterrupted = true
			msg.CachedLines = nil
			r.vl.InvalidateItem(msg.InternalID)
			changed = true
		} else if msg.Role == types.RoleThinking {
			msg.ThinkingActive = false
			changed = true
		}
	}
	if changed {
		r.bumpVersion()
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
			if msg.CachedLines != nil {
				return len(msg.CachedLines) + 1
			}
			return len(rendering.WrapText(msg.Content, contentWidth)) + 1
		}
	}
	return 0
}

func (r *Renderer) InvalidateCache() {
	for _, msg := range r.sync.Messages {
		if msg.Role == types.RoleTool && (msg.ToolStatus == "streaming" || msg.ToolStatus == "calling") {
			msg.CachedLines = nil
			r.vl.InvalidateItem(msg.InternalID)
		}
		if msg.Role == types.RoleThinking && msg.ThinkingActive {
			msg.CachedLines = nil
			r.vl.InvalidateItem(msg.InternalID)
		}
	}
	if len(r.sync.Messages) > 0 {
		last := r.sync.Messages[len(r.sync.Messages)-1]
		if last.Role == types.RoleAssistant || (last.Role == types.RoleThinking && last.ThinkingActive) {
			last.CachedLines = nil
			r.vl.InvalidateItem(last.InternalID)
		}
	}
}

func (r *Renderer) SetStreaming(s bool) {
	if r.streaming == s {
		return
	}
	if len(r.sync.Messages) > 0 {
		last := r.sync.Messages[len(r.sync.Messages)-1]
		if last.Role == types.RoleAssistant {
			last.CachedLines = nil
			r.vl.InvalidateItem(last.InternalID)
		}
	}
	r.streaming = s
}

func (r *Renderer) EndStreamingCursor() {
	r.SetStreaming(false)
}

func (r *Renderer) SetSpinFrame(f int) {
	r.spinFrame = f
}

func (r *Renderer) SetActiveTyping(v bool) {
	r.activeTyping = v
}

func (r *Renderer) SetPhase(p string) {
	r.phase = p
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

	previousLen := result.PreviousLen
	if result.PureAppend && len(messages) > previousLen {
		if cursorOwnerID != "" {
			if previous := r.sync.MsgByID[cursorOwnerID]; previous != nil {
				previous.CachedLines = nil
			}
			r.vl.InvalidateItem(cursorOwnerID)
		}
		for index := previousLen; index < len(r.sync.Messages); index++ {
			r.vl.AppendItem(r.messageToItem(index))
		}
		for _, id := range result.ChangedIDs {
			if r.sync.MsgByID[id] != nil {
				r.vl.InvalidateItem(id)
			}
		}
		return
	}
	if result.StructureChanged {
		r.RebuildItems()
		return
	}
	for _, id := range result.ChangedIDs {
		r.vl.InvalidateItem(id)
	}
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
}

func (r *Renderer) InvalidateItem(id string) {
	for _, msg := range r.sync.Messages {
		if msg.InternalID == id {
			msg.CachedLines = nil
			r.vl.InvalidateItem(id)
			return
		}
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

// ExportMessageRows renders all messages and returns message-level rows for
// shellchat ui.VirtualList (Height = len(Lines)). PaneLineMap is indexed by
// global content line.
func (r *Renderer) ExportMessageRows(width int) ([]MessageRow, []PaneLineInfo, int) {
	if r.chatAreaHeight <= 0 {
		return nil, nil, 0
	}
	r.prepareRender(width)
	// shellchat owns the visible viewport and may scroll to any exported row.
	// Materialize every dirty item before handing it to the second VirtualList;
	// otherwise offscreen items become blank bands and lack pane gutter metadata.
	r.vl.EnsureHeights(r.renderItem, width)
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
	rows := make([]MessageRow, 0, r.vl.ItemCount())
	for i := 0; i < r.vl.ItemCount(); i++ {
		item := r.vl.Item(i)
		if item == nil {
			continue
		}
		subPaneID := ""
		toggleLine := -1
		if msg, ok := item.Data.(*types.ChatMessage); ok && msg != nil {
			subPaneID = msg.SubPaneID
			if r.hasDisclosure(msg) {
				toggleLine = 0
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
		if toggleLine >= 0 {
			// The leading spacer occupies row 0 when present; the disclosure
			// affordance lives on the message's first content row.
			if toggleLine < len(lines) && strings.TrimSpace(lines[0]) == "" {
				toggleLine = 1
			}
		}
		rows = append(rows, MessageRow{
			ID:         item.ID,
			Lines:      lines,
			Native:     native,
			SubPaneID:  subPaneID,
			Toggle:     toggleLine >= 0,
			ToggleLine: toggleLine,
		})
	}
	paneMap := r.buildGlobalPaneLineMap()
	return rows, paneMap, r.vl.TotalHeight()
}

func (r *Renderer) prepareRender(width int) {
	sbW := r.ScrollbarWidth()
	if sbW != r.lastScrollbarW {
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
	if len(r.sync.Messages) == 0 {
		return ""
	}
	last := r.sync.Messages[len(r.sync.Messages)-1]
	if r.isStreamingCursor(last) {
		return last.InternalID
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
	r.vl.BuildIndex()
	total := r.vl.TotalHeight()
	paneMap := make([]PaneLineInfo, total)
	if len(r.sync.Messages) == 0 || total == 0 {
		return paneMap
	}
	for globalLine := 0; globalLine < total; globalLine++ {
		itemIdx, localLine := r.vl.ItemAtLine(globalLine)
		if itemIdx < 0 || itemIdx >= len(r.sync.Messages) {
			continue
		}
		msg := r.sync.Messages[itemIdx]
		if msg.SubPaneID == "" {
			continue
		}
		item := r.vl.Item(itemIdx)
		if item != nil && localLine >= 1 && localLine <= item.PaneRows {
			paneMap[globalLine] = PaneLineInfo{
				PaneID:  msg.SubPaneID,
				LineIdx: localLine - 2,
			}
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
		if itemIdx < 0 || itemIdx >= len(r.sync.Messages) {
			continue
		}
		msg := r.sync.Messages[itemIdx]
		if msg.SubPaneID == "" {
			continue
		}

		// localLine 0 is the leading spacer. PaneRows excludes a collapsed
		// pane's trailing separator, so neither blank row inherits pane
		// background, scrollbar paint, or nested wheel ownership.
		item := r.vl.Item(itemIdx)
		if item != nil && localLine >= 1 && localLine <= item.PaneRows {
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
	msg, ok := item.Data.(*types.ChatMessage)
	if !ok {
		item.Native = nil
		item.PaneRows = 0
		return []string{""}
	}

	if msg.SubPaneID != "" && r.subPaneContentFn != nil {
		maxH := r.chatAreaHeight / 3
		if maxH < 5 {
			maxH = 5
		}
		paneWidth := max(0, width-r.scrollbarFootprint())
		expanded := r.disclosureState(msg) == DisclosureExpanded
		paneLines := r.subPaneContentFn(msg.SubPaneID, paneWidth, maxH, r.spinFrame, expanded)
		result := make([]string, 0, len(paneLines)+1)
		native := make([]PaintLine, 0, len(paneLines)+1)
		item.PaneRows = 0
		result = append(result, "")
		native = append(native, buildPlainBodyLine(""))
		for _, sl := range paneLines {
			if !sl.Separator {
				item.PaneRows++
			}
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
		return result
	}
	item.PaneRows = 0

	isStreamingCursor := r.isStreamingCursor(msg)
	msgIdx := r.messageIndexOf(msg)
	suppressHeader := r.shouldSuppressHeader(msgIdx)
	needsSpacer := r.needsLeadingSpacer(msgIdx)

	if msg.CachedLines != nil && !isStreamingCursor {
		result := make([]string, 0, len(msg.CachedLines)+1)
		native := make([]PaintLine, 0, len(msg.CachedLines)+1)
		if needsSpacer {
			result = append(result, "")
			native = append(native, buildPlainBodyLine(""))
		}
		result = append(result, msg.CachedLines...)
		cachedNative := r.nativeCache[msg.InternalID]
		if len(cachedNative) == len(msg.CachedLines) {
			native = append(native, cachedNative...)
		} else {
			// Native cache is stale or missing — re-render fresh so native
			// rows are always real (never ANSI fallback). The fresh render
			// bridges once and repopulates the cache.
			ctx := r.buildRenderContext(msg, suppressHeader)
			if r.streaming && r.isLastMessage(msg) && msg.Role == types.RoleAssistant {
				ctx.streamNative = r.streamNativeRows(msg, r.Tokens())
			}
			fresh := renderMessageContent(*msg, ctx, suppressHeader)
			if len(fresh.native) == len(msg.CachedLines) {
				r.nativeCache[msg.InternalID] = fresh.native
				native = append(native, fresh.native...)
			} else {
				// Length mismatch (e.g. spacer difference) — rebuild plain
				// body lines from cached text so every row stays native.
				for _, tl := range msg.CachedLines {
					native = append(native, buildPlainBodyLine(tl))
				}
			}
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
	innerW = max(0, r.width-padLR*2-r.scrollbarFootprint())
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
		th:               r.theme,
		tokens:           r.Tokens(),
		contentWidth:     contentWidth,
		innerW:           innerW,
		width:            r.width,
		height:           r.height,
		chatHeight:       chatH,
		spinFrame:        r.spinFrame,
		activeTyping:     r.activeTyping,
		modelName:        r.modelName,
		username:         r.username,
		streaming:        r.streaming,
		isLastMsg:        r.isLastMessage(msg),
		phase:            r.phase,
		mdRenderer:       r.MdRenderer(),
		disclosure:       r.disclosureState(msg),
		thinkingBodyOnly: r.thinkingBodyOnly,
		planSuperseded:   r.planMessageSuperseded(msg),
	}
}

// planMessageSuperseded collapses historical snapshots when a newer plan
// mutation/snapshot exists. The underlying transcript remains intact and an
// explicit expansion still reveals the older snapshot.
func (r *Renderer) planMessageSuperseded(msg *types.ChatMessage) bool {
	if msg == nil || msg.Role != types.RoleTool || !tools.IsPlanTool(msg.ToolName) || msg.ToolStatus == "error" {
		return false
	}
	idx := r.messageIndexOf(msg)
	for i := idx + 1; i < len(r.sync.Messages); i++ {
		next := r.sync.Messages[i]
		if next != nil && next.Role == types.RoleTool && tools.IsPlanTool(next.ToolName) && next.ToolStatus != "error" {
			return true
		}
	}
	return false
}

func (r *Renderer) renderSingleMessage(msg *types.ChatMessage) ([]string, []PaintLine) {
	origMsg := msg
	sanitized := *msg
	sanitized.Content = rendering.SanitizeForTerminal(msg.Content)
	sanitized.ToolArgs = rendering.SanitizeForTerminal(msg.ToolArgs)
	sanitized.ToolProgressLabel = rendering.SanitizeForTerminal(msg.ToolProgressLabel)
	msg = &sanitized

	msgIdx := r.messageIndexOf(origMsg)
	suppressHeader := r.shouldSuppressHeader(msgIdx)
	needsSpacer := r.needsLeadingSpacer(msgIdx)

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
		case msg.Role == types.RoleThinking && msg.ThinkingActive:
			cursorIdx = len(ml.lines) - 1
		}
	}

	isStreamingCursor := cursorIdx >= 0
	if isStreamingCursor {
		ml = ml.withCursor(cursorIdx, r.spinFrame, r.activeTyping)
	}

	var lines []string
	var native []PaintLine
	if needsSpacer {
		lines = append(lines, "")
		native = append(native, buildPlainBodyLine(""))
	}
	lines = append(lines, ml.lines...)
	native = append(native, ml.native...)

	if !isStreamingCursor {
		msg.CachedLines = make([]string, len(ml.lines))
		copy(msg.CachedLines, ml.lines)
		r.nativeCache[msg.InternalID] = append([]PaintLine(nil), ml.native...)
	}

	return lines, native
}

func (r *Renderer) isLastMessage(msg *types.ChatMessage) bool {
	if len(r.sync.Messages) == 0 {
		return false
	}
	return r.sync.Messages[len(r.sync.Messages)-1] == msg
}

func messageParty(role string) string {
	switch role {
	case types.RoleUser:
		return "user"
	case types.RoleAssistant, types.RoleThinking:
		return "agent"
	default:
		return ""
	}
}

// previousEndsWithPaneSeparator reports whether the message right before idx
// owns a collapsed subpane that already paints a trailing separator blank. A
// message following it must not add its own leading spacer, or the two blanks
// stack into a double empty row between the thought block and the next block.
func (r *Renderer) previousEndsWithPaneSeparator(msgIdx int) bool {
	if msgIdx <= 0 {
		return false
	}
	return r.sync.Messages[msgIdx-1].SubPaneID != ""
}

// needsLeadingSpacer decides whether a message should paint a blank row before
// it. Exactly one blank row separates any two adjacent chat items: every
// message gets a leading spacer (the first one's serves as top padding) except
// when the previous message is a collapsed subpane that already paints its own
// trailing separator blank. Header suppression only controls whether a party
// label is shown — it must never remove the inter-item separator, or a tool
// followed by a same-party assistant (header suppressed) gets zero gap while a
// tool followed by thinking gets two (the thinking's internal header↔body gap
// stacks on top of the spacer).
func (r *Renderer) needsLeadingSpacer(msgIdx int) bool {
	return !r.previousEndsWithPaneSeparator(msgIdx)
}

func (r *Renderer) shouldSuppressHeader(msgIdx int) bool {
	if msgIdx <= 0 {
		return false
	}
	currentParty := messageParty(r.sync.Messages[msgIdx].Role)
	if currentParty == "" {
		return false
	}
	for i := msgIdx - 1; i >= 0; i-- {
		prevParty := messageParty(r.sync.Messages[i].Role)
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

func (r *Renderer) messageToItem(idx int) renderItem {
	msg := r.sync.Messages[idx]
	height := -1
	var rendered []string
	if msg.CachedLines != nil {
		needsSpacer := r.needsLeadingSpacer(idx)
		if needsSpacer {
			height = len(msg.CachedLines) + 1
			rendered = make([]string, 0, height)
			rendered = append(rendered, "")
			rendered = append(rendered, msg.CachedLines...)
		} else {
			height = len(msg.CachedLines)
			rendered = make([]string, 0, height)
			rendered = append(rendered, msg.CachedLines...)
		}
	} else if h, ok := r.vl.ItemHeight(msg.InternalID); ok {
		height = h
	}
	return renderItem{
		ID:       msg.InternalID,
		Height:   height,
		Rendered: rendered,
		Dirty:    msg.CachedLines == nil,
		Data:     msg,
	}
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
	md := rendering.MakeMdRenderer(contentWidth + 4 + messageBodyChromeWidth)
	if md != nil {
		r.mdRenderer = md
		r.cachedWidth = contentWidth
	}
	return md
}

func (r *Renderer) RebuildItems() {
	items := make([]renderItem, len(r.sync.Messages))
	for i := range r.sync.Messages {
		items[i] = r.messageToItem(i)
	}
	r.vl.SetItems(items)
}

func (r *Renderer) InvalidateAllMessages() {
	for i := range r.sync.Messages {
		r.invalidateMessageCache(r.sync.Messages[i])
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
