package remote

import (
	"context"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
)

var _ shellport.Suggestions = (*Workspace)(nil)

func (w *Workspace) RequestEditorSuggestion(ctx context.Context, request types.EditorPredictionRequest) (types.EditorPredictionResult, error) {
	var out coreprotocol.EditorSuggestReply
	err := w.callCancellable(ctx, coreprotocol.EditorSuggestMethod, &coreprotocol.EditorSuggestRequest{Request: request}, &out)
	return out.Result, err
}

func (w *Workspace) ReleaseEditorSuggestion(ctx context.Context, view string, sequence uint64) error {
	var out coreprotocol.EditorSuggestReply
	return w.callCancellable(ctx, coreprotocol.EditorSuggestReleaseMethod, &coreprotocol.EditorSuggestReleaseRequest{ViewID: view, Sequence: sequence}, &out)
}

func (w *Workspace) EditorSuggestionStatus(ctx context.Context) (types.EditorPredictionStatus, error) {
	var out coreprotocol.EditorSuggestReply
	err := w.callCancellable(ctx, coreprotocol.EditorSuggestStatusMethod, &coreprotocol.EditorSuggestStatusRequest{Refresh: true}, &out)
	return out.Status, err
}

func (w *Workspace) RecentEdits(ctx context.Context, path string, limit int) ([]types.RecentEdit, error) {
	var out coreprotocol.EditorSuggestReply
	err := w.callCancellable(ctx, coreprotocol.EditorRecentEditsMethod, &coreprotocol.EditorRecentEditsRequest{Path: path, Limit: limit}, &out)
	return out.Edits, err
}
