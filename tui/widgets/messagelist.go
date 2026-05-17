package widgets

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"

	"crude/tui/rendering"
	"crude/tui/scrollbar"
	"crude/tui/theme"
	"crude/tui/tools"
	"crude/tui/types"
	"crude/tui/virtlist"
)

var selScrollInterval = 60 * time.Millisecond

type SubPaneContentFn func(paneID string, width, maxH, spinFrame int) []string

type PaneLineInfo struct {
	PaneID  string
	LineIdx int
}

type MessageList struct {
	vl         virtlist.VirtualList
	scroll     virtlist.ScrollState
	scrollbar  scrollbar.ScrollBar
	theme      *theme.Theme
	messages   []*types.ChatMessage

	selecting     bool
	selAnchorLine int
	selAnchorCol  int
	selEndLine    int
	selEndCol     int
	selAutoScroll bool
	selMouseY     int

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

	lastScrollbarW     int
	lastRenderedLines  []string
	subPaneContentFn   SubPaneContentFn
	paneLineMap        []PaneLineInfo
	paneScrollbarW     int
}

func NewMessageList(t *theme.Theme) *MessageList {
	ml := &MessageList{
		theme:  t,
		vl:     *virtlist.New(),
		scroll: virtlist.NewScrollState(),
	}
	ml.scrollbar = scrollbar.New(0)
	return ml
}

func (ml *MessageList) SetSubPaneContentFn(fn SubPaneContentFn) {
	ml.subPaneContentFn = fn
}

func (ml *MessageList) PaneLineMap() []PaneLineInfo {
	return ml.paneLineMap
}

func (ml *MessageList) SetPaneScrollbarWidth(w int) {
	if w != ml.paneScrollbarW {
		ml.paneScrollbarW = w
		for _, msg := range ml.messages {
			msg.CachedLines = nil
		}
		ml.vl.InvalidateAll()
	}
}

func (ml *MessageList) PaneScrollbarWidth() int {
	return ml.paneScrollbarW
}

func (ml *MessageList) SetTheme(t *theme.Theme) {
	if t != nil {
		ml.theme = t
	}
}

func (ml *MessageList) SetModelName(name string) {
	ml.modelName = name
}

func (ml *MessageList) SetUsername(name string) {
	ml.username = name
}

func (ml *MessageList) SetSize(w, h int) {
	if ml.width != w {
		for i := range ml.messages {
			ml.messages[i].CachedLines = nil
			ml.messages[i].StreamCompletedLines = nil
			ml.messages[i].StreamCompletedLen = 0
		}
		ml.mdRenderer = nil
		ml.vl.InvalidateHeights()
		ml.vl.InvalidateAll()
	}
	ml.width = w
	ml.height = h
	ml.vl.SetViewport(ml.chatAreaHeight)
}

func (ml *MessageList) SetChatAreaBounds(top, height int) {
	ml.chatAreaTop = top
	ml.chatAreaHeight = height
	ml.vl.SetViewport(height)
	ml.scrollbar.SetTrackHeight(height)
}

func (ml *MessageList) AppendMessage(msg types.ChatMessage) {
	msgCopy := msg
	ml.messages = append(ml.messages, &msgCopy)
	item := ml.messageToItem(len(ml.messages) - 1)
	ml.vl.AppendItem(item)
}

func (ml *MessageList) AppendToken(token string) {
	if len(ml.messages) == 0 {
		return
	}
	last := ml.messages[len(ml.messages)-1]
	if last.Role != types.RoleAssistant {
		return
	}
	last.Content += token
	last.CachedLines = nil
	if strings.Contains(token, "\n") {
		last.StreamCompletedLines = nil
		last.StreamCompletedLen = 0
	}
	ml.vl.InvalidateItem(ml.itemID(len(ml.messages) - 1))
}

