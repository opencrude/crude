package localhistory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (s *Store) Snapshot(ctx context.Context, opts SnapshotOptions) (Manifest, error) {
	if err := s.beginOperation(); err != nil {
		return Manifest{}, err
	}
	defer s.endOperation()
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockMutation(ctx)
	if err != nil {
		return Manifest{}, err
	}
	defer unlock()
	return s.snapshot(ctx, opts)
}

func (s *Store) SnapshotIfChanged(ctx context.Context, opts SnapshotOptions) (Manifest, bool, error) {
	if err := s.beginOperation(); err != nil {
		return Manifest{}, false, err
	}
	defer s.endOperation()
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockMutation(ctx)
	if err != nil {
		return Manifest{}, false, err
	}
	defer unlock()
	entries, err := s.scan(ctx, opts, true)
	if err != nil {
		return Manifest{}, false, err
	}
	records, err := s.timeline(1)
	if err != nil {
		return Manifest{}, false, err
	}
	if len(records) == 1 {
		latest, err := s.LoadManifest(records[0].ID)
		if err != nil {
			return Manifest{}, false, err
		}
		if entryMapsEqual(latest.Entries, entries) {
			return latest, false, nil
		}
		if opts.ParentID == "" {
			opts.ParentID = latest.ID
		}
	}
	manifest := Manifest{Version: FormatVersion, ParentID: opts.ParentID, CreatedAt: time.Now().UnixNano(), Kind: opts.Kind, Label: opts.Label, Entries: entries}
	if manifest.Kind == "" {
		manifest.Kind = "automatic"
	}
	if err := s.saveManifest(&manifest); err != nil {
		return Manifest{}, false, err
	}
	if err := s.recordSnapshot(manifest); err != nil {
		return Manifest{}, false, err
	}
	return manifest, true, nil
}

func entryMapsEqual(a, b map[string]Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for path, entry := range a {
		other, ok := b[path]
		if !ok || !entriesEqual(entry, other) {
			return false
		}
	}
	return true
}

