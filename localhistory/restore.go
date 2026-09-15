package localhistory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	crudediff "crude/diff"
)

func (s *Store) PlanRestore(ctx context.Context, targetSnapshotID string, opts SnapshotOptions) (RestorePlan, error) {
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
	return s.planRestore(ctx, targetSnapshotID, opts)
}

func (s *Store) PlanRestorePaths(ctx context.Context, targetSnapshotID string, opts SnapshotOptions, paths []string) (RestorePlan, error) {
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
	plan, err := s.planRestore(ctx, targetSnapshotID, opts)
	if err != nil {
		return RestorePlan{}, err
	}
	wanted := make(map[string]bool, len(paths))
	for _, path := range paths {
		wanted[filepath.ToSlash(filepath.Clean(path))] = true
	}
	filtered := make([]Change, 0, len(paths))
	for _, change := range plan.Changes {
		if wanted[change.Path] {
			filtered = append(filtered, change)
		}
	}
	plan.ID = ""
	plan.Changes = filtered
	plan.CreatedAt = time.Now().UnixNano()
	if err := s.savePlan(&plan); err != nil {
		return RestorePlan{}, err
	}
	return plan, nil
}

func (s *Store) planRestore(ctx context.Context, targetSnapshotID string, opts SnapshotOptions) (RestorePlan, error) {
	target, err := s.LoadManifest(targetSnapshotID)
	if err != nil {
		return RestorePlan{}, err
	}
	current, err := s.scan(ctx, opts, false)
	if err != nil {
		return RestorePlan{}, err
	}
	for _, path := range target.TrackedPaths {
		if !filepath.IsAbs(filepath.FromSlash(path)) {
			continue
		}
		entry, err := s.currentEntry(path)
		if err != nil {
			return RestorePlan{}, err
		}
		if entry != nil {
			current[path] = *entry
		}
	}
	paths := make(map[string]struct{}, len(target.Entries)+len(current))
	for path := range target.Entries {
		paths[path] = struct{}{}
	}
	for path := range current {
		paths[path] = struct{}{}
	}
	for _, path := range target.TrackedPaths {
		paths[path] = struct{}{}
	}
	changes := make([]Change, 0)
	for path := range paths {
		targetEntry, targetOK := target.Entries[path]
		currentEntry, currentOK := current[path]
		if targetOK && currentOK && entriesEqual(targetEntry, currentEntry) {
			continue
		}
		change := Change{Path: path}
		if filepath.IsAbs(filepath.FromSlash(path)) {
			change.ParentIdentity, err = s.pathParentIdentity(path)
			if err != nil {
				change.UnsafeReason = "cannot verify restore parent: " + err.Error()
			}
		}
		switch {
		case !targetOK:
			change.Action = ChangeDelete
		case !currentOK:
			change.Action = ChangeCreate
		default:
			change.Action = ChangeRestore
		}
		if targetOK {
			entry := targetEntry
			change.Target = &entry
			if entry.Type == EntrySymlink && !s.safeSymlink(path, entry.SymlinkTarget) {
				change.UnsafeReason = "symlink target escapes workspace"
			}
			if entry.Type == EntrySpecial {
				change.UnsafeReason = "special filesystem entries are metadata-only"
			}
		}
		if currentOK {
			entry := currentEntry
			change.Current = &entry
		}
		change.TextDiff = s.changeDiff(path, change.Target, change.Current)
		changes = append(changes, change)
	}
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
	s.guardDirectoryDeletions(changes)
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	plan := RestorePlan{
		Version: FormatVersion, TargetSnapshotID: target.ID, CreatedAt: time.Now().UnixNano(),
		CaptureOptions: opts, Changes: changes,
	}
	if err := s.savePlan(&plan); err != nil {
		return RestorePlan{}, err
	}
	return plan, nil
}

func (s *Store) guardDirectoryDeletions(changes []Change) {
	deletions := make(map[string]bool)
	for _, change := range changes {
		if change.Target == nil {
			deletions[change.Path] = true
		}
	}
	for index := range changes {
		change := &changes[index]
		if change.Current == nil || change.Current.Type != EntryDirectory || (change.Target != nil && change.Target.Type == EntryDirectory) {
			continue
		}
		root, err := s.workspacePath(change.Path)
		if err != nil {
			change.UnsafeReason = err.Error()
			continue
		}
		external := filepath.IsAbs(filepath.FromSlash(change.Path))
		err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == root {
				return nil
			}
			key := filepath.ToSlash(path)
			if !external {
				rel, err := filepath.Rel(s.workspaceRoot, path)
				if err != nil {
					return err
				}
				key = filepath.ToSlash(rel)
			}
			if !deletions[key] {
				return errors.New("directory contains data outside the restore selection")
			}
			return nil
		})
		if err != nil {
			change.ConflictReason = err.Error()
			change.ExactAllowed = false
		}
	}
}

