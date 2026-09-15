package localhistory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/sergi/go-diff/diffmatchpatch"
)

type persistedOperationPath struct {
	Sequence  int64
	Operation string
	AgentID   string
	Source    string
	Path      string
	Before    *Entry
	After     *Entry
}

// RecordAgentOperation writes exact preimages and intended postimages before a
// direct file tool mutates the filesystem. This is the selective-undo WAL: if
// persistence fails, the tool must not proceed.
func (s *Store) RecordAgentOperation(ctx context.Context, operation AgentOperation) error {
	if err := s.beginOperation(); err != nil {
		return err
	}
	defer s.endOperation()
	if operation.ID == "" || operation.SessionID == "" || operation.MessageID == "" || operation.AgentID == "" || operation.Source == "" || len(operation.Paths) == 0 {
		return errors.New("localhistory: invalid agent operation")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockMutation(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	type storedPath struct {
		path          string
		before, after *Entry
	}
	stored := make([]storedPath, 0, len(operation.Paths))
	seen := make(map[string]bool, len(operation.Paths))
	for _, operationPath := range operation.Paths {
		path, ok := cleanOperationPath(operationPath.Path)
		if !ok || seen[path] {
			return fmt.Errorf("localhistory: invalid or duplicate operation path %q", operationPath.Path)
		}
		seen[path] = true
		before, err := s.storeOperationState(ctx, operationPath.Before)
		if err != nil {
			return fmt.Errorf("localhistory: store before state for %s: %w", path, err)
		}
		after, err := s.storeOperationState(ctx, operationPath.After)
		if err != nil {
			return fmt.Errorf("localhistory: store after state for %s: %w", path, err)
		}
		if entryPointersEqual(before, after) {
			continue
		}
		stored = append(stored, storedPath{path: path, before: before, after: after})
	}
	if len(stored) == 0 {
		return errors.New("localhistory: agent operation has no state change")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM history_message_scopes WHERE session_id=? AND message_id=? AND state IN ('active','closed'))`, operation.SessionID, operation.MessageID).Scan(&active); err != nil {
		return err
	}
	if !active {
		return errors.New("localhistory: message history scope is unavailable")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO history_agent_operations(operation_id,session_id,message_id,agent_id,source,status,created_at) VALUES(?,?,?,?,?,'prepared',?)`, operation.ID, operation.SessionID, operation.MessageID, operation.AgentID, operation.Source, time.Now().UnixNano()); err != nil {
		return err
	}
	for index, path := range stored {
		beforeJSON, err := marshalOptionalEntry(path.before)
		if err != nil {
			return err
		}
		afterJSON, err := marshalOptionalEntry(path.after)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO history_agent_operation_paths(operation_id,ordinal,path,before_entry,after_entry) VALUES(?,?,?,?,?)`, operation.ID, index, path.path, beforeJSON, afterJSON); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func cleanOperationPath(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	native := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(native) {
		volume := filepath.VolumeName(native)
		if native == string(filepath.Separator) || native == volume+string(filepath.Separator) {
			return "", false
		}
		return filepath.ToSlash(native), true
	}
	return cleanRelativeHistoryPath(path)
}

func (s *Store) CompleteAgentOperation(operationID string, committed bool) error {
	if err := s.beginOperation(); err != nil {
		return err
	}
	defer s.endOperation()
	if operationID == "" {
		return errors.New("localhistory: operation ID is required")
	}
	status := "aborted"
	if committed {
		status = "committed"
	}
	result, err := s.db.Exec(`UPDATE history_agent_operations SET status=? WHERE operation_id=? AND status='prepared'`, status, operationID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("localhistory: prepared agent operation not found")
	}
	return nil
}

func (s *Store) markPreparedOperationsAmbiguous() error {
	_, err := s.db.Exec(`UPDATE history_agent_operations SET status='ambiguous' WHERE status='prepared'`)
	return err
}

func (s *Store) storeOperationState(ctx context.Context, state *OperationState) (*Entry, error) {
	if state == nil {
		return nil, nil
	}
	switch state.Type {
	case EntryFile:
		entry, err := s.storeBytes(ctx, state.Data, os.FileMode(state.Mode))
		if err != nil {
			return nil, err
		}
		entry.Mode = state.Mode
		return &entry, nil
	case EntryDirectory:
		entry := Entry{Type: EntryDirectory, Mode: state.Mode}
		return &entry, nil
	default:
		return nil, fmt.Errorf("unsupported operation state type %q", state.Type)
	}
}

func marshalOptionalEntry(entry *Entry) (string, error) {
	if entry == nil {
		return "", nil
	}
	data, err := json.Marshal(entry)
	return string(data), err
}

func unmarshalOptionalEntry(data string) (*Entry, error) {
	if data == "" {
		return nil, nil
	}
	var entry Entry
	if err := json.Unmarshal([]byte(data), &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

func (s *Store) operationPaths(sessionID, messageID, agentID string) ([]persistedOperationPath, error) {
	query := `SELECT o.sequence,o.operation_id,o.agent_id,o.source,p.path,p.before_entry,p.after_entry
FROM history_agent_operations o JOIN history_agent_operation_paths p ON p.operation_id=o.operation_id
WHERE o.session_id=? AND o.message_id=? AND o.status='committed'`
	args := []any{sessionID, messageID}
	if agentID != "" {
		query += ` AND o.agent_id=?`
		args = append(args, agentID)
	}
	query += ` ORDER BY o.sequence,p.ordinal`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []persistedOperationPath
	for rows.Next() {
		var operation persistedOperationPath
		var beforeJSON, afterJSON string
		if err := rows.Scan(&operation.Sequence, &operation.Operation, &operation.AgentID, &operation.Source, &operation.Path, &beforeJSON, &afterJSON); err != nil {
			return nil, err
		}
		operation.Before, err = unmarshalOptionalEntry(beforeJSON)
		if err != nil {
			return nil, fmt.Errorf("localhistory: decode operation preimage: %w", err)
		}
		operation.After, err = unmarshalOptionalEntry(afterJSON)
		if err != nil {
			return nil, fmt.Errorf("localhistory: decode operation postimage: %w", err)
		}
		out = append(out, operation)
	}
	return out, rows.Err()
}

func sortedOperationPathNames(operations map[string][]persistedOperationPath) []string {
	paths := make([]string, 0, len(operations))
	for path := range operations {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// AgentUndoableScopes returns closed scopes that still produce a non-empty
// selective inverse for the requested agent. Evaluating current state here
// lets repeated /undo advance past a scope that was already fully restored.
func (s *Store) AgentUndoableScopes(sessionID, agentID string, limit int) ([]MessageScope, error) {
	if err := s.beginOperation(); err != nil {
		return nil, err
	}
	defer s.endOperation()
	if sessionID == "" || agentID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockMutation(context.Background())
	if err != nil {
		return nil, err
	}
	defer unlock()
	rows, err := s.db.Query(`SELECT s.message_id FROM history_message_scopes s
JOIN history_agent_operations o ON o.session_id=s.session_id AND o.message_id=s.message_id
WHERE s.session_id=? AND o.agent_id=? AND o.status='committed' AND s.state='closed' AND s.after_snapshot_id<>''
GROUP BY s.message_id ORDER BY MAX(o.created_at) DESC LIMIT ?`, sessionID, agentID, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]MessageScope, 0, len(ids))
	for _, messageID := range ids {
		scope, ok, err := s.MessageScope(sessionID, messageID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		changes, err := s.operationRestoreChanges(context.Background(), sessionID, messageID, agentID)
		if err != nil {
			return nil, err
		}
		if len(changes) == 0 {
			continue
		}
		scope.AgentID = agentID
		scope.ChangedPaths = changePaths(changes)
		result = append(result, scope)
	}
	return result, nil
}

func (s *Store) PlanMessageRestore(ctx context.Context, sessionID, messageID string, opts SnapshotOptions) (RestorePlan, error) {
	return s.planOperationRestore(ctx, sessionID, messageID, "", opts)
}

// PlanAgentRestore selectively reverses one agent's direct file operations.
// Every target is an operation preimage, never the message-wide workspace
// snapshot, so unrelated changes that happened before the operation survive.
func (s *Store) PlanAgentRestore(ctx context.Context, sessionID, messageID, agentID string, opts SnapshotOptions) (RestorePlan, error) {
	if agentID == "" {
		return RestorePlan{}, errors.New("localhistory: agent ID is required")
	}
	return s.planOperationRestore(ctx, sessionID, messageID, agentID, opts)
}

func (s *Store) planOperationRestore(ctx context.Context, sessionID, messageID, agentID string, opts SnapshotOptions) (RestorePlan, error) {
	if err := s.beginOperation(); err != nil {
		return RestorePlan{}, err
	}
	defer s.endOperation()
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockMutation(ctx)
	if err != nil {
		return RestorePlan{}, err
	}
	defer unlock()
	scope, ok, err := s.MessageScope(sessionID, messageID)
	if err != nil {
		return RestorePlan{}, err
	}
	if !ok {
		return RestorePlan{}, errors.New("localhistory: message has no history scope")
	}
	if scope.State != "closed" || scope.AfterSnapshot == "" {
		return RestorePlan{}, errors.New("localhistory: message history is not complete")
	}
	changes, err := s.operationRestoreChanges(ctx, sessionID, messageID, agentID)
	if err != nil {
		return RestorePlan{}, err
	}
	if len(changes) == 0 {
		return RestorePlan{}, errors.New("localhistory: no remaining direct file changes for this agent and message")
	}
	plan := RestorePlan{
		Version: FormatVersion, TargetSnapshotID: scope.BeforeSnapshot, CreatedAt: time.Now().UnixNano(),
		CaptureOptions: opts, SessionID: sessionID, MessageID: messageID, AgentID: agentID, Changes: changes,
	}
	if err := s.savePlan(&plan); err != nil {
		return RestorePlan{}, err
	}
	return plan, nil
}

func (s *Store) operationRestoreChanges(ctx context.Context, sessionID, messageID, agentID string) ([]Change, error) {
	all, err := s.operationPaths(sessionID, messageID, "")
	if err != nil {
		return nil, err
	}
	targetByPath := make(map[string][]persistedOperationPath)
	targetOperationPaths := make(map[string][]string)
	for _, operation := range all {
		if agentID == "" || operation.AgentID == agentID {
			targetByPath[operation.Path] = append(targetByPath[operation.Path], operation)
			targetOperationPaths[operation.Operation] = append(targetOperationPaths[operation.Operation], operation.Path)
		}
	}
	atomicGroups := operationAtomicGroups(targetOperationPaths)
	changes := make([]Change, 0, len(targetByPath))
	for _, path := range sortedOperationPathNames(targetByPath) {
		targetOps := targetByPath[path]
		current, err := s.operationCurrentEntry(ctx, path)
		if err != nil {
			return nil, err
		}
		working := cloneEntry(current)
		exact := cloneEntry(targetOps[0].Before)
		autoMerged := false
		conflict := ""
		exactAllowed := true
		for index := len(targetOps) - 1; index >= 0; index-- {
			operation := targetOps[index]
			laterAny, laterForeign, laterHistorical, err := s.hasLaterUnselectedOperation(path, operation.Sequence, sessionID, messageID, agentID)
			if err != nil {
				return nil, err
			}
			if laterForeign {
				conflict = "another agent changed this path after the selected operation; undo that agent first"
				exactAllowed = false
				break
			}
			if operationStateMatches(working, operation.After) {
				working = cloneEntry(operation.Before)
				continue
			}
			if entryPointersEqual(working, operation.Before) {
				continue
			}
			if laterAny {
				conflict = "a later agent operation still owns this path; undo newer work first"
				exactAllowed = false
				break
			}
			if laterHistorical {
				conflict = "a later operation on this path was restored and changed again"
				exactAllowed = false
				break
			}
			merged, ok := s.inverseMergeEntry(ctx, path, operation.Before, operation.After, working)
			if !ok {
				conflict = "path changed after this operation"
				break
			}
			working = merged
			autoMerged = true
		}
		if conflict != "" {
			working = cloneEntry(exact)
		}
		if conflict == "" && entryPointersEqual(current, working) {
			continue
		}
		change := Change{Path: path, Current: cloneEntry(current), Target: cloneEntry(working), ConflictReason: conflict, ExactAllowed: conflict != "" && exactAllowed, AutoMerged: autoMerged && conflict == "", AtomicPaths: append([]string(nil), atomicGroups[path]...)}
		change.ParentIdentity, err = s.pathParentIdentity(path)
		if err != nil {
			change.UnsafeReason = "cannot verify restore parent: " + err.Error()
			change.ExactAllowed = false
		}
		if filepath.IsAbs(filepath.FromSlash(path)) && (!operationExternalStateSupported(current) || !operationExternalStateSupported(working)) {
			change.UnsafeReason = "external operation changed to an unsupported filesystem entry"
			change.ExactAllowed = false
		}
		if autoMerged && conflict == "" {
			change.ExactTarget = cloneEntry(exact)
		}
		switch {
		case working == nil && current == nil:
			change.Action = ChangeRestore
		case working == nil:
			change.Action = ChangeDelete
		case current == nil:
			change.Action = ChangeCreate
		default:
			change.Action = ChangeRestore
		}
		change.TextDiff = s.changeDiff(path, change.Target, change.Current)
		changes = append(changes, change)
	}
	changeByPath := make(map[string]*Change, len(changes))
	for index := range changes {
		changeByPath[changes[index].Path] = &changes[index]
	}
	for _, change := range changes {
		if len(change.AtomicPaths) <= 1 || (change.ConflictReason == "" && change.UnsafeReason == "") {
			continue
		}
		for _, path := range change.AtomicPaths {
			peer := changeByPath[path]
			if peer == nil {
				continue
			}
			if peer.ConflictReason == "" {
				peer.ConflictReason = "multi-path operation has an unresolved endpoint"
			}
			peer.ExactAllowed = false
			peer.AutoMerged = false
		}
	}
	s.guardDirectoryDeletions(changes)
	linkMovePeers(changes)
	return changes, nil
}

func operationExternalStateSupported(entry *Entry) bool {
	return entry == nil || entry.Type == EntryFile || entry.Type == EntryDirectory
}

func operationStateMatches(actual, intended *Entry) bool {
	if actual != nil && intended != nil && actual.Type == EntryDirectory && intended.Type == EntryDirectory {
		return true
	}
	return entryPointersEqual(actual, intended)
}

func operationAtomicGroups(operationPaths map[string][]string) map[string][]string {
	adjacent := make(map[string]map[string]bool)
	for _, paths := range operationPaths {
		paths = sortedUnique(paths)
		for _, path := range paths {
			if adjacent[path] == nil {
				adjacent[path] = make(map[string]bool)
			}
			for _, peer := range paths {
				adjacent[path][peer] = true
			}
		}
	}
	result := make(map[string][]string, len(adjacent))
	for path := range adjacent {
		seen := map[string]bool{path: true}
		queue := []string{path}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for peer := range adjacent[current] {
				if !seen[peer] {
					seen[peer] = true
					queue = append(queue, peer)
				}
			}
		}
		group := make([]string, 0, len(seen))
		for peer := range seen {
			group = append(group, peer)
		}
		sort.Strings(group)
		result[path] = group
	}
	return result
}

func cloneEntry(entry *Entry) *Entry {
	if entry == nil {
		return nil
	}
	copy := *entry
	return &copy
}

func (s *Store) hasLaterUnselectedOperation(path string, sequence int64, sessionID, messageID, agentID string) (bool, bool, bool, error) {
	query := `SELECT o.session_id,o.message_id,o.agent_id,o.created_at FROM history_agent_operations o
JOIN history_agent_operation_paths p ON p.operation_id=o.operation_id
WHERE p.path=? AND o.sequence>? AND o.status='committed'
AND NOT (o.session_id=? AND o.message_id=?`
	args := []any{path, sequence, sessionID, messageID}
	if agentID != "" {
		query += ` AND o.agent_id=?`
		args = append(args, agentID)
	}
	query += `)`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return false, false, false, err
	}
	type laterOperation struct {
		sessionID, messageID, agentID string
		createdAt                     int64
	}
	var later []laterOperation
	for rows.Next() {
		var operation laterOperation
		if err := rows.Scan(&operation.sessionID, &operation.messageID, &operation.agentID, &operation.createdAt); err != nil {
			_ = rows.Close()
			return false, false, false, err
		}
		later = append(later, operation)
	}
	if err := rows.Close(); err != nil {
		return false, false, false, err
	}
	if err := rows.Err(); err != nil {
		return false, false, false, err
	}
	anyLater, foreign, historical := false, false, false
	for _, operation := range later {
		undone, err := s.operationPathWasRestored(operation.sessionID, operation.messageID, operation.agentID, path, operation.createdAt)
		if err != nil {
			return false, false, false, err
		}
		if undone {
			historical = true
			continue
		}
		anyLater = true
		if agentID == "" || operation.agentID != agentID {
			foreign = true
		}
	}
	return anyLater, foreign, historical, nil
}

func (s *Store) operationPathWasRestored(sessionID, messageID, agentID, path string, operationCreatedAt int64) (bool, error) {
	rows, err := s.db.Query(`SELECT paths FROM history_restore_events WHERE session_id=? AND message_id=? AND agent_id=? AND created_at>? ORDER BY created_at DESC`, sessionID, messageID, agentID, operationCreatedAt)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var pathsJSON string
		if err := rows.Scan(&pathsJSON); err != nil {
			return false, err
		}
		var paths []string
		if err := json.Unmarshal([]byte(pathsJSON), &paths); err != nil {
			return false, err
		}
		for _, restored := range paths {
			if restored == path {
				return true, nil
			}
		}
	}
	return false, rows.Err()
}

const maxSelectiveMergeBytes int64 = 8 << 20

func (s *Store) inverseMergeEntry(ctx context.Context, path string, before, after, current *Entry) (*Entry, bool) {
	if before == nil || after == nil || current == nil || before.Type != EntryFile || after.Type != EntryFile || current.Type != EntryFile {
		return nil, false
	}
	if before.Size > maxSelectiveMergeBytes || after.Size > maxSelectiveMergeBytes || current.Size > maxSelectiveMergeBytes {
		return nil, false
	}
	beforeData, err := s.readFileBytes(before.Hash)
	if err != nil {
		return nil, false
	}
	afterData, err := s.readFileBytes(after.Hash)
	if err != nil {
		return nil, false
	}
	currentData, err := s.readFileBytes(current.Hash)
	if err != nil {
		workspacePath, pathErr := s.workspacePath(path)
		if pathErr != nil {
			return nil, false
		}
		currentData, err = os.ReadFile(workspacePath)
		if err != nil {
			return nil, false
		}
	}
	if !textMergeable(beforeData, afterData, currentData) {
		return nil, false
	}
	dmp := diffmatchpatch.New()
	if editsOverlap(operationEdits(dmp.DiffMain(string(afterData), string(beforeData), false)), operationEdits(dmp.DiffMain(string(afterData), string(currentData), false))) {
		return nil, false
	}
	patches := dmp.PatchMake(string(afterData), string(beforeData))
	merged, applied := dmp.PatchApply(patches, string(currentData))
	for _, ok := range applied {
		if !ok {
			return nil, false
		}
	}
	entry, err := s.storeBytes(ctx, []byte(merged), os.FileMode(current.Mode))
	if err != nil {
		return nil, false
	}
	entry.Mode = current.Mode
	return &entry, true
}

func (s *Store) operationCurrentEntry(_ context.Context, path string) (*Entry, error) {
	return s.currentEntry(path)
}

type textEditSpan struct {
	start int
	end   int
}

func operationEdits(diffs []diffmatchpatch.Diff) []textEditSpan {
	position := 0
	var spans []textEditSpan
	for index := 0; index < len(diffs); {
		if diffs[index].Type == diffmatchpatch.DiffEqual {
			position += utf8.RuneCountInString(diffs[index].Text)
			index++
			continue
		}
		start := position
		for index < len(diffs) && diffs[index].Type != diffmatchpatch.DiffEqual {
			if diffs[index].Type == diffmatchpatch.DiffDelete {
				position += utf8.RuneCountInString(diffs[index].Text)
			}
			index++
		}
		spans = append(spans, textEditSpan{start: start, end: position})
	}
	return spans
}

func editsOverlap(left, right []textEditSpan) bool {
	for _, a := range left {
		for _, b := range right {
			if a.start == a.end && b.start == b.end {
				if a.start == b.start {
					return true
				}
				continue
			}
			if a.start == a.end {
				if a.start >= b.start && a.start <= b.end {
					return true
				}
				continue
			}
			if b.start == b.end {
				if b.start >= a.start && b.start <= a.end {
					return true
				}
				continue
			}
			if a.start < b.end && b.start < a.end {
				return true
			}
		}
	}
	return false
}

func textMergeable(entries ...[]byte) bool {
	for _, data := range entries {
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return false
		}
	}
	return true
}

func linkMovePeers(changes []Change) {
	deleted := make(map[string]int)
	for index, change := range changes {
		if change.Action == ChangeDelete && change.Current != nil && change.Current.Type == EntryFile {
			deleted[change.Current.Hash] = index
		}
	}
	for index := range changes {
		change := &changes[index]
		if change.Action == ChangeCreate && change.Target != nil && change.Target.Type == EntryFile {
			if deletedIndex, ok := deleted[change.Target.Hash]; ok {
				change.MovePeer = changes[deletedIndex].Path
				changes[deletedIndex].MovePeer = change.Path
			}
		}
	}
}
