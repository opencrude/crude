package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"time"

	"crude/agent/collaboration"
	"crude/agent/prompt"
	"crude/types/statuscenter"
)

// Crude asks its workspace Errand for help through the collaboration journal:
// a problem that the owner marked Investigate, and that stayed open for
// attentionRipe, becomes one runtime notice to Errand. The journal's
// idempotency key is the only memory of what Errand was told, so a problem is
// raised once per cause, across core restarts, and a changed cause is new.
// The actor decides on its own goroutine from in-memory state; the publish
// itself runs on the resource lane.
const (
	attentionRipe = 30 * time.Second
	// attentionBatch bounds the notices of one evaluation. The idle wake
	// admits the first; the running turn admits the rest at its next boundary.
	attentionBatch = 3
)

type attentionState struct {
	timer *time.Timer
	ready <-chan time.Time
	// sent holds the keys this actor published, so it does not publish
	// them again while the journal answers Duplicate.
	sent map[string]bool
}

func (s *attentionState) stop() {
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer, s.ready = nil, nil
}

// attentionKey names one cause: the issue and its detail. A different detail
// is a different cause and is raised again; a repeated one is not.
func attentionKey(issue statuscenter.IssueSnapshot) string {
	sum := sha256.Sum256([]byte(issue.ID + "\x00" + issue.Detail))
	return "attention:" + hex.EncodeToString(sum[:])
}

// errandReady reports whether the workspace Errand can take a notice now: it
// runs, has no turn and waits for no answer. A turn that the user cancelled or
// that failed waits for the user, not for Crude.
func errandReady(status statuscenter.AgentStatusSnapshot) bool {
	return !status.NeedsAction && (status.Phase == statuscenter.AgentIdle || status.Phase == statuscenter.AgentCompleted)
}

// evaluateAttention runs on the actor after a status, language-server or
// agent change, after attach or detach, and when the ripeness timer fires.
func (a *actor) evaluateAttention(now time.Time) {
	a.attention.stop()
	if a.retiring || a.ws.Status == nil || a.ws.Collaboration == nil || a.ws.LSPMgr == nil ||
		!a.ws.LSPMgr.Settings().ErrandInvestigates() || len(a.subscribers) == 0 {
		return
	}
	errand := a.ws.ErrandAgentID()
	if errand == "" {
		return
	}
	if status, ok := a.ws.Status.Agent(errand); !ok || !errandReady(status) {
		return
	}
	due, next := dueAttention(now, a.ws.Status.Issues(), a.attention.sent)
	if next > 0 {
		a.attention.timer = time.NewTimer(next)
		a.attention.ready = a.attention.timer.C
	}
	if len(due) == 0 {
		return
	}
	if a.attention.sent == nil {
		a.attention.sent = make(map[string]bool)
	}
	requests := make([]collaboration.PublishRequest, 0, len(due))
	for _, issue := range due {
		key := attentionKey(issue)
		a.attention.sent[key] = true
		requests = append(requests, collaboration.PublishRequest{
			SenderID: errand, SenderName: "Crude", IdempotencyKey: key,
			RecipientIDs: []string{errand}, Origin: collaboration.OriginRuntime, Kind: collaboration.EventPeerNote,
			Body: prompt.AttentionNotice(issue.Summary),
		})
	}
	store, logger := a.ws.Collaboration, a.ws.Logger
	requests = slices.Clone(requests)
	a.resourceLane.submit(func(ctx context.Context) {
		for _, request := range requests {
			if _, err := store.Publish(ctx, request); err != nil && !errors.Is(err, collaboration.ErrNotFound) && ctx.Err() == nil && logger != nil {
				logger.Warn("CORE", "attention_publish_failed", "error", err.Error())
			}
		}
	})
}

// dueAttention returns the open Investigate issues that Errand was not told
// about and that stayed unchanged for attentionRipe, most severe first and at
// most attentionBatch, and how long until the next one ripens (0 for none).
func dueAttention(now time.Time, issues []statuscenter.IssueSnapshot, sent map[string]bool) ([]statuscenter.IssueSnapshot, time.Duration) {
	var due []statuscenter.IssueSnapshot
	var next time.Duration
	for _, issue := range issues {
		if !issue.Investigate || !issue.ResolvedAt.IsZero() || sent[attentionKey(issue)] {
			continue
		}
		if wait := attentionRipe - now.Sub(issue.UpdatedAt); wait > 0 {
			if next == 0 || wait < next {
				next = wait
			}
			continue
		}
		due = append(due, issue)
	}
	// Issues() lists the most severe first.
	return due[:min(len(due), attentionBatch)], next
}