func (s *Store) changeDiff(path string, target, current *Entry) string {
	const maxPreview = 256 * 1024
	if (target != nil && target.Type != EntryFile) || (current != nil && current.Type != EntryFile) {
		return "Type or directory metadata change"
	}
	var before, after []byte
	if current != nil {
		p, err := s.workspacePath(path)
		if err != nil {
			return ""
		}
		before, err = os.ReadFile(p)
		if err != nil || len(before) > maxPreview {
			return "Binary or large file change"
		}
	}
	if target != nil {
		data, err := s.readFileBytes(target.Hash)
		if err != nil || len(data) > maxPreview {
			return "Binary or large file change"
		}
		after = data
	}
	if !utf8.Valid(before) || !utf8.Valid(after) || bytes.IndexByte(before, 0) >= 0 || bytes.IndexByte(after, 0) >= 0 {
		return "Binary file replacement"
	}
	diff, _, _ := crudediff.GenerateDiff(string(before), string(after), path)
	return diff
}

func entriesEqual(a, b Entry) bool {
	return a.Type == b.Type && a.Mode == b.Mode && a.Hash == b.Hash &&
		a.Size == b.Size && a.SymlinkTarget == b.SymlinkTarget && a.HardlinkTarget == b.HardlinkTarget && a.SpecialMode == b.SpecialMode
}

func (s *Store) savePlan(plan *RestorePlan) error {
	plan.ID = ""
	payload, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	plan.ID = sha256Hex(payload)
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	if err := writeImmutable(filepath.Join(s.root, "plans", plan.ID+".json"), data, 0o600); err != nil {
		return err
	}
	return s.recordPlan(*plan)
}

func (s *Store) LoadPlan(id string) (RestorePlan, error) {
	if !validID(id) {
		return RestorePlan{}, errors.New("localhistory: invalid restore plan ID")
	}
	data, err := os.ReadFile(filepath.Join(s.root, "plans", id+".json"))
	if err != nil {
		return RestorePlan{}, err
	}
	var plan RestorePlan
	if err := json.Unmarshal(data, &plan); err != nil || plan.Version != FormatVersion || plan.ID != id {
		return RestorePlan{}, errors.New("localhistory: corrupt restore plan")
	}
	plan.ID = ""
	payload, err := json.Marshal(plan)
	if err != nil {
		return RestorePlan{}, err
	}
	if sha256Hex(payload) != id {
		return RestorePlan{}, errors.New("localhistory: restore plan hash mismatch")
	}
	plan.ID = id
	return plan, nil
}

func (s *Store) ApplyRestore(ctx context.Context, planID string, selected []string) (RestoreResult, error) {
	choices := make([]RestoreChoice, 0, len(selected))
	for _, path := range selected {
		choices = append(choices, RestoreChoice{Path: path})
	}
	return s.ApplyRestoreChoices(ctx, planID, choices)
}

