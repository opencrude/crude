package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"crude/agent/prompt"
	"crude/agent/provider"
	"crude/agent/sessiondb"
	"crude/text/anchor"
	"crude/text/unidiff"
	"crude/types/conversation"
	"crude/workspace/fileio"
)

const planReviewKey = "plan_review"
const planHandoffReceiptKey = "plan_handoff_receipt"

type planReviewRecord struct {
	Revisions []planRevisionRecord `json:"revisions"`
	dir       string               // absolute session directory; not persisted
}

type planRevisionRecord struct {
	conversation.PlanRevision        // Path is relative in storage.
	Proposal                  string `json:"proposal"`
	// Approval in a new session closes only this proposal's originating
	// planning cycle, never an independently resumed or forked investigation.
	InvestigationCycle   string `json:"investigation_cycle,omitempty"`
	InvestigationSession string `json:"investigation_session,omitempty"`
	HandoffSource        string `json:"handoff_source,omitempty"`
	HandoffAgent         string `json:"handoff_agent,omitempty"`
	HandoffInput         string `json:"handoff_input,omitempty"`
	HandoffEpoch         uint64 `json:"handoff_epoch,omitempty"`
}

// PlanReviewError is a complete message the shell can show to the user.
type PlanReviewError string

func (e PlanReviewError) Error() string { return string(e) }

const (
	// ErrPlanReviewTooLong is also used when the disk source exceeds its read bound.
	ErrPlanReviewTooLong  = PlanReviewError("This plan is too long. Shorten it and try again.")
	errPlanMissing        = PlanReviewError("This plan revision is not available. Open the latest plan.")
	errPlanClosed         = PlanReviewError("This plan revision is closed. Open the latest plan.")
	errPlanFile           = PlanReviewError("Could not open the plan file. Check the session folder and try again.")
	errPlanHandoffOpen    = PlanReviewError("This plan was sent to a new session. Choose Check to confirm that it arrived.")
	errPlanHandoffCheck   = PlanReviewError("Could not confirm that the new session received the plan. Choose Check to try again.")
	errPlanHandoffTarget  = PlanReviewError("The new session is not available. Open a new session from this plan.")
	errPlanHandoffSend    = PlanReviewError("Could not send the plan to the new session. Try again from this plan.")
	errPlanHandoffMissing = PlanReviewError("The new session did not receive the plan. Choose New session to try again.")
)

func (s *Session) loadPlanReview() error {
	raw, err := sessiondb.LoadRuntimeState(context.Background(), s.db, planReviewKey)
	if err != nil || len(raw) == 0 {
		return err
	}
	var review planReviewRecord
	if err := json.Unmarshal(raw, &review); err != nil {
		return err
	}
	review.dir, err = filepath.Abs(s.SessionDir())
	if err != nil {
		return err
	}
	for _, r := range review.Revisions {
		if r.Number < 1 || r.Path != planRevisionPath(r.Title, r.Number) || len(r.Evidence) > maxEvidenceCommands {
			return errors.New("invalid plan revision")
		}
		for _, ref := range r.Evidence {
			if !validEvidenceDigest(ref.Digest) {
				return errors.New("invalid plan evidence digest")
			}
		}
	}
	s.planReview.Store(&review)
	return nil
}

// PlanReview reads a detached view without taking the file-operation lock.
func (s *Session) PlanReview() (conversation.PlanReview, bool) {
	record := s.planReview.Load()
	if record == nil || len(record.Revisions) == 0 {
		return conversation.PlanReview{}, false
	}
	out := conversation.PlanReview{Revisions: make([]conversation.PlanRevision, len(record.Revisions))}
	for i, stored := range record.Revisions {
		revision := stored.PlanRevision
		revision.Path = record.path(stored)
		if revision.Open() && stored.HandoffSource != s.sessionID {
			revision.NewSessionID = ""
		}
		revision.Comments = slices.Clone(revision.Comments)
		revision.Evidence = slices.Clone(revision.Evidence)
		out.Revisions[i] = revision
	}
	return out, true
}

func (r *planReviewRecord) path(revision planRevisionRecord) string {
	return filepath.Join(r.dir, filepath.FromSlash(revision.Path))
}

