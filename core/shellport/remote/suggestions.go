package remote

import (
	"context"

	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/types/insight"
)

var _ shellport.Suggestions = (*Workspace)(nil)

// RequestEditorSuggestion asks for an editor suggestion at the cursor.
func (w *Workspace) RequestEditorSuggestion(ctx context.Context, request insight.EditorPredictionRequest) (insight.EditorPredictionResult, error) {
	var out coreprotocol.EditorSuggestReply
	err := w.callCancellable(ctx, coreprotocol.EditorSuggestMethod, &coreprotocol.EditorSuggestRequest{Request: request}, &out)
	return out.Result, err
}

func (w *Workspace) ReleaseEditorSuggestion(ctx context.Context, view string, sequence uint64) error {
	var out coreprotocol.EditorSuggestReply
	return w.callCancellable(ctx, coreprotocol.EditorSuggestReleaseMethod, &coreprotocol.EditorSuggestReleaseRequest{ViewID: view, Sequence: sequence}, &out)
}

// EditorSuggestionStatus returns the readiness of editor suggestions.
func (w *Workspace) EditorSuggestionStatus(ctx context.Context) (insight.EditorPredictionStatus, error) {
	var out coreprotocol.EditorSuggestReply
	err := w.callCancellable(ctx, coreprotocol.EditorSuggestStatusMethod, &coreprotocol.EditorSuggestStatusRequest{Refresh: true}, &out)
	return out.Status, err
}

// RecentEdits returns the recent edits of a file.
func (w *Workspace) RecentEdits(ctx context.Context, path string, limit int) ([]insight.RecentEdit, error) {
	var out coreprotocol.EditorSuggestReply
	err := w.callCancellable(ctx, coreprotocol.EditorRecentEditsMethod, &coreprotocol.EditorRecentEditsRequest{Path: path, Limit: limit}, &out)
	return out.Edits, err
}