func (s *Store) ApplyRestoreChoices(ctx context.Context, planID string, selected []RestoreChoice) (RestoreResult, error) {
	if err := s.beginOperation(); err != nil {
		return RestoreResult{}, err
	}
	defer s.endOperation()
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockMutation(ctx)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("localhistory: acquire restore lock: %w", err)
	}
	defer unlock()
	plan, err := s.LoadPlan(planID)
	if err != nil {
		return RestoreResult{}, err
	}
	changes, err := selectedChanges(plan, selected)
	if err != nil {
		return RestoreResult{}, err
	}
	if len(changes) == 0 {
		return RestoreResult{}, errors.New("localhistory: restore plan has no selected safe changes")
	}
	if err := s.validateChanges(ctx, changes); err != nil {
		return RestoreResult{}, err
	}
	if err := s.validateHardlinkClosure(changes); err != nil {
		return RestoreResult{}, err
	}
	paths := changePaths(changes)
	safetyOpts := plan.CaptureOptions
	safetyOpts.Kind = "safety_before_restore"
	safetyOpts.Label = "Safety snapshot before restore " + plan.ID[:12]
	safetyOpts.ParentID = ""
	safetyOpts.IncludePaths = append([]string(nil), paths...)
	safety, err := s.snapshotIncludingPaths(ctx, safetyOpts, paths)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("localhistory: capture safety snapshot: %w", err)
	}
	target, err := s.LoadManifest(plan.TargetSnapshotID)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := s.beginRestoreTransaction(plan.ID, safety.ID, paths, changes); err != nil {
		return RestoreResult{}, fmt.Errorf("localhistory: persist restore intent: %w", err)
	}
	rollback := func(cause error, applied []Change) (RestoreResult, error) {
		rollbackChanges, conflicts, inspectErr := s.safeRollbackChanges(safety, applied)
		if inspectErr != nil {
			return RestoreResult{}, fmt.Errorf("localhistory: %v; inspect rollback state: %w", cause, inspectErr)
		}
		rollbackErr := s.applySnapshotPaths(context.Background(), rollbackChanges, false)
		if rollbackErr != nil {
			return RestoreResult{}, fmt.Errorf("localhistory: %v; rollback failed: %w", cause, rollbackErr)
		}
		if len(conflicts) > 0 {
			if err := s.finishRestoreTransaction(plan.ID, "conflicted"); err != nil {
				return RestoreResult{}, fmt.Errorf("localhistory: %v; newer writes preserved at %s; transaction update failed: %w", cause, strings.Join(conflicts, ", "), err)
			}
			return RestoreResult{}, fmt.Errorf("localhistory: %v; rolled back unchanged restore paths but preserved newer writes at %s", cause, strings.Join(conflicts, ", "))
		}
		if err := s.finishRestoreTransaction(plan.ID, "rolled_back"); err != nil {
			return RestoreResult{}, fmt.Errorf("localhistory: %v; rollback succeeded but transaction update failed: %w", cause, err)
		}
		return RestoreResult{}, fmt.Errorf("localhistory: %w; workspace was rolled back", cause)
	}
	if err := s.applySnapshotPaths(ctx, changes, true); err != nil {
		if !restoreApplyMutated(err) {
			if finishErr := s.finishRestoreTransaction(plan.ID, "rolled_back"); finishErr != nil {
				return RestoreResult{}, fmt.Errorf("localhistory: restore rejected: %v; transaction update failed: %w", err, finishErr)
			}
			return RestoreResult{}, err
		}
		return rollback(fmt.Errorf("restore failed: %w", err), restoreAppliedChanges(err))
	}
	if err := s.validateAppliedChanges(ctx, changes); err != nil {
		return rollback(fmt.Errorf("restore verification failed: %w", err), changes)
	}
	afterOpts := plan.CaptureOptions
	afterOpts.Kind = "after_restore"
	afterOpts.Label = "After restore " + plan.ID[:12]
	afterOpts.ParentID = safety.ID
	afterOpts.IncludePaths = append([]string(nil), paths...)
	after, err := s.snapshotIncludingPaths(ctx, afterOpts, paths)
	if err != nil {
		return rollback(fmt.Errorf("after-restore snapshot failed: %w", err), changes)
	}
	result := RestoreResult{
		PlanID: plan.ID, TargetSnapshotID: target.ID, SafetySnapshotID: safety.ID,
		AfterSnapshotID: after.ID, SessionID: plan.SessionID, MessageID: plan.MessageID,
		AgentID: plan.AgentID, Paths: paths,
	}
	if err := s.recordRestore(result); err != nil {
		return rollback(fmt.Errorf("record restore event failed: %w", err), changes)
	}
	return result, nil
}

func (s *Store) safeRollbackChanges(safety Manifest, applied []Change) ([]Change, []string, error) {
	changes := make([]Change, 0, len(applied))
	var conflicts []string
	for _, appliedChange := range applied {
		current, err := s.currentEntry(appliedChange.Path)
		if err != nil {
			return nil, nil, err
		}
		if !entryPointersEqual(current, appliedChange.Target) {
			conflicts = append(conflicts, appliedChange.Path)
			continue
		}
		change := Change{Path: appliedChange.Path, Current: cloneEntry(current), Action: ChangeDelete}
		if entry, ok := safety.Entries[appliedChange.Path]; ok {
			entry := entry
			change.Target = &entry
			change.Action = ChangeRestore
		}
		changes = append(changes, change)
	}
	sort.Strings(conflicts)
	return changes, conflicts, nil
}

