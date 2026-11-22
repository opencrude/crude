package shellport

import (
	"context"

	"crude/agent/prompthistory"
	"crude/agent/sessiondb"
)

// PromptHistory is an optional connected service. Calls run in shell commands;
// only the core may record or persist global history. A record returns the
// committed snapshot, including exact core-assigned identities and usage counts.
type PromptHistory interface {
	ListPromptHistory(context.Context, string) ([]prompthistory.Entry, error)
	RecordPromptHistory(context.Context, string, string, []prompthistory.Attachment) ([]prompthistory.Entry, error)
}

// PromptHistoryAttachments imports one exact immutable history item on the core,
// never by resolving its old reference in the destination session or using a
// server path locally. The shell installs the result on Update.
type PromptHistoryAttachments interface {
	ImportPromptHistoryAttachment(context.Context, string, string, int) (sessiondb.Reference, error)
}
