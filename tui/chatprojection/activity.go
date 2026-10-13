package chatprojection

import (
	"fmt"
	"strings"
	"time"

	"crude/tui/rendering"
	"crude/tui/tools"
	"crude/tui/ui"
	"crude/types"
	"crude/types/recovery"
)

// activityGroup is a presentation-only view over adjacent canonical messages.
// Never mutate the messages: their IDs and complete bodies remain the audit log.
type activityGroup struct {
	id             string
	messages       []*types.ChatMessage
	active         bool
	leafRows       map[int]string
	label          string
	visible        []*types.ChatMessage
	paneBackground bool
}

// redundantStopNotice belongs only to the fixed Stopped strip. Keep the
// canonical boundary/incident for history and context, but reserve no transcript
// item, spacer or grouping boundary. Recovery state outranks a generic phase;
// actionable and resumed incidents must retain their evidence and disclosure.
func redundantStopNotice(msg *types.ChatMessage) bool {
	if msg.Role != types.RoleStatus {
		return false
	}
	if msg.Recovery != nil {
		return msg.Recovery.State == recovery.Cancelled
	}
	return msg.LifecyclePhase == "stopped"
}

// Empty assistant envelopes separate provider calls but contain no visible
// communication. They must neither consume a row nor split adjacent activity.
// Keep the live streaming placeholder until it is replaced by real content.
func (r *Renderer) activityInvisible(msg *types.ChatMessage) bool {
	if redundantStopNotice(msg) {
		return true
	}
	if msg.ToolName == "multi_tool_use.parallel" && msg.ToolStatus == types.ToolStatusStreaming && msg.Content == "" {
		return true // the empty argument-streaming envelope belongs to the fixed strip
	}
	if msg.Role == types.RoleThinking && msg.ThinkingActive && msg.ThinkingDisplay.Kind.HasCompactPreview() && r.disclosureState(msg) == DisclosureCollapsed {
		return true // the live compact preview already lives in the fixed strip
	}
	if msg.Role == types.RoleStatus && msg.LifecycleOperationID == "" {
		switch msg.LifecyclePhase {
		case "preparing", "waiting_provider", "retrying", "cancelling":
			return true // transient lifecycle belongs to the fixed activity strip
		}
	}
	return msg.Role == types.RoleAssistant && msg.Content == "" && msg.SubPaneID == "" && !msg.Incomplete && !r.isStreamingCursor(msg)
}

func activityEligible(msg *types.ChatMessage) bool {
	if msg == nil || msg.SubPaneID != "" {
		return false
	}
	if msg.Role == types.RoleThinking {
		return !msg.ThinkingActive && !msg.ThinkingInterrupted && msg.Content != ""
	}
	if msg.Role != types.RoleTool {
		return false
	}
	if msg.PermTag != "" && msg.PermTag != "auto" {
		return false // explicit permission decisions stay independently visible
	}
	if msg.ToolStatus != types.ToolStatusSuccess && msg.ToolStatus != types.ToolStatusStreaming && msg.ToolStatus != types.ToolStatusCalling {
		return false
	}
	// Preview phases may lag a successful result while their minimum dwell
	// elapses. The canonical tool outcome, not that animation, owns grouping.
	// Tool errors are boundaries, even if the transport reported success but
	// returned an explicit failure in its payload.
	if msg.ToolStatus == types.ToolStatusSuccess {
		if _, exit, session, ok := tools.CommandResult(msg.ToolName, msg.Content); ok && ((exit != nil && *exit != 0) || (exit == nil && session > 0)) {
			return false
		}
		content := strings.TrimSpace(msg.Content)
		if strings.HasPrefix(content, "Error:") || strings.HasPrefix(content, "Command failed") {
			return false
		}
	}
	// A successful transport may still report a failed/killed background job;
	// unknown result shapes stay visible rather than asserting success.
	if msg.ToolStatus == types.ToolStatusSuccess {
		switch msg.ToolName {
		case "check_async_command", "read_async_output", "agent_wait":
			if !activitySuccessfulResult(msg) {
				return false
			}
		}
	}
	switch msg.ToolName {
	case "exec_command", "write_stdin", "execute_command", "async_execute_command", "check_async_command", "read_async_output",
		"read_file", "list_directory", "glob_files", "grep_files", "web_search", "web_fetch",
		"arxiv_search", "recall_memory", "search_history", "wait",
		"agent_list", "agent_inspect", "agent_wait", "board_read", "artifact_read":
		return true
	}
	return false
}

