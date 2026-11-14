package chatprojection

import (
	"strings"

	"crude/tui/tools"
	"crude/types"
)

// disclosure.go owns the per-message disclosure (expand/collapse) state for
// chat blocks. Disclosure is TUI-owned presentation state: it never persists
// and never reaches workspace, and hydration always starts from the same
// deterministic defaults so live and resumed sessions render the same shape.

// DisclosureState is the resolved presentation mode for one message.
type DisclosureState uint8

const (
	// DisclosureCollapsed shows a title and bounded reason for tools, or one
	// summary row for completed thinking and subpanes. Evidence stays closed.
	DisclosureCollapsed DisclosureState = iota
	// DisclosureExpanded shows the full retained body.
	DisclosureExpanded
)

// disclosureState resolves the current disclosure mode for a message.
// Explicit user toggles win; otherwise a deterministic default keeps live and
// hydrated sessions rendering the same shape.
func (r *Renderer) disclosureState(msg *types.ChatMessage) DisclosureState {
	if msg == nil {
		return DisclosureCollapsed
	}
	if expanded, set := r.expanded[msg.InternalID]; set {
		if expanded {
			return DisclosureExpanded
		}
		return DisclosureCollapsed
	}
	return r.defaultDisclosure(msg)
}

// defaultDisclosure derives the untoggled mode from message semantics:
//   - active/interrupted thinking always shows its stream;
//   - completed thinking collapses to a single "Thought" row;
//   - completed subpane placeholders collapse to the pane summary;
//   - file changes and the current plan show their full body;
//   - other settled tools and diagnostics show only their title until expanded.
func (r *Renderer) defaultDisclosure(msg *types.ChatMessage) DisclosureState {
	switch {
	case isCommandOutcome(msg):
		return DisclosureCollapsed
	case msg.Recovery != nil:
		return DisclosureCollapsed
	case msg.Role == types.RoleError || settledCompaction(*msg):
		return DisclosureCollapsed
	case msg.Role == types.RoleThinking:
		if r.thinkingBodyOnly {
			return DisclosureExpanded // the enclosing pane owns disclosure
		}
		if msg.ThinkingDisplay.Kind.HasCompactPreview() {
			return DisclosureCollapsed
		}
		if msg.ThinkingActive || msg.ThinkingInterrupted {
			return DisclosureExpanded
		}
		return DisclosureCollapsed
	case msg.SubPaneID != "":
		return DisclosureCollapsed
	case msg.Role == types.RoleTool:
		if tools.IsPatchCardTool(msg.ToolName) || tools.IsPlanTool(msg.ToolName) && !r.planMessageSuperseded(msg) {
			return DisclosureExpanded
		}
		return DisclosureCollapsed
	}
	return DisclosureExpanded
}

// A new plan snapshot changes the default view of earlier snapshots even when
// their own bytes did not change. Only plan-source changes invalidate these
// rows; ordinary tool streams and presentation ticks retain their caches.
func (r *Renderer) invalidateRelatedPlanRows(messages []*types.ChatMessage, from int, changed map[string]bool) int {
	plans := make(map[string]bool)
	for _, msg := range messages[from:] {
		if msg.Role == types.RoleTool && tools.IsPlanTool(msg.ToolName) {
			if view, ok := parsePlanView(msg.Content); ok && view.ID != "" {
				plans[view.ID] = true
			}
		}
	}
	if len(plans) == 0 {
		return from
	}
	first := from
	for i, msg := range messages {
		if msg.Role != types.RoleTool || !tools.IsPlanTool(msg.ToolName) {
			continue
		}
		if view, ok := parsePlanView(msg.Content); ok && plans[view.ID] {
			r.invalidateMessageCache(msg)
			changed[msg.InternalID] = true
			first = min(first, i)
		}
	}
	return first
}

// ToggleDisclosure flips a message's disclosure state and invalidates its
// cached render so the next frame re-renders with the new geometry.
func (r *Renderer) ToggleDisclosure(msgID string) {
	if index, found := r.vl.byID[msgID]; found {
		if group, ok := r.vl.Item(index).Data.(*activityGroup); ok {
			if r.expanded[msgID] {
				delete(r.expanded, msgID)
			} else {
				r.expanded[msgID] = true
			}
			r.updateActivityVisibility(group)
			r.vl.InvalidateItem(msgID)
			return
		}
	}
	msg := r.sync.MsgByID[msgID]
	if msg == nil {
		msg = r.activityMessages[msgID]
	}
	if msg == nil {
		return
	}
	r.expanded[msgID] = r.disclosureState(msg) != DisclosureExpanded
	msg.CachedLines = nil
	r.InvalidateItem(msgID)
	if msg.Role == types.RoleThinking && msg.ThinkingActive && msg.ThinkingDisplay.Kind.HasCompactPreview() {
		r.syncActivityItems([]string{msgID}) // the hidden item enters/leaves the list
		return
	}
	if parent := r.activityParents[msgID]; parent != "" {
		if index, ok := r.vl.byID[parent]; ok {
			if group, ok := r.vl.Item(index).Data.(*activityGroup); ok {
				r.updateActivityVisibility(group)
				r.vl.InvalidateItem(parent)
			}
		}
	}
}

