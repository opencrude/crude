package remote

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"
	"time"

	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/internal/metrics"
	"crude/types/statuscenter"
)

// statusSnapshot is the replicated part of the workspace status center.
type statusSnapshot = coreprotocol.StatusSnapshot

// statusReplica serves the shell's status reads without I/O. Shared records
// mirror the core; transient notices (toasts) belong to this shell alone, so
// one shell's feedback never appears in another. Mutations apply to the
// replica immediately and are sent to the core in order; the next core
// snapshot is authoritative.
type statusReplica struct {
	w             *Workspace
	core          statusSnapshot
	transient     []statuscenter.NotificationRecord
	version       uint64
	footerVersion uint64
	unread        int
	toast         *statuscenter.NotificationRecord
}

var _ shellport.StatusService = (*statusReplica)(nil)

func newStatusReplica(w *Workspace) *statusReplica { return &statusReplica{w: w} }

func (s *statusReplica) fetch(ctx context.Context) (statusSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var snapshot statusSnapshot
	err := s.w.call(ctx, coreprotocol.StatusSnapshotMethod, &coreprotocol.Empty{}, &snapshot)
	return snapshot, err
}

// install adopts a core snapshot on the Update goroutine. It reports whether
// the shell must re-read status.
func (s *statusReplica) install(snapshot statusSnapshot) bool {
	if snapshot.Version <= s.core.Version && s.version != 0 {
		return false
	}
	s.core = snapshot
	s.version++
	return true
}

// A replacement core starts a new version domain. Preserve private toasts but
// do not compare its cold snapshot against the previous core's revision.
func (s *statusReplica) resetRevision() { s.core.Version, s.version, s.footerVersion = 0, 0, 0 }

func (s *statusReplica) send(mutation coreprotocol.StatusMutation) {
	s.w.services.push(func(ctx context.Context) {
		if err := s.w.call(ctx, coreprotocol.StatusMutateMethod, &mutation, &coreprotocol.StatusMutationResult{}); err != nil {
			s.w.logger.Warn("REMOTE", "status_mutation_failed", "op", mutation.Op, "error", err.Error())
		}
	})
}

func (s *statusReplica) Agent(id string) (statuscenter.AgentStatusSnapshot, bool) {
	for _, agent := range s.core.Agents {
		if agent.Ref.ID == id {
			return agent, true
		}
	}
	return statuscenter.AgentStatusSnapshot{}, false
}

func (s *statusReplica) Agents() []statuscenter.AgentStatusSnapshot {
	return slices.Clone(s.core.Agents)
}

func (s *statusReplica) Issues() []statuscenter.IssueSnapshot {
	return slices.Clone(s.core.Issues)
}