func activitySuccessfulResult(msg *types.ChatMessage) bool {
	if msg.ToolName == "read_async_output" {
		return true // output is evidence, not an assertion that the job succeeded
	}
	if msg.ToolName == "check_async_command" {
		return strings.Contains(msg.Content, "\nStatus: completed\n") && strings.Contains(msg.Content, "\nExit code: 0\n")
	}
	if msg.ToolName == "agent_wait" {
		known, success := tools.CollaborationResultSucceeded(msg.ToolName, msg.Content)
		return known && success
	}
	return false
}

func activityRunning(msg *types.ChatMessage) bool {
	return msg.Role == types.RoleTool && (msg.ToolStatus == types.ToolStatusStreaming || msg.ToolStatus == types.ToolStatusCalling)
}

func newActivityGroup(messages []*types.ChatMessage) *activityGroup {
	group := &activityGroup{id: "activity:" + messages[0].InternalID, messages: messages, paneBackground: true}
	for _, msg := range messages {
		if !messageHasPaneBackground(msg) {
			group.paneBackground = false
		}
		if activityRunning(msg) {
			group.active = true
		}
	}
	return group
}

func activityLabel(name string, count int) string {
	plural := func(singular, multiple string) string {
		if count == 1 {
			return "1 " + singular
		}
		return fmt.Sprintf("%d %s", count, multiple)
	}
	switch name {
	case "exec_command", "execute_command":
		return "Ran " + plural("command", "commands")
	case "async_execute_command":
		return "Started " + plural("background command", "background commands")
	case "read_file":
		return "Read " + plural("file", "files")
	case "thinking":
		return "Thought " + plural("time", "times")
	case "grep_files":
		return "Searched files " + plural("time", "times")
	case "web_search":
		return "Searched web " + plural("time", "times")
	case "list_directory":
		return "Listed " + plural("directory", "directories")
	case "glob_files":
		return "Found files " + plural("time", "times")
	case "web_fetch":
		return "Fetched " + plural("page", "pages")
	case "arxiv_search":
		return "Searched papers " + plural("time", "times")
	case "recall_memory":
		return "Recalled memory " + plural("time", "times")
	case "search_history":
		return "Searched history " + plural("time", "times")
	case "wait":
		if count == 1 {
			return "Waited"
		}
		return "Waited " + plural("time", "times")
	case "agent_list":
		return "Listed workspace agents " + plural("time", "times")
	case "agent_inspect":
		return "Inspected agents " + plural("time", "times")
	case "agent_wait":
		return "Observed agent work " + plural("time", "times")
	case "board_read":
		return "Read workspace communication " + plural("time", "times")
	case "artifact_read":
		return "Read reports " + plural("time", "times")
	case "check_async_command":
		return "Checked background job " + plural("time", "times")
	case "read_async_output":
		return "Read background output " + plural("time", "times")
	case "write_stdin":
		return "Checked command output " + plural("time", "times")
	default:
		return fmt.Sprintf("%s ×%d", rendering.PascalCaseTool(name), count)
	}
}

func (g *activityGroup) title() string {
	order := make([]string, 0)
	counts := make(map[string]int)
	var thoughtDuration time.Duration
	preparing, running := 0, 0
	for _, msg := range g.messages {
		if msg.Role == types.RoleTool && msg.ToolStatus != types.ToolStatusSuccess {
			if msg.ToolPreviewPhase == types.ToolPreviewPreparing {
				preparing++
			} else {
				running++
			}
			continue
		}
		name := msg.ToolName
		if msg.Role == types.RoleThinking {
			name = "thinking"
			thoughtDuration += msg.ThinkingDuration
		}
		if counts[name] == 0 {
			order = append(order, name)
		}
		counts[name]++
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		label := activityLabel(name, counts[name])
		if name == "thinking" && thoughtDuration > 0 {
			label = "Thought for " + rendering.FormatThinkingDuration(thoughtDuration)
		}
		parts = append(parts, label)
	}
	if preparing > 0 {
		parts = append(parts, fmt.Sprintf("%d preparing", preparing))
	}
	if running > 0 {
		parts = append(parts, fmt.Sprintf("%d running", running))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%d tools in progress", len(g.messages))
	}
	return strings.Join(parts, " · ")
}

