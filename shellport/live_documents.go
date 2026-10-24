package shellport

import (
	"context"

	"crude/coreprotocol"
	"crude/document"
	"crude/workspace"
)

// LiveDocuments is the connected editor's core-owned text authority. All methods
// except DocumentOwner run off Update; editor view state remains shell-local.
type LiveDocuments interface {
	DocumentOwner() document.Owner
	OpenDocument(context.Context, string) (document.Snapshot, error)
	SyncDocument(context.Context, string, uint64) (document.SyncResult, error)
	EditDocument(context.Context, document.Change) (document.Receipt, error)
	UndoDocument(context.Context, string, string, uint64, bool) (document.Receipt, error)
	SaveDocument(context.Context, string, uint64, workspace.DocumentSaveReason) (document.Snapshot, error)
	CloseDocument(context.Context, string) error
}

// LiveDocumentFormatter formats an exact core revision as one author-owned edit
// group. The shell must flush its edits first and never run remote tools locally.
type LiveDocumentFormatter interface {
	FormatDocument(context.Context, string, uint64) (document.Snapshot, error)
}

// LiveDocumentReceipts observes an earlier edit/undo outcome without replay.
// settled is false for pending/uncertain receipts; callers retain their draft.
type LiveDocumentReceipts interface {
	DocumentReceipt(context.Context, string) (receipt document.Receipt, settled bool, err error)
}

// LiveDocumentsChangedMsg advertises committed text or savepoint changes. It is
// not permission to replace an editor's optimistic pending edits.
type LiveDocumentsChangedMsg struct {
	Owner      Workspace
	DocumentID string
	Header     coreprotocol.TopicHeader
	Update     coreprotocol.DocumentUpdate
	Valid      func() bool
	Done       func(error)
}

// LiveDocumentEpoch fences queued publications at a shell lifecycle boundary.
// ResumeDocumentTopics starts fresh subscriptions for still-open views only.
// Both methods are nonblocking and perform no document commands on Update.
type LiveDocumentEpoch interface {
	FenceDocumentTopics()
	ResumeDocumentTopics()
}
