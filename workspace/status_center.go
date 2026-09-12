package workspace

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	ttypes "crude/types"
)

const (
	statusNotificationLimit  = 1000
	statusRetention          = 30 * 24 * time.Hour
	statusPersistQueueLimit  = 2048
	statusOperationLimit     = 500
	statusOperationRetention = 24 * time.Hour
)

type StatusCenterHydratedMsg struct{}

type statusPersistKind uint8

const (
	persistNotice statusPersistKind = iota
	persistDeleteNotice
)

type statusPersistRequest struct {
	kind   statusPersistKind
	notice ttypes.NotificationRecord
	id     string
}

// StatusCenter owns workspace-scoped status projections that do not belong to
// any renderer. Resource owners still own their actual lifecycle; this center
// retains immutable summaries, operations, issues, and notification history.
type StatusCenter struct {
	mu            sync.RWMutex
	db            *sql.DB
	logger        Logger
	agents        map[string]ttypes.AgentStatusSnapshot
	operations    map[string]ttypes.OperationSnapshot
	issues        map[string]ttypes.IssueSnapshot
	notifications []ttypes.NotificationRecord
	byNoticeID    map[string]int
	nextID        uint64
	version       uint64

	persistMu    sync.Mutex
	persistQueue map[string]statusPersistRequest
	persistOrder []string
	persistWake  chan struct{}
	persistStop  chan struct{}
	persistDone  chan struct{}
	closeOnce    sync.Once
	lifecycleMu  sync.Mutex
	closed       bool
	workWG       sync.WaitGroup
}

func NewStatusCenter(db *sql.DB, logger Logger) *StatusCenter {
	c := &StatusCenter{
		db: db, logger: logger,
		agents:       make(map[string]ttypes.AgentStatusSnapshot),
		operations:   make(map[string]ttypes.OperationSnapshot),
		issues:       make(map[string]ttypes.IssueSnapshot),
		byNoticeID:   make(map[string]int),
		persistQueue: make(map[string]statusPersistRequest),
		persistWake:  make(chan struct{}, 1), persistStop: make(chan struct{}), persistDone: make(chan struct{}),
	}
	go c.persistLoop()
	return c
}

func (c *StatusCenter) Hydrate() error {
	if c == nil || c.db == nil {
		return nil
	}
	c.lifecycleMu.Lock()
	if c.closed {
		c.lifecycleMu.Unlock()
		return nil
	}
	c.workWG.Add(1)
	c.lifecycleMu.Unlock()
	defer c.workWG.Done()
	rows, err := c.db.Query(`
		SELECT id, dedup_key, owner_kind, owner_id, session_id, parent_id,
		       severity, source, summary, detail, created_at, updated_at,
		       resolved_at, occurrences, is_read, dismissed, transient, actions_json
		FROM workspace_notifications
		ORDER BY updated_at DESC LIMIT ?`, statusNotificationLimit)
	if err != nil {
		return fmt.Errorf("load workspace notifications: %w", err)
	}
	defer func() { _ = rows.Close() }()
	loaded := make([]ttypes.NotificationRecord, 0, statusNotificationLimit)
	for rows.Next() {
		var n ttypes.NotificationRecord
		var kind string
		var created, updated, resolved int64
		var actions string
		if err := rows.Scan(&n.ID, &n.DedupKey, &kind, &n.Owner.ID, &n.Owner.SessionID,
			&n.Owner.ParentID, &n.Severity, &n.Source, &n.Summary, &n.Detail,
			&created, &updated, &resolved, &n.Occurrences, &n.Read, &n.Dismissed,
			&n.Transient, &actions); err != nil {
			return fmt.Errorf("scan workspace notification: %w", err)
		}
		n.Owner.Kind = ttypes.ResourceKind(kind)
		n.CreatedAt, n.UpdatedAt = time.Unix(0, created), time.Unix(0, updated)
		if resolved > 0 {
			n.ResolvedAt = time.Unix(0, resolved)
		}
		_ = json.Unmarshal([]byte(actions), &n.Actions)
		loaded = append(loaded, n)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	for i := len(loaded) - 1; i >= 0; i-- {
		if _, exists := c.byNoticeID[loaded[i].ID]; exists {
			continue
		}
		c.notifications = append(c.notifications, loaded[i])
	}
	c.reindexLocked()
	c.pruneLocked(time.Now())
	c.version++
	c.mu.Unlock()
	return nil
}

func (c *StatusCenter) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		c.lifecycleMu.Lock()
		c.closed = true
		c.lifecycleMu.Unlock()
		c.workWG.Wait()
		close(c.persistStop)
		<-c.persistDone
	})
}