// syncActivityItems is the sole construction path for visible transcript items.
// The canonical messages are retained independently of grouping and expansion.
func (r *Renderer) syncActivityItems(changedIDs []string) {
	changed := make(map[string]bool, len(changedIDs))
	for _, id := range changedIDs {
		changed[id] = true
	}
	previous := make(map[string]renderItem, r.vl.ItemCount())
	for _, item := range r.vl.Items() {
		previous[item.ID] = item
	}
	messages := make([]*types.ChatMessage, 0, len(r.sync.Messages))
	parallelLeaves := make(map[string]bool)
	r.activityMessages = make(map[string]*types.ChatMessage)
	if r.activityBatches == nil {
		r.activityBatches = make(map[string][]*types.ChatMessage)
	}
	for _, msg := range r.sync.Messages {
		if msg.ToolName == "multi_tool_use.parallel" {
			batch, ok := r.activityBatches[msg.InternalID]
			if !ok || changed[msg.InternalID] {
				batch = parallelMessages(msg)
				r.activityBatches[msg.InternalID] = batch
			}
			if len(batch) > 0 {
				for _, child := range batch {
					messages = append(messages, child)
					parallelLeaves[child.InternalID] = true
					r.activityMessages[child.InternalID] = child
					if changed[msg.InternalID] {
						changed[child.InternalID] = true
					}
				}
				continue
			}
		}
		messages = append(messages, msg)
		r.activityMessages[msg.InternalID] = msg
	}
	for id := range r.activityBatches {
		if r.sync.MsgByID[id] == nil {
			delete(r.activityBatches, id)
		}
	}
	// Retain whole request rounds with both a round floor and a useful leaf
	// floor. Singleton requests must not reduce a long investigation to two
	// leaves; thinking and pending notes must not consume or reset that budget.
	recentRounds := make(map[string]bool)
	recentTools := 0
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == types.RoleUser && !msg.PendingSteering {
			break
		}
		if msg.Role != types.RoleTool || !activityEligible(msg) {
			continue
		}
		id := activityRoundID(msg)
		if !recentRounds[id] && len(recentRounds) >= 2 && recentTools >= 6 {
			break
		}
		recentRounds[id] = true
		recentTools++
	}
	items := make([]renderItem, 0, len(messages))
	for i := 0; i < len(messages); {
		msg := messages[i]
		if r.activityInvisible(msg) {
			i++
			continue
		}
		if !activityEligible(msg) {
			items = append(items, r.activityMessageItem(msg, previous, changed))
			i++
			continue
		}
		end := i + 1
		members := []*types.ChatMessage{msg}
		for end < len(messages) && (activityEligible(messages[end]) || r.activityInvisible(messages[end])) {
			if activityEligible(messages[end]) {
				members = append(members, messages[end])
			}
			end++
		}
		// A batch leaf starts in its tree, even before its siblings arrive.
		// Independent singleton calls retain their ordinary informative row.
		if len(members) == 1 && !parallelLeaves[msg.InternalID] {
			items = append(items, r.activityMessageItem(msg, previous, changed))
			i = end
			continue
		}
		group := newActivityGroup(members)
		group.label = group.title()
		for _, member := range members {
			if r.expanded[group.id] || r.expanded[member.InternalID] || activityRunning(member) || member.Role == types.RoleTool && recentRounds[activityRoundID(member)] {
				group.visible = append(group.visible, member)
			}
		}
		if hidden := len(members) - len(group.visible); hidden > 0 {
			group.label += fmt.Sprintf(" · %d hidden", hidden)
		}
		item, ok := previous[group.id]
		if !ok {
			item = renderItem{ID: group.id, Height: 2, Dirty: true}
		}
		if old, ok := item.Data.(*activityGroup); ok {
			group.leafRows = old.leafRows
			if old.active != group.active || len(old.messages) != len(group.messages) || len(old.visible) != len(group.visible) {
				item.Dirty = true
			} else {
				for j, member := range group.messages {
					if old.messages[j].InternalID != member.InternalID {
						item.Dirty = true
						break
					}
				}
				for j, member := range group.visible {
					if old.visible[j].InternalID != member.InternalID {
						item.Dirty = true
						break
					}
				}
			}
		}
		item.Data = group
		if item.Rendered == nil || item.Height < 1 {
			item.Dirty = true
		}
		for _, member := range group.messages {
			if changed[member.InternalID] {
				item.Dirty = true
			}
		}
		items = append(items, item)
		i = end
	}
	// Dormant group toggles cannot survive a split/removal and accidentally
	// expand a later, unrelated group that happens to start with the same ID.
	present := make(map[string]bool)
	for _, item := range items {
		if _, ok := item.Data.(*activityGroup); ok {
			present[item.ID] = true
		}
	}
	for id := range r.expanded {
		if strings.HasPrefix(id, "activity:") && !present[id] {
			delete(r.expanded, id)
		}
	}
	for id := range r.frameSensitive {
		if r.activityMessages[id] == nil && !present[id] {
			delete(r.frameSensitive, id)
		}
	}
	structure := len(items) != r.vl.ItemCount()
	if !structure {
		for i := range items {
			if old := r.vl.Item(i); old == nil || old.ID != items[i].ID {
				structure = true
				break
			}
		}
	}
	if structure {
		// Width-only lazy reflow estimates offscreen heights. A new pane must
		// have its actual height before scroll-to-bottom or pane hit testing.
		r.lazyWidthReflow = false
		r.vl.SetItems(items)
		return
	}
	for i := range items {
		if items[i].Dirty {
			r.vl.UpdateItemAt(i, items[i])
		}
	}
}

