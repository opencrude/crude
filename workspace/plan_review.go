package workspace

import (
	"context"
	"errors"

	"crude/agent/provider"
	"crude/agent/session"
	"crude/text/textencoding"
	"crude/types/conversation"
	"crude/workspace/fileio"
)

const errPlanReviewOwner = session.PlanReviewError("This agent has no plan review. Select the main agent.")

// PlanReviewForAgent reads the main agent's detached review without doing I/O.
func (w *Workspace) PlanReviewForAgent(agentID string) (conversation.PlanReview, bool) {
	sess := w.SessionsByAgent[agentID]
	if sess == nil || sess.backend == nil {
		return conversation.PlanReview{}, false
	}
	return sess.backend.PlanReview()
}

// PrepareOpenPlanRevision resolves the owner before any file work.
func (w *Workspace) PrepareOpenPlanRevision(agentID string, revision int) (func() (string, error), error) {
	sess := w.SessionsByAgent[agentID]
	if sess == nil || sess.backend == nil {
		return nil, errPlanReviewOwner
	}
	backend := sess.backend
	return func() (string, error) { return backend.OpenPlanRevision(revision) }, nil
}

// PrepareReadPlanEvidence fixes the session before the worker reads its verified record.
func (w *Workspace) PrepareReadPlanEvidence(agentID string, revision int, digest string) (func() (string, error), error) {
	sess := w.SessionsByAgent[agentID]
	if sess == nil || sess.backend == nil {
		return nil, errPlanReviewOwner
	}
	backend := sess.backend
	return func() (string, error) { return backend.ReadPlanEvidence(revision, digest) }, nil
}

// PrepareEditPlanComment resolves the owner before persistence.
func (w *Workspace) PrepareEditPlanComment(agentID string, edit conversation.PlanCommentEdit) (func() error, error) {
	sess := w.SessionsByAgent[agentID]
	if sess == nil || sess.backend == nil {
		return nil, errPlanReviewOwner
	}
	backend := sess.backend
	return func() error { return backend.EditPlanComment(edit) }, nil
}

// PrepareSettlePlanReview captures the owner and document service. The worker
// resolves missing text from the live document, or its disk file if not open.
// The session rechecks the revision at commit, even if text was supplied.
func (w *Workspace) PrepareSettlePlanReview(agentID string, settle conversation.PlanReviewSettle, sender provider.UserInput) (func(context.Context) (conversation.PlanReviewSettlement, error), error) {
	sess := w.SessionsByAgent[agentID]
	if sess == nil || sess.backend == nil {
		return nil, errPlanReviewOwner
	}
	backend, docs := sess.backend, w.LiveDocuments
	var destination *session.Session
	if settle.Action == conversation.PlanReviewNewSession {
		destination = w.BackendSession(settle.DestinationSessionID)
	}
	return func(ctx context.Context) (conversation.PlanReviewSettlement, error) {
		if err := ctx.Err(); err != nil {
			return conversation.PlanReviewSettlement{}, err
		}
		input := settle
		if input.Text == "" {
			review, _ := backend.PlanReview()
			latest, _ := review.Latest()
			if latest.Number == input.Revision && latest.Path != "" && latest.Open() && latest.NewSessionID == "" {
				var open bool
				if docs != nil {
					snap, found := docs.SnapshotIfOpen(latest.Path)
					if found {
						input.Text, open = string(snap.Text), true
					}
				}
				if !open {
					path, err := backend.OpenPlanRevision(input.Revision)
					if err != nil {
						return conversation.PlanReviewSettlement{}, err
					}
					text, err := readPlanReviewText(ctx, path)
					if err != nil {
						return conversation.PlanReviewSettlement{}, err
					}
					input.Text = text
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return conversation.PlanReviewSettlement{}, err
		}
		return backend.SettlePlanReviewForAgent(ctx, agentID, input, sender, destination)
	}, nil
}

func readPlanReviewText(ctx context.Context, path string) (string, error) {
	// Allow a line terminator and a UTF-16 BOM even at the plan text limit.
	// The session checks the decoded text after trimming trailing newlines.
	raw, err := fileio.ReadRegularNoFollow(ctx, path, 2*(conversation.PlanReviewMaxPlanText+1)+2)
	if err != nil {
		if errors.Is(err, fileio.ErrReadLimit) {
			return "", session.ErrPlanReviewTooLong
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", session.PlanReviewError("Could not read the plan file. Open it and try again.")
	}
	text, _, err := textencoding.DecodeText(raw)
	if err != nil {
		return "", session.PlanReviewError("Could not read the plan text. Save it as UTF-8 and try again.")
	}
	return string(text), nil
}

// OpenPlanRevision runs the prepared file operation for the local port.
func (w *Workspace) OpenPlanRevision(agentID string, revision int) (string, error) {
	run, err := w.PrepareOpenPlanRevision(agentID, revision)
	if err != nil {
		return "", err
	}
	return run()
}

// EditPlanComment runs the prepared change for the local port.
func (w *Workspace) EditPlanComment(agentID string, edit conversation.PlanCommentEdit) error {
	run, err := w.PrepareEditPlanComment(agentID, edit)
	if err != nil {
		return err
	}
	return run()
}

// SettlePlanReview runs the prepared review action for the local port.
func (w *Workspace) SettlePlanReview(agentID string, settle conversation.PlanReviewSettle) (conversation.PlanReviewSettlement, error) {
	run, err := w.PrepareSettlePlanReview(agentID, settle, provider.UserInput{})
	if err != nil {
		return conversation.PlanReviewSettlement{}, err
	}
	return run(context.Background())
}