func planRevisionPath(title string, number int) string {
	var slug strings.Builder
	const maxSlugBytes = 48
	gap := false
	for i := 0; i < len(title) && slug.Len() < maxSlugBytes; i++ {
		c := title[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			gap = slug.Len() > 0
			continue
		}
		if gap {
			if slug.Len()+1 == maxSlugBytes {
				break
			}
			slug.WriteByte('-')
			gap = false
		}
		slug.WriteByte(c)
	}
	name := slug.String()
	if name == "" {
		name = "plan"
	}
	return fmt.Sprintf("plans/%s-v%d.md", name, number)
}

// recordProposedPlan is called only by a completed Plan mode root turn.
func (s *Session) recordProposedPlan(generation uint64, reply, investigationCycle, sourceID string) error {
	proposal, ok := conversation.ProposedPlan(reply)
	if !ok || len(proposal) > conversation.PlanReviewMaxPlanText {
		return nil
	}
	s.planReviewMu.Lock()
	defer s.planReviewMu.Unlock()
	next := planReviewRecord{}
	number := 1
	if prior := s.planReview.Load(); prior != nil {
		next.dir = prior.dir
		next.Revisions = slices.Clone(prior.Revisions)
		if len(next.Revisions) > 0 {
			latest := next.Revisions[len(next.Revisions)-1]
			if latest.Generation == generation {
				return nil // a retry of the same turn
			}
			number = latest.Number + 1
		}
	} else {
		dir, err := filepath.Abs(s.SessionDir())
		if err != nil {
			return err
		}
		next.dir = dir
	}
	for i := range next.Revisions {
		if next.Revisions[i].Open() {
			next.Revisions[i].Outcome = conversation.PlanOutcomeReplaced
		}
	}
	ctx := context.Background()
	journal, err := s.loadInvestigationEvidenceJournal(ctx)
	if err != nil {
		return err
	}
	evidence, omitted, err := s.proposedPlanEvidence(ctx, journal, generation, investigationCycle, sourceID)
	if err != nil {
		return err
	}
	title := conversation.PlanTitle(proposal)
	revision := planRevisionRecord{
		PlanRevision: conversation.PlanRevision{
			Number: number, Generation: generation, Title: title,
			Path: planRevisionPath(title, number), CreatedAt: time.Now().UTC(), Evidence: evidence, EvidenceOmitted: omitted,
		},
		Proposal: proposal,
	}
	if investigationCycle != "" {
		revision.InvestigationCycle, revision.InvestigationSession = investigationCycle, s.sessionID
	}
	path := next.path(revision)
	created, err := ensurePlanFile(path, proposal, true)
	if err != nil {
		return fmt.Errorf("save plan file: %w", err)
	}
	next.Revisions = append(next.Revisions, revision)
	dropped := next.Revisions[:max(0, len(next.Revisions)-conversation.PlanRevisionMaxCount)]
	next.Revisions = slices.Clone(next.Revisions[len(dropped):])
	var retired []string
	for _, old := range dropped {
		for _, ref := range old.Evidence {
			retired = append(retired, ref.Digest)
		}
	}
	if err := s.savePlanReviewState(ctx, &next, nil, evidenceRetiredKeys(&next, journal, retired)...); err != nil {
		if created {
			_ = os.Remove(path)
		}
		return err
	}
	for _, old := range dropped {
		if err := os.Remove(next.path(old)); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.logger.Warn("SESSION", "plan_file_prune_failed", "path", old.Path, "error", err.Error())
		}
	}
	return nil
}

// ensurePlanFile never replaces edits. A record that did not reach the DB can
// adopt the exact file it staged before a crash, but never unrelated bytes.
func ensurePlanFile(path, proposal string, requireProposal bool) (created bool, err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return false, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("not a plan directory: %s", dir)
	}
	content := proposal + "\n"
	info, err = os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		err = fileio.WriteAtomicNewChecked(path, []byte(content), 0600, nil)
		return err == nil || fileio.WriteCommitted(err), err
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("not a plan file: %s", path)
	}
	if requireProposal {
		data, err := fileio.ReadRegularNoFollow(context.Background(), path, int64(len(content)))
		if err != nil {
			return false, err
		}
		if string(data) != content {
			return false, fmt.Errorf("plan file already has different content: %s", path)
		}
	}
	return false, nil
}

func (s *Session) beginPlanReviewOperation() (func(), error) {
	s.planReviewMu.Lock()
	finish, ok := s.beginIdleWork()
	if !ok {
		s.planReviewMu.Unlock()
		return nil, PlanReviewError("The session is closing. Reopen it to review the plan.")
	}
	return func() {
		finish()
		s.planReviewMu.Unlock()
	}, nil
}