func activityRoundID(msg *types.ChatMessage) string {
	if msg.ToolRoundID != "" {
		return msg.ToolRoundID
	}
	if msg.ToolID != "" {
		return msg.ToolID
	}
	return msg.InternalID
}

func (r *Renderer) activityMessageItem(msg *types.ChatMessage, previous map[string]renderItem, changed map[string]bool) renderItem {
	if item, ok := previous[msg.InternalID]; ok {
		item.Data = msg
		if changed[msg.InternalID] || msg.CachedLines == nil {
			item.Dirty = true
		}
		return item
	}
	return renderItem{ID: msg.InternalID, Data: msg, Height: 2, Dirty: true}
}

func activityCommand(msg *types.ChatMessage) bool {
	return (msg.ToolName == "exec_command" || msg.ToolName == "execute_command" || msg.ToolName == "async_execute_command") && msg.ToolCommand != ""
}

func (r *Renderer) renderActivity(group *activityGroup) ([]string, []PaintLine) {
	_, innerW := r.availableMessageWidths()
	label := group.label
	marker := "● "
	if group.active {
		marker = ui.ActivityDot(r.spinFrame) + " "
	}
	chevron := " ▸"
	if r.expanded[group.id] {
		chevron = " ▾"
	}
	lines := []string{""}
	native := []PaintLine{buildPlainBodyLine("")}
	for i, text := range rendering.WordWrap(label+chevron, max(1, innerW-4)) {
		prefix := []RowSpan{{Text: "│   ", Role: ui.ColorMuted}}
		if i == 0 {
			prefix = []RowSpan{{Text: "│ ", Role: ui.ColorMuted}, {Text: marker, Role: ui.ColorAccent2}}
		}
		pl := PaintLine{Spans: append(prefix, RowSpan{Text: text, Role: ui.ColorMuted}), PaneBackground: group.paneBackground}
		lines = append(lines, pl.PlainText())
		native = append(native, pl)
	}
	group.leafRows = make(map[int]string)
	// Membership is source/disclosure-derived, never rescanned on animation ticks.
	visible := group.visible
	for i, msg := range visible {
		ctx := r.buildRenderContext(msg, false)
		ctx.innerW = max(1, innerW-4)
		ctx.contentWidth = max(1, ctx.innerW-4)
		ctx.disclosure = r.disclosureState(msg)
		ml := renderMessageContent(*msg, ctx, true)
		for j, row := range ml.native {
			branch := "│  │  "
			if i == len(visible)-1 {
				branch = "│     "
			}
			if j == 0 {
				branch = "│  ├─ "
				if i == len(visible)-1 {
					branch = "│  └─ "
				}
			}
			spans := append([]RowSpan(nil), row.Spans...)
			if len(spans) > 0 && strings.HasPrefix(spans[0].Text, "│ ") {
				spans[0].Text = branch + strings.TrimPrefix(spans[0].Text, "│ ")
			} else {
				spans = append([]RowSpan{{Text: branch, Role: ui.ColorMuted}}, spans...)
			}
			pl := row
			pl.Spans = spans
			if r.hasDisclosure(msg) {
				group.leafRows[len(lines)] = msg.InternalID
			}
			lines = append(lines, pl.PlainText())
			native = append(native, pl)
		}
	}
	return lines, native
}
