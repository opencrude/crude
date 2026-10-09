package workspace

import ttypes "crude/types"

// ConversationDelta is a detached publication at Revision. Reset replaces all
// subscriber state; otherwise Upserts and Removed update records by InternalID.
// A non-nil Order replaces the ordered membership (including an empty order).
type ConversationDelta struct {
	BaseRevision, Revision uint64
	Reset                  bool
	Order                  []string
	Upserts                []Message
	Removed                []string
}

const conversationJournalLimit = 256

// Only identities are retained: readers receive current values, not historical
// token bodies. This bounds retained evidence without copying dense transcripts
// or retaining every intermediate streamed string.
type conversationChange struct {
	revision   uint64
	ids        []string
	removed    []string
	structural bool
}

func (c *Conversation) changed(message *Message) {
	c.pendingChange.ids = append(c.pendingChange.ids, message.InternalID)
}

func (c *Conversation) commitChange(message *Message) {
	if message != nil {
		c.changed(message)
	}
	c.version++
	if c.changeJournal == nil {
		c.changeJournal = make([]conversationChange, conversationJournalLimit)
	}
	c.pendingChange.revision = c.version
	c.changeJournal[c.version%conversationJournalLimit] = c.pendingChange
	c.pendingChange = conversationChange{}
}

// ChangesSince reads independently of other subscribers. Like the existing
// Conversation API, callers must serialize reads with live mutations. Future
// revisions and uninstrumented revision gaps fail closed to a complete reset.
func (c *Conversation) ChangesSince(revision uint64) ConversationDelta {
	delta := ConversationDelta{BaseRevision: revision}
	if c == nil {
		delta.Reset = true
		delta.Order = []string{}
		return delta
	}
	delta.Revision = c.version
	reset := revision == 0 || revision > c.version || c.version-revision > conversationJournalLimit
	if !reset {
		for r := revision + 1; r <= c.version; r++ {
			if c.changeJournal == nil || c.changeJournal[r%conversationJournalLimit].revision != r {
				reset = true
				break
			}
		}
	}
	if reset {
		delta.Reset = true
		delta.Order = c.messageOrder()
		delta.Upserts = c.Messages()
		return delta
	}
	if revision == c.version {
		return delta
	}
	seen := make(map[string]bool)
	structural := false
	for r := revision + 1; r <= c.version; r++ {
		change := c.changeJournal[r%conversationJournalLimit]
		structural = structural || change.structural
		for _, id := range change.ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			if message := c.byID[id]; message != nil {
				delta.Upserts = append(delta.Upserts, ttypes.CloneConversationMessage(*message))
			} else {
				delta.Removed = append(delta.Removed, id)
			}
		}
		for _, id := range change.removed {
			if seen[id] {
				continue
			}
			seen[id] = true
			if message := c.byID[id]; message != nil {
				delta.Upserts = append(delta.Upserts, ttypes.CloneConversationMessage(*message))
			} else {
				delta.Removed = append(delta.Removed, id)
			}
		}
	}
	if structural {
		delta.Order = c.messageOrder()
	}
	return delta
}

func (c *Conversation) messageOrder() []string {
	order := make([]string, len(c.messages))
	for i, message := range c.messages {
		order[i] = message.InternalID
	}
	return order
}
