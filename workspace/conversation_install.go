package workspace

import (
	"fmt"

	"crude/types"
)

// InstallDelta updates a client-owned read mirror. It never runs a semantic
// reducer or makes the mirror an execution owner. Validation precedes mutation,
// so a missing cursor/record cannot leave a half-installed transcript.
func (c *Conversation) InstallDelta(delta ConversationDelta) error {
	if c == nil {
		return fmt.Errorf("conversation mirror is unavailable")
	}
	if !delta.Reset && (delta.BaseRevision != c.version || delta.Revision < delta.BaseRevision) {
		return fmt.Errorf("conversation cursor mismatch: have %d, base %d, next %d", c.version, delta.BaseRevision, delta.Revision)
	}
	if delta.Reset && delta.Order == nil {
		return fmt.Errorf("conversation reset requires ordered membership")
	}
	upserts := make(map[string]Message, len(delta.Upserts))
	for _, message := range delta.Upserts {
		if message.InternalID == "" {
			return fmt.Errorf("conversation record has no identity")
		}
		if _, duplicate := upserts[message.InternalID]; duplicate {
			return fmt.Errorf("duplicate conversation record %q", message.InternalID)
		}
		upserts[message.InternalID] = types.CloneConversationMessage(message)
	}
	removed := make(map[string]bool, len(delta.Removed))
	for _, id := range delta.Removed {
		if id == "" || removed[id] {
			return fmt.Errorf("invalid removed conversation identity")
		}
		if _, conflict := upserts[id]; conflict {
			return fmt.Errorf("conversation record is both removed and updated")
		}
		removed[id] = true
	}
	order := delta.Order
	if order == nil {
		if len(removed) != 0 {
			return fmt.Errorf("conversation removal requires ordered membership")
		}
		for id := range upserts {
			if c.byID[id] == nil {
				return fmt.Errorf("new conversation record requires ordered membership")
			}
		}
	} else {
		seen := make(map[string]bool, len(order))
		for _, id := range order {
			if id == "" || seen[id] || removed[id] {
				return fmt.Errorf("invalid conversation order")
			}
			seen[id] = true
			_, changed := upserts[id]
			if !changed && (delta.Reset || c.byID[id] == nil) {
				return fmt.Errorf("conversation order references unavailable record %q", id)
			}
		}
		for id := range upserts {
			if !seen[id] {
				return fmt.Errorf("conversation update is absent from order")
			}
		}
		if !delta.Reset {
			for _, message := range c.messages {
				if !seen[message.InternalID] && !removed[message.InternalID] {
					return fmt.Errorf("conversation order silently removed a record")
				}
			}
		}
	}
	if delta.Reset {
		c.byID = make(map[string]*Message, len(upserts))
		c.toolTimelines = make(map[*Message]*toolTimeline)
		c.brief = ""
	}
	for _, id := range delta.Removed {
		delete(c.byID, id)
	}
	for id, value := range upserts {
		if current := c.byID[id]; current != nil {
			*current = value
		} else {
			copy := value
			c.byID[id] = &copy
		}
		if value.Recovery != nil && (c.lastRecovery.ID == "" || !value.Recovery.UpdatedAt.Before(c.lastRecovery.UpdatedAt)) {
			c.lastRecovery = *value.Recovery
		}
	}
	if order != nil {
		c.messages = make([]*Message, len(order))
		for i, id := range order {
			c.messages[i] = c.byID[id]
		}
	}
	c.version = delta.Revision
	c.changeJournal = nil
	c.pendingChange = conversationChange{}
	return nil
}