// RevealCommand expands the originating call of one command execution and
// returns that call's message ID. Expanding a tree leaf also shows it when its
// activity row hides older calls. It never collapses an already open call.
func (r *Renderer) RevealCommand(executionID string) (string, bool) {
	origin, ok := r.commandOrigins[executionID]
	if !ok || executionID == "" || origin.message == nil {
		return "", false
	}
	id := origin.message.InternalID
	msg := r.sync.MsgByID[id]
	if msg == nil {
		msg = r.activityMessages[id]
	}
	if msg == nil {
		return "", false
	}
	if r.disclosureState(msg) != DisclosureExpanded || !r.expanded[id] {
		// An explicit expansion also keeps a hidden tree leaf visible.
		r.expanded[id] = false
		r.ToggleDisclosure(id)
	}
	r.revealMessage = id
	return id, true
}

// applyCommandReveal runs after final width is known, before any rows paint.
// Materialize only the target item, then reuse the authoritative scroll model.
// Later background reflow preserves this exact item/local-line anchor.
func (r *Renderer) applyCommandReveal(width int) {
	id := r.revealMessage
	if id == "" {
		return
	}
	r.revealMessage = ""
	itemID := id
	if parent := r.activityParents[id]; parent != "" {
		itemID = parent
	}
	index, ok := r.vl.byID[itemID]
	if !ok {
		return
	}
	item := r.vl.Item(index)
	if item.Dirty || item.Rendered == nil {
		item.Rendered = r.renderItem(index, item, width)
		item.Height, item.Dirty = len(item.Rendered), false
		r.vl.indexDirty = true
		r.vl.revision++
	}
	local := 0
	if group, ok := item.Data.(*activityGroup); ok {
		local = -1
		for line, leaf := range group.leafRows {
			if leaf == id && (local < 0 || line < local) {
				local = line
			}
		}
		if local < 0 {
			return
		}
	}
	r.vl.BuildIndex()
	line := r.vl.LineOfItem(index) + local
	if scroll := r.authoritativeScroll(); scroll != nil {
		scroll.SetContent(r.vl.TotalHeight())
		scroll.SetAutoScroll(false)
		scroll.ScrollTo(max(0, line-2))
	}
	r.vl.ScrollTo(max(0, line-2))
	r.syncScrollFromVL()
}

// pruneExpanded drops disclosure entries for messages that are no longer in the
// projection, so the expanded map cannot grow without bound across syncs that
// remove messages (e.g. undo or compaction). It is called after every message
// set rebuild.
func (r *Renderer) pruneExpanded() {
	for id := range r.expanded {
		if r.sync.MsgByID[id] != nil {
			continue
		}
		if r.activityMessages[id] != nil {
			continue
		}
		if strings.HasPrefix(id, "activity:") {
			if r.activityMessages[strings.TrimPrefix(id, "activity:")] != nil {
				continue
			}
		}
		delete(r.expanded, id)
	}
}

// ToggleLastDisclosure toggles the most recent message that owns a disclosure
// affordance (the keyboard shortcut path).
func (r *Renderer) ToggleLastDisclosure() {
	for i := len(r.sync.Messages) - 1; i >= 0; i-- {
		msg := r.sync.Messages[i]
		if msg.Role == types.RoleThinking && msg.ThinkingActive && msg.ThinkingDisplay.Kind.HasCompactPreview() {
			r.ToggleDisclosure(msg.InternalID)
			return
		}
		if msg.Role != types.RoleStatus && (msg.Role != types.RoleAssistant || msg.Content != "") {
			break
		}
	}
	for i := r.vl.ItemCount() - 1; i >= 0; i-- {
		item := r.vl.Item(i)
		if _, ok := item.Data.(*activityGroup); ok {
			r.ToggleDisclosure(item.ID)
			return
		}
		if msg, ok := item.Data.(*types.ChatMessage); ok && r.hasDisclosure(msg) {
			r.ToggleDisclosure(msg.InternalID)
			return
		}
	}
}

// hasDisclosure reports whether the message renders a toggle affordance.
func (r *Renderer) hasDisclosure(msg *types.ChatMessage) bool {
	if msg == nil {
		return false
	}
	switch {
	case isCommandOutcome(msg):
		return true
	case msg.Recovery != nil:
		return true
	case msg.Role == types.RoleError:
		return msg.Content != ""
	case settledCompaction(*msg):
		return msg.Content != "" || msg.LifecycleBeforeTokens > 0
	case msg.Role == types.RoleThinking:
		return msg.Content != "" && (msg.ThinkingDisplay.Kind.HasCompactPreview() || !msg.ThinkingActive && !msg.ThinkingInterrupted)
	case msg.SubPaneID != "":
		return true
	case msg.Role == types.RoleTool && activityCommand(msg):
		return true
	case msg.Role == types.RoleTool && msg.ToolName == "multi_tool_use.parallel":
		return msg.ToolInput != "" || msg.Content != ""
	case msg.Role == types.RoleTool && (msg.Content != "" || msg.ToolInput != ""):
		return toolSupportsDisclosure(msg.ToolName)
	}
	return false
}

// DisclosureExpanded reports whether a message is currently expanded by an
// explicit user toggle. It is presentation state, used by tests and any
// caller that needs to observe the resolved disclosure mode.
func (r *Renderer) DisclosureExpanded(msgID string) bool {
	return r.expanded[msgID]
}