func (ml *MessageList) AppendThinking(s string) {
	if len(ml.messages) > 0 {
		last := ml.messages[len(ml.messages)-1]
		if last.Role == types.RoleThinking {
			last.Content += s
			last.CachedLines = nil
			ml.vl.InvalidateItem(ml.itemID(len(ml.messages) - 1))
			return
		}
	}
	for _, msg := range ml.messages {
		if msg.ThinkingActive {
			msg.ThinkingActive = false
			msg.CachedLines = nil
			ml.vl.InvalidateItem(ml.itemID(ml.msgIndex(msg)))
		}
	}
	ml.messages = append(ml.messages, &types.ChatMessage{
		Role:           types.RoleThinking,
		Content:        s,
		ThinkingActive: true,
	})
	item := ml.messageToItem(len(ml.messages) - 1)
	ml.vl.AppendItem(item)
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

func (ml *MessageList) UpdateToolStreaming(toolID, name string, data map[string]any) {
	target, _ := data["target"].(string)
	estTokens := toIntField(data["tool_est_tokens"])
	contentLines := toIntField(data["content_lines_int"])
	oldLines := toIntField(data["old_lines_int"])
	newLines := toIntField(data["new_lines_int"])

	previewPhase := types.ToolPreviewPreparing
	if target != "" {
		previewPhase = types.ToolPreviewStreaming
	}

	for i := range ml.messages {
		if ml.messages[i].ToolID == toolID {
			msg := ml.messages[i]
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
			msg.CachedLines = nil
			ml.vl.InvalidateItem(ml.itemID(i))
			return
		}
	}

	if toolID != "" {
		for i, msg := range ml.messages {
			if msg.ToolName == name && msg.ToolID == "" && msg.ToolStatus == "streaming" {
				msg.ToolID = toolID
				msg.ToolProgressLabel = target
				msg.ToolPreviewPhase = previewPhase
				msg.ToolProgressPhase = "streaming"
				msg.ToolProgressMessage = ""
				msg.CachedLines = nil
				ml.vl.InvalidateItem(ml.itemID(i))
				return
			}
		}
	}

	msg := types.ChatMessage{
		Role:              types.RoleTool,
		Time:              time.Now(),
		ToolName:          name,
		ToolID:            toolID,
		ToolArgs:          "",
		ToolStatus:        "streaming",
		ToolIndex:         len(ml.messages),
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
	ml.messages = append(ml.messages, &msg)
	item := ml.messageToItem(len(ml.messages) - 1)
	ml.vl.AppendItem(item)
}

func (ml *MessageList) UpdateToolCall(toolID, name, argsCompact string) {
	if toolID != "" {
		for _, msg := range ml.messages {
			if msg.ToolName == name && msg.ToolID == "" && (msg.ToolStatus == "calling" || msg.ToolStatus == "streaming") {
				msg.ToolID = toolID
			}
		}
	}
	found := false
	for i, msg := range ml.messages {
		if msg.ToolID == toolID && msg.ToolName == name &&
			(msg.ToolStatus == "calling" || msg.ToolStatus == "streaming") {
			msg.ToolStatus = "calling"
			msg.ToolArgs = argsCompact
			msg.Content = name + " " + argsCompact
			msg.ToolPreviewPhase = types.ToolPreviewStreaming
			msg.ToolProgressPhase = ""
			msg.ToolProgressMessage = ""
			if msg.ToolProgressLabel == "" {
				msg.ToolProgressLabel = argsCompact
			}
			msg.CachedLines = nil
			ml.vl.InvalidateItem(ml.itemID(i))
			found = true
			break
		}
	}
	if !found {
		ml.messages = append(ml.messages, &types.ChatMessage{
			Role:              types.RoleTool,
			Content:           name + " " + argsCompact,
			Time:              time.Now(),
			ToolName:          name,
			ToolID:            toolID,
			ToolArgs:          argsCompact,
			ToolStatus:        "calling",
			ToolIndex:         len(ml.messages),
			ToolPreviewPhase:  types.ToolPreviewStreaming,
			ToolProgressLabel: argsCompact,
			ToolStartTime:     time.Now(),
		})
		item := ml.messageToItem(len(ml.messages) - 1)
		ml.vl.AppendItem(item)
	}
}

func (ml *MessageList) UpdateToolProgress(toolID, name, phase, message, path string, linesAdded, linesRemoved, estTokens int) {
	if toolID != "" {
		for _, msg := range ml.messages {
			if msg.ToolName == name && msg.ToolID == "" && (msg.ToolStatus == "calling" || msg.ToolStatus == "streaming") {
				msg.ToolID = toolID
			}
		}
	}
	for i, msg := range ml.messages {
		if msg.ToolStatus != "calling" && msg.ToolStatus != "streaming" && msg.ToolStatus != "success" {
			continue
		}
		matched := false
		if toolID != "" && msg.ToolID == toolID && msg.ToolName == name {
			matched = true
		} else if toolID == "" && msg.ToolName == name {
			matched = true
		}
		if matched {
			msg.ToolProgressPhase = phase
			msg.ToolProgressMessage = message
			if path != "" {
				msg.ToolProgressLabel = path
			}
			if phase == "done" {
				msg.ToolLivedAdded = linesAdded
				msg.ToolLivedRemoved = linesRemoved
				if !msg.ToolStartTime.IsZero() {
					msg.ToolDuration = time.Since(msg.ToolStartTime)
				}
				if estTokens > 0 {
					msg.ToolEstTokens = estTokens
				}
			} else {
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
			msg.CachedLines = nil
			ml.vl.InvalidateItem(ml.itemID(i))
			return
		}
	}
}

func (ml *MessageList) UpdateToolResult(toolID, name, result string, isError bool, estTokens, linesAdded, linesRemoved int) {
	if toolID != "" {
		for _, msg := range ml.messages {
			if msg.ToolName == name && msg.ToolID == "" && (msg.ToolStatus == "calling" || msg.ToolStatus == "streaming") {
				msg.ToolID = toolID
			}
		}
	}
	for i, msg := range ml.messages {
		if msg.ToolStatus != "calling" && msg.ToolStatus != "streaming" {
			continue
		}
		matched := false
		if toolID != "" && msg.ToolID == toolID && msg.ToolName == name {
			matched = true
		} else if toolID == "" && msg.ToolName == name {
			matched = true
		}
		if matched {
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
			ml.vl.InvalidateItem(ml.itemID(i))
			return
		}
	}
}

// SetToolPermTag sets the PermTag on a tool message matched by toolID.
func (ml *MessageList) SetToolPermTag(toolID, tag string) {
	for i, msg := range ml.messages {
		if msg.ToolID == toolID {
			msg.PermTag = tag
			msg.CachedLines = nil
			ml.vl.InvalidateItem(ml.itemID(i))
			return
		}
	}
}

func (ml *MessageList) ClearStreamingTools() {
	var keep []*types.ChatMessage
	var items []virtlist.Item
	for i, msg := range ml.messages {
		if msg.ToolStatus == "streaming" || msg.ToolStatus == "calling" {
			continue
		}
		keep = append(keep, msg)
		items = append(items, ml.messageToItem(i))
	}
	ml.messages = keep
	ml.vl.SetItems(items)
}

func (ml *MessageList) MarkAllThinkingDone() {
	for i, msg := range ml.messages {
		if msg.Role == types.RoleThinking {
			msg.ThinkingActive = false
			msg.CachedLines = nil
			ml.vl.InvalidateItem(ml.itemID(i))
		}
	}
}

func (ml *MessageList) RemoveActiveThinking() string {
	var content string
	var keep []*types.ChatMessage
	for _, msg := range ml.messages {
		if msg.Role == types.RoleThinking && msg.ThinkingActive {
			content = msg.Content
			continue
		}
		keep = append(keep, msg)
	}
	if len(keep) != len(ml.messages) {
		ml.messages = keep
		ml.RebuildItems()
	}
	return content
}

func (ml *MessageList) ActiveThinkingLineCount(contentWidth int) int {
	for _, msg := range ml.messages {
		if msg.Role == types.RoleThinking && msg.ThinkingActive {
			if msg.CachedLines != nil {
				return len(msg.CachedLines) + 1
			}
			return len(rendering.WrapText(msg.Content, contentWidth)) + 1
		}
	}
	return 0
}

func (ml *MessageList) InvalidateCache() {
	for i, msg := range ml.messages {
		if msg.Role == types.RoleTool && (msg.ToolStatus == "streaming" || msg.ToolStatus == "calling") {
			msg.CachedLines = nil
			ml.vl.InvalidateItem(ml.itemID(i))
		}
		if msg.Role == types.RoleThinking && msg.ThinkingActive {
			msg.CachedLines = nil
			ml.vl.InvalidateItem(ml.itemID(i))
		}
	}
	if len(ml.messages) > 0 {
		last := ml.messages[len(ml.messages)-1]
		if last.Role == types.RoleAssistant || (last.Role == types.RoleThinking && last.ThinkingActive) {
			last.CachedLines = nil
			ml.vl.InvalidateItem(ml.itemID(len(ml.messages) - 1))
		}
	}
}

func (ml *MessageList) SetStreaming(s bool) {
	ml.streaming = s
}

func (ml *MessageList) EndStreamingCursor() {
	ml.streaming = false
	// Invalidate the LAST assistant message — that's the one whose
	// item.Rendered buffer still contains the cursor block (█).
	// Iterating backward matches isStreamingCursor's isLastMessage check.
	for i := len(ml.messages) - 1; i >= 0; i-- {
		if ml.messages[i].Role == types.RoleAssistant {
			ml.vl.InvalidateItem(ml.itemID(i))
			break
		}
	}
}

func (ml *MessageList) SetSpinFrame(f int) {
	ml.spinFrame = f
}

func (ml *MessageList) SetActiveTyping(v bool) {
	ml.activeTyping = v
}

func (ml *MessageList) SetPhase(p string) {
	ml.phase = p
}

func (ml *MessageList) ScrollToBottom() {
	ml.vl.ScrollToBottom()
	ml.scroll.SetAutoScroll(true)
}

func (ml *MessageList) IsAtBottom() bool {
	return ml.vl.IsAtBottom()
}

func (ml *MessageList) Messages() []*types.ChatMessage {
	return ml.messages
}

func (ml *MessageList) Update(msg tea.Msg) (*MessageList, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyPgUp:
			ml.vl.ScrollBy(-ml.chatAreaHeight)
			ml.scroll.SetAutoScroll(false)
		case tea.KeyPgDown:
			ml.vl.ScrollBy(ml.chatAreaHeight)
			if ml.scroll.ShouldAutoScroll(ml.vl.ScrollY(), ml.vl.MaxScroll()) {
				ml.vl.ScrollToBottom()
			}
		case tea.KeyHome:
			ml.vl.ScrollTo(0)
			ml.scroll.SetAutoScroll(false)
		case tea.KeyEnd:
			ml.ScrollToBottom()
		}

	case tea.MouseMsg:
		return ml, ml.handleMouse(msg)

	case types.SelectionTickMsg:
		return ml, ml.handleSelectionTick()
	}

	return ml, tea.Batch(cmds...)
}

func (ml *MessageList) handleMouse(msg tea.MouseMsg) tea.Cmd {
	chatBottom := ml.chatAreaTop + ml.chatAreaHeight - 1

	// If scrollbar is being dragged, route all motion/release events to it
	if ml.scrollbar.IsDragging() {
		if msg.Action == tea.MouseActionMotion || msg.Action == tea.MouseActionRelease {
			trackMsg := msg
			trackMsg.Y = msg.Y - ml.chatAreaTop
			updated, cmd := ml.scrollbar.Update(trackMsg)
			ml.scrollbar = updated
			if ml.scrollbar.ConsumeScrollChanged() {
				ml.vl.ScrollTo(ml.scrollbar.GetScrollY())
				ml.vl.ClampScroll()
				if ml.vl.IsAtBottom() {
					ml.scroll.SetAutoScroll(true)
				} else {
					ml.scroll.SetAutoScroll(false)
				}
			}
			return cmd
		}
	}

	// Check if the mouse event is in the scrollbar zone
	inScrollbarZone := ml.vl.MaxScroll() > 0 &&
		msg.X >= ml.width-ml.theme.Spacing.PadLR-scrollbar.ScrollbarWidth &&
		msg.Y >= ml.chatAreaTop && msg.Y <= chatBottom

	if inScrollbarZone && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		// Cancel any active selection when interacting with scrollbar
		ml.selecting = false
		ml.selAutoScroll = false

		trackMsg := msg
		trackMsg.Y = msg.Y - ml.chatAreaTop
		updated, cmd := ml.scrollbar.Update(trackMsg)
		ml.scrollbar = updated
		if ml.scrollbar.ConsumeScrollChanged() {
			ml.vl.ScrollTo(ml.scrollbar.GetScrollY())
			ml.vl.ClampScroll()
			if ml.vl.IsAtBottom() {
				ml.scroll.SetAutoScroll(true)
			} else {
				ml.scroll.SetAutoScroll(false)
			}
		}
		return cmd
	}

	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		if msg.Y >= ml.chatAreaTop && msg.Y <= chatBottom {
			lineIdx, ok := ml.MouseToGlobalLine(msg.Y)
			if ok {
				ml.selecting = true
				ml.selAnchorLine = lineIdx
				ml.selAnchorCol = rendering.Max(0, msg.X-ml.theme.Spacing.PadLR)
				ml.selEndLine = lineIdx
				ml.selEndCol = ml.selAnchorCol
				ml.selMouseY = msg.Y
				ml.selAutoScroll = false
			}
		} else {
			ml.selecting = false
			ml.selAutoScroll = false
		}

	case msg.Action == tea.MouseActionMotion && ml.selecting:
		lineIdx, ok := ml.MouseToGlobalLine(msg.Y)
		if ok {
			ml.selEndLine = lineIdx
			ml.selEndCol = rendering.Max(0, msg.X-ml.theme.Spacing.PadLR)
		} else if msg.Y < ml.chatAreaTop {
			firstLine, _ := ml.ChatAreaContentBounds()
			if firstLine >= 0 {
				ml.selEndLine = firstLine
				ml.selEndCol = 0
			}
		} else if msg.Y > chatBottom {
			_, lastLine := ml.ChatAreaContentBounds()
			if lastLine >= 0 {
				ml.selEndLine = lastLine
				ml.selEndCol = 1 << 30
			}
		}
		ml.selMouseY = msg.Y

		if !ml.selAutoScroll && (ml.selMouseY <= ml.chatAreaTop-1 || ml.selMouseY >= chatBottom+1) {
			ml.selAutoScroll = true
			return tea.Tick(selScrollInterval, func(t time.Time) tea.Msg {
				return types.SelectionTickMsg(t)
			})
		}

	case msg.Action == tea.MouseActionRelease && ml.selecting:
		ml.selecting = false
		ml.selAutoScroll = false
		sLine, sCol, eLine, eCol := ml.SelectionRange()
		text := ml.ExtractSelectedText(sLine, sCol, eLine, eCol)
		text = strings.TrimRight(text, " \t\n\r")
		ml.selAnchorLine = 0
		ml.selAnchorCol = 0
		ml.selEndLine = 0
		ml.selEndCol = 0
		if text != "" {
			return rendering.CopyToClipboardCmd(text)
		}

	case msg.Button == tea.MouseButtonWheelUp:
		n := ml.scroll.OnWheelUp()
		ml.vl.ScrollBy(-n)
		ml.scroll.SetAutoScroll(false)
		ml.scrollbar.SetScrollInfo(ml.vl.TotalHeight(), ml.vl.ViewportHeight(), ml.vl.ScrollY())

	case msg.Button == tea.MouseButtonWheelDown:
		n := ml.scroll.OnWheelDown()
		ml.vl.ScrollBy(n)
		if ml.scroll.ShouldAutoScroll(ml.vl.ScrollY(), ml.vl.MaxScroll()) {
			ml.vl.ScrollToBottom()
		}
		ml.scrollbar.SetScrollInfo(ml.vl.TotalHeight(), ml.vl.ViewportHeight(), ml.vl.ScrollY())
	}

	return nil
}

