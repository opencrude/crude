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
	paths := make(map[string]struct{}, len(target.Entries)+len(current))
	for path := range target.Entries {
		paths[path] = struct{}{}
	}
	for path := range current {
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
	safetyOpts := plan.CaptureOptions
	safetyOpts.Kind = "safety_before_restore"
	safetyOpts.Label = "Safety snapshot before restore " + plan.ID[:12]
	safetyOpts.ParentID = ""
	safety, err := s.snapshot(ctx, safetyOpts)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("localhistory: capture safety snapshot: %w", err)
	}
	target, err := s.LoadManifest(plan.TargetSnapshotID)
	if err != nil {
		return RestoreResult{}, err
	}
	paths := changePaths(changes)
	if err := s.applySnapshotPaths(ctx, changes, true); err != nil {
		rollbackChanges := changesForSnapshotPaths(safety, paths)
		rollbackErr := s.applySnapshotPaths(context.Background(), rollbackChanges, false)
		if rollbackErr != nil {
			return RestoreResult{}, fmt.Errorf("localhistory: restore failed: %v; rollback failed: %w", err, rollbackErr)
		}
		return RestoreResult{}, fmt.Errorf("localhistory: restore failed and was rolled back: %w", err)
	}
	afterOpts := plan.CaptureOptions
	afterOpts.Kind = "after_restore"
	afterOpts.Label = "After restore " + plan.ID[:12]
	afterOpts.ParentID = safety.ID
	after, err := s.snapshot(ctx, afterOpts)
	if err != nil {
		rollbackChanges := changesForSnapshotPaths(safety, paths)
		rollbackErr := s.applySnapshotPaths(context.Background(), rollbackChanges, false)
		if rollbackErr != nil {
			return RestoreResult{}, fmt.Errorf("localhistory: after-restore snapshot failed: %v; rollback failed: %w", err, rollbackErr)
		}
		return RestoreResult{}, fmt.Errorf("localhistory: after-restore snapshot failed and was rolled back: %w", err)
	}
	result := RestoreResult{
		PlanID: plan.ID, TargetSnapshotID: target.ID, SafetySnapshotID: safety.ID,
		AfterSnapshotID: after.ID, Paths: paths,
	}
	if err := s.recordRestore(result); err != nil {
		rollbackChanges := changesForSnapshotPaths(safety, paths)
		rollbackErr := s.applySnapshotPaths(context.Background(), rollbackChanges, false)
		if rollbackErr != nil {
			return RestoreResult{}, fmt.Errorf("localhistory: record restore event: %v; rollback failed: %w", err, rollbackErr)
		}
		return RestoreResult{}, fmt.Errorf("localhistory: record restore event failed and was rolled back: %w", err)
	}
	return result, nil
}

func (s *Store) validateChanges(ctx context.Context, changes []Change) error {
	for _, change := range changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if change.UnsafeReason != "" {
			return fmt.Errorf("localhistory: unsafe restore path %s: %s", change.Path, change.UnsafeReason)
		}
		current, err := s.currentEntry(change.Path)
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
	return changes, nil
}

func (s *Store) currentEntry(rel string) (*Entry, error) {
	path, err := s.workspacePath(rel)
	if err != nil {
		return nil, err
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
		entry = Entry{Type: EntryDirectory, Mode: uint32(info.Mode().Perm())}
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return nil, err
		}
		entry = Entry{Type: EntrySymlink, Mode: uint32(info.Mode().Perm()), SymlinkTarget: target}
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

func entryPointersEqual(a, b *Entry) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return entriesEqual(*a, *b)
}

func (s *Store) workspacePath(rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
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

func (s *Store) safeSymlink(rel, target string) bool {
	path, err := s.workspacePath(rel)
	if err != nil || filepath.IsAbs(target) {
		return false
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(path), target))
	return sameOrInside(resolved, s.workspaceRoot)
}

func changePaths(changes []Change) []string {
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		paths = append(paths, change.Path)
	}
	sort.Strings(paths)
	return paths
}

func changesForSnapshotPaths(snapshot Manifest, paths []string) []Change {
	changes := make([]Change, 0, len(paths))
	for _, path := range paths {
		change := Change{Path: path, Action: ChangeDelete}
		if entry, ok := snapshot.Entries[path]; ok {
			entry := entry
			change.Target = &entry
			change.Action = ChangeRestore
		}
		changes = append(changes, change)
	}
	return changes
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
		stagePath := filepath.Join(stageRoot, filepath.FromSlash(change.Path))
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
	ordered := append([]Change(nil), changes...)
	finalDirectories := make([]Change, 0)
	for _, change := range changes {
		if change.Target != nil && change.Target.Type == EntryDirectory {
			finalDirectories = append(finalDirectories, change)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		aCaseDelete := a.Action == ChangeDelete && a.MovePeer != "" && strings.EqualFold(a.Path, a.MovePeer)
		bCaseDelete := b.Action == ChangeDelete && b.MovePeer != "" && strings.EqualFold(b.Path, b.MovePeer)
		if aCaseDelete != bCaseDelete {
			return aCaseDelete
		}
		if a.Action == ChangeDelete && b.Action != ChangeDelete {
			return false
		}
		if a.Action != ChangeDelete && b.Action == ChangeDelete {
			return true
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
			return strings.Count(a.Path, "/") > strings.Count(b.Path, "/")
		}
		return a.Path < b.Path
	})
	for index, change := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		if inject && s.beforeApply != nil {
			if err := s.beforeApply(index, change); err != nil {
				return err
			}
		}
		if change.Target == nil {
			path, err := s.workspacePath(change.Path)
			if err != nil {
				return err
			}
			if err := os.RemoveAll(path); err != nil {
				return err
			}
			continue
		}
		if err := s.applyEntry(change.Path, *change.Target, staged[change.Path]); err != nil {
			return err
		}
	}
	sort.Slice(finalDirectories, func(i, j int) bool {
		return strings.Count(finalDirectories[i].Path, "/") > strings.Count(finalDirectories[j].Path, "/")
	})
	for _, change := range finalDirectories {
		path, err := s.workspacePath(change.Path)
		if err != nil {
			return err
		}
		if err := os.Chmod(path, os.FileMode(change.Target.Mode)); err != nil {
			return err
		}
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
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
		return os.Chmod(path, 0o755)
	case EntrySymlink:
		if !s.safeSymlink(rel, entry.SymlinkTarget) {
			return errors.New("localhistory: unsafe symlink target")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		return os.Symlink(entry.SymlinkTarget, path)
	case EntryHardlink:
		target, err := s.workspacePath(entry.HardlinkTarget)
		if err != nil {
			return err
		}
		targetInfo, err := os.Lstat(target)
		if err != nil || !targetInfo.Mode().IsRegular() {
			return errors.New("localhistory: hardlink source is unavailable")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		return os.Link(target, path)
	case EntryFile:
		if stagedPath == "" {
			return errors.New("localhistory: missing staged file")
		}
		if info, err := os.Lstat(path); err == nil && info.IsDir() {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		}
		return copyAtomic(stagedPath, path, os.FileMode(entry.Mode))
	default:
		return errors.New("localhistory: unsupported target type")
	}
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