func (c *StatusCenter) SetAgent(snapshot ttypes.AgentStatusSnapshot) {
	if c == nil || snapshot.Ref.ID == "" {
		return
	}
	c.mu.Lock()
	previous := c.agents[snapshot.Ref.ID]
	now := time.Now()
	if snapshot.StartedAt.IsZero() && previous.Phase == snapshot.Phase {
		snapshot.StartedAt = previous.StartedAt
	}
	if snapshot.StartedAt.IsZero() {
		snapshot.StartedAt = now
	}
	if snapshot.PhaseStartedAt.IsZero() || previous.Phase != snapshot.Phase {
		snapshot.PhaseStartedAt = now
	}
	if snapshot.UpdatedAt.IsZero() {
		snapshot.UpdatedAt = now
	}
	if previous == snapshot {
		c.mu.Unlock()
		return
	}
	c.agents[snapshot.Ref.ID] = snapshot
	c.version++
	c.mu.Unlock()
}

func (c *StatusCenter) RemoveAgent(id string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if _, exists := c.agents[id]; exists {
		delete(c.agents, id)
		c.version++
	}
	c.mu.Unlock()
}

func (c *StatusCenter) Agent(id string) (ttypes.AgentStatusSnapshot, bool) {
	if c == nil {
		return ttypes.AgentStatusSnapshot{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.agents[id]
	return v, ok
}

func (c *StatusCenter) Agents() []ttypes.AgentStatusSnapshot {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	out := make([]ttypes.AgentStatusSnapshot, 0, len(c.agents))
	for _, v := range c.agents {
		out = append(out, v)
	}
	c.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

func (c *StatusCenter) SetOperation(op ttypes.OperationSnapshot) {
	if c == nil || op.ID == "" {
		return
	}
	now := time.Now()
	if op.StartedAt.IsZero() {
		op.StartedAt = now
	}
	if operationTerminal(op.State) && op.EndedAt.IsZero() {
		op.EndedAt = now
	}
	c.mu.Lock()
	if previous, exists := c.operations[op.ID]; exists && operationEquivalent(previous, op) {
		c.mu.Unlock()
		return
	}
	op.UpdatedAt = now
	c.operations[op.ID] = op
	c.pruneOperationsLocked(now)
	c.version++
	c.mu.Unlock()
}

func (c *StatusCenter) pruneOperationsLocked(now time.Time) {
	cutoff := now.Add(-statusOperationRetention)
	for id, operation := range c.operations {
		if operationTerminal(operation.State) && !operation.EndedAt.IsZero() && operation.EndedAt.Before(cutoff) {
			delete(c.operations, id)
		}
	}
	for len(c.operations) > statusOperationLimit {
		oldestID := ""
		var oldest time.Time
		for id, operation := range c.operations {
			if !operationTerminal(operation.State) {
				continue
			}
			when := operation.EndedAt
			if when.IsZero() {
				when = operation.UpdatedAt
			}
			if oldestID == "" || when.Before(oldest) {
				oldestID, oldest = id, when
			}
		}
		if oldestID == "" {
			break
		}
		delete(c.operations, oldestID)
	}
}

func operationEquivalent(a, b ttypes.OperationSnapshot) bool {
	return a.ID == b.ID && a.Owner == b.Owner && a.Kind == b.Kind && a.Label == b.Label &&
		a.Detail == b.Detail && a.State == b.State && a.Progress == b.Progress &&
		a.Total == b.Total && a.Unit == b.Unit && a.StartedAt.Equal(b.StartedAt) &&
		a.EndedAt.Equal(b.EndedAt) && a.Cancellable == b.Cancellable &&
		a.OutputRef == b.OutputRef && a.Error == b.Error
}

func (c *StatusCenter) Operations() []ttypes.OperationSnapshot {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	out := make([]ttypes.OperationSnapshot, 0, len(c.operations))
	for _, op := range c.operations {
		out = append(out, op)
	}
	c.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		it, jt := operationTerminal(out[i].State), operationTerminal(out[j].State)
		if it != jt {
			return !it
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func (c *StatusCenter) FinishOperations(ownerID string, state ttypes.OperationState, errText string) {
	if c == nil || ownerID == "" {
		return
	}
	now := time.Now()
	c.mu.Lock()
	for id, op := range c.operations {
		if op.Owner.ID != ownerID || operationTerminal(op.State) {
			continue
		}
		op.State, op.Error, op.UpdatedAt, op.EndedAt = state, errText, now, now
		c.operations[id] = op
		c.version++
	}
	c.mu.Unlock()
}

func (c *StatusCenter) FinishOperation(id string, state ttypes.OperationState, errText string) {
	if c == nil || id == "" {
		return
	}
	now := time.Now()
	c.mu.Lock()
	op, ok := c.operations[id]
	if ok && !operationTerminal(op.State) {
		op.State, op.Error, op.UpdatedAt, op.EndedAt = state, errText, now, now
		c.operations[id] = op
		c.version++
	}
	c.mu.Unlock()
}

func operationTerminal(state ttypes.OperationState) bool {
	return state == ttypes.OperationSucceeded || state == ttypes.OperationFailed || state == ttypes.OperationCancelled
}

func (c *StatusCenter) SetIssue(issue ttypes.IssueSnapshot) {
	if c == nil || issue.ID == "" {
		return
	}
	c.mu.Lock()
	if previous, exists := c.issues[issue.ID]; exists && previous.Owner == issue.Owner && previous.Severity == issue.Severity && previous.Summary == issue.Summary && previous.Detail == issue.Detail && previous.ResolvedAt.IsZero() {
		c.mu.Unlock()
		return
	}
	issue.UpdatedAt = time.Now()
	c.issues[issue.ID] = issue
	c.version++
	c.mu.Unlock()
}

func (c *StatusCenter) ResolveIssue(id string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	issue, ok := c.issues[id]
	if ok && issue.ResolvedAt.IsZero() {
		issue.ResolvedAt = time.Now()
		issue.UpdatedAt = issue.ResolvedAt
		c.issues[id] = issue
		c.version++
	}
	c.mu.Unlock()
}

func (c *StatusCenter) Issues() []ttypes.IssueSnapshot {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	out := make([]ttypes.IssueSnapshot, 0, len(c.issues))
	for _, issue := range c.issues {
		out = append(out, issue)
	}
	c.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		ir, jr := !out[i].ResolvedAt.IsZero(), !out[j].ResolvedAt.IsZero()
		if ir != jr {
			return !ir
		}
		if out[i].Severity != out[j].Severity {
			return out[i].Severity > out[j].Severity
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func (c *StatusCenter) Notify(n ttypes.NotificationRecord) string {
	if c == nil || strings.TrimSpace(n.Summary) == "" {
		return ""
	}
	now := time.Now()
	c.mu.Lock()
	if n.DedupKey != "" {
		for i := len(c.notifications) - 1; i >= 0; i-- {
			old := &c.notifications[i]
			if old.DedupKey == n.DedupKey && old.ResolvedAt.IsZero() && !old.Dismissed {
				old.Summary, old.Detail, old.Severity = n.Summary, n.Detail, n.Severity
				old.UpdatedAt, old.Read = now, false
				old.Occurrences++
				old.Actions = append([]ttypes.StatusAction(nil), n.Actions...)
				updated := *old
				c.version++
				c.mu.Unlock()
				c.enqueue(statusPersistRequest{kind: persistNotice, notice: updated})
				return updated.ID
			}
		}
	}
	c.nextID++
	if n.ID == "" {
		n.ID = fmt.Sprintf("notice-%d-%d", now.UnixNano(), c.nextID)
	}
	if n.Occurrences < 1 {
		n.Occurrences = 1
	}
	n.CreatedAt, n.UpdatedAt = now, now
	c.notifications = append(c.notifications, n)
	c.version++
	c.pruneLocked(now)
	c.reindexLocked()
	c.mu.Unlock()
	c.enqueue(statusPersistRequest{kind: persistNotice, notice: n})
	return n.ID
}

func (c *StatusCenter) Notifications(includeDismissed bool) []ttypes.NotificationRecord {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	out := make([]ttypes.NotificationRecord, 0, len(c.notifications))
	for i := len(c.notifications) - 1; i >= 0; i-- {
		n := c.notifications[i]
		if includeDismissed || !n.Dismissed {
			out = append(out, n)
		}
	}
	c.mu.RUnlock()
	return out
}

func (c *StatusCenter) UnreadCount() int {
	count := 0
	for _, n := range c.Notifications(false) {
		if !n.Read && !n.Transient {
			count++
		}
	}
	return count
}

func (c *StatusCenter) LatestUnread(minSeverity ttypes.StatusSeverity) (ttypes.NotificationRecord, bool) {
	for _, n := range c.Notifications(false) {
		if !n.Read && !n.Transient && n.Severity >= minSeverity {
			return n, true
		}
	}
	return ttypes.NotificationRecord{}, false
}

func (c *StatusCenter) LatestTransient(now time.Time, maxAge time.Duration) (ttypes.NotificationRecord, bool) {
	for _, n := range c.Notifications(false) {
		if n.Transient && now.Sub(n.UpdatedAt) <= maxAge {
			return n, true
		}
	}
	return ttypes.NotificationRecord{}, false
}

func (c *StatusCenter) MarkRead(id string, read bool) bool {
	return c.updateNotice(id, func(n *ttypes.NotificationRecord) { n.Read = read })
}

func (c *StatusCenter) Dismiss(id string) bool {
	return c.updateNotice(id, func(n *ttypes.NotificationRecord) { n.Dismissed, n.Read = true, true })
}

func (c *StatusCenter) ResolveNotification(id string) bool {
	return c.updateNotice(id, func(n *ttypes.NotificationRecord) {
		if n.ResolvedAt.IsZero() {
			n.ResolvedAt = time.Now()
		}
	})
}

func (c *StatusCenter) updateNotice(id string, fn func(*ttypes.NotificationRecord)) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	i, ok := c.byNoticeID[id]
	if !ok || i < 0 || i >= len(c.notifications) {
		c.mu.Unlock()
		return false
	}
	fn(&c.notifications[i])
	c.notifications[i].UpdatedAt = time.Now()
	c.version++
	n := c.notifications[i]
	c.mu.Unlock()
	c.enqueue(statusPersistRequest{kind: persistNotice, notice: n})
	return true
}

func (c *StatusCenter) Version() uint64 {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version
}

func (c *StatusCenter) pruneLocked(now time.Time) {
	cutoff := now.Add(-statusRetention)
	kept := c.notifications[:0]
	for _, n := range c.notifications {
		live := n.ResolvedAt.IsZero() && n.Severity >= ttypes.SeverityWarning
		if live || n.UpdatedAt.After(cutoff) {
			kept = append(kept, n)
		} else {
			c.enqueue(statusPersistRequest{kind: persistDeleteNotice, id: n.ID})
		}
	}
	c.notifications = kept
	if len(c.notifications) <= statusNotificationLimit {
		return
	}
	drop := len(c.notifications) - statusNotificationLimit
	for i := 0; i < len(c.notifications) && drop > 0; i++ {
		n := c.notifications[i]
		if n.ResolvedAt.IsZero() && n.Severity >= ttypes.SeverityWarning {
			continue
		}
		c.enqueue(statusPersistRequest{kind: persistDeleteNotice, id: n.ID})
		c.notifications = append(c.notifications[:i], c.notifications[i+1:]...)
		i--
		drop--
	}
	for drop > 0 && len(c.notifications) > 0 {
		oldest := c.notifications[0]
		c.enqueue(statusPersistRequest{kind: persistDeleteNotice, id: oldest.ID})
		c.notifications = c.notifications[1:]
		drop--
	}
}

func (c *StatusCenter) reindexLocked() {
	c.byNoticeID = make(map[string]int, len(c.notifications))
	for i := range c.notifications {
		c.byNoticeID[c.notifications[i].ID] = i
	}
}

func (c *StatusCenter) enqueue(req statusPersistRequest) {
	if c == nil || c.db == nil {
		return
	}
	c.lifecycleMu.Lock()
	closed := c.closed
	c.lifecycleMu.Unlock()
	if closed {
		return
	}
	c.persistMu.Lock()
	key := req.id
	if req.kind == persistNotice {
		key = req.notice.ID
	}
	if _, exists := c.persistQueue[key]; !exists {
		if len(c.persistQueue) >= statusPersistQueueLimit {
			if req.kind == persistDeleteNotice {
				c.persistMu.Unlock()
				return
			}
			for index, pendingKey := range c.persistOrder {
				if c.persistQueue[pendingKey].kind == persistDeleteNotice {
					delete(c.persistQueue, pendingKey)
					c.persistOrder = append(c.persistOrder[:index], c.persistOrder[index+1:]...)
					break
				}
			}
		}
		c.persistOrder = append(c.persistOrder, key)
	}
	c.persistQueue[key] = req
	c.persistMu.Unlock()
	select {
	case c.persistWake <- struct{}{}:
	default:
	}
}

func (c *StatusCenter) persistLoop() {
	defer close(c.persistDone)
	for {
		select {
		case <-c.persistWake:
			c.drainPersistence()
		case <-c.persistStop:
			c.drainPersistence()
			return
		}
	}
}

func (c *StatusCenter) drainPersistence() {
	for {
		c.persistMu.Lock()
		if len(c.persistOrder) == 0 {
			c.persistMu.Unlock()
			return
		}
		queue := make([]statusPersistRequest, 0, len(c.persistOrder))
		for _, key := range c.persistOrder {
			queue = append(queue, c.persistQueue[key])
		}
		c.persistQueue = make(map[string]statusPersistRequest)
		c.persistOrder = nil
		c.persistMu.Unlock()
		for _, req := range queue {
			var err error
			switch req.kind {
			case persistDeleteNotice:
				_, err = c.db.Exec(`DELETE FROM workspace_notifications WHERE id=?`, req.id)
			default:
				err = c.persistNotice(req.notice)
			}
			if err != nil && c.logger != nil {
				c.logger.Warn("STATUS", "notification_persist_failed", "error", err.Error())
			}
		}
	}
}

func (c *StatusCenter) persistNotice(n ttypes.NotificationRecord) error {
	actions, _ := json.Marshal(n.Actions)
	resolved := int64(0)
	if !n.ResolvedAt.IsZero() {
		resolved = n.ResolvedAt.UnixNano()
	}
	_, err := c.db.Exec(`
		INSERT INTO workspace_notifications (
			id, dedup_key, owner_kind, owner_id, session_id, parent_id, severity,
			source, summary, detail, created_at, updated_at, resolved_at, occurrences,
			is_read, dismissed, transient, actions_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			dedup_key=excluded.dedup_key, severity=excluded.severity,
			summary=excluded.summary, detail=excluded.detail,
			updated_at=excluded.updated_at, resolved_at=excluded.resolved_at,
			occurrences=excluded.occurrences, is_read=excluded.is_read,
			dismissed=excluded.dismissed, transient=excluded.transient,
			actions_json=excluded.actions_json
	`, n.ID, n.DedupKey, n.Owner.Kind, n.Owner.ID, n.Owner.SessionID, n.Owner.ParentID,
		n.Severity, n.Source, n.Summary, n.Detail, n.CreatedAt.UnixNano(),
		n.UpdatedAt.UnixNano(), resolved, n.Occurrences, n.Read, n.Dismissed,
		n.Transient, string(actions))
	return err
}