func (ml *MessageList) handleSelectionTick() tea.Cmd {
	if !ml.selecting || !ml.selAutoScroll {
		return nil
	}

	chatBottom := ml.chatAreaTop + ml.chatAreaHeight - 1

	if ml.selMouseY <= ml.chatAreaTop-1 {
		ml.vl.ScrollBy(-1)
		lineIdx, ok := ml.MouseToGlobalLine(ml.chatAreaTop)
		if ok {
			ml.selEndLine = lineIdx
			ml.selEndCol = 0
		}
	} else if ml.selMouseY >= chatBottom+1 {
		ml.vl.ScrollBy(1)
		lineIdx, ok := ml.MouseToGlobalLine(chatBottom)
		if ok {
			ml.selEndLine = lineIdx
			ml.selEndCol = 1 << 30
		}
	}

	if ml.selMouseY <= ml.chatAreaTop-1 || ml.selMouseY >= chatBottom+1 {
		return tea.Tick(selScrollInterval, func(t time.Time) tea.Msg {
			return types.SelectionTickMsg(t)
		})
	}

	ml.selAutoScroll = false
	return nil
}

func (ml *MessageList) SelectionRange() (startLine, startCol, endLine, endCol int) {
	if ml.selAnchorLine < ml.selEndLine ||
		(ml.selAnchorLine == ml.selEndLine && ml.selAnchorCol <= ml.selEndCol) {
		return ml.selAnchorLine, ml.selAnchorCol, ml.selEndLine, ml.selEndCol
	}
	return ml.selEndLine, ml.selEndCol, ml.selAnchorLine, ml.selAnchorCol
}

