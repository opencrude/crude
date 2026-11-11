package shellport

import (
	"context"
	"crude/textencoding"

	"crude/coreprotocol"
	"crude/document"
	"crude/indentation"
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

// UntitledDocuments creates or reopens a pathless canonical document. It is an
// optional port so other workspace implementations do not invent local files.
type UntitledDocuments interface {
	CreateUntitledDocument(context.Context) (document.Snapshot, error)
	OpenUntitledDocument(context.Context, string) (document.Snapshot, error)
}

// UntitledDocumentSaver saves the exact canonical revision once under the
// retained request identity; the original receipt settles uncertain outcomes.
type UntitledDocumentSaver interface {
	SaveUntitledDocument(context.Context, string, uint64, string, string, textencoding.Encoding, string) (document.Snapshot, error)
}

// UntitledOverwriteReviewer prepares an owner-bound preview; applying and
// inspecting it use the shared ReviewedEdits transaction and receipt.
type UntitledOverwriteReviewer interface {
	PrepareUntitledOverwrite(context.Context, string, uint64, string, string, textencoding.Encoding) (workspace.ReviewedEditPreview, error)
	ReadUntitledOverwrite(context.Context, string, string) (workspace.UntitledOverwriteComparison, error)
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

// EditorConfiguration reads saved project data on the selected core, never the shell host.
type EditorConfiguration interface {
	ReadEditorConfig(context.Context, string) (string, indentation.Choices, error)
}
