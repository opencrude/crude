package types

// EditorPredictionRequest carries private invocation identity, never source text
// or authority. The core obtains bytes and facts from the canonical owners.
type EditorPredictionRequest struct {
	Source          InsightSource
	ViewID          string
	Sequence        uint64
	Caret           int // Canonical UTF-8 byte offset.
	Automatic       bool
	LocalExactMatch bool // May suppress a request, never authorize one.
}

// EditorPredictionResult is a literal insertion, not a patch or Apply permit.
// The shell must still match its exact editor, source, caret and input sequence.
type EditorPredictionResult struct {
	Source           InsightSource
	ViewID           string
	Sequence         uint64
	PolicyGeneration uint64
	Caret            int
	Text             string
	ContextDigest    string
	CacheHit         bool
	MissingInputs    []string
}

// EditorPredictionStatus is a constant-size, pushed workspace projection. No
// reader or painter queries the accounting database. Tokens are not a price.
type EditorPredictionStatus struct {
	Generation        uint64
	PolicyGeneration  uint64
	Mode              EditorPredictionMode
	Ready             bool
	Reason            string
	LimitTokens       int64
	ActiveTokens      int64
	ChargedTokens     int64
	ObservedTokens    int64
	RemainingTokens   int64
	UnknownRecords    int64
	InFlight          int
	MeasuredLatencyMS int64
	PauseMS           int64 // Zero means no connection measurement exists yet.
}
