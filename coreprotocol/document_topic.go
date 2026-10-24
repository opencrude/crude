package coreprotocol

import "crude/document"

const TopicDocument = "document"

// DocumentUpdate is fenced by canonical text revision. Snapshot is present only
// at a reset. Ordinary updates contain operations and metadata, never full text.
// Savepoint distinguishes an unchanged savepoint from an empty saved document.
type DocumentUpdate struct {
	Base      uint64
	State     document.Snapshot
	Changes   []document.Change
	Snapshot  *document.Snapshot
	Savepoint bool
	Fault     *Fault
}