func (ml *MessageList) HasActiveSelection() bool {
	if !ml.selecting && ml.selAnchorLine == ml.selEndLine && ml.selAnchorCol == ml.selEndCol {
		return false
	}
	return ml.selAnchorLine != ml.selEndLine || ml.selAnchorCol != ml.selEndCol
}

func (ml *MessageList) ClearSelection() {
	ml.selecting = false
	ml.selAutoScroll = false
	ml.selAnchorLine = 0
	ml.selAnchorCol = 0
	ml.selEndLine = 0
	ml.selEndCol = 0
}

func (ml *MessageList) MouseToGlobalLine(mouseY int) (globalLine int, ok bool) {
	if len(ml.messages) == 0 || ml.chatAreaHeight <= 0 {
		return -1, false
	}
	chatY := mouseY - ml.chatAreaTop
	if chatY < 0 || chatY >= ml.chatAreaHeight {
		return -1, false
	}

	ml.vl.BuildIndex()
	total := ml.vl.TotalHeight()
	scrollY := ml.vl.ScrollY()

	if total <= 0 {
		return -1, false
	}

	if total <= ml.chatAreaHeight {
		// Gravity case: content bottom-aligned, empty at top
		emptyTop := ml.chatAreaHeight - total
		if chatY < emptyTop {
			return -1, false
		}
		globalLine = chatY - emptyTop
	} else {
		// Normal/overscroll case: content starts at top of viewport, empty at bottom
		globalLine = scrollY + chatY
		if globalLine >= total {
			// In the bottom padding area
			return -1, false
		}
	}

	if globalLine < 0 || globalLine >= total {
		return -1, false
	}
	return globalLine, true
}

