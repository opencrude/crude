package chatprojection

import (
	"strings"

	"crude/tui/types"
)

// disclosure.go owns the per-message disclosure (expand/collapse) state for
// chat blocks. Disclosure is TUI-owned presentation state: it never persists
// and never reaches workspace, and hydration always starts from the same
// deterministic defaults so live and resumed sessions render the same shape.

// DisclosureState is the resolved presentation mode for one message.
type DisclosureState uint8

const (
	// DisclosureCollapsed is the compact form: a budgeted preview body for
	// tools, a single summary row for completed thinking and subpanes.
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
//   - completed tools keep a budgeted preview body (full only on toggle).
func (r *Renderer) defaultDisclosure(msg *types.ChatMessage) DisclosureState {
	switch {
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
		if msg.ToolName == "write_file" || msg.ToolName == "edit_file" || msg.ToolName == "apply_patch" {
			return DisclosureExpanded
		}
		return DisclosureCollapsed
	}
	return DisclosureExpanded
}

// ToggleDisclosure flips a message's disclosure state and invalidates its
// cached render so the next frame re-renders with the new geometry.
func (r *Renderer) ToggleDisclosure(msgID string) {
	if index, found := r.vl.byID[msgID]; found {
		if _, ok := r.vl.Item(index).Data.(*activityGroup); ok {
			if r.expanded[msgID] {
				delete(r.expanded, msgID)
			} else {
				r.expanded[msgID] = true
			}
			r.syncActivityItems(nil)
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
	// A command leaf in the retained tree has no standalone render item.
	// Its disclosure still needs to refresh the parent tree's geometry.
	for _, item := range r.vl.Items() {
		if group, ok := item.Data.(*activityGroup); ok {
			for _, member := range group.messages {
				if member.InternalID == msgID {
					r.syncActivityItems([]string{msgID})
					r.vl.InvalidateItem(group.id)
					return
				}
			}
		}
	}
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
