package shellport

import (
	"context"

	"crude/history"
)

// PromptHistory is an optional connected service. Calls run in shell commands;
// only the core may record or persist global history. A record returns the
// committed snapshot, including exact core-assigned identities and usage counts.
type PromptHistory interface {
	ListPromptHistory(context.Context, string) ([]history.Entry, error)
	RecordPromptHistory(context.Context, string, string, []history.Attachment) ([]history.Entry, error)
}