func (ml *MessageList) ChatAreaContentBounds() (firstLine, lastLine int) {
	if len(ml.messages) == 0 {
		return -1, -1
	}
	ml.vl.BuildIndex()
	scrollY := ml.vl.ScrollY()
	total := ml.vl.TotalHeight()

	endLine := scrollY + ml.chatAreaHeight
	if endLine > total {
		endLine = total
	}
	startLine := scrollY
	if startLine >= total {
		return -1, -1
	}
	return startLine, endLine - 1
}

func (ml *MessageList) ExtractSelectedText(startLine, startCol, endLine, endCol int) string {
	allLines := ml.buildAllLinesForSelection()
	return rendering.ExtractSelectedText(allLines, startLine, startCol, endLine, endCol)
}

func (ml *MessageList) buildAllLinesForSelection() []string {
	var all []string
	for _, msg := range ml.messages {
		all = append(all, "")
		if msg.CachedLines != nil {
			all = append(all, msg.CachedLines...)
		} else {
			lines := ml.renderSingleMessage(msg)
			if len(lines) > 1 {
				all = append(all, lines[1:]...)
			}
		}
	}
	return all
}

// Render produces the visible chat lines. The rendering pipeline is:
//  1. Render dirty items (calculates accurate heights)
//  2. Apply auto-scroll with accurate heights
//  3. Re-extract visible lines at the correct scroll position
//
// This ensures auto-scroll is never one frame behind.
func (ml *MessageList) Render(width int) []string {
	if ml.chatAreaHeight <= 0 {
		return []string{}
	}

	sbW := ml.ScrollbarWidth()
	if sbW != ml.lastScrollbarW {
		ml.lastScrollbarW = sbW
		for _, msg := range ml.messages {
			msg.CachedLines = nil
		}
		ml.vl.InvalidateAll()
	}

	_ = ml.vl.RenderVisible(ml.renderItem, width)

	if ml.scroll.AutoScroll {
		ml.vl.BuildIndex()
		ml.vl.ScrollToBottom()
	}
	ml.vl.ClampScroll()

	visible := ml.vl.ExtractVisible()

	ml.applySelection(visible)

	ml.lastRenderedLines = visible

	ml.scrollbar.SetScrollInfo(ml.vl.TotalHeight()+ml.vl.BottomPadding(), ml.vl.ViewportHeight(), ml.vl.ScrollY())

	ml.buildPaneLineMap(visible)

	return visible
}