// OpenPlanRevision returns the existing plan file, or recreates it from the
// proposal after a fork or deletion. Call it off the UI thread.
func (s *Session) OpenPlanRevision(number int) (string, error) {
	finish, err := s.beginPlanReviewOperation()
	if err != nil {
		return "", err
	}
	defer finish()
	record, i := s.findPlanRevision(number)
	if i < 0 {
		return "", errPlanMissing
	}
	revision := record.Revisions[i]
	path := record.path(revision)
	if _, err := ensurePlanFile(path, revision.Proposal, false); err != nil {
		s.logger.Warn("SESSION", "plan_file_open_failed", "revision", number, "error", err.Error())
		return "", errPlanFile
	}
	return path, nil
}

func (s *Session) findPlanRevision(number int) (*planReviewRecord, int) {
	if r := s.planReview.Load(); r != nil {
		return r, slices.IndexFunc(r.Revisions, func(v planRevisionRecord) bool { return v.Number == number })
	}
	return nil, -1
}

func (r *planReviewRecord) cloneRevision(i int) *planReviewRecord {
	next := &planReviewRecord{Revisions: slices.Clone(r.Revisions), dir: r.dir}
	next.Revisions[i].Comments = slices.Clone(next.Revisions[i].Comments)
	return next
}

func (s *Session) savePlanReviewState(ctx context.Context, next *planReviewRecord, updates []sessiondb.RuntimeStateUpdate, clear ...string) error {
	if s.db == nil {
		return errors.New("session database is unavailable")
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	updates = append(updates, sessiondb.RuntimeStateUpdate{Key: planReviewKey, Value: raw})
	err = sessiondb.CommitRuntimeState(ctx, s.db, updates, clear...)
	if err == nil {
		s.planReview.Store(next)
	}
	return err
}

// EditPlanComment changes one unsent comment on an open revision.
func (s *Session) EditPlanComment(edit conversation.PlanCommentEdit) error {
	finish, err := s.beginPlanReviewOperation()
	if err != nil {
		return err
	}
	defer finish()
	record, i := s.findPlanRevision(edit.Revision)
	if i < 0 {
		return errPlanMissing
	}
	if !record.Revisions[i].Open() {
		return errPlanClosed
	}
	if record.Revisions[i].HandoffSource == s.sessionID {
		return errPlanHandoffOpen
	}
	next := record.cloneRevision(i)
	revision := &next.Revisions[i]
	comment := edit.Comment
	comment.Body = strings.TrimSpace(comment.Body)
	if edit.Op == conversation.PlanCommentAdd || edit.Op == conversation.PlanCommentUpdate {
		if comment.Body == "" {
			return PlanReviewError("Enter a comment first.")
		}
		if len(comment.Body) > conversation.PlanCommentMaxBody {
			return PlanReviewError("This comment is too long. Shorten it and try again.")
		}
	}
	j := slices.IndexFunc(revision.Comments, func(c conversation.PlanComment) bool { return c.ID == comment.ID })
	switch edit.Op {
	case conversation.PlanCommentAdd:
		if strings.TrimSpace(comment.ID) == "" || len(comment.ID) > 64 {
			return PlanReviewError("Could not add the comment. Add it again.")
		}
		if j >= 0 {
			if revision.Comments[j].Body == comment.Body {
				return nil
			}
			return PlanReviewError("This comment already exists. Edit it instead.")
		}
		if len(comment.Anchor.Exact) > conversation.PlanCommentMaxQuote {
			return PlanReviewError("The selected text is too long. Select less text.")
		}
		// Accept position-only anchors, but do not store unbounded
		// selector context or reversed offsets.
		a := comment.Anchor
		if a.Start < 0 || a.End < a.Start ||
			len(a.Prefix) > conversation.PlanCommentMaxQuote || len(a.Suffix) > conversation.PlanCommentMaxQuote {
			return PlanReviewError("Could not use the selected text. Select it again.")
		}
		if len(revision.Comments) >= conversation.PlanCommentMaxCount {
			return PlanReviewError("This revision has too many comments. Delete an unsent comment first.")
		}
		comment.CreatedAt, comment.Sent = time.Now().UTC(), false
		revision.Comments = append(revision.Comments, comment)
	case conversation.PlanCommentUpdate, conversation.PlanCommentDelete:
		if j < 0 {
			return PlanReviewError("This comment is not available. Open the latest plan.")
		}
		if revision.Comments[j].Sent {
			return PlanReviewError("This comment was sent. Add a new comment instead.")
		}
		if edit.Op == conversation.PlanCommentDelete {
			revision.Comments = slices.Delete(revision.Comments, j, j+1)
		} else {
			revision.Comments[j].Body = comment.Body
		}
	default:
		return PlanReviewError("Could not change the comment. Use Add, Edit, or Delete.")
	}
	return s.commitPlanReviewChange(next)
}

// SettlePlanReview settles a review action that stays in this session.
func (s *Session) SettlePlanReview(settle conversation.PlanReviewSettle) (conversation.PlanReviewSettlement, error) {
	agentID := ""
	if root := s.liveOrchestrator(); root != nil {
		agentID = root.ID
	}
	return s.SettlePlanReviewForAgent(context.Background(), agentID, settle, provider.UserInput{}, nil)
}

// SettlePlanReviewForAgent also captures the initiating shell's control target
// and exact request identity. Text comes from its editor or the canonical file.
func (s *Session) SettlePlanReviewForAgent(ctx context.Context, agentID string, settle conversation.PlanReviewSettle, input provider.UserInput, destination *Session) (conversation.PlanReviewSettlement, error) {
	var out conversation.PlanReviewSettlement
	implement := settle.Action == conversation.PlanReviewImplement
	newSession := settle.Action == conversation.PlanReviewNewSession
	finish, err := s.beginPlanReviewOperation()
	if err != nil {
		return out, err
	}
	defer finish()
	record, i := s.findPlanRevision(settle.Revision)
	if i < 0 {
		return out, errPlanMissing
	}
	if i != len(record.Revisions)-1 || !record.Revisions[i].Open() {
		return out, errPlanClosed
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	prior := record.Revisions[i]
	observe := prior.HandoffSource == s.sessionID
	if observe && (!newSession || prior.NewSessionID != settle.DestinationSessionID) {
		return out, errPlanHandoffOpen
	}
	after := strings.TrimRight(settle.Text, "\n")
	if !observe && len(after) > conversation.PlanReviewMaxPlanText {
		return out, ErrPlanReviewTooLong
	}
	next := record.cloneRevision(i)
	revision := &next.Revisions[i]
	pending := revision.PendingComments()
	before := strings.TrimRight(revision.Proposal, "\n")
	edited := before != after
	if settle.Action == conversation.PlanReviewFeedback && pending == 0 && !edited {
		return out, PlanReviewError("Add a comment or edit the plan first.")
	}
	if (implement || newSession) && pending > 0 {
		return out, PlanReviewError("Send or delete the comments first.")
	}
	if newSession && (destination == nil || destination == s || destination.sessionID != settle.DestinationSessionID) {
		return out, errPlanHandoffTarget
	}
	if newSession {
		finishDestination, ok := destination.beginIdleWork()
		if !ok {
			return out, errPlanHandoffTarget
		}
		defer finishDestination()
	}
	if implement || newSession {
		if !s.planInvestigationToolMu.TryLock() {
			return out, PlanReviewError("An investigation command or edit is still running. Wait for it to finish, then try again.")
		}
		defer s.planInvestigationToolMu.Unlock()
	}
	if observe {
		return out, s.submitFreshPlanReview(ctx, agentID, record, next, i, destination, nil)
	}
	var edits string
	var added, removed int
	if edited && !newSession {
		// Feed complete lines to the diff so a final newline alone never
		// appears as a changed line.
		if after != "" {
			after += "\n"
		}
		edits, added, removed = unidiff.GenerateDiff(before+"\n", after, "plan.md")
	}
	switch settle.Action {
	case conversation.PlanReviewFeedback:
		comments := make([]prompt.PlanFeedbackComment, 0, pending)
		for j, c := range revision.Comments {
			if c.Sent {
				continue
			}
			resolved := anchor.Resolve(settle.Text, c.Anchor)
			first, last := anchor.Lines(settle.Text, resolved.Start, resolved.End)
			comments = append(comments, prompt.PlanFeedbackComment{
				FirstLine: first, LastLine: last, Quote: c.Anchor.Exact, Note: c.Body, Changed: !resolved.Exact,
			})
			revision.Comments[j].Sent = true
		}
		out.Text = prompt.PlanReviewFeedback(revision.Number, settle.Text, edits, comments)
		out.Display = planFeedbackDisplay(revision.Number, comments, edited, added, removed)
	case conversation.PlanReviewImplement:
		out.Text = prompt.PlanImplementationRequest
		if edited {
			out.Text = prompt.PlanImplementationEdited(settle.Text, edits)
			out.Display = fmt.Sprintf("Implement the plan (revision %d, with my edits).", revision.Number)
		}
		revision.Outcome = conversation.PlanOutcomeImplemented
	case conversation.PlanReviewNewSession:
		out.Text = prompt.PlanImplementationFreshContext(settle.Text)
	default:
		return out, PlanReviewError("Choose how to use this plan.")
	}
	if (implement || newSession) && len(revision.Evidence) != 0 {
		if implement {
			out.Text, err = s.implementationEvidence(ctx, next.dir, revision, out.Text)
			if err != nil {
				return conversation.PlanReviewSettlement{}, err
			}
		}
		if out.Display == "" {
			out.Display = fmt.Sprintf("Implement the plan (revision %d).", revision.Number)
		}
	}
	if newSession {
		fresh := provider.UserInput{Text: out.Text, DisplayText: out.Display, Origin: provider.OriginHuman}
		if input.ControlTarget != nil {
			target := input.ControlTarget.Clone()
			target.SessionID, target.ExecutionID = destination.sessionID, ""
			fresh.ControlTarget = &target
		}
		err = s.submitFreshPlanReview(ctx, agentID, record, next, i, destination, &fresh)
		out.Text = fresh.Text
	} else {
		input.Text, input.DisplayText, input.Origin = out.Text, out.Display, provider.OriginHuman
		err = s.submitPlanReview(ctx, agentID, input, next, implement)
	}
	if err != nil {
		return conversation.PlanReviewSettlement{}, err
	}
	return out, nil
}

// New session spans two databases. Bind the exact first input before sending
// it. If the last source write fails, only an observation of that input in
// that destination can finish the handoff; never send it a second time.
func (s *Session) submitFreshPlanReview(ctx context.Context, agentID string, prior, next *planReviewRecord, i int, destination *Session, input *provider.UserInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.queueAdmitMu.Lock()
	defer s.queueAdmitMu.Unlock()
	revision := &next.Revisions[i]
	fromThisCycle := revision.InvestigationSession == s.sessionID && revision.InvestigationCycle != ""
	if agentID != "" {
		const busy = PlanReviewError("The agent has work in progress. Wait for it to finish, then try again.")
		if s.shouldQueueInput(agentID) {
			return busy
		}
		intent, err := s.prepareReservedAdmission(agentID, provider.UserInput{}, 0)
		if err != nil {
			return busy
		}
		defer s.finishInputAdmission(intent)
		defer intent.orchestrator.endTurn(intent.TurnGeneration)
	}
	if err := s.beginSessionControlOperation(); err != nil {
		return PlanReviewError("The session is closing. Reopen it to review the plan.")
	}
	defer s.endSettingsOperation()
	var updates []sessiondb.RuntimeStateUpdate
	if fromThisCycle && s.PlanMode() {
		cycle, err := s.planInvestigationCycle(ctx)
		if err != nil {
			return s.planReviewSaveError(err)
		}
		if cycle == revision.InvestigationCycle {
			updates = append(updates, sessiondb.RuntimeStateUpdate{Key: planModeKey, Value: []byte(newPlanInvestigationCycle())})
		}
	}
	if revision.HandoffSource == s.sessionID {
		raw, err := sessiondb.LoadRuntimeState(ctx, destination.db, planHandoffReceiptKey)
		var receipt InputAdmissionReceipt
		if err != nil {
			return errPlanHandoffCheck
		}
		if len(raw) == 0 {
			// No destination transaction committed, and the original
			// worker no longer owns the source review. A new choice is safe.
			revision.NewSessionID, revision.HandoffSource, revision.HandoffAgent, revision.HandoffInput = "", "", "", ""
			revision.HandoffEpoch = 0
			if err := s.savePlanReviewState(ctx, next, nil); err != nil {
				return errPlanHandoffCheck
			}
			return errPlanHandoffMissing
		}
		if json.Unmarshal(raw, &receipt) != nil || receipt.SessionID != destination.sessionID || receipt.AgentID != revision.HandoffAgent ||
			receipt.InputID != revision.HandoffInput || receipt.OwnerEpoch != revision.HandoffEpoch || receipt.AcceptedAt.IsZero() {
			// A conflicting record is not evidence of non-delivery.
			return errPlanHandoffCheck
		}
	} else {
		owner := destination.liveOrchestrator()
		if owner == nil || !destination.queueAdmitMu.TryLock() {
			return errPlanHandoffTarget
		}
		defer destination.queueAdmitMu.Unlock()
		if destination.PlanMode() || destination.shouldQueueInput(owner.ID) {
			return errPlanHandoffTarget
		}
		if raw, err := sessiondb.LoadRuntimeState(ctx, destination.db, planHandoffReceiptKey); err != nil || len(raw) != 0 {
			return errPlanHandoffTarget
		}
		// Reserve the first turn without binding a worktree or continuing old
		// work. Its durable handoff receipt identifies it as implementation.
		intent, err := destination.prepareReservedAdmission(owner.ID, *input, 0)
		if err != nil {
			return errPlanHandoffTarget
		}
		defer func() {
			intent.orchestrator.endTurn(intent.TurnGeneration)
			destination.finishInputAdmission(intent)
		}()
		if intent.request.sessionTurn != 1 || destination.shouldQueueInput(owner.ID) {
			return errPlanHandoffTarget
		}
		if len(revision.Evidence) != 0 {
			// Read the approved bytes from the source's immutable record, but
			// keep the verified files with the destination and its first input.
			input.Text, err = s.implementationEvidence(ctx, destination.SessionDir(), revision, input.Text)
			if err != nil {
				return err
			}
		}
		input.InternalID = intent.InputID
		revision.NewSessionID, revision.HandoffSource = destination.sessionID, s.sessionID
		revision.HandoffAgent, revision.HandoffInput, revision.HandoffEpoch = intent.AgentID, intent.InputID, intent.OwnerEpoch
		if err := s.savePlanReviewState(ctx, next, nil); err != nil {
			return s.planReviewSaveError(err)
		}
		// Once the intent is bound, this worker owns the durable decision,
		// including if the shell disconnects before the reply.
		ctx = context.WithoutCancel(ctx)
		receipt, err := destination.queueInput(intent.AgentID, *input, 0, func(items []QueuedInput) error {
			if len(items) != 1 || items[0].Input.InternalID != intent.InputID {
				return errors.New("the destination already has an input")
			}
			queued, err := json.Marshal(items)
			if err != nil {
				return err
			}
			proof, err := json.Marshal(InputAdmissionReceipt{SessionID: intent.SessionID, AgentID: intent.AgentID,
				InputID: intent.InputID, OwnerEpoch: intent.OwnerEpoch, AcceptedAt: items[0].QueuedAt})
			if err != nil {
				return err
			}
			return sessiondb.CommitRuntimeState(ctx, destination.db, []sessiondb.RuntimeStateUpdate{
				{Key: queuedInputsStateKey, Value: queued}, {Key: planHandoffReceiptKey, Value: proof},
			})
		})
		if err != nil {
			s.logger.Warn("SESSION", "plan_handoff_admission_failed", "error", err.Error())
			if err := s.savePlanReviewState(ctx, prior, nil); err != nil {
				return errPlanHandoffCheck
			}
			return errPlanHandoffSend
		}
		if receipt.SessionID != intent.SessionID || receipt.AgentID != intent.AgentID ||
			receipt.InputID != intent.InputID || receipt.AcceptedAt.IsZero() {
			return errPlanHandoffCheck
		}
		next = next.cloneRevision(i) // the open handoff record is now published
		revision = &next.Revisions[i]
	}
	revision.Outcome = conversation.PlanOutcomeNewSession
	revision.HandoffSource, revision.HandoffAgent, revision.HandoffInput = "", "", ""
	revision.HandoffEpoch = 0
	if err := s.savePlanReviewState(ctx, next, updates); err != nil {
		s.logger.Warn("SESSION", "plan_handoff_settle_failed", "error", err.Error())
		return errPlanHandoffCheck
	}
	return nil
}

func (s *Session) isPlanHandoffInput(ctx context.Context, inputID string) (bool, error) {
	if inputID == "" {
		return false, nil
	}
	raw, err := sessiondb.LoadRuntimeState(ctx, s.db, planHandoffReceiptKey)
	if err != nil || len(raw) == 0 {
		return false, err
	}
	var receipt InputAdmissionReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return false, fmt.Errorf("read plan handoff receipt: %w", err)
	}
	return receipt.SessionID == s.sessionID && receipt.InputID == inputID &&
		receipt.AgentID != "" && !receipt.AcceptedAt.IsZero(), nil
}

func (s *Session) submitPlanReview(ctx context.Context, agentID string, input provider.UserInput, next *planReviewRecord, implement bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The queue's existing dispatcher must not consume the new message (or
	// publish its admission) until Plan mode and the queued row are visible.
	s.queueAdmitMu.Lock()
	defer s.queueAdmitMu.Unlock()
	intent, err := s.prepareInputAdmission(agentID, input, 0, true)
	if err != nil {
		return s.planReviewAdmissionError(err)
	}
	defer s.finishInputAdmission(intent)
	if err := s.beginSessionControlOperation(); err != nil {
		return PlanReviewError("The session is closing. Reopen it to review the plan.")
	}
	defer s.endSettingsOperation()
	if !implement && !s.PlanMode() {
		return PlanReviewError("Turn on Plan mode to send plan feedback.")
	}
	unlock, err := s.lockInputExecution(intent.request.input)
	if err != nil {
		s.logger.Warn("SESSION", "plan_review_execution_unavailable", "error", err.Error())
		return PlanReviewError("The selected worktree is not available. Select another worktree and try again.")
	}
	defer unlock()
	if err := s.invalidateGoalHandover("New queued human input was accepted."); err != nil {
		return s.planReviewSaveError(err)
	}
	review, err := json.Marshal(next)
	if err != nil {
		return s.planReviewSaveError(err)
	}
	var clear []string
	if implement {
		clear = []string{planModeKey}
	}
	modeChanged := false
	_, err = s.queueInput(intent.AgentID, intent.request.input, intent.DraftRevision, func(items []QueuedInput) error {
		queued, err := json.Marshal(items)
		if err != nil {
			return err
		}
		updates := []sessiondb.RuntimeStateUpdate{{Key: planReviewKey, Value: review}, {Key: queuedInputsStateKey, Value: queued}}
		if err := sessiondb.CommitRuntimeState(ctx, s.db, updates, clear...); err != nil {
			return err
		}
		if implement {
			modeChanged = s.planMode.Swap(false)
		}
		s.planReview.Store(next)
		return nil
	})
	if err != nil {
		return s.planReviewSaveError(err)
	}
	if modeChanged {
		s.recordPlanModeChange(false)
	}
	return nil
}

func (s *Session) planReviewAdmissionError(err error) error {
	var refusal PlanReviewError
	if errors.As(err, &refusal) {
		return refusal
	}
	s.logger.Warn("SESSION", "plan_review_input_rejected", "error", err.Error())
	return PlanReviewError("Could not send the plan review. Make sure the session is open and try again.")
}

func (s *Session) commitPlanReviewChange(next *planReviewRecord) error {
	if err := s.savePlanReviewState(context.Background(), next, nil); err != nil {
		return s.planReviewSaveError(err)
	}
	return nil
}

func (s *Session) planReviewSaveError(err error) error {
	s.logger.Warn("SESSION", "plan_review_save_failed", "error", err.Error())
	return PlanReviewError("Could not save the plan review. Check the session folder and try again.")
}

func planFeedbackDisplay(revision int, comments []prompt.PlanFeedbackComment, edited bool, added, removed int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plan feedback on revision %d", revision)
	for _, comment := range comments {
		if comment.LastLine > comment.FirstLine {
			fmt.Fprintf(&b, "\n- Lines %d-%d:", comment.FirstLine, comment.LastLine)
		} else {
			fmt.Fprintf(&b, "\n- Line %d:", comment.FirstLine)
		}
		quote := strings.Join(strings.Fields(comment.Quote), " ")
		count := 0
		for i := range quote {
			if count == 60 {
				quote = quote[:i] + "…"
				break
			}
			count++
		}
		fmt.Fprintf(&b, " \"%s\" %s", quote, strings.Join(strings.Fields(comment.Note), " "))
	}
	if edited {
		fmt.Fprintf(&b, "\n- I edited the plan: %s added, %s removed.", lineCount(added), lineCount(removed))
	}
	return b.String()
}

// lineCount is "1 line" or "N lines".
func lineCount(n int) string {
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}
