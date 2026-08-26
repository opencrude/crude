package chatprojection

import "crude/tui/types"

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
	if r.expanded[msg.InternalID] {
		return DisclosureExpanded
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
		if msg.ThinkingActive || msg.ThinkingInterrupted {
			return DisclosureExpanded
		}
		return DisclosureCollapsed
	case msg.SubPaneID != "":
		return DisclosureCollapsed
	case msg.Role == types.RoleTool:
		return DisclosureCollapsed
	}
	return DisclosureExpanded
}

// ToggleDisclosure flips a message's disclosure state and invalidates its
// cached render so the next frame re-renders with the new geometry.
func (r *Renderer) ToggleDisclosure(msgID string) {
	if r.sync.MsgByID[msgID] == nil {
		return
	}
	if r.expanded[msgID] {
		delete(r.expanded, msgID)
	} else {
		r.expanded[msgID] = true
	}
	r.InvalidateItem(msgID)
}

// ToggleLastDisclosure toggles the most recent message that owns a disclosure
// affordance (the keyboard shortcut path).
func (r *Renderer) ToggleLastDisclosure() {
	for i := len(r.sync.Messages) - 1; i >= 0; i-- {
		msg := r.sync.Messages[i]
		if r.hasDisclosure(msg) {
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
		return true
	case msg.SubPaneID != "":
		return true
	case msg.Role == types.RoleTool && msg.ToolStatus == "success" && msg.Content != "":
		return true
	}
	return false
}