func (ml *MessageList) buildPaneLineMap(visible []string) {
	ml.paneLineMap = make([]PaneLineInfo, len(visible))
	if len(ml.messages) == 0 {
		return
	}

	ml.vl.BuildIndex()
	scrollY := ml.vl.ScrollY()
	total := ml.vl.TotalHeight()

	for i := range visible {
		var globalLine int
		if total <= ml.chatAreaHeight {
			emptyTop := ml.chatAreaHeight - total
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

		itemIdx, localLine := ml.vl.ItemAtLine(globalLine)
		if itemIdx < 0 || itemIdx >= len(ml.messages) {
			continue
		}
		msg := ml.messages[itemIdx]
		if msg.SubPaneID == "" {
			continue
		}

		if localLine >= 2 {
			ml.paneLineMap[i] = PaneLineInfo{
				PaneID:  msg.SubPaneID,
				LineIdx: localLine - 2,
			}
		}
	}
}

func (ml *MessageList) ScrollbarView() string {
	return ml.scrollbar.View()
}

func (ml *MessageList) ScrollbarWidth() int {
	w := 0
	if ml.scrollbar.IsVisible() && ml.vl.MaxScroll() > 0 {
		w += scrollbar.ScrollbarWidth
	}
	w += ml.paneScrollbarW
	return w
}

func (ml *MessageList) applySelection(visible []string) {
	if !ml.HasActiveSelection() {
		return
	}
	sLine, sCol, eLine, eCol := ml.SelectionRange()
	scrollY := ml.vl.ScrollY()
	total := ml.vl.TotalHeight()
	for i := range visible {
		var globalLine int
		if total <= ml.chatAreaHeight {
			// Gravity case
			emptyTop := ml.chatAreaHeight - total
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
		if globalLine >= sLine && globalLine <= eLine {
			lineStartCol := 0
			lineEndCol := 1 << 30
			if globalLine == sLine {
				lineStartCol = sCol
			}
			if globalLine == eLine {
				lineEndCol = eCol
			}
			visible[i] = rendering.ApplySelection(visible[i], lineStartCol, lineEndCol)
		}
	}
}

func (ml *MessageList) renderItem(idx int, item *virtlist.Item, width int) []string {
	msg, ok := item.Data.(*types.ChatMessage)
	if !ok {
		return []string{""}
	}

	if msg.SubPaneID != "" && ml.subPaneContentFn != nil {
		maxH := ml.chatAreaHeight / 3
		if maxH < 5 {
			maxH = 5
		}
		lines := ml.subPaneContentFn(msg.SubPaneID, ml.width, maxH, ml.spinFrame)
		return lines
	}

	isStreamingCursor := ml.isStreamingCursor(msg)

	if msg.CachedLines != nil && !isStreamingCursor {
		result := make([]string, 0, len(msg.CachedLines)+1)
		result = append(result, "")
		result = append(result, msg.CachedLines...)
		return result
	}

	lines := ml.renderSingleMessage(msg)

	if !isStreamingCursor {
		msg.CachedLines = make([]string, len(lines)-1)
		if len(lines) > 1 {
			copy(msg.CachedLines, lines[1:])
		}
	}

	return lines
}

func (ml *MessageList) isStreamingCursor(msg *types.ChatMessage) bool {
	if !ml.isLastMessage(msg) {
		return false
	}
	switch {
	case msg.Role == types.RoleAssistant && ml.streaming:
		return true
	case msg.Role == types.RoleThinking && msg.ThinkingActive:
		return true
	}
	return false
}

func (ml *MessageList) renderSingleMessage(msg *types.ChatMessage) []string {
	sanitized := *msg
	sanitized.Content = rendering.SanitizeForTerminal(msg.Content)
	sanitized.ToolArgs = rendering.SanitizeForTerminal(msg.ToolArgs)
	sanitized.ToolProgressLabel = rendering.SanitizeForTerminal(msg.ToolProgressLabel)
	msg = &sanitized

	padLR := ml.theme.Spacing.PadLR
	// sbW reflects the actual scrollbar width: 0 when hidden, ScrollbarWidth when visible.
	// This means content (including time labels) shifts when the scrollbar appears/disappears.
	// This is intentional — it maximizes content width when no scrollbar is needed,
	// at the cost of a slight reflow when scrollability changes. The alternative (always
	// reserving scrollbar space) wastes horizontal space when content fits the viewport.
	sbW := ml.ScrollbarWidth()
	contentWidth := ml.width - padLR*2 - 16 - sbW
	if contentWidth < 10 {
		contentWidth = 10
	}
	innerW := ml.width - padLR*2 - sbW
	if innerW < 20 {
		innerW = 20
	}

	var lines []string
	isLastMsg := ml.isLastMessage(msg)

	timeLine1, timeLine2 := rendering.FormatTimeLabel(msg.Time)
	timeW := rendering.Max(len(timeLine1), len(timeLine2))

	lines = append(lines, "")

	switch msg.Role {
	case types.RoleUser:
		msgLines := RenderUserMessage(*msg, ml.theme, contentWidth, innerW, timeLine1, timeLine2, timeW, ml.MdRenderer(), ml.username)
		lines = append(lines, msgLines...)
	case types.RoleAssistant:
		if ml.streaming && isLastMsg {
			updateStreamingCache(msg, ml.MdRenderer(), contentWidth)
		}
		msgLines := RenderAssistantMessage(*msg, ml.theme, contentWidth, innerW, timeLine1, timeLine2, timeW, ml.MdRenderer(), ml.modelName, ml.streaming, isLastMsg)
		lines = append(lines, msgLines...)
	case types.RoleThinking:
		msgLines := RenderThinkingMessage(*msg, ml.theme, contentWidth, innerW, ml.spinFrame)
		lines = append(lines, msgLines...)
	case types.RoleTool:
		msgLines := tools.RenderToolMessage(*msg, ml.theme, contentWidth, innerW, ml.width, ml.spinFrame, ml.height, ml.phase)
		lines = append(lines, msgLines...)
	case types.RoleError:
		msgLines := RenderErrorMessage(*msg, ml.theme, contentWidth, innerW, timeLine1, timeLine2, timeW)
		lines = append(lines, msgLines...)
	}

	cursorLine := -1
	if isLastMsg && len(lines) > 0 {
		switch {
		case msg.Role == types.RoleAssistant && ml.streaming:
			cursorLine = len(lines) - 1
		case msg.Role == types.RoleThinking && msg.ThinkingActive:
			cursorLine = len(lines) - 1
		}
	}

	if cursorLine >= 0 && cursorLine < len(lines) {
		cursor := blockCursor(ml.spinFrame, ml.theme.Colors.Accent, ml.activeTyping)
		lines[cursorLine] = lines[cursorLine] + cursor
	}

	return lines
}

func (ml *MessageList) isLastMessage(msg *types.ChatMessage) bool {
	if len(ml.messages) == 0 {
		return false
	}
	return ml.messages[len(ml.messages)-1] == msg
}

func (ml *MessageList) msgIndex(msg *types.ChatMessage) int {
	for i, m := range ml.messages {
		if m == msg {
			return i
		}
	}
	return -1
}

func (ml *MessageList) itemID(idx int) string {
	return fmt.Sprintf("msg-%d", idx)
}

func (ml *MessageList) messageToItem(idx int) virtlist.Item {
	msg := ml.messages[idx]
	height := -1
	if msg.CachedLines != nil {
		height = len(msg.CachedLines) + 1
	} else if h, ok := ml.vl.ItemHeight(ml.itemID(idx)); ok {
		height = h
	}
	return virtlist.Item{
		ID:     ml.itemID(idx),
		Height: height,
		Dirty:  msg.CachedLines == nil,
		Data:   msg,
	}
}

func (ml *MessageList) MdRenderer() *glamour.TermRenderer {
	return ml.makeMdRenderer()
}

func (ml *MessageList) makeMdRenderer() *glamour.TermRenderer {
	w := ml.width
	if ml.mdRenderer != nil && ml.cachedWidth == w {
		return ml.mdRenderer
	}
	r := rendering.MakeMdRenderer(w)
	if r != nil {
		ml.mdRenderer = r
		ml.cachedWidth = w
	}
	return r
}

func (ml *MessageList) RebuildItems() {
	items := make([]virtlist.Item, len(ml.messages))
	for i := range ml.messages {
		items[i] = ml.messageToItem(i)
	}
	ml.vl.SetItems(items)
}

func (ml *MessageList) InvalidateAllMessages() {
	for i := range ml.messages {
		ml.messages[i].CachedLines = nil
	}
	ml.vl.InvalidateHeights()
	ml.vl.InvalidateAll()
}

func (ml *MessageList) HandleScrollbarTick(msg tea.Msg) tea.Cmd {
	updated, cmd := ml.scrollbar.Update(msg)
	ml.scrollbar = updated
	return cmd
}

func (ml *MessageList) InvalidateSubPane(paneID string) {
	for i, msg := range ml.messages {
		if msg.SubPaneID == paneID {
			msg.CachedLines = nil
			ml.vl.InvalidateItem(ml.itemID(i))
			return
		}
	}
}

func (ml *MessageList) HasVisiblePaneScrollbar() bool {
	for _, info := range ml.paneLineMap {
		if info.PaneID != "" {
			return true
		}
	}
	return false
}

func (ml *MessageList) CanScroll() bool {
	ml.vl.BuildIndex()
	return ml.vl.MaxScroll() > 0
}

func (ml *MessageList) ContentHeight() int {
	ml.vl.BuildIndex()
	return ml.vl.TotalHeight()
}
