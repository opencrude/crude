package types

// EditorPredictionRequest carries invocation identity, never source text or
// authority. The core reads text, facts and edit history from their owners.
// Completions lists the labels of a visible completion list at the caret,
// the chosen row first; it steers the model and is never a source of truth.
// Follow asks only for the next change of the last answer after the user
// accepted one; it never sends a model request.
type EditorPredictionRequest struct {
	Source      InsightSource
	ViewID      string
	Sequence    uint64
	Caret       int // Canonical UTF-8 byte offset.
	Automatic   bool
	Follow      bool
	Completions []string
}

// EditorPredictionKind names how the shell shows a suggestion.
type EditorPredictionKind string

const (
	// PredictionInsert adds text at the caret (one or more lines).
	PredictionInsert EditorPredictionKind = "insert"
	// PredictionEdit replaces text near the caret.
	PredictionEdit EditorPredictionKind = "edit"
	// PredictionJump points to the next place to edit in this file. Start, End
	// and Text describe the change there; the first Tab moves the caret.
	PredictionJump EditorPredictionKind = "jump"
)

// EditorPredictionResult is a minimal edit of the exact source revision it
// names, not a patch permit. The shell must still match its editor, source,
// revision and sequence before it shows or applies the change.
type EditorPredictionResult struct {
	Source           InsightSource
	ViewID           string
	Sequence         uint64
	PolicyGeneration uint64
	Caret            int
	Kind             EditorPredictionKind
	Start, End       int // Canonical byte range that Text replaces.
	Text             string
	ContextDigest    string
	CacheHit         bool
	// Sources names the context sources the request carried (see
	// PredictionSource*), so outcomes show whether a source helps.
	Sources []string
	Stages  EditorPredictionStages
	Status  EditorPredictionStatus
}

// Context sources that a suggestion request can carry beyond the code at the
// caret.
const (
	PredictionSourceEdits       = "recent edits"
	PredictionSourceDiagnostics = "problems"
	PredictionSourceCompletions = "completion list"
	PredictionSourceSnippets    = "related code"
	PredictionSourceChain       = "follow-up"
)

// Empty reports a result that has nothing to show.
func (r EditorPredictionResult) Empty() bool { return r.Kind == "" }

// EditorPredictionStages are the measured parts of one request, in
// microseconds. Zero means the stage did not run (for example a cache hit).
type EditorPredictionStages struct {
	ContextUS   int64
	AccountUS   int64
	FirstByteUS int64
	ModelUS     int64
	CheckUS     int64
	TotalUS     int64
}

// EditorPredictionStatus is a constant-size projection. No reader or painter
// queries the accounting database. Tokens are not a price.
type EditorPredictionStatus struct {
	Generation        uint64
	PolicyGeneration  uint64
	Mode              EditorPredictionMode
	Ready             bool
	Reason            string
	Model             string
	LimitTokens       int64
	ActiveTokens      int64
	ChargedTokens     int64
	ObservedTokens    int64
	RemainingTokens   int64
	UnknownRecords    int64
	InFlight          int
	MeasuredLatencyMS int64
	Requests          int64
	Shown             int64
}

// RecentEdit is one change in a live document, newest last in a list. Author
// is RecentEditYou for the requesting editor, RecentEditDisk for a change that
// came from the file on disk (agents write files), or RecentEditOther.
type RecentEdit struct {
	Path     string
	Line     int // 1-based first line of the change in the text after it.
	Removed  string
	Inserted string
	Author   string
	Unix     int64
}

const (
	RecentEditYou   = "you"
	RecentEditDisk  = "disk"
	RecentEditOther = "other"
)