// Notifications merges this shell's transient notices with the shared
// history, newest first, matching the local status center's order.
func (s *statusReplica) Notifications(includeDismissed bool) []statuscenter.NotificationRecord {
	out := make([]statuscenter.NotificationRecord, 0, len(s.transient)+len(s.core.Notifications))
	for i := len(s.transient) - 1; i >= 0; i-- {
		if includeDismissed || !s.transient[i].Dismissed {
			out = append(out, s.transient[i])
		}
	}
	for _, n := range s.core.Notifications {
		if includeDismissed || !n.Dismissed {
			out = append(out, n)
		}
	}
	slices.SortStableFunc(out, func(a, b statuscenter.NotificationRecord) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return out
}

// The footer needs two facts, not a sorted copy of every notice on every frame.
// Keep the same tie order as Notifications: newest local insertion first.
func (s *statusReplica) refreshFooter() {
	if s.footerVersion == s.version && s.version != 0 {
		return
	}
	s.footerVersion, s.unread, s.toast = s.version, 0, nil
	visit := func(n *statuscenter.NotificationRecord) {
		if n.Dismissed {
			return
		}
		if n.Transient {
			if s.toast == nil || n.UpdatedAt.After(s.toast.UpdatedAt) {
				s.toast = n
			}
		} else if !n.Read {
			s.unread++
		}
	}
	for i := len(s.transient) - 1; i >= 0; i-- {
		visit(&s.transient[i])
	}
	for i := range s.core.Notifications {
		visit(&s.core.Notifications[i])
	}
}

func (s *statusReplica) UnreadCount() int {
	s.refreshFooter()
	return s.unread
}

func (s *statusReplica) LatestTransient(now time.Time, maxAge time.Duration) (statuscenter.NotificationRecord, bool) {
	s.refreshFooter()
	if s.toast != nil && now.Sub(s.toast.UpdatedAt) <= maxAge {
		return *s.toast, true
	}
	return statuscenter.NotificationRecord{}, false
}

// Version changes whenever anything this shell can read changed.
func (s *statusReplica) Version() uint64 { return s.version }

// Hydrate is owned by the core, which loads persisted history on open.
func (s *statusReplica) Hydrate() error { return nil }

const maxTransientNotices = 32

func (s *statusReplica) Notify(n statuscenter.NotificationRecord) string {
	if strings.TrimSpace(n.Summary) == "" {
		return ""
	}
	now := time.Now()
	if n.Transient {
		if n.ID == "" {
			n.ID = "toast-" + randomID()
		}
		if n.Occurrences < 1 {
			n.Occurrences = 1
		}
		n.CreatedAt, n.UpdatedAt = now, now
		s.transient = append(s.transient, n)
		if len(s.transient) > maxTransientNotices {
			s.transient = slices.Delete(s.transient, 0, len(s.transient)-maxTransientNotices)
		}
		s.version++
		return n.ID
	}
	// Mirror the core's deduplication so the returned identity stays valid.
	if n.DedupKey != "" {
		for i := range s.core.Notifications {
			old := &s.core.Notifications[i]
			if old.DedupKey == n.DedupKey && old.ResolvedAt.IsZero() && !old.Dismissed {
				old.Summary, old.Detail, old.Severity = n.Summary, n.Detail, n.Severity
				old.UpdatedAt, old.Read = now, false
				old.Occurrences++
				s.version++
				s.send(coreprotocol.StatusMutation{Op: coreprotocol.StatusNotify, Notification: &n})
				return old.ID
			}
		}
	}
	if n.ID == "" {
		n.ID = "notice-" + randomID()
	}
	if n.Occurrences < 1 {
		n.Occurrences = 1
	}
	n.CreatedAt, n.UpdatedAt = now, now
	s.core.Notifications = append([]statuscenter.NotificationRecord{n}, s.core.Notifications...)
	s.version++
	sent := n
	s.send(coreprotocol.StatusMutation{Op: coreprotocol.StatusNotify, Notification: &sent})
	return n.ID
}

func (s *statusReplica) updateLocal(id string, update func(*statuscenter.NotificationRecord)) bool {
	for i := range s.transient {
		if s.transient[i].ID == id {
			update(&s.transient[i])
			s.version++
			return true
		}
	}
	for i := range s.core.Notifications {
		if s.core.Notifications[i].ID == id {
			update(&s.core.Notifications[i])
			s.version++
			return true
		}
	}
	return false
}

func (s *statusReplica) isTransient(id string) bool {
	for _, n := range s.transient {
		if n.ID == id {
			return true
		}
	}
	return false
}

func (s *statusReplica) MarkRead(id string, read bool) bool {
	transient := s.isTransient(id)
	if !s.updateLocal(id, func(n *statuscenter.NotificationRecord) { n.Read = read }) {
		return false
	}
	if !transient {
		s.send(coreprotocol.StatusMutation{Op: coreprotocol.StatusMarkRead, ID: id, Read: read})
	}
	return true
}

func (s *statusReplica) Dismiss(id string) bool {
	transient := s.isTransient(id)
	if !s.updateLocal(id, func(n *statuscenter.NotificationRecord) { n.Dismissed, n.Read = true, true }) {
		return false
	}
	if !transient {
		s.send(coreprotocol.StatusMutation{Op: coreprotocol.StatusDismiss, ID: id})
	}
	return true
}

func (s *statusReplica) SetIssue(issue statuscenter.IssueSnapshot) {
	if issue.ID == "" {
		return
	}
	// Mirror the core: an unchanged open report is not a mutation, so callers
	// that reconcile every frame send nothing to the core.
	for _, previous := range s.core.Issues {
		if previous.ID == issue.ID && issue.SameOpenReport(previous) {
			return
		}
	}
	issue.UpdatedAt = time.Now()
	replaced := false
	for i := range s.core.Issues {
		if s.core.Issues[i].ID == issue.ID {
			s.core.Issues[i], replaced = issue, true
		}
	}
	if !replaced {
		s.core.Issues = append(s.core.Issues, issue)
	}
	s.version++
	s.send(coreprotocol.StatusMutation{Op: coreprotocol.StatusSetIssue, Issue: &issue})
}

// ResolveIssue mirrors the core: only an open issue changes, so resolving an
// absent or already resolved issue sends nothing.
func (s *statusReplica) ResolveIssue(id string) {
	for i := range s.core.Issues {
		if s.core.Issues[i].ID == id && s.core.Issues[i].ResolvedAt.IsZero() {
			s.core.Issues[i].ResolvedAt = time.Now()
			s.version++
			s.send(coreprotocol.StatusMutation{Op: coreprotocol.StatusResolveIssue, ID: id})
			return
		}
	}
}

func (s *statusReplica) SetOperation(op statuscenter.OperationSnapshot) {
	if op.ID == "" {
		return
	}
	s.send(coreprotocol.StatusMutation{Op: coreprotocol.StatusSetOperation, Operation: &op})
}

func (s *statusReplica) FinishOperation(id string, state statuscenter.OperationState, errText string) {
	s.send(coreprotocol.StatusMutation{Op: coreprotocol.StatusFinishOperation, ID: id, State: state, Error: errText})
}

func randomID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// CoreMetrics reads the core process's performance history for the
// Performance surface. It is a core-level request, not workspace-bound.
func (w *Workspace) CoreMetrics(ctx context.Context, query metrics.Query) (metrics.Snapshot, error) {
	client, _ := w.conn()
	if client == nil {
		return metrics.Snapshot{}, errDisconnected
	}
	var out metrics.Snapshot
	err := client.Call(ctx, "", coreprotocol.CoreMetricsMethod, query, &out)
	return out, err
}