func (s *Store) DiffSnapshotPaths(beforeID, afterID string) ([]string, error) {
	before, err := s.LoadManifest(beforeID)
	if err != nil {
		return nil, err
	}
	after, err := s.LoadManifest(afterID)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]bool, len(before.Entries)+len(after.Entries))
	for path := range before.Entries {
		paths[path] = true
	}
	for path := range after.Entries {
		paths[path] = true
	}
	changed := make([]string, 0)
	for path := range paths {
		a, aok := before.Entries[path]
		b, bok := after.Entries[path]
		if !aok || !bok || !entriesEqual(a, b) {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return changed, nil
}

func (s *Store) snapshot(ctx context.Context, opts SnapshotOptions) (Manifest, error) {
	if opts.Kind == "" {
		opts.Kind = "manual"
	}
	entries, err := s.scan(ctx, opts, true)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{
		Version: FormatVersion, ParentID: opts.ParentID, CreatedAt: time.Now().UnixNano(),
		Kind: opts.Kind, Label: opts.Label, Entries: entries,
	}
	if err := s.saveManifest(&manifest); err != nil {
		return Manifest{}, err
	}
	if err := s.recordSnapshot(manifest); err != nil {
		return Manifest{}, fmt.Errorf("localhistory: index snapshot: %w", err)
	}
	return manifest, nil
}

func (s *Store) snapshotIncludingPaths(ctx context.Context, opts SnapshotOptions, paths []string) (Manifest, error) {
	entries, err := s.scan(ctx, opts, true)
	if err != nil {
		return Manifest{}, err
	}
	tracked := make([]string, 0, len(paths))
	for _, key := range paths {
		key, ok := cleanOperationPath(key)
		if !ok || !filepath.IsAbs(filepath.FromSlash(key)) {
			continue
		}
		tracked = append(tracked, key)
		entry, exists, err := s.captureExternalEntry(ctx, key)
		if err != nil {
			return Manifest{}, err
		}
		if exists {
			entries[key] = entry
		}
	}
	manifest := Manifest{Version: FormatVersion, ParentID: opts.ParentID, CreatedAt: time.Now().UnixNano(), Kind: opts.Kind, Label: opts.Label, Entries: entries, TrackedPaths: sortedUnique(tracked)}
	if manifest.Kind == "" {
		manifest.Kind = "automatic"
	}
	if err := s.saveManifest(&manifest); err != nil {
		return Manifest{}, err
	}
	if err := s.recordSnapshot(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (s *Store) captureExternalEntry(ctx context.Context, key string) (Entry, bool, error) {
	path, err := s.workspacePath(key)
	if err != nil {
		return Entry{}, false, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	if info.IsDir() {
		return Entry{Type: EntryDirectory, Mode: storedMode(info.Mode())}, true, nil
	}
	if !info.Mode().IsRegular() {
		return Entry{}, false, fmt.Errorf("localhistory: unsupported external safety path %s", key)
	}
	entry, err := s.storeRegularFile(ctx, path)
	return entry, err == nil, err
}

func (s *Store) scan(ctx context.Context, opts SnapshotOptions, storeObjects bool) (map[string]Entry, error) {
	if err := s.validateWorkspaceRoot(); err != nil {
		return nil, err
	}
	matcher, err := newIgnoreMatcher(s.workspaceRoot, opts)
	if err != nil {
		return nil, fmt.Errorf("localhistory: resolve ignores: %w", err)
	}
	maxFileSize := opts.MaxFileSize
	if maxFileSize <= 0 {
		maxFileSize = defaultMaxFileSize
	}
	entries := make(map[string]Entry)
	included := normalizedIncludedPaths(opts.IncludePaths)
	hardlinks := make(map[string]string)
	err = filepath.WalkDir(s.workspaceRoot, func(path string, dirEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(s.workspaceRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, err := dirEntry.Info()
		if err != nil {
			return err
		}
		forced := pathForcedIncluded(rel, included)
		if !forced && matcher.ignored(rel, info.IsDir()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		mode := info.Mode()
		switch {
		case mode.IsDir():
			entries[rel] = Entry{Type: EntryDirectory, Mode: storedMode(mode)}
		case mode&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			entries[rel] = Entry{Type: EntrySymlink, Mode: storedMode(mode), SymlinkTarget: target}
		case mode.IsRegular():
			if !forced && info.Size() > maxFileSize {
				return nil
			}
			var entry Entry
			if storeObjects {
				entry, err = s.storeRegularFile(ctx, path)
			} else {
				entry, err = hashRegularFile(path, mode)
			}
			if err != nil {
				return err
			}
			if identity, ok := fileIdentityKey(path, info); ok {
				if canonical := hardlinks[identity]; canonical != "" {
					entry.Type = EntryHardlink
					entry.HardlinkTarget = canonical
				} else {
					hardlinks[identity] = rel
				}
			}
			entries[rel] = entry
		default:
			entries[rel] = Entry{Type: EntrySpecial, Mode: storedMode(mode), SpecialMode: mode.Type().String()}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("localhistory: scan workspace: %w", err)
	}
	return entries, nil
}

func normalizedIncludedPaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		path, ok := cleanRelativeHistoryPath(filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))))
		if ok {
			result = append(result, path)
		}
	}
	return sortedUnique(result)
}

func pathForcedIncluded(path string, included []string) bool {
	for _, candidate := range included {
		if path == candidate || strings.HasPrefix(path, candidate+"/") || strings.HasPrefix(candidate, path+"/") {
			return true
		}
	}
	return false
}

func hashRegularFile(path string, mode os.FileMode) (Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return Entry{}, err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Type: EntryFile, Mode: storedMode(mode), Hash: hex.EncodeToString(h.Sum(nil)), Size: size}, nil
}

func (s *Store) saveManifest(manifest *Manifest) error {
	if manifest == nil || manifest.Version != FormatVersion {
		return errors.New("localhistory: invalid manifest")
	}
	manifest.ID = ""
	payload, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	manifest.ID = sha256Hex(payload)
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return writeImmutable(filepath.Join(s.root, "manifests", manifest.ID+".json"), data, 0o600)
}

func (s *Store) LoadManifest(id string) (Manifest, error) {
	if !validID(id) {
		return Manifest{}, errors.New("localhistory: invalid manifest ID")
	}
	data, err := os.ReadFile(filepath.Join(s.root, "manifests", id+".json"))
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Version != FormatVersion || manifest.ID != id {
		return Manifest{}, errors.New("localhistory: corrupt manifest")
	}
	manifest.ID = ""
	payload, err := json.Marshal(manifest)
	if err != nil {
		return Manifest{}, err
	}
	if sha256Hex(payload) != id {
		return Manifest{}, errors.New("localhistory: manifest hash mismatch")
	}
	manifest.ID = id
	return manifest, nil
}

func (s *Store) ListManifests() ([]Manifest, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "manifests"))
	if err != nil {
		return nil, err
	}
	manifests := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		manifest, err := s.LoadManifest(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, manifest)
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].CreatedAt < manifests[j].CreatedAt })
	return manifests, nil
}

func (s *Store) Verify(ctx context.Context, manifestID string) error {
	if err := s.beginOperation(); err != nil {
		return err
	}
	defer s.endOperation()
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockMutation(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return s.verify(ctx, manifestID)
}

func (s *Store) verify(ctx context.Context, manifestID string) error {
	manifest, err := s.LoadManifest(manifestID)
	if err != nil {
		return err
	}
	for path, entry := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type != EntryFile {
			continue
		}
		if _, err := s.readFileObject(entry.Hash); err != nil {
			return fmt.Errorf("localhistory: verify %s: %w", path, err)
		}
	}
	return nil
}

func validID(id string) bool {
	if len(id) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func (s *Store) Stats() (Stats, error) {
	if err := s.beginOperation(); err != nil {
		return Stats{}, err
	}
	defer s.endOperation()
	return s.stats()
}

func (s *Store) stats() (Stats, error) {
	var stats Stats
	err := filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		stats.PhysicalBytes += info.Size()
		switch {
		case strings.Contains(path, string(filepath.Separator)+"objects"+string(filepath.Separator)+"chunks"+string(filepath.Separator)):
			stats.ChunkObjects++
		case strings.Contains(path, string(filepath.Separator)+"objects"+string(filepath.Separator)+"files"+string(filepath.Separator)):
			stats.FileObjects++
		case strings.Contains(path, string(filepath.Separator)+"manifests"+string(filepath.Separator)):
			stats.Manifests++
		}
		return nil
	})
	if err != nil {
		return Stats{}, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(logical_bytes),0) FROM history_snapshots`).Scan(&stats.Snapshots, &stats.LogicalBytes); err != nil {
		return Stats{}, err
	}
	return stats, nil
}
