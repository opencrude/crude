package shellport

import (
	"context"

	"crude/types/insight"
)

// Suggestions is the connected flow for Errand's editor suggestions and the
// recent-edit history. The core owns text, context, the model call and the
// ledger; a shell sends identity and the caret and paints the answer.
type Suggestions interface {
	RequestEditorSuggestion(context.Context, insight.EditorPredictionRequest) (insight.EditorPredictionResult, error)
	ReleaseEditorSuggestion(context.Context, string, uint64) error
	EditorSuggestionStatus(context.Context) (insight.EditorPredictionStatus, error)
	RecentEdits(context.Context, string, int) ([]insight.RecentEdit, error)
}
