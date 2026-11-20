package shellport

import (
	"context"

	"crude/text/encoding"
	"crude/workspace"
	"crude/workspace/document"
)

// LiveDocumentCommands preserves save/format request identities across lost
// responses. A request sent again with its identity returns the outcome the
// core recorded; the core never executes one identity twice.
type LiveDocumentCommands interface {
	SaveDocumentRequest(context.Context, string, uint64, workspace.DocumentSaveReason, string) (document.Snapshot, error)
	FormatDocumentRequest(context.Context, string, uint64, string) (document.Snapshot, error)
}

// DocumentEncodingSaver preserves the same request/receipt contract as Save.
type DocumentEncodingSaver interface {
	SaveDocumentEncodingRequest(context.Context, string, uint64, textencoding.Encoding, string) (document.Snapshot, error)
	// ReloadDocumentRequest loads the saved file. A resolution keeps or replaces
	// the text instead and makes the current file the save baseline.
	ReloadDocumentRequest(context.Context, string, uint64, string, *document.Resolution) (document.Snapshot, error)
}
