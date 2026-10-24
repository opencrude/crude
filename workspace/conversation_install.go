package workspace

import (
	"fmt"
	"reflect"
	"unicode/utf8"

	"crude/types"
	"crude/types/recovery"
)

// ResetReplicaRevision starts a newly validated core revision domain without
// replacing the shell's Conversation identity. Call only after validating the
// complete replacement snapshot; it does not apply or discard text by itself.
func (c *Conversation) ResetReplicaRevision() {
	c.version = 0
	c.changeJournal = nil
	c.pendingChange = conversationChange{}
}

// InstallDelta updates a client-owned read mirror. It never runs a semantic
// reducer or makes the mirror an execution owner. Validation precedes mutation,
// so a missing cursor/record cannot leave a half-installed transcript.
func (c *Conversation) InstallDelta(delta ConversationDelta) error {
	if err := c.ValidateDelta(delta); err != nil {
		return err
	}
	if !delta.Reset && delta.Revision == c.version {
		return nil
	}
	upserts := make(map[string]Message, len(delta.Upserts))
	for _, message := range delta.Upserts {
		upserts[message.InternalID] = types.CloneConversationMessage(message)
	}
	if delta.Reset {
		c.sourceGeneration++
		c.byID = make(map[string]*Message, len(upserts))
		c.toolTimelines = make(map[*Message]*toolTimeline)
		c.brief = ""
		c.lastRecovery = recovery.Status{}
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
	// A reset/upsert can replace the growing row's content. Never keep its old
	// builder as a second text authority.
	if delta.Reset || len(delta.Upserts) != 0 || len(delta.Removed) != 0 {
		c.textMessage = nil
		c.textBuilder.Reset()
	}
	for _, append := range delta.Appends {
		message := c.byID[append.InternalID]
		c.appendText(message, append.Text)
		message.ThinkingDisplay = append.ThinkingDisplay
	}
	if delta.Order != nil {
		c.messages = make([]*Message, len(delta.Order))
		for i, id := range delta.Order {
			c.messages[i] = c.byID[id]
		}
	}
	c.version = delta.Revision
	c.changeJournal = nil
	c.pendingChange = conversationChange{}
	return nil
}

// ValidateDelta checks the entire transaction without copying message bodies or
// mutating the replica, so a workspace can preflight a batch of conversations.
func (c *Conversation) ValidateDelta(delta ConversationDelta) error {
	if c == nil {
		return fmt.Errorf("conversation mirror is unavailable")
	}
	duplicate := !delta.Reset && delta.Revision == c.version && delta.BaseRevision < c.version
	if delta.Revision < c.version || delta.Revision < delta.BaseRevision && !delta.Reset || !delta.Reset && !duplicate && delta.BaseRevision != c.version {
		return fmt.Errorf("conversation cursor mismatch: have %d, base %d, next %d", c.version, delta.BaseRevision, delta.Revision)
	}
	if delta.Reset && delta.Order == nil {
		return fmt.Errorf("conversation reset requires ordered membership")
	}
	if delta.Reset && (len(delta.Appends) != 0 || len(delta.Removed) != 0) {
		return fmt.Errorf("conversation reset cannot append or remove records")
	}
	if !delta.Reset && !duplicate && delta.Revision == c.version && (delta.Order != nil || len(delta.Upserts)+len(delta.Removed)+len(delta.Appends) != 0) {
		return fmt.Errorf("conversation mutation must advance revision")
	}
	upserts := make(map[string]Message, len(delta.Upserts))
	for _, message := range delta.Upserts {
		if message.InternalID == "" {
			return fmt.Errorf("conversation record has no identity")
		}
		if _, duplicate := upserts[message.InternalID]; duplicate {
			return fmt.Errorf("duplicate conversation record %q", message.InternalID)
		}
		upserts[message.InternalID] = message
		if duplicate {
			current := c.byID[message.InternalID]
			if current == nil || !reflect.DeepEqual(*current, message) {
				return fmt.Errorf("conflicting duplicate conversation upsert")
			}
		}
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
		if duplicate && c.byID[id] != nil {
			return fmt.Errorf("conflicting duplicate conversation removal")
		}
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
		if duplicate {
			if len(order) != len(c.messages) {
				return fmt.Errorf("conflicting duplicate conversation order")
			}
			for i, id := range order {
				if c.messages[i].InternalID != id {
					return fmt.Errorf("conflicting duplicate conversation order")
				}
			}
		}
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
	appended := make(map[string]bool, len(delta.Appends))
	for _, append := range delta.Appends {
		message := c.byID[append.InternalID]
		_, upsert := upserts[append.InternalID]
		if message == nil || message.InternalID != append.InternalID || removed[append.InternalID] || upsert || appended[append.InternalID] || append.Text == "" || !utf8.ValidString(append.Text) {
			return fmt.Errorf("invalid conversation append target %q", append.InternalID)
		}
		appended[append.InternalID] = true
		if message.Role != MessageRoleAssistant && message.Role != MessageRoleThinking {
			return fmt.Errorf("conversation append requires a text row")
		}
		length := uint64(len(message.Content))
		if duplicate {
			if append.Offset > length || uint64(len(append.Text)) != length-append.Offset || message.Content[append.Offset:] != append.Text || message.ThinkingDisplay != append.ThinkingDisplay {
				return fmt.Errorf("conflicting duplicate conversation append")
			}
		} else if append.Offset != length {
			return fmt.Errorf("conversation text offset mismatch: have %d, append %d", length, append.Offset)
		}
		if message.Role == MessageRoleAssistant && append.ThinkingDisplay != (Message{}).ThinkingDisplay || append.ThinkingDisplay.Start < 0 || uint64(append.ThinkingDisplay.Start) > append.Offset+uint64(len(append.Text)) {
			return fmt.Errorf("invalid conversation reasoning index")
		}
		start := append.ThinkingDisplay.Start
		if start < len(message.Content) {
			if !utf8.RuneStart(message.Content[start]) {
				return fmt.Errorf("conversation reasoning index splits a rune")
			}
		} else if !duplicate && start > len(message.Content) && start-len(message.Content) < len(append.Text) && !utf8.RuneStart(append.Text[start-len(message.Content)]) {
			return fmt.Errorf("conversation reasoning index splits a rune")
		}
	}
	return nil
}
