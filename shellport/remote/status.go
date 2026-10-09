package remote

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"
	"time"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
)

// statusSnapshot is the replicated part of the workspace status center.
type statusSnapshot = coreprotocol.StatusSnapshot

// statusReplica serves the shell's status reads without I/O. Shared records
// mirror the core; transient notices (toasts) belong to this shell alone, so
// one shell's feedback never appears in another. Mutations apply to the
// replica immediately and are sent to the core in order; the next core
// snapshot is authoritative.
type statusReplica struct {
	w         *Workspace
	core      statusSnapshot
	transient []types.NotificationRecord
	version   uint64
}

var _ shellport.StatusService = (*statusReplica)(nil)

func newStatusReplica(w *Workspace) *statusReplica { return &statusReplica{w: w} }

func (s *statusReplica) fetch() (statusSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var snapshot statusSnapshot
	err := s.w.call(ctx, coreprotocol.StatusSnapshotMethod, struct{}{}, &snapshot)
	return snapshot, err
}

// install adopts a core snapshot on the Update goroutine. It reports whether
// the shell must re-read status.
func (s *statusReplica) install(snapshot statusSnapshot) bool {
	if snapshot.Version == s.core.Version && s.version != 0 {
		return false
	}
	s.core = snapshot
	s.version++
	return true
}

func (s *statusReplica) send(mutation coreprotocol.StatusMutation) {
	s.w.commands.push(func(ctx context.Context) {
		if err := s.w.call(ctx, coreprotocol.StatusMutateMethod, mutation, &coreprotocol.StatusMutationResult{}); err != nil {
			s.w.logger.Warn("REMOTE", "status_mutation_failed", "op", mutation.Op, "error", err.Error())
		}
	})
}

func (s *statusReplica) Agent(id string) (types.AgentStatusSnapshot, bool) {
	for _, agent := range s.core.Agents {
		if agent.Ref.ID == id {
			return agent, true
		}
	}
	return types.AgentStatusSnapshot{}, false
}

func (s *statusReplica) Agents() []types.AgentStatusSnapshot {
	return slices.Clone(s.core.Agents)
}

func (s *statusReplica) Issues() []types.IssueSnapshot {
	return slices.Clone(s.core.Issues)
}

// Notifications merges this shell's transient notices with the shared
// history, newest first, matching the local status center's order.
func (s *statusReplica) Notifications(includeDismissed bool) []types.NotificationRecord {
	out := make([]types.NotificationRecord, 0, len(s.transient)+len(s.core.Notifications))
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
	slices.SortStableFunc(out, func(a, b types.NotificationRecord) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return out
}

func (s *statusReplica) UnreadCount() int {
	count := 0
	for _, n := range s.Notifications(false) {
		if !n.Read && !n.Transient {
			count++
		}
	}
	return count
}

func (s *statusReplica) LatestTransient(now time.Time, maxAge time.Duration) (types.NotificationRecord, bool) {
	for _, n := range s.Notifications(false) {
		if n.Transient && now.Sub(n.UpdatedAt) <= maxAge {
			return n, true
		}
	}
	return types.NotificationRecord{}, false
}

// Version changes whenever anything this shell can read changed.
func (s *statusReplica) Version() uint64 { return s.version }

// Hydrate is owned by the core, which loads persisted history on open.
func (s *statusReplica) Hydrate() error { return nil }

const maxTransientNotices = 32

func (s *statusReplica) Notify(n types.NotificationRecord) string {
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
	s.core.Notifications = append([]types.NotificationRecord{n}, s.core.Notifications...)
	s.version++
	sent := n
	s.send(coreprotocol.StatusMutation{Op: coreprotocol.StatusNotify, Notification: &sent})
	return n.ID
}

func (s *statusReplica) updateLocal(id string, update func(*types.NotificationRecord)) bool {
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
	if !s.updateLocal(id, func(n *types.NotificationRecord) { n.Read = read }) {
		return false
	}
	if !transient {
		s.send(coreprotocol.StatusMutation{Op: coreprotocol.StatusMarkRead, ID: id, Read: read})
	}
	return true
}

func (s *statusReplica) Dismiss(id string) bool {
	transient := s.isTransient(id)
	if !s.updateLocal(id, func(n *types.NotificationRecord) { n.Dismissed, n.Read = true, true }) {
		return false
	}
	if !transient {
		s.send(coreprotocol.StatusMutation{Op: coreprotocol.StatusDismiss, ID: id})
	}
	return true
}

func (s *statusReplica) SetIssue(issue types.IssueSnapshot) {
	if issue.ID == "" {
		return
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

func (s *statusReplica) ResolveIssue(id string) {
	for i := range s.core.Issues {
		if s.core.Issues[i].ID == id && s.core.Issues[i].ResolvedAt.IsZero() {
			s.core.Issues[i].ResolvedAt = time.Now()
			s.version++
		}
	}
	s.send(coreprotocol.StatusMutation{Op: coreprotocol.StatusResolveIssue, ID: id})
}

func (s *statusReplica) SetOperation(op types.OperationSnapshot) {
	if op.ID == "" {
		return
	}
	s.send(coreprotocol.StatusMutation{Op: coreprotocol.StatusSetOperation, Operation: &op})
}

func (s *statusReplica) FinishOperation(id string, state types.OperationState, errText string) {
	s.send(coreprotocol.StatusMutation{Op: coreprotocol.StatusFinishOperation, ID: id, State: state, Error: errText})
}

func randomID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
