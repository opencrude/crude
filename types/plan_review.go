package types

import (
	"strings"
	"time"
	"unicode/utf8"
)

// A plan review is the user's review of the plans a session's main agent
// proposes in Plan mode. Each proposal is one revision: a Markdown file in the
// session's own directory that the user edits in the editor and comments on.
// The session owns the record; every shell paints the same detached view.

// PlanOutcome is how the user settled a revision. An open revision still
// takes comments and can be implemented.
type PlanOutcome string

const (
	PlanOutcomeOpen PlanOutcome = ""
	// PlanOutcomeImplemented: the session implements the revision itself.
	PlanOutcomeImplemented PlanOutcome = "implemented"
	// PlanOutcomeNewSession: a new session implements the revision.
	PlanOutcomeNewSession PlanOutcome = "new_session"
	// PlanOutcomeReplaced: a later proposal replaced the revision.
	PlanOutcomeReplaced PlanOutcome = "replaced"
)

// PlanReview is the detached view of a session's proposed plans.
type PlanReview struct {
	Revisions []PlanRevision `json:"revisions,omitempty"`
}

// Latest returns the newest revision.
func (r PlanReview) Latest() (PlanRevision, bool) {
	if len(r.Revisions) == 0 {
		return PlanRevision{}, false
	}
	return r.Revisions[len(r.Revisions)-1], true
}

// Revision returns the revision with this number.
func (r PlanReview) Revision(number int) (PlanRevision, bool) {
	for _, revision := range r.Revisions {
		if revision.Number == number {
			return revision, true
		}
	}
	return PlanRevision{}, false
}

// RevisionAtPath returns the revision whose plan file is path.
func (r PlanReview) RevisionAtPath(path string) (PlanRevision, bool) {
	for _, revision := range r.Revisions {
		if path != "" && revision.Path == path {
			return revision, true
		}
	}
	return PlanRevision{}, false
}

// PlanRevision is one proposed plan.
type PlanRevision struct {
	// Number counts the session's proposals from 1.
	Number int `json:"number"`
	// Generation is the turn generation whose final reply proposed it.
	Generation uint64 `json:"generation"`
	Title      string `json:"title"`
	// Path is the absolute plan file in the owning session's directory. Open
	// it with OpenPlanRevision, which recreates a missing file.
	Path      string        `json:"path"`
	CreatedAt time.Time     `json:"created_at"`
	Outcome   PlanOutcome   `json:"outcome,omitempty"`
	Comments  []PlanComment `json:"comments,omitempty"`
}

// Open reports whether the revision can still take comments and be settled.
func (r PlanRevision) Open() bool { return r.Outcome == PlanOutcomeOpen }

// PendingComments counts the comments not yet sent to the agent.
func (r PlanRevision) PendingComments() int {
	count := 0
	for _, comment := range r.Comments {
		if !comment.Sent {
			count++
		}
	}
	return count
}

// PlanComment is one comment on a range of a plan file.
type PlanComment struct {
	// ID is created by the shell that adds the comment, so a retried add
	// cannot create a second comment.
	ID        string     `json:"id"`
	Anchor    TextAnchor `json:"anchor"`
	Body      string     `json:"body"`
	CreatedAt time.Time  `json:"created_at"`
	// Sent comments went to the agent with feedback and are read-only.
	Sent bool `json:"sent,omitempty"`
}

// TextAnchor locates a range of text the way the W3C Web Annotation model
// does: a TextQuoteSelector (Exact, with Prefix and Suffix context) and a
// TextPositionSelector (Start, End byte offsets) used as a hint. Package
// crude/text/anchor captures and resolves it; nothing else interprets it.
type TextAnchor struct {
	Start  int    `json:"start"`
	End    int    `json:"end"`
	Exact  string `json:"exact"`
	Prefix string `json:"prefix,omitempty"`
	Suffix string `json:"suffix,omitempty"`
}

// PlanCommentOp is a change to the comments of an open revision.
type PlanCommentOp string

const (
	PlanCommentAdd    PlanCommentOp = "add"
	PlanCommentUpdate PlanCommentOp = "update"
	PlanCommentDelete PlanCommentOp = "delete"
)

// PlanCommentEdit changes one comment. Add needs ID, Anchor and Body; update
// needs ID and Body; delete needs ID. Sent comments cannot change.
type PlanCommentEdit struct {
	Revision int           `json:"revision"`
	Op       PlanCommentOp `json:"op"`
	Comment  PlanComment   `json:"comment"`
}

// Plan comment bounds keep the record small enough to publish with the agent.
const (
	PlanCommentMaxBody      = 4 << 10
	PlanCommentMaxQuote     = 2 << 10
	PlanCommentMaxCount     = 100
	PlanRevisionMaxCount    = 50
	PlanReviewMaxPlanText   = 256 << 10
	PlanReviewMaxTitleBytes = 200
)

// PlanReviewAction is how the user settles an open revision.
type PlanReviewAction string

const (
	// PlanReviewFeedback sends the pending comments and edits; the agent
	// revises the plan in Plan mode.
	PlanReviewFeedback PlanReviewAction = "feedback"
	// PlanReviewImplement turns Plan mode off and implements the revision
	// in the same session.
	PlanReviewImplement PlanReviewAction = "implement"
	// PlanReviewNewSession implements the revision in a new session that
	// starts from the plan alone.
	PlanReviewNewSession PlanReviewAction = "new_session"
)

// PlanReviewSettle settles the latest open revision. Text is the plan exactly
// as the user sees it when choosing the action.
type PlanReviewSettle struct {
	Revision int              `json:"revision"`
	Action   PlanReviewAction `json:"action"`
	Text     string           `json:"text"`
}

// PlanReviewSettlement is the message of a settled action: Text for the model
// and Display for the transcript. For feedback and implement the core queues
// it with the settled record; for a new session the shell sends it as that
// session's first message.
type PlanReviewSettlement struct {
	Text    string `json:"text"`
	Display string `json:"display,omitempty"`
}

// ProposedPlan returns the Markdown of the last proposed_plan block in a
// Plan mode reply, whose tags stand alone on their own lines.
func ProposedPlan(reply string) (string, bool) {
	var plan, current []string
	open, found := false, false
	for _, line := range strings.Split(reply, "\n") {
		switch strings.TrimSpace(line) {
		case "<proposed_plan>":
			open, current = true, nil
			continue
		case "</proposed_plan>":
			if open {
				plan, found, open = current, true, false
			}
			continue
		}
		if open {
			current = append(current, line)
		}
	}
	text := strings.TrimSpace(strings.Join(plan, "\n"))
	return text, found && text != ""
}

// PlanTitle is the plan's first Markdown heading, or its first line.
func PlanTitle(plan string) string {
	first := ""
	for _, line := range strings.Split(plan, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if heading := strings.TrimLeft(line, "#"); heading != line && strings.HasPrefix(heading, " ") {
			first = strings.TrimSpace(heading)
			break
		}
		if first == "" {
			first = line
		}
	}
	if len(first) > PlanReviewMaxTitleBytes {
		cut := PlanReviewMaxTitleBytes
		for cut > 0 && !utf8.RuneStart(first[cut]) {
			cut--
		}
		first = first[:cut]
	}
	if first == "" {
		return "Plan"
	}
	return first
}
