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
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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

// SnapshotSubtree keeps delete recovery proportional to the deleted tree.
// It uses the same immutable object/index owner, without discovering ignores
// or reading any sibling tree. Ignore and size rules cannot omit deleted bytes.
func (s *Store) SnapshotSubtree(ctx context.Context, opts SnapshotOptions, rel string) (Manifest, error) {
	if !validSubtreePath(rel) {
		return Manifest{}, errors.New("localhistory: invalid subtree path")
	}
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
	return s.snapshotRoots(ctx, opts, []string{rel})
}

func (s *Store) snapshotRoots(ctx context.Context, opts SnapshotOptions, roots []string) (Manifest, error) {
	entries, err := s.scanWithin(ctx, opts, true, roots)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{
		Version: FormatVersion, ParentID: opts.ParentID, CreatedAt: time.Now().UnixNano(),
		Kind: opts.Kind, Label: opts.Label, Entries: entries, ScopeRoots: append([]string(nil), roots...),
	}
	if err := s.saveManifest(&manifest); err != nil {
		return Manifest{}, err
	}
	if err := s.recordSnapshot(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// SnapshotMessageScope captures the safety preimage and registers its existing
// message-scope reference under the same mutation gate as pruning. Callers must
// not expose an unreferenced safety snapshot between capture and scope creation.
func (s *Store) SnapshotMessageScope(ctx context.Context, sessionID, messageID string, opts SnapshotOptions) (Manifest, error) {
	if sessionID == "" || messageID == "" {
		return Manifest{}, errors.New("localhistory: invalid message scope")
	}
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
	manifest, err := s.snapshot(ctx, opts)
	if err != nil {
		return Manifest{}, err
	}
	if err := s.BeginMessageScope(sessionID, messageID, manifest.ID, time.Now()); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// SnapshotIfChanged records the workspace when it differs from the latest
// snapshot. Requests that wait while another capture holds the store share
// the next scan: it starts after each of them asked, so its result is as
// current as a scan of their own. Concurrent agents then wait for at most
// two scans instead of one scan per agent.
func (s *Store) SnapshotIfChanged(ctx context.Context, opts SnapshotOptions) (Manifest, bool, error) {
	if err := s.beginOperation(); err != nil {
		return Manifest{}, false, err
	}
	defer s.endOperation()
	request := &captureRequest{opts: opts, done: make(chan struct{})}
	s.capturesMu.Lock()
	s.captures = append(s.captures, request)
	s.capturesMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-request.done:
		return request.manifest, request.changed, request.err
	default:
	}
	s.capturesMu.Lock()
	batch := s.captures
	s.captures = nil
	s.capturesMu.Unlock()
	unlock, err := s.lockMutation(ctx)
	if err != nil {
		s.requeueCaptures(batch, request)
		return Manifest{}, false, err
	}
	defer unlock()
	for len(batch) > 0 {
		group, rest := sameScan(batch)
		batch = rest
		entries, err := s.scan(ctx, group[0].opts, true)
		if err != nil && ctx.Err() != nil {
			// This caller stopped waiting. The others keep their place and
			// the next holder of the store serves them.
			s.requeueCaptures(append(group, batch...), request)
			return Manifest{}, false, err
		}
		for _, waiting := range group {
			if err == nil {
				waiting.manifest, waiting.changed, waiting.err = s.recordIfChanged(waiting.opts, entries)
			} else {
				waiting.err = err
			}
			close(waiting.done)
		}
	}
	return request.manifest, request.changed, request.err
}

type captureRequest struct {
	opts     SnapshotOptions
	done     chan struct{}
	manifest Manifest
	changed  bool
	err      error
}

// sameScan splits off the requests whose options select the same files as
// the first one.
func sameScan(batch []*captureRequest) (group, rest []*captureRequest) {
	first := batch[0].opts
	for _, request := range batch {
		opts := request.opts
		if opts.DisableGitIgnore == first.DisableGitIgnore && opts.MaxFileSize == first.MaxFileSize &&
			slices.Equal(opts.ExtraIgnorePatterns, first.ExtraIgnorePatterns) && slices.Equal(opts.IncludePaths, first.IncludePaths) {
			group = append(group, request)
		} else {
			rest = append(rest, request)
		}
	}
	return group, rest
}

// requeueCaptures returns unserved requests, except the caller's own, to the
// front of the queue.
func (s *Store) requeueCaptures(batch []*captureRequest, self *captureRequest) {
	batch = slices.DeleteFunc(batch, func(request *captureRequest) bool { return request == self })
	s.capturesMu.Lock()
	s.captures = append(batch, s.captures...)
	s.capturesMu.Unlock()
}

// recordIfChanged saves entries as a new snapshot unless the latest
// snapshot already holds them.
func (s *Store) recordIfChanged(opts SnapshotOptions, entries map[string]Entry) (Manifest, bool, error) {
	records, err := s.timeline(1)
	if err != nil {
		return Manifest{}, false, err
	}
	if len(records) == 1 {
		latest, err := s.LoadManifest(records[0].ID)
		if err != nil {
			return Manifest{}, false, err
		}
		if latest.ScopePaths == nil && latest.ScopeRoots == nil && entryMapsEqual(latest.Entries, entries) {
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
		if !manifestObserves(before, path) || !manifestObserves(after, path) {
			continue
		}
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
	matcher, err := newIgnoreMatcher(s.workspaceRoot, opts)
	if err != nil {
		return Manifest{}, err
	}
	tracked := make([]string, 0, len(paths))
	for _, key := range paths {
		key, ok := cleanOperationPath(key)
		if !ok || !filepath.IsAbs(filepath.FromSlash(key)) {
			continue
		}
		if matcher.hardExcluded(key) {
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
	return s.scanWithin(ctx, opts, storeObjects, nil)
}

func (s *Store) scanWithin(ctx context.Context, opts SnapshotOptions, storeObjects bool, roots []string) (map[string]Entry, error) {
	if err := s.validateWorkspaceRoot(); err != nil {
		return nil, err
	}
	var matcher *ignoreMatcher
	var err error
	if roots == nil {
		matcher, err = newIgnoreMatcher(s.workspaceRoot, opts)
	} else {
		if err := validateSubtreeRoots(roots); err != nil {
			return nil, err
		}
		if err := s.validateSelectedAdministration(roots); err != nil {
			return nil, err
		}
		var admin []string
		admin, err = gitAdministration(s.workspaceRoot)
		matcher = &ignoreMatcher{root: s.workspaceRoot, gitAdmin: admin}
	}
	if err != nil {
		return nil, fmt.Errorf("localhistory: resolve ignores: %w", err)
	}
	maxFileSize := opts.MaxFileSize
	if maxFileSize <= 0 {
		maxFileSize = defaultMaxFileSize
	}
	entries := make(map[string]Entry)
	included := normalizedIncludedPaths(opts.IncludePaths)
	if roots != nil {
		included = roots
	}
	scanStarted := time.Now().UnixNano()
	seen := make(map[string]bool)
	if roots == nil {
		defer func() { s.forgetUnseen(seen) }()
	}
	var files []scanFile
	visit := func(path string, dirEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if roots != nil && os.IsNotExist(walkErr) {
				for _, root := range roots {
					if path == filepath.Join(s.workspaceRoot, filepath.FromSlash(root)) {
						return nil
					}
				}
			}
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
		seen[rel] = true
		if matcher.hardExcluded(rel) {
			if dirEntry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		forced := pathForcedIncluded(rel, included)
		if !dirEntry.IsDir() {
			files = append(files, scanFile{rel: rel, path: path, dirEntry: dirEntry, forced: forced})
			return nil
		}
		info, err := dirEntry.Info()
		if err != nil {
			return err
		}
		if !forced && matcher.ignored(rel, true) {
			return filepath.SkipDir
		}
		entries[rel] = Entry{Type: EntryDirectory, Mode: storedMode(info.Mode())}
		return nil
	}
	if roots == nil {
		err = filepath.WalkDir(s.workspaceRoot, visit)
	} else {
		for _, root := range roots {
			var path string
			path, err = s.workspacePath(root)
			if err != nil {
				break
			}
			err = filepath.WalkDir(path, visit)
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		err = s.scanFiles(ctx, files, entries, matcher, maxFileSize, storeObjects, scanStarted)
	}
	if err != nil {
		return nil, fmt.Errorf("localhistory: scan workspace: %w", err)
	}
	return entries, nil
}

// scanFile is one non-directory entry found by the walk.
type scanFile struct {
	rel, path string
	dirEntry  fs.DirEntry
	forced    bool

	info     os.FileInfo
	entry    Entry
	include  bool
	read     bool // content must be read; the stat cache did not cover it
	identity string
	linkable bool
	err      error
}

// scanFiles inspects the walked files. Each file needs several metadata
// calls: its type, its ignore rule, its identity and its cached object. On
// Windows every such call costs tens of microseconds, so they run on a few
// workers, as Git's preloaded index does. Content reads, cache updates and
// hardlink detection then run in walk order, as before.
func (s *Store) scanFiles(ctx context.Context, files []scanFile, entries map[string]Entry, matcher *ignoreMatcher, maxFileSize int64, storeObjects bool, scanStarted int64) error {
	var next atomic.Int64
	var wg sync.WaitGroup
	for range min(scanWorkers, len(files)) {
		wg.Go(func() {
			for {
				i := int(next.Add(1) - 1)
				if i >= len(files) {
					return
				}
				s.inspectFile(ctx, &files[i], matcher, maxFileSize, storeObjects)
			}
		})
	}
	wg.Wait()
	hardlinks := make(map[string]string)
	for i := range files {
		file := &files[i]
		if file.err != nil {
			return file.err
		}
		if !file.include {
			continue
		}
		entry := file.entry
		if file.read {
			var err error
			if storeObjects {
				entry, err = s.storeRegularFile(ctx, file.path)
			} else {
				entry, err = hashRegularFile(file.path, file.info.Mode())
			}
			if err != nil {
				return err
			}
			s.rememberEntry(file.rel, file.info, file.identity, storeObjects, entry, scanStarted)
		}
		if file.info.Mode().IsRegular() {
			entry.Mode = storedMode(file.info.Mode())
			if file.linkable {
				if canonical := hardlinks[file.identity]; canonical != "" {
					entry.Type = EntryHardlink
					entry.HardlinkTarget = canonical
				} else {
					hardlinks[file.identity] = file.rel
				}
			}
		}
		entries[file.rel] = entry
	}
	return nil
}

// scanWorkers bounds the concurrent metadata calls of one scan.
var scanWorkers = min(runtime.GOMAXPROCS(0), 8)

// inspectFile classifies one walked file without reading its content.
func (s *Store) inspectFile(ctx context.Context, file *scanFile, matcher *ignoreMatcher, maxFileSize int64, storeObjects bool) {
	if file.err = ctx.Err(); file.err != nil {
		return
	}
	info, err := file.dirEntry.Info()
	if err != nil {
		file.err = err
		return
	}
	file.info = info
	if !file.forced && matcher.ignored(file.rel, info.IsDir()) {
		return
	}
	mode := info.Mode()
	switch {
	case mode&os.ModeSymlink != 0:
		target, err := os.Readlink(file.path)
		if err != nil {
			file.err = err
			return
		}
		file.entry = Entry{Type: EntrySymlink, Mode: storedMode(mode), SymlinkTarget: target}
	case mode.IsRegular():
		if !file.forced && info.Size() > maxFileSize {
			return
		}
		file.identity, file.linkable = fileIdentityKey(file.path, info)
		var cached bool
		file.entry, cached = s.cachedEntry(file.rel, info, file.identity, storeObjects)
		file.read = !cached
	case mode.IsDir():
		// The entry became a directory after the walk listed it. The next
		// scan walks it; this one records it without contents.
		file.entry = Entry{Type: EntryDirectory, Mode: storedMode(mode)}
	default:
		file.entry = Entry{Type: EntrySpecial, Mode: storedMode(mode), SpecialMode: mode.Type().String()}
	}
	file.include = true
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
	if err := validateScopedManifest(*manifest); err != nil {
		return err
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
	if err := validateScopedManifest(manifest); err != nil {
		return Manifest{}, err
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

// verifyEvidence is verify for retention's repeated evidence checks: a
// protected snapshot's content is checked completely once per Store. Later
// prunes only confirm its immutable manifest is still present; restore and
// explicit Verify read content again.
func (s *Store) verifyEvidence(ctx context.Context, manifestID string) error {
	if s.snapshotEvidenceVerified(manifestID) && validID(manifestID) {
		if _, err := os.Stat(filepath.Join(s.root, "manifests", manifestID+".json")); err == nil {
			return nil
		}
		s.forgetVerifiedSnapshots([]string{manifestID})
	}
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
		if _, err := s.verifiedFileObject(entry.Hash); err != nil {
			return fmt.Errorf("localhistory: verify %s: %w", path, err)
		}
	}
	s.markSnapshotEvidenceVerified(manifestID)
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

// stats measures what the store retains. SQLite metadata counts its in-use
// pages: free pages left by pruned snapshots and the WAL/shared-memory
// sidecars are reusable working space, and counting them made a store whose
// content fit its budget look permanently over it, so every prune discarded
// all ordinary history.
func (s *Store) stats() (Stats, error) {
	var stats Stats
	metadata := filepath.Join(s.root, "history.db")
	err := filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || path == metadata || path == metadata+"-wal" || path == metadata+"-shm" {
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
	var pages, free, pageSize int64
	if err := s.db.QueryRow(`SELECT page_count,freelist_count,page_size FROM pragma_page_count(),pragma_freelist_count(),pragma_page_size()`).Scan(&pages, &free, &pageSize); err != nil {
		return Stats{}, err
	}
	stats.PhysicalBytes += max(0, pages-free) * pageSize
	return stats, nil
}

// cachedEntry returns the previous scan's entry for an unchanged file. A
// stored scan only trusts entries whose objects were stored and still exist.
func (s *Store) cachedEntry(rel string, info os.FileInfo, identity string, storeObjects bool) (Entry, bool) {
	// Restore planning reads every file: its preview must not depend on
	// metadata that a same-granule write could have left unchanged.
	if !storeObjects {
		return Entry{}, false
	}
	s.statMu.Lock()
	cached, ok := s.statCache[rel]
	s.statMu.Unlock()
	if !ok || cached.size != info.Size() || cached.modTime != info.ModTime().UnixNano() || cached.identity != identity || cached.change != changeKey(info) {
		return Entry{}, false
	}
	// A write in the same timestamp granule as the observation is invisible
	// to metadata; such entries are re-read until they age out.
	if cached.scanned-cached.modTime < int64(racyWindow) {
		return Entry{}, false
	}
	if !cached.stored {
		return Entry{}, false
	}
	path, err := objectPath(s.root, "files", cached.entry.Hash, ".json")
	if err != nil {
		return Entry{}, false
	}
	if _, err := os.Stat(path); err != nil {
		return Entry{}, false
	}
	return cached.entry, true
}

func (s *Store) rememberEntry(rel string, info os.FileInfo, identity string, stored bool, entry Entry, scanned int64) {
	s.statMu.Lock()
	defer s.statMu.Unlock()
	if s.statCache == nil {
		s.statCache = make(map[string]statEntry)
	}
	s.statCache[rel] = statEntry{size: info.Size(), modTime: info.ModTime().UnixNano(), change: changeKey(info), identity: identity, scanned: scanned, stored: stored, entry: entry}
}

// forgetUnseen drops cache entries for paths the last walk did not visit.
func (s *Store) forgetUnseen(seen map[string]bool) {
	s.statMu.Lock()
	defer s.statMu.Unlock()
	for rel := range s.statCache {
		if !seen[rel] {
			delete(s.statCache, rel)
		}
	}
}
