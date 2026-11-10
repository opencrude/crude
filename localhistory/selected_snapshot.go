package localhistory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	crudediff "crude/diff"
)

const (
	selectedMaxFiles = 200
	selectedMaxPath  = 4096
	selectedMaxFile  = 2 * 1024 * 1024
	selectedMaxBytes = 16 * 1024 * 1024
)

func validSelectedText(text string, maxBytes int) bool {
	if len(text) > maxBytes || !utf8.ValidString(text) {
		return false
	}
	for _, r := range text {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validSelectedPath(path string) bool {
	if path == "" || !validSelectedText(path, selectedMaxPath) || strings.ContainsAny(path, `\:`) {
		return false
	}
	clean, ok := cleanRelativeHistoryPath(path)
	if !ok || clean != path {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if strings.EqualFold(part, ".git") {
			return false
		}
	}
	return true
}

func validSelectedMode(mode uint32) bool {
	m := os.FileMode(mode)
	return m.IsRegular() && mode == storedMode(m)
}

// Validate the caller's entire shape before copying, sorting or allocating
// storage proportional to caller input. The quadratic duplicate check is bounded
// by 200 keys and requires no caller-sized allocation.
func validateRetainedFiles(reference, label string, files []RetainedFile) error {
	if !validSafetyReference(reference) || !validSelectedText(label, 256) {
		return errors.New("localhistory: invalid selected snapshot reference or label")
	}
	if len(files) == 0 || len(files) > selectedMaxFiles {
		return errors.New("localhistory: selected snapshot requires 1–200 files")
	}
	total := 0
	for i, file := range files {
		if !validSelectedPath(file.Path) || !validSelectedMode(file.Mode) || len(file.Data) > selectedMaxFile {
			return errors.New("localhistory: invalid selected file path, mode or size")
		}
		total += len(file.Data)
		if total > selectedMaxBytes {
			return errors.New("localhistory: selected snapshot exceeds 16 MiB")
		}
		for j := 0; j < i; j++ {
			if files[j].Path == file.Path {
				return errors.New("localhistory: duplicate selected file path")
			}
		}
	}
	return nil
}

// SnapshotRetainedFiles stores exactly the supplied immutable preview bytes,
// under the same capture/reference/prune gate as SnapshotRetained. It never
// walks the workspace or reads the declared files. Native precondition checking
// remains the caller's responsibility immediately before its canonical commit.
func (s *Store) SnapshotRetainedFiles(ctx context.Context, reference, label string, files []RetainedFile) (Manifest, error) {
	if err := validateRetainedFiles(reference, label, files); err != nil {
		return Manifest{}, err
	}
	if err := s.beginOperation(); err != nil {
		return Manifest{}, err
	}
	defer s.endOperation()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	unlock, err := s.lockMutation(ctx)
	if err != nil {
		return Manifest{}, err
	}
	defer unlock()
	if err := s.checkRetainedReferenceAvailable(ctx, reference); err != nil {
		return Manifest{}, err
	}
	if err := s.validateWorkspaceRoot(); err != nil {
		return Manifest{}, err
	}
	paths := make([]string, len(files))
	owned := make([]RetainedFile, len(files))
	for i, file := range files {
		paths[i] = file.Path
		owned[i] = RetainedFile{Path: file.Path, Mode: file.Mode, Data: append([]byte(nil), file.Data...)}
	}
	sort.Strings(paths)
	if err := s.validateSelectedAdministration(paths); err != nil {
		return Manifest{}, err
	}
	manifest, err := s.storeSelectedManifest(ctx, SnapshotOptions{Kind: "safety", Label: label}, paths, owned)
	if err != nil {
		return Manifest{}, err
	}
	if err := s.pinRetainedManifest(ctx, reference, manifest.ID); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateSelectedScope(paths []string) error {
	if len(paths) == 0 || len(paths) > selectedMaxFiles {
		return errors.New("localhistory: invalid selected snapshot scope")
	}
	for i, path := range paths {
		if !validSelectedPath(path) || (i > 0 && paths[i-1] >= path) {
			return errors.New("localhistory: selected scope must contain canonical sorted unique file keys")
		}
	}
	return nil
}

func validateScopedManifest(manifest Manifest) error {
	if manifest.ScopeRoots != nil {
		if manifest.ScopePaths != nil || len(manifest.TrackedPaths) != 0 {
			return errors.New("localhistory: mixed snapshot scopes")
		}
		if err := validateSubtreeRoots(manifest.ScopeRoots); err != nil {
			return err
		}
		for path := range manifest.Entries {
			if !validSubtreePath(path) || !manifestObserves(manifest, path) {
				return errors.New("localhistory: entry outside subtree snapshot")
			}
		}
		return nil
	}
	if manifest.ScopePaths == nil {
		return nil
	}
	if err := validateSelectedScope(manifest.ScopePaths); err != nil {
		return err
	}
	if len(manifest.TrackedPaths) != 0 || len(manifest.Entries) > len(manifest.ScopePaths) || !validSelectedText(manifest.Label, 256) {
		return errors.New("localhistory: invalid selected manifest shape")
	}
	var total int64
	for path, entry := range manifest.Entries {
		if !manifestObserves(manifest, path) || entry.Type != EntryFile || !validSelectedMode(entry.Mode) ||
			!validID(entry.Hash) || entry.Size < 0 || entry.Size > selectedMaxFile ||
			entry.SymlinkTarget != "" || entry.HardlinkTarget != "" || entry.SpecialMode != "" {
			return errors.New("localhistory: invalid selected manifest entry")
		}
		total += entry.Size
		if total > selectedMaxBytes {
			return errors.New("localhistory: selected manifest exceeds 16 MiB")
		}
	}
	return nil
}

func validSubtreePath(path string) bool {
	clean, ok := cleanRelativeHistoryPath(path)
	if !ok || clean != path {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if strings.EqualFold(part, ".git") {
			return false
		}
	}
	return true
}

func validateSubtreeRoots(paths []string) error {
	if len(paths) == 0 {
		return errors.New("localhistory: empty subtree scope")
	}
	for i, path := range paths {
		if !validSubtreePath(path) || (i > 0 && paths[i-1] >= path) {
			return errors.New("localhistory: invalid subtree scope")
		}
	}
	return nil
}

func manifestObserves(manifest Manifest, path string) bool {
	if manifest.ScopeRoots != nil {
		for _, root := range manifest.ScopeRoots {
			if path == root || strings.HasPrefix(path, root+"/") {
				return true
			}
		}
		return false
	}
	if manifest.ScopePaths == nil {
		return true
	}
	i := sort.SearchStrings(manifest.ScopePaths, path)
	return i < len(manifest.ScopePaths) && manifest.ScopePaths[i] == path
}

// All callers hold the store/process mutation gate. This reuses the sole
// immutable object, manifest and index publication owners.
func (s *Store) storeSelectedManifest(ctx context.Context, opts SnapshotOptions, paths []string, files []RetainedFile) (Manifest, error) {
	manifest := Manifest{
		Version: FormatVersion, CreatedAt: time.Now().UnixNano(), ParentID: opts.ParentID,
		Kind: opts.Kind, Label: opts.Label, ScopePaths: append([]string(nil), paths...),
		Entries: make(map[string]Entry, len(files)),
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		entry, err := s.storeBytes(ctx, file.Data, os.FileMode(file.Mode))
		if err != nil {
			return Manifest{}, err
		}
		manifest.Entries[file.Path] = entry
	}
	if err := s.saveManifest(&manifest); err != nil {
		return Manifest{}, err
	}
	if err := s.recordSnapshot(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (s *Store) snapshotSelectedPaths(ctx context.Context, opts SnapshotOptions, paths []string) (Manifest, error) {
	files, err := s.readSelectedFiles(ctx, paths)
	if err != nil {
		return Manifest{}, err
	}
	return s.storeSelectedManifest(ctx, opts, paths, files)
}

func (s *Store) planSelectedRestore(ctx context.Context, target Manifest, opts SnapshotOptions, paths []string) (RestorePlan, error) {
	plan := RestorePlan{
		Version: FormatVersion, TargetSnapshotID: target.ID, CreatedAt: time.Now().UnixNano(),
		CaptureOptions: opts,
	}
	var files []RetainedFile
	if len(paths) > 0 {
		var err error
		files, err = s.readSelectedFiles(ctx, paths)
		if err != nil {
			return RestorePlan{}, err
		}
	}
	current := make(map[string]RetainedFile, len(files))
	for _, file := range files {
		current[file.Path] = file
	}
	for _, path := range paths {
		file, exists := current[path]
		entry, wanted := target.Entries[path]
		var actual *Entry
		if exists {
			actual = &Entry{Type: EntryFile, Mode: file.Mode, Hash: sha256Hex(file.Data), Size: int64(len(file.Data))}
		}
		if (!exists && !wanted) || (wanted && actual != nil && entriesEqual(entry, *actual)) {
			continue
		}
		change := Change{Path: path, Current: actual, Action: ChangeDelete}
		if wanted {
			change.Target = cloneEntry(&entry)
			change.Action = ChangeRestore
			if !exists {
				change.Action = ChangeCreate
			}
		}
		var err error
		change.TextDiff, err = s.selectedChangeDiff(path, change.Target, file.Data)
		if err != nil {
			return RestorePlan{}, err
		}
		plan.Changes = append(plan.Changes, change)
	}
	if err := s.savePlan(&plan); err != nil {
		return RestorePlan{}, err
	}
	return plan, nil
}

func (s *Store) selectedChangeDiff(path string, target *Entry, before []byte) (string, error) {
	const maxPreview = 256 * 1024
	var after []byte
	if target != nil {
		// Verify the object before materializing its bounded preview bytes.
		object, err := s.readFileObject(target.Hash)
		if err != nil {
			return "", err
		}
		if object.Size != target.Size {
			return "", errors.New("localhistory: selected target object size mismatch")
		}
		if object.Size > maxPreview || len(before) > maxPreview {
			return "Binary or large file change", nil
		}
		after, err = s.readFileBytes(target.Hash)
		if err != nil {
			return "", err
		}
	}
	if len(before) > maxPreview {
		return "Binary or large file change", nil
	}
	if !utf8.Valid(before) || !utf8.Valid(after) || strings.IndexByte(string(before), 0) >= 0 || strings.IndexByte(string(after), 0) >= 0 {
		return "Binary file replacement", nil
	}
	diff, _, _ := crudediff.GenerateDiff(string(before), string(after), path)
	return diff, nil
}

// Direct bounded reads only. Missing regular-file keys are observed absence;
// nonregular or oversized replacements are errors, never a fallback tree scan.
func (s *Store) readSelectedFiles(ctx context.Context, paths []string) ([]RetainedFile, error) {
	if err := validateSelectedScope(paths); err != nil {
		return nil, err
	}
	if err := s.validateWorkspaceRoot(); err != nil {
		return nil, err
	}
	if err := s.validateSelectedAdministration(paths); err != nil {
		return nil, err
	}
	files := make([]RetainedFile, 0, len(paths))
	total := 0
	for _, key := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path, err := s.workspacePath(key)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > selectedMaxFile {
			return nil, fmt.Errorf("localhistory: selected path %s is nonregular or exceeds 2 MiB", key)
		}
		limit := min(int64(selectedMaxFile), int64(selectedMaxBytes-total))
		data, err := readSelectedRegular(path, info, limit)
		if err != nil {
			return nil, err
		}
		total += len(data)
		if total > selectedMaxBytes {
			return nil, errors.New("localhistory: selected current state exceeds 16 MiB")
		}
		files = append(files, RetainedFile{Path: key, Data: data, Mode: storedMode(info.Mode())})
	}
	return files, nil
}

func readSelectedRegular(path string, info os.FileInfo, limit int64) ([]byte, error) {
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > limit {
		return nil, errors.New("localhistory: selected file is nonregular or exceeds read bound")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() > limit {
		return nil, errors.New("localhistory: selected file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit || int64(len(data)) != info.Size() || opened.Mode() != info.Mode() ||
		after.Mode() != info.Mode() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) || changeKey(after) != changeKey(info) {
		return nil, errors.New("localhistory: selected file changed or exceeded read bound")
	}
	return data, nil
}

// Resolve only Git's two administration pointers, with bounded regular-file
// reads. Do not construct an ignore matcher: that recursively discovers ignores.
func (s *Store) validateSelectedAdministration(paths []string) error {
	pointer := filepath.Join(s.workspaceRoot, ".git")
	info, err := os.Lstat(pointer)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	admin := pointer
	switch {
	case info.Mode().IsRegular():
		data, err := readSelectedRegular(pointer, info, selectedMaxPath+32)
		if err != nil {
			return err
		}
		text := strings.TrimSpace(string(data))
		if !strings.HasPrefix(text, "gitdir: ") {
			return errors.New("localhistory: invalid Git administration pointer")
		}
		admin = strings.TrimSpace(strings.TrimPrefix(text, "gitdir: "))
		if admin == "" || strings.ContainsRune(admin, 0) {
			return errors.New("localhistory: invalid Git administration path")
		}
		if !filepath.IsAbs(admin) {
			admin = filepath.Join(s.workspaceRoot, admin)
		}
	case !info.IsDir():
		return errors.New("localhistory: unsupported Git administration pointer")
	}
	admin, err = filepath.EvalSymlinks(admin)
	if err != nil {
		return err
	}
	admins := []string{admin}
	commonPath := filepath.Join(admin, "commondir")
	commonInfo, err := os.Lstat(commonPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		data, err := readSelectedRegular(commonPath, commonInfo, selectedMaxPath+32)
		if err != nil {
			return err
		}
		common := strings.TrimSpace(string(data))
		if common == "" || strings.ContainsRune(common, 0) {
			return errors.New("localhistory: invalid Git common administration path")
		}
		if !filepath.IsAbs(common) {
			common = filepath.Join(admin, common)
		}
		common, err = filepath.EvalSymlinks(common)
		if err != nil {
			return err
		}
		admins = append(admins, common)
	}
	for _, key := range paths {
		path := filepath.Join(s.workspaceRoot, filepath.FromSlash(key))
		for _, admin := range admins {
			if sameOrInside(path, admin) {
				return errors.New("localhistory: selected path is Git administration")
			}
		}
	}
	return nil
}
