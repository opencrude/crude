package workspace

import (
	ttypes "crude/types"
	"crude/types/reasoning"
)

// ConversationDelta is a detached publication at Revision. Reset replaces all
// subscriber state; otherwise Upserts and Removed update records by InternalID
// and Appends extend existing text rows. These three sets are disjoint. A non-nil
// Order replaces the ordered membership (including an empty order).
type ConversationDelta struct {
	BaseRevision, Revision uint64
	Reset                  bool
	Order                  []string
	Upserts                []Message
	Removed                []string
	Appends                []ConversationTextAppend
}

// ConversationTextAppend adds Text at an exact byte offset in the canonical
// message. ThinkingDisplay is the latest semantic block index for thinking rows;
// it is zero for assistant rows. Offsets, like reasoning.Display.Start, are bytes.
type ConversationTextAppend struct {
	InternalID      string
	Offset          uint64
	Text            string
	ThinkingDisplay reasoning.Display
}

const conversationJournalLimit = 256

// Only identities and scalar offsets are retained: readers receive current
// suffixes, not historical token bodies. This bounds retained evidence without
// copying dense transcripts or retaining every intermediate streamed string.
type conversationChange struct {
	revision   uint64
	ids        []string
	removed    []string
	structural bool
	textID     string
	textOffset int
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

func (c *Conversation) commitText(message *Message, offset int) {
	c.pendingChange.textID, c.pendingChange.textOffset = message.InternalID, offset
	c.commitChange(nil)
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
	type changedRecord struct {
		id     string
		offset int
		upsert bool
	}
	var records []changedRecord
	add := func(id string, offset int, upsert bool) {
		for i := range records {
			if records[i].id == id {
				records[i].upsert = records[i].upsert || upsert
				return
			}
		}
		records = append(records, changedRecord{id, offset, upsert})
	}
	structural := false
	for r := revision + 1; r <= c.version; r++ {
		change := c.changeJournal[r%conversationJournalLimit]
		structural = structural || change.structural
		for _, id := range change.ids {
			add(id, 0, true)
		}
		for _, id := range change.removed {
			add(id, 0, true)
		}
		if change.textID != "" {
			add(change.textID, change.textOffset, false)
		}
	}
	for _, record := range records {
		message := c.byID[record.id]
		if message == nil {
			delta.Removed = append(delta.Removed, record.id)
		} else if record.upsert {
			delta.Upserts = append(delta.Upserts, ttypes.CloneConversationMessage(*message))
		} else {
			delta.Appends = append(delta.Appends, ConversationTextAppend{
				InternalID: record.id, Offset: uint64(record.offset), Text: message.Content[record.offset:], ThinkingDisplay: message.ThinkingDisplay,
			})
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
