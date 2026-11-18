package coreprotocol

import "crude/types"

const (
	EditorSuggestMethod        = "editor.suggest"
	EditorSuggestReleaseMethod = "editor.suggest_release"
	EditorSuggestStatusMethod  = "editor.suggest_status"
	EditorRecentEditsMethod    = "editor.recent_edits"
)

// EditorSuggestRequest asks for one suggestion. The shell sends identity and
// the caret, never source text.
type EditorSuggestRequest struct {
	Request types.EditorPredictionRequest
}

type EditorSuggestReleaseRequest struct {
	ViewID   string
	Sequence uint64
}

// EditorSuggestStatusRequest reads the status; Refresh also reads the meter.
type EditorSuggestStatusRequest struct {
	Refresh bool
}

type EditorRecentEditsRequest struct {
	Path  string
	Limit int
}

type EditorSuggestReply struct {
	Result types.EditorPredictionResult
	Status types.EditorPredictionStatus
	Edits  []types.RecentEdit
}