func (s *Store) beginRestoreTransaction(planID, safetySnapshotID string, paths []string, changes ...[]Change) error {
	payload, err := json.Marshal(paths)
	if err != nil {
		return err
	}
	appliedEntries := make(map[string]*Entry)
	if len(changes) > 0 {
		for _, change := range changes[0] {
			appliedEntries[change.Path] = cloneEntry(change.Target)
		}
	}
	entriesJSON, err := json.Marshal(appliedEntries)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO history_restore_transactions(plan_id,safety_snapshot_id,paths,applied_entries,state,updated_at)
VALUES(?,?,?,?,'applying',?) ON CONFLICT(plan_id) DO UPDATE SET safety_snapshot_id=excluded.safety_snapshot_id,
paths=excluded.paths,applied_entries=excluded.applied_entries,state='applying',updated_at=excluded.updated_at`, planID, safetySnapshotID, string(payload), string(entriesJSON), time.Now().UnixNano())
	return err
}

func (s *Store) finishRestoreTransaction(planID, state string) error {
	_, err := s.db.Exec(`UPDATE history_restore_transactions SET state=?,updated_at=? WHERE plan_id=?`, state, time.Now().UnixNano(), planID)
	return err
}

func (s *Store) recoverInterruptedRestores() error {
	rows, err := s.db.Query(`SELECT plan_id,safety_snapshot_id,paths,applied_entries FROM history_restore_transactions WHERE state='applying' ORDER BY updated_at`)
	if err != nil {
		return err
	}
	type pending struct {
		planID, safetyID string
		paths            []string
		applied          map[string]*Entry
	}
	var restores []pending
	for rows.Next() {
		var item pending
		var pathsJSON, appliedJSON string
		if err := rows.Scan(&item.planID, &item.safetyID, &pathsJSON, &appliedJSON); err != nil {
			_ = rows.Close()
			return err
		}
		if err := json.Unmarshal([]byte(pathsJSON), &item.paths); err != nil {
			_ = rows.Close()
			return err
		}
		if err := json.Unmarshal([]byte(appliedJSON), &item.applied); err != nil {
			_ = rows.Close()
			return err
		}
		restores = append(restores, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range restores {
		safety, err := s.LoadManifest(item.safetyID)
		if err != nil {
			return err
		}
		rollback := make([]Change, 0, len(item.paths))
		var conflicts []string
		for _, path := range item.paths {
			current, err := s.currentEntryExpected(path, item.applied[path])
			if err != nil {
				return err
			}
			var safetyEntry *Entry
			if entry, ok := safety.Entries[path]; ok {
				entry := entry
				safetyEntry = &entry
			}
			if entryPointersEqual(current, safetyEntry) {
				continue
			}
			intended, ok := item.applied[path]
			if !ok || !entryPointersEqual(current, intended) {
				conflicts = append(conflicts, path)
				continue
			}
			action := ChangeRestore
			if safetyEntry == nil {
				action = ChangeDelete
			}
			rollback = append(rollback, Change{Path: path, Current: cloneEntry(current), Target: safetyEntry, Action: action})
		}
		if err := s.applySnapshotPaths(context.Background(), rollback, false); err != nil {
			return fmt.Errorf("recover restore %s: %w", item.planID, err)
		}
		state := "rolled_back"
		if len(conflicts) > 0 {
			state = "conflicted"
		}
		if err := s.finishRestoreTransaction(item.planID, state); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) validateAppliedChanges(ctx context.Context, changes []Change) error {
	for _, change := range changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if change.Target != nil && change.Target.Type == EntryHardlink {
			if err := s.validateHardlinkTarget(change.Path, *change.Target); err != nil {
				return err
			}
			continue
		}
		current, err := s.currentEntryExpected(change.Path, change.Target)
		if err != nil {
			return err
		}
		if !entryPointersEqual(current, change.Target) {
			return fmt.Errorf("%s does not match the immutable restore target", change.Path)
		}
	}
	return nil
}

func (s *Store) validateChanges(ctx context.Context, changes []Change) error {
	for _, change := range changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if change.UnsafeReason != "" {
			return fmt.Errorf("localhistory: unsafe restore path %s: %s", change.Path, change.UnsafeReason)
		}
		if err := s.validatePathParentIdentity(change); err != nil {
			return err
		}
		current, err := s.currentEntryExpected(change.Path, change.Current)
		if err != nil {
			return err
		}
		if !entryPointersEqual(current, change.Current) {
			return fmt.Errorf("localhistory: %s changed after preview", change.Path)
		}
	}
	return nil
}

func selectedChanges(plan RestorePlan, selected []RestoreChoice) ([]Change, error) {
	byPath := make(map[string]Change, len(plan.Changes))
	for _, change := range plan.Changes {
		byPath[change.Path] = change
	}
	if len(selected) == 0 {
		changes := make([]Change, 0, len(plan.Changes))
		for _, change := range plan.Changes {
			if change.UnsafeReason == "" && change.ConflictReason == "" {
				changes = append(changes, change)
			}
		}
		return changes, nil
	}
	changes := make([]Change, 0, len(selected))
	seen := make(map[string]bool, len(selected))
	for _, choice := range selected {
		path := choice.Path
		path = filepath.ToSlash(filepath.Clean(path))
		change, ok := byPath[path]
		if !ok {
			return nil, fmt.Errorf("localhistory: path %s is not in restore plan", path)
		}
		if change.ConflictReason != "" && !choice.Exact {
			return nil, fmt.Errorf("localhistory: %s requires explicit exact replacement: %s", path, change.ConflictReason)
		}
		if change.ConflictReason != "" && !change.ExactAllowed {
			return nil, fmt.Errorf("localhistory: conflict for %s cannot be overridden", path)
		}
		if !seen[path] {
			if choice.Exact && change.ExactTarget != nil {
				exact := *change.ExactTarget
				change.Target = &exact
				change.AutoMerged = false
			}
			changes = append(changes, change)
			seen[path] = true
		}
	}
	selectedPaths := make(map[string]bool, len(selected))
	for _, choice := range selected {
		selectedPaths[filepath.ToSlash(filepath.Clean(choice.Path))] = true
	}
	for _, change := range changes {
		for _, peer := range change.AtomicPaths {
			if _, planned := byPath[peer]; planned && !selectedPaths[peer] {
				return nil, fmt.Errorf("localhistory: %s belongs to an atomic operation with %s", change.Path, peer)
			}
		}
	}
	return changes, nil
}

func (s *Store) currentEntry(rel string) (*Entry, error) {
	path, err := s.workspacePath(rel)
	if err != nil {
		return nil, err
	}
	if filepath.IsAbs(filepath.FromSlash(rel)) {
		exact, err := exactAbsolutePathExists(path)
		if err != nil {
			return nil, err
		}
		if !exact {
			return nil, nil
		}
	} else {
		exact, err := exactRelativePathExists(s.workspaceRoot, rel)
		if err != nil {
			return nil, err
		}
		if !exact {
			return nil, nil
		}
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entry Entry
	switch {
	case info.IsDir():
		entry = Entry{Type: EntryDirectory, Mode: storedMode(info.Mode())}
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return nil, err
		}
		entry = Entry{Type: EntrySymlink, Mode: storedMode(info.Mode()), SymlinkTarget: target}
	case info.Mode().IsRegular():
		entry, err = hashRegularFile(path, info.Mode())
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("localhistory: unsupported current path %s", rel)
	}
	return &entry, nil
}

func exactAbsolutePathExists(path string) (bool, error) {
	volume := filepath.VolumeName(path)
	root := volume + string(filepath.Separator)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false, err
	}
	return exactRelativePathExists(root, filepath.ToSlash(rel))
}

func (s *Store) currentEntryExpected(rel string, expected *Entry) (*Entry, error) {
	current, err := s.currentEntry(rel)
	if err != nil || expected == nil || expected.Type != EntryHardlink {
		return current, err
	}
	path, err := s.workspacePath(rel)
	if err != nil {
		return nil, err
	}
	target, err := s.workspacePath(expected.HardlinkTarget)
	if err != nil {
		return nil, err
	}
	pathInfo, pathErr := os.Lstat(path)
	targetInfo, targetErr := os.Lstat(target)
	if pathErr != nil || targetErr != nil || !pathInfo.Mode().IsRegular() || !targetInfo.Mode().IsRegular() || !os.SameFile(pathInfo, targetInfo) {
		return current, nil
	}
	actual, err := hashRegularFile(path, pathInfo.Mode())
	if err != nil {
		return nil, err
	}
	actual.Type = EntryHardlink
	actual.HardlinkTarget = expected.HardlinkTarget
	return &actual, nil
}

func exactRelativePathExists(root, rel string) (bool, error) {
	current := root
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		entries, err := os.ReadDir(current)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		found := false
		for _, entry := range entries {
			if entry.Name() == part {
				current = filepath.Join(current, entry.Name())
				found = true
				break
			}
		}
		if !found {
			return false, nil
		}
	}
	return true, nil
}

func entryPointersEqual(a, b *Entry) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return entriesEqual(*a, *b)
}

func (s *Store) workspacePath(rel string) (string, error) {
	native := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(native) {
		volume := filepath.VolumeName(native)
		if native == string(filepath.Separator) || native == volume+string(filepath.Separator) {
			return "", errors.New("localhistory: refusing filesystem-root operation")
		}
		if err := validateAbsolutePathParents(native); err != nil {
			return "", err
		}
		return native, nil
	}
	if err := s.validateWorkspaceRoot(); err != nil {
		return "", err
	}
	if rel == "" {
		return "", errors.New("localhistory: invalid workspace path")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("localhistory: workspace path escapes root")
	}
	path := filepath.Join(s.workspaceRoot, clean)
	if !sameOrInside(path, s.workspaceRoot) {
		return "", errors.New("localhistory: workspace path escapes root")
	}
	parent := s.workspaceRoot
	parts := strings.Split(filepath.ToSlash(clean), "/")
	for _, part := range parts[:len(parts)-1] {
		parent = filepath.Join(parent, part)
		info, err := os.Lstat(parent)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("localhistory: workspace path has symlink parent")
		}
		if !info.IsDir() {
			return "", errors.New("localhistory: workspace path parent is not a directory")
		}
	}
	return path, nil
}

func validateAbsolutePathParents(path string) error {
	parent := filepath.Dir(path)
	volume := filepath.VolumeName(parent)
	relative := strings.TrimPrefix(parent, volume)
	current := volume + string(filepath.Separator)
	for _, part := range strings.Split(filepath.ToSlash(relative), "/") {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("localhistory: absolute path has symlink parent %s", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("localhistory: absolute path parent is not a directory: %s", current)
		}
	}
	return nil
}

func (s *Store) safeSymlink(rel, target string) bool {
	path, err := s.workspacePath(rel)
	if err != nil || filepath.IsAbs(target) {
		return false
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(path), target))
	if !sameOrInside(resolved, s.workspaceRoot) {
		return false
	}
	resolvedExisting, err := resolveExistingPath(resolved)
	return err == nil && sameOrInside(resolvedExisting, s.workspaceRoot)
}

func resolveExistingPath(path string) (string, error) {
	original := path
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", fmt.Errorf("cannot resolve symlink target %s", original)
		}
		suffix = append(suffix, filepath.Base(path))
		path = parent
	}
}

func changePaths(changes []Change) []string {
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		paths = append(paths, change.Path)
	}
	sort.Strings(paths)
	return paths
}

func (s *Store) applySnapshotPaths(ctx context.Context, changes []Change, inject bool) error {
	stageRoot, err := os.MkdirTemp(filepath.Join(s.root, "tmp"), "restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stageRoot)
	staged := make(map[string]string)
	for _, change := range changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if change.Target == nil || change.Target.Type != EntryFile {
			continue
		}
		stagePath := filepath.Join(stageRoot, sha256Hex([]byte(change.Path)))
		if err := s.materializeFile(change.Target.Hash, stagePath); err != nil {
			return fmt.Errorf("materialize %s: %w", change.Path, err)
		}
		staged[change.Path] = stagePath
	}
	if inject {
		if err := s.validateChanges(ctx, changes); err != nil {
			return err
		}
	}
	hardlinkCanonicals := make(map[string]bool)
	for _, change := range changes {
		if change.Target != nil && change.Target.Type == EntryHardlink {
			hardlinkCanonicals[change.Target.HardlinkTarget] = true
		}
	}
	transitionedCanonicals := make(map[string]bool)
	ordered := append([]Change(nil), changes...)
	finalDirectories := make([]Change, 0)
	for _, change := range changes {
		if change.Target != nil && change.Target.Type == EntryDirectory {
			finalDirectories = append(finalDirectories, change)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.Action == ChangeDelete && b.Action != ChangeDelete {
			return true
		}
		if a.Action != ChangeDelete && b.Action == ChangeDelete {
			return false
		}
		if a.Action != ChangeDelete && b.Action != ChangeDelete {
			rank := func(change Change) int {
				if change.Target == nil {
					return 4
				}
				switch change.Target.Type {
				case EntryDirectory:
					return 0
				case EntryFile:
					return 1
				case EntryHardlink:
					return 2
				case EntrySymlink:
					return 3
				}
				return 4
			}
			ar, br := rank(a), rank(b)
			if ar != br {
				return ar < br
			}
		}
		if a.Action == ChangeDelete && b.Action == ChangeDelete {
			aDepth, bDepth := strings.Count(a.Path, "/"), strings.Count(b.Path, "/")
			if aDepth != bDepth {
				return aDepth > bDepth
			}
		}
		return a.Path < b.Path
	})
	mutated := false
	applied := make([]Change, 0, len(ordered))
	fail := func(err error) error {
		if err == nil {
			return nil
		}
		return restoreApplyFailure{cause: err, mutated: mutated, applied: append([]Change(nil), applied...)}
	}
	for index, change := range ordered {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if inject && s.beforeApply != nil {
			if err := s.beforeApply(index, change); err != nil {
				return fail(err)
			}
		}
		if inject {
			if err := s.validatePathParentIdentity(change); err != nil {
				return fail(err)
			}
			current, err := s.currentEntryExpected(change.Path, change.Current)
			if err != nil {
				return fail(err)
			}
			preconditionMatches := entryPointersEqual(current, change.Current)
			if !preconditionMatches && change.Target != nil && change.Target.Type == EntryHardlink && transitionedCanonicals[change.Target.HardlinkTarget] && change.Current != nil {
				raw, rawErr := s.currentEntry(change.Path)
				expectedFile := *change.Current
				expectedFile.Type = EntryFile
				expectedFile.HardlinkTarget = ""
				preconditionMatches = rawErr == nil && entryPointersEqual(raw, &expectedFile)
			}
			if !preconditionMatches {
				alreadyTarget, targetErr := s.currentEntryExpected(change.Path, change.Target)
				if targetErr != nil || !entryPointersEqual(alreadyTarget, change.Target) {
					return fail(fmt.Errorf("localhistory: %s changed immediately before apply", change.Path))
				}
				applied = append(applied, change)
				continue
			}
		}
		if change.Target == nil {
			path, err := s.workspacePath(change.Path)
			if err != nil {
				return fail(err)
			}
			mutated = true
			if err := removeExactPath(path); err != nil {
				return fail(err)
			}
			applied = append(applied, change)
			continue
		}
		mutated = true
		applyErr := s.applyEntry(change.Path, *change.Target, staged[change.Path])
		if applyErr != nil {
			return fail(applyErr)
		}
		applied = append(applied, change)
		if change.Target.Type == EntryFile && hardlinkCanonicals[change.Path] {
			transitionedCanonicals[change.Path] = true
		}
	}
	sort.Slice(finalDirectories, func(i, j int) bool {
		return strings.Count(finalDirectories[i].Path, "/") > strings.Count(finalDirectories[j].Path, "/")
	})
	for _, change := range finalDirectories {
		if inject && s.beforeDirectoryMode != nil {
			if err := s.beforeDirectoryMode(change); err != nil {
				return fail(err)
			}
		}
		path, err := s.workspacePath(change.Path)
		if err != nil {
			return fail(err)
		}
		if err := os.Chmod(path, os.FileMode(change.Target.Mode)); err != nil {
			return fail(err)
		}
	}
	return nil
}

func (s *Store) pathParentIdentity(key string) (string, error) {
	path, err := s.workspacePath(key)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil {
		return "", err
	}
	identity, ok := fileIdentityKey(parent, info)
	if !ok {
		return "", fmt.Errorf("localhistory: cannot identify parent directory %s", parent)
	}
	return identity, nil
}

func (s *Store) validatePathParentIdentity(change Change) error {
	if change.ParentIdentity == "" {
		return nil
	}
	identity, err := s.pathParentIdentity(change.Path)
	if err != nil {
		return err
	}
	if identity != change.ParentIdentity {
		return fmt.Errorf("localhistory: parent directory changed after preview for %s", change.Path)
	}
	return nil
}

type restoreApplyFailure struct {
	cause   error
	mutated bool
	applied []Change
}

func (e restoreApplyFailure) Error() string { return e.cause.Error() }
func (e restoreApplyFailure) Unwrap() error { return e.cause }

func restoreApplyMutated(err error) bool {
	var failure restoreApplyFailure
	return errors.As(err, &failure) && failure.mutated
}

func restoreAppliedChanges(err error) []Change {
	var failure restoreApplyFailure
	if errors.As(err, &failure) {
		return append([]Change(nil), failure.applied...)
	}
	return nil
}

func (s *Store) applyEntry(rel string, entry Entry, stagedPath string) error {
	path, err := s.workspacePath(rel)
	if err != nil {
		return err
	}
	switch entry.Type {
	case EntryDirectory:
		if info, err := os.Lstat(path); err == nil && !info.IsDir() {
			if err := removeExactPath(path); err != nil {
				return err
			}
		}
		if err := os.MkdirAll(path, os.FileMode(entry.Mode)); err != nil {
			return err
		}
		return os.Chmod(path, os.FileMode(entry.Mode))
	case EntrySymlink:
		if !s.safeSymlink(rel, entry.SymlinkTarget) {
			return errors.New("localhistory: unsafe symlink target")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := removeExactPath(path); err != nil {
			return err
		}
		return os.Symlink(entry.SymlinkTarget, path)
	case EntryHardlink:
		target, err := s.workspacePath(entry.HardlinkTarget)
		if err != nil {
			return err
		}
		if err := s.validateHardlinkSource(target, entry); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := removeExactPath(path); err != nil {
			return err
		}
		return os.Link(target, path)
	case EntryFile:
		if stagedPath == "" {
			return errors.New("localhistory: missing staged file")
		}
		if info, err := os.Lstat(path); err == nil && info.IsDir() {
			if err := removeExactPath(path); err != nil {
				return err
			}
		}
		return copyAtomic(stagedPath, path, os.FileMode(entry.Mode))
	default:
		return errors.New("localhistory: unsupported target type")
	}
}

func removeExactPath(path string) error {
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (s *Store) validateHardlinkSource(target string, entry Entry) error {
	targetInfo, err := os.Lstat(target)
	if err != nil || !targetInfo.Mode().IsRegular() {
		return errors.New("localhistory: hardlink source is unavailable")
	}
	actual, err := hashRegularFile(target, targetInfo.Mode())
	if err != nil {
		return err
	}
	if actual.Hash != entry.Hash || actual.Size != entry.Size || actual.Mode != entry.Mode {
		return errors.New("localhistory: hardlink source does not match immutable target")
	}
	return nil
}

func (s *Store) validateHardlinkClosure(changes []Change) error {
	selected := make(map[string]bool, len(changes))
	canonicals := make(map[string]bool)
	for _, change := range changes {
		selected[change.Path] = true
		if change.Target != nil && change.Target.Type == EntryHardlink {
			canonicals[change.Target.HardlinkTarget] = true
		}
	}
	for canonical := range canonicals {
		path, err := s.workspacePath(canonical)
		if err != nil {
			return err
		}
		canonicalInfo, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !canonicalInfo.Mode().IsRegular() {
			return errors.New("localhistory: hardlink canonical path is unavailable")
		}
		err = filepath.WalkDir(s.workspaceRoot, func(candidate string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || candidate == path {
				return nil
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() || !os.SameFile(canonicalInfo, info) {
				return err
			}
			rel, err := filepath.Rel(s.workspaceRoot, candidate)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if !selected[rel] {
				return fmt.Errorf("localhistory: hardlink alias %s is outside the restore selection", rel)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) validateHardlinkTarget(rel string, entry Entry) error {
	path, err := s.workspacePath(rel)
	if err != nil {
		return err
	}
	if err := s.validateHardlinkSource(path, entry); err != nil {
		return err
	}
	target, err := s.workspacePath(entry.HardlinkTarget)
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	targetInfo, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if !os.SameFile(pathInfo, targetInfo) {
		return errors.New("localhistory: restored hardlink does not share the target inode")
	}
	return nil
}

func copyAtomic(source, target string, mode os.FileMode) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	return writeAtomic(target, mode, func(w io.Writer) error {
		_, err := io.Copy(w, src)
		return err
	})
}
