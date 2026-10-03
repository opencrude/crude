// Package memory implements crude's durable workspace memory system:
// FTS5/BM25 scored recall with recency, importance and path signals,
// evidence-backed knowledge revisions, scoped episodes, history search,
// exposure and feedback ledgers, and an append-only audit journal.
//
// It is a neutral domain library (like sessiondb): it owns schema,
// storage, and retrieval only. Callers (session, workspace) own LLM
// access, logging, lifecycle policy, and rendering. Records are never
// hard-deleted: "forget" marks a record superseded so the full history
// stays auditable in place.
package memory

// Memory kinds describe the durable assertion, independently of episodes.
const (
	KindFact       = "fact"
	KindDecision   = "decision"
	KindConvention = "convention"
	KindPreference = "preference"
	KindLesson     = "lesson"
	KindReference  = "reference"
)

// Record sources: who created the memory.
const (
	SourceTool          = "tool"          // agent tool call
	SourceUser          = "user"          // explicit user command (/remember)
	SourceAuto          = "auto"          // background extraction
	SourceConsolidation = "consolidation" // background consolidator rewrite
)

// Record statuses.
const (
	// StatusActive records are eligible for recall and auto-injection.
	StatusActive = "active"
	// StatusSuperseded records were replaced or forgotten. They are kept
	// for audit/provenance and excluded from all recall paths.
	StatusSuperseded = "superseded"
	// StatusArchived records decayed out of the auto-injection set (long
	// unused). They still match explicit recall_memory searches.
	StatusArchived = "archived"
)

// Journal actors.
const (
	ActorTool          = "tool"
	ActorUser          = "user"
	ActorAuto          = "auto"
	ActorConsolidation = "consolidation"
	ActorSystem        = "system"
)

// DefaultImportance is assigned when a writer does not score a record.
const DefaultImportance = 40

// Record is one durable workspace memory.
type Record struct {
	ID              string
	Revision        int64
	Kind            string
	Title           string
	Content         string
	Entities        string   // space-joined mechanical entities (paths, symbols, commands)
	ScopePaths      []string // path/glob scopes that boost recall when active
	AppliesWhen     string
	Evidence        []Evidence
	Actor           string
	Importance      int // 0-100
	Status          string
	SupersededBy    string
	Source          string
	Provenance      string // e.g. "session:<id> turns:3-6"
	SourceSessionID string
	Pinned          bool
	UseCount        int
	LastUsedAt      int64
	CreatedAt       int64
	UpdatedAt       int64
}

// ScoredRecord is a Record with retrieval scores attached.
type ScoredRecord struct {
	Record
	Score     float64
	Relevance float64
}

// RecallQuery describes one retrieval request.
type RecallQuery struct {
	// Text is the free-text query (user prompt or tool query).
	Text string
	// ActivePaths are IDE-context paths (active editor file, open tabs).
	ActivePaths []string
	// TouchedPaths are files touched by tools earlier in the session.
	TouchedPaths []string
	// Limit caps the number of returned records (default 12).
	Limit int
	// BudgetTokens caps the estimated token cost of the returned set.
	// Zero means no budget cap.
	BudgetTokens int
	// IncludeArchived widens the search to decayed records (explicit
	// recall_memory searches set this; auto-injection does not).
	IncludeArchived bool
}

// ListQuery describes a browse/list request (memory browser UI).
type ListQuery struct {
	Text   string // optional FTS filter
	Kind   string // optional kind filter
	Status string // optional status filter; empty means active+archived
	Limit  int
}

// TurnRecord is one compact cross-session history row (one per turn).
type TurnRecord struct {
	SessionID     string
	Turn          int
	UserText      string
	AssistantText string
	ToolNames     []string
	Files         []string
	CreatedAt     int64
	Observations  []Observation
}

// HistoryHit is one history search result.
type HistoryHit struct {
	SessionID     string
	Turn          int
	UserText      string
	AssistantText string
	ToolNames     string
	Files         string
	CreatedAt     int64
}

// SessionSummary is the durable summary of one session.
type SessionSummary struct {
	SessionID string
	Title     string
	Summary   string
	KeyFiles  []string
	Outcome   string
	TurnCount int
	CreatedAt int64
	UpdatedAt int64
}

// JournalEntry is one append-only audit record of a memory operation.
type JournalEntry struct {
	ID        int64
	At        int64
	Actor     string
	Op        string
	MemoryID  string
	SessionID string
	Detail    string
}

const (
	OutcomeSucceeded      = "succeeded"
	OutcomeFailed         = "failed"
	OutcomeUnmeasured     = "unmeasured"
	OutcomeCancelled      = "cancelled"
	EvidenceUser          = "user_statement"
	EvidenceTool          = "tool_result"
	EvidenceChange        = "verified_change"
	EvidenceValidation    = "validation"
	EvidenceAssistant     = "assistant_claim"
	FeedbackUseful        = "useful"
	FeedbackMisleading    = "misleading"
	FeedbackContradicted  = "contradicted"
	FeedbackNotApplicable = "not_applicable"
)

type Evidence struct {
	SessionID  string `json:"session_id"`
	MessageID  string `json:"message_id,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Kind       string `json:"kind"`
	Summary    string `json:"summary"`
	Outcome    string `json:"outcome"`
}

type Observation struct {
	Evidence
	Turn       int      `json:"turn"`
	Method     string   `json:"method,omitempty"`
	Conditions string   `json:"conditions,omitempty"`
	Paths      []string `json:"paths,omitempty"`
}

type Episode struct {
	ID         string
	SessionID  string
	Turn       int
	Situation  string
	Method     string
	Outcome    string
	Conditions string
	Evidence   []Evidence
	CreatedAt  int64
}

type Revision struct {
	Record
	SessionID string
	Op        string
}

type RevisionRef struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

type Exposure struct {
	RevisionRef
	SessionID string
	Kind      string
	At        int64
}

type Feedback struct {
	RevisionRef
	Kind      string     `json:"kind"`
	Evidence  []Evidence `json:"evidence"`
	SessionID string     `json:"session_id,omitempty"`
	Actor     string     `json:"actor,omitempty"`
	At        int64      `json:"at,omitempty"`
}
