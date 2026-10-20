package worktree

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"crude/fileio"
)

type Service struct {
	mu          sync.Mutex
	root, store string
	options     Options
	contexts    map[string]Context
	sizes       map[string]int64
}

// Open is passive: it loads only current-format manifests; it neither invokes
// Git nor creates directories, repairs checkouts, unpins crash-left runs or replays setup.
func Open(root, store string, options Options) (*Service, error) {
	r, err := canonical(root)
	if err != nil {
		return nil, err
	}
	a, err := filepath.Abs(store)
	if err != nil {
		return nil, err
	}
	// Resolve existing ancestors so a symlink cannot conceal an in-project store.
	a, err = canonicalFuture(a)
	if err != nil {
		return nil, err
	}
	if within(r, a) || within(a, r) {
		return nil, errors.New("managed store and logical checkout must be disjoint")
	}
	if options.MaxFiles <= 0 {
		options.MaxFiles = 20000
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = 256 << 20
	}
	if options.MaxFileBytes <= 0 {
		options.MaxFileBytes = 32 << 20
	}
	s := &Service{root: r, store: a, options: options, contexts: map[string]Context{}, sizes: map[string]int64{}}
	entries, err := os.ReadDir(a)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(a, e.Name())
		fi, err := os.Lstat(p)
		if err != nil {
			return nil, err
		}
		if !fi.Mode().IsRegular() || fi.Size() > options.MaxBytes*2+(16<<20) {
			return nil, errors.New("unsafe or oversized manifest")
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var c Context
		if err := json.Unmarshal(data, &c); err != nil {
			return nil, err
		}
		if c.Version != FormatVersion || c.LogicalRoot != r || e.Name() != c.ID+".json" || !validID(c.ID) || !validOwner(c.Owner) {
			return nil, errors.New("invalid current-format worktree manifest")
		}
		if c.Mode != Shared && c.Mode != Isolated {
			return nil, errors.New("invalid resolved mode")
		}
		want := r
		if c.Mode == Isolated {
			want = filepath.Join(a, c.ID, "checkout")
		}
		if c.Mode == Isolated && c.Root != want {
			return nil, errors.New("manifest checkout ownership mismatch")
		}
		if len(c.Baseline) > options.MaxFiles {
			return nil, errors.New("manifest baseline file budget exceeded")
		}
		var total int64
		for path, f := range c.Baseline {
			if err := safeRelative(path); err != nil {
				return nil, err
			}
			if f.Type != "file" || f.Hash != hash(f.Data) {
				return nil, errors.New("invalid baseline identity")
			}
			total += int64(len(f.Data))
			if int64(len(f.Data)) > options.MaxFileBytes || total > options.MaxBytes {
				return nil, errors.New("manifest baseline byte budget exceeded")
			}
		}
		for path, f := range c.ArchivedResult {
			if err := safeRelative(path); err != nil {
				return nil, err
			}
			if f.Type != "file" || f.Hash != hash(f.Data) {
				return nil, errors.New("invalid archived result")
			}
			total += int64(len(f.Data))
			if int64(len(f.Data)) > options.MaxFileBytes || total > options.MaxBytes*2 {
				return nil, errors.New("archive byte budget exceeded")
			}
		}
		for path, revision := range c.DocumentRevisions {
			if err := safeRelative(path); err != nil {
				return nil, err
			}
			if revision == 0 {
				return nil, errors.New("invalid accepted document revision")
			}
		}
		for _, pin := range c.Pins {
			if !validOwner(pin) {
				return nil, errors.New("invalid pin identity")
			}
		}
		s.contexts[c.ID] = c
		s.sizes[c.ID] = fi.Size()
	}
	for _, c := range s.contexts {
		if c.Mode == Shared {
			want := r
			if c.SourceContextID != "" {
				source, ok := s.contexts[c.SourceContextID]
				if !ok || source.ID == c.ID || source.CommonGitDir != c.CommonGitDir {
					return nil, errors.New("invalid shared source ownership")
				}
				want = source.Root
			}
			if c.Root != want {
				return nil, errors.New("manifest shared checkout ownership mismatch")
			}
		}
	}
	return s, nil
}

func canonical(path string) (string, error) {
	a, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(a)
}

func canonicalFuture(path string) (string, error) {
	if _, err := os.Lstat(path); err == nil {
		return canonical(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	p := filepath.Dir(path)
	if p == path {
		return "", errors.New("cannot resolve store")
	}
	r, err := canonicalFuture(p)
	return filepath.Join(r, filepath.Base(path)), err
}

func within(root, path string) bool {
	r, err := filepath.Rel(root, path)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

func validOwner(o Owner) bool {
	return strings.TrimSpace(o.AgentID) != "" && strings.TrimSpace(o.RunID) != ""
}
func validID(id string) bool  { b, err := hex.DecodeString(id); return err == nil && len(b) == 16 }
func hash(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

func safeRelative(path string) error {
	if path == "" || strings.Contains(path, "\\") || strings.ContainsRune(path, 0) || filepath.IsAbs(path) || filepath.ToSlash(filepath.Clean(path)) != path {
		return fmt.Errorf("unsafe repository path %q", path)
	}
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." || strings.EqualFold(part, ".git") {
			return fmt.Errorf("git administration or unsafe path %q", path)
		}
	}
	return nil
}

// boundedOutput prevents Git output from bypassing capture budgets.
type boundedOutput struct {
	bytes.Buffer
	limit int64
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if int64(b.Len()+len(p)) > b.limit {
		return 0, errors.New("git output exceeds worktree budget")
	}
	return b.Buffer.Write(p)
}
func (s *Service) git(ctx context.Context, root string, args ...string) ([]byte, error) {
	return s.gitInput(ctx, root, nil, args...)
}

// rejectAppliedFilters refuses setup only when a configured filter driver would
// actually run for a copied path. Merely configured drivers, such as the
// filter.lfs entries Git for Windows installs system-wide, execute nothing.
func (s *Service) rejectAppliedFilters(ctx context.Context, root string, extra []string) error {
	filters, filterErr := s.git(ctx, root, "config", "--get-regexp", `^filter\.`)
	if filterErr != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if len(filters) == 0 {
		return nil
	}
	input, err := s.git(ctx, root, "ls-files", "-z")
	if err != nil {
		return err
	}
	for _, path := range extra {
		input = append(append(input, path...), 0)
	}
	if len(input) == 0 {
		return nil
	}
	attributes, err := s.gitInput(ctx, root, input, "check-attr", "-z", "--stdin", "filter")
	if err != nil {
		return err
	}
	fields := strings.Split(string(attributes), "\x00")
	for i := 0; i+2 < len(fields); i += 3 {
		if value := fields[i+2]; value != "unspecified" && value != "unset" {
			return fmt.Errorf("git filter %q applies to %s; setup execution requires separate authorization", value, fields[i])
		}
	}
	return nil
}

func (s *Service) gitInput(ctx context.Context, root string, stdin []byte, args ...string) ([]byte, error) {
	argv := append([]string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "GIT_") {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	var out = boundedOutput{limit: s.options.MaxBytes}
	var stderr = boundedOutput{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, stderr.String())
	}
	return out.Bytes(), nil
}

func (s *Service) save(c Context) error {
	if err := os.MkdirAll(s.store, 0700); err != nil {
		return err
	}
	c.StorageBytes = 0
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if c.Removed && (len(c.Baseline) != 0 || len(c.ArchivedResult) != 0) {
		// Keep undo evidence on disk, not in every live inventory/request. The
		// compact ledger remains inspectable; full archive loads only on demand.
		archiveDir := filepath.Join(s.store, "archive")
		if err := os.MkdirAll(archiveDir, 0700); err != nil {
			return err
		}
		if err := fileio.WriteAtomicChecked(filepath.Join(archiveDir, c.ID+".json"), data, 0600, nil); err != nil {
			return err
		}
		c.Baseline, c.ArchivedResult, c.DocumentRevisions = nil, nil, nil
		data, err = json.Marshal(c)
		if err != nil {
			return err
		}
	}
	if err := fileio.WriteAtomicChecked(filepath.Join(s.store, c.ID+".json"), data, 0600, nil); err != nil {
		return err
	}
	s.contexts[c.ID] = c
	s.sizes[c.ID] = int64(len(data))
	return nil
}

func clone[T any](v T) T { data, _ := json.Marshal(v); var c T; _ = json.Unmarshal(data, &c); return c }
func (s *Service) Inspect(id string) (Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.contexts[id]
	if !ok {
		return Context{}, errors.New("unknown execution context")
	}
	if c.Removed && len(c.Baseline) == 0 {
		data, err := os.ReadFile(filepath.Join(s.store, "archive", c.ID+".json"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Context{}, err
		}
		if err == nil {
			var archived Context
			if err := json.Unmarshal(data, &archived); err != nil {
				return Context{}, err
			}
			if archived.ID != c.ID || archived.Owner != c.Owner || !archived.Removed {
				return Context{}, errors.New("execution archive identity mismatch")
			}
			return archived, nil
		}
	}
	return clone(c), nil
}
func (s *Service) List() []Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Context, 0, len(s.contexts))
	for _, c := range s.contexts {
		// Inventory must not export every baseline byte just to show paths/pins.
		c.Baseline, c.ArchivedResult, c.DocumentRevisions = nil, nil, nil
		c.StorageBytes = s.sizes[c.ID]
		result = append(result, clone(c))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// Create persists a crash-retained pinned identity before worktree creation. A
// failed materialization stays inspectable and must never be resumed as usable.
func (s *Service) Create(ctx context.Context, req Request) (Context, error) {
	unlock, err := fileio.LockMutationContext(ctx)
	if err != nil {
		return Context{}, err
	}
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Context{}, err
	}
	if !validOwner(req.Owner) {
		return Context{}, errors.New("agent and run identity required")
	}
	mode := req.Mode
	if mode == "" {
		mode = Auto
	}
	if mode != Auto && mode != Shared && mode != Isolated {
		return Context{}, errors.New("unknown isolation mode")
	}
	sourceRoot := s.root
	var source *Context
	if req.SourceContextID != "" {
		c, ok := s.contexts[req.SourceContextID]
		if !ok || !c.Ready || c.Removed || c.LogicalRoot != s.root {
			return Context{}, errors.New("source execution context unavailable or foreign")
		}
		if err := s.verifyCheckout(ctx, c); err != nil {
			return Context{}, err
		}
		sourceRoot, source = c.Root, &c
		// Parent checkout bytes are authoritative; caller overlays belong only to logical root.
		req.Documents = nil
		for path := range c.Baseline {
			req.IncludeIgnored = append(req.IncludeIgnored, path)
		}
	}
	// Shared execution is a binding, not a new snapshot. A child sharing an
	// isolated parent pins that existing checkout for its own exact run.
	shared := func(reason string) (Context, error) {
		c := Context{Version: FormatVersion, LogicalRoot: s.root, Root: sourceRoot, Mode: Shared, Reason: reason, Ready: true, Owner: req.Owner}
		if source != nil {
			pinned := *source
			if !slices.Contains(pinned.Pins, req.Owner) {
				pinned.Pins = append(append([]Owner(nil), pinned.Pins...), req.Owner)
				if err := s.save(pinned); err != nil {
					return Context{}, err
				}
			}
			c.ID, c.CommonGitDir = source.ID, source.CommonGitDir
		}
		return c, nil
	}
	if mode == Shared || mode == Auto && !req.Independent {
		return shared("shared checkout; no separate worktree or snapshot")
	}
	gitRoot, gitErr := s.git(ctx, sourceRoot, "rev-parse", "--show-toplevel")
	if err := ctx.Err(); err != nil {
		return Context{}, err
	}
	isGit := gitErr == nil
	if isGit {
		r, err := canonical(strings.TrimSpace(string(gitRoot)))
		if err != nil || r != sourceRoot {
			return Context{}, errors.New("logical root must be repository top-level")
		}
	}
	// A freshly initialized repository has no commit to branch a worktree
	// from. It shares the checkout like a non-Git project until it does.
	unborn := false
	if isGit && source == nil && req.BaseCommit == "" {
		if _, err := s.git(ctx, sourceRoot, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err != nil {
			unborn = true
		}
	}
	reason := "explicit shared checkout"
	if mode == Auto {
		mode, reason = Shared, "coupled or read-only task"
		if req.Independent && isGit && !unborn {
			mode, reason = Isolated, "independent task isolation"
		}
		if !isGit {
			reason = "non-Git project uses shared checkout"
		}
		if unborn {
			reason = "repository has no commits yet; shared checkout"
		}
	}
	if mode == Isolated && !isGit {
		return Context{}, fmt.Errorf("worktree requires Git: %w", gitErr)
	}
	if mode == Isolated && unborn {
		return Context{}, errors.New("worktree isolation needs at least one commit; commit once or use shared isolation")
	}
	if mode == Shared {
		return shared(reason)
	}
	if err := s.rejectAppliedFilters(ctx, sourceRoot, req.IncludeIgnored); err != nil {
		return Context{}, err
	}
	var base, common string
	if isGit {
		if !unborn {
			ref := "HEAD"
			if req.BaseCommit != "" {
				ref = req.BaseCommit
			}
			b, err := s.git(ctx, sourceRoot, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
			if err != nil {
				return Context{}, err
			}
			base = strings.TrimSpace(string(b))
		}
		g, err := s.git(ctx, sourceRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return Context{}, err
		}
		common, err = canonical(strings.TrimSpace(string(g)))
		if err != nil {
			return Context{}, err
		}
		if within(s.store, common) || within(common, s.store) {
			return Context{}, errors.New("store overlaps Git administration")
		}
	}
	if source != nil {
		if common != source.CommonGitDir {
			return Context{}, errors.New("source repository ownership changed")
		}
		if common != "" {
			g, err := s.git(ctx, s.root, "rev-parse", "--path-format=absolute", "--git-common-dir")
			if err != nil {
				return Context{}, err
			}
			logicalCommon, err := canonical(strings.TrimSpace(string(g)))
			if err != nil || logicalCommon != common {
				return Context{}, errors.New("source belongs to another repository")
			}
		}
	}
	baseline, err := s.capture(ctx, sourceRoot, common, req.Documents, req.IncludeIgnored, isGit)
	if err != nil {
		return Context{}, err
	}
	second, err := s.capture(ctx, sourceRoot, common, req.Documents, req.IncludeIgnored, isGit)
	if err != nil {
		return Context{}, err
	}
	firstBytes, _ := json.Marshal(baseline)
	secondBytes, _ := json.Marshal(second)
	if !bytes.Equal(firstBytes, secondBytes) {
		return Context{}, errors.New("source changed during baseline capture")
	}
	if isGit && !unborn {
		head, err := s.git(ctx, sourceRoot, "rev-parse", "HEAD")
		if err != nil {
			return Context{}, err
		}
		if strings.TrimSpace(string(head)) != base {
			return Context{}, errors.New("HEAD changed during baseline capture")
		}
	}
	if req.BaseCommit != "" {
		head, err := s.git(ctx, sourceRoot, "rev-parse", "HEAD")
		if err != nil {
			return Context{}, err
		}
		if strings.TrimSpace(string(head)) != base {
			return Context{}, errors.New("alternate commit baseline is not supported; exact local HEAD required")
		}
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Context{}, err
	}
	c := Context{Version: FormatVersion, ID: hex.EncodeToString(random[:]), LogicalRoot: s.root, Root: sourceRoot, SourceContextID: req.SourceContextID, CommonGitDir: common, Mode: mode, Reason: reason, BaseCommit: base, Owner: req.Owner, CreatedAt: time.Now().UTC(), Baseline: baseline, Pins: []Owner{req.Owner}}
	c.DocumentRevisions = map[string]uint64{}
	for path, document := range req.Documents {
		c.DocumentRevisions[path] = document.Revision
	}
	if mode == Isolated {
		c.Root = filepath.Join(s.store, c.ID, "checkout")
	}
	// Preparation-incomplete contexts stay inspectable, never runnable. Readiness
	// is not intentional Keep: a parent can review/discard a failed setup.
	if err := s.save(c); err != nil {
		return Context{}, err
	}
	if mode == Isolated {
		if err := os.MkdirAll(filepath.Dir(c.Root), 0700); err != nil {
			return clone(c), err
		}
		// No checkout: Git filters and post-checkout hooks must not execute.
		if _, err := s.git(ctx, s.root, "worktree", "add", "--detach", "--no-checkout", c.Root, base); err != nil {
			return clone(c), err
		}
		if _, err := s.git(ctx, c.Root, "read-tree", base); err != nil {
			return clone(c), err
		}
		// Remove only known tracked files missing from the accepted baseline in
		// this newly allocated checkout, never anything in the logical checkout.
		tracked, err := s.git(ctx, c.Root, "ls-files", "-z")
		if err != nil {
			return clone(c), err
		}
		for _, p := range nulPaths(tracked) {
			if err := safeRelative(p); err != nil {
				return clone(c), err
			}
			if _, ok := baseline[p]; !ok {
				if err := os.Remove(filepath.Join(c.Root, filepath.FromSlash(p))); err != nil && !errors.Is(err, os.ErrNotExist) {
					return clone(c), err
				}
			}
		}
		for p, f := range baseline {
			target, err := safePath(c.Root, p)
			if err != nil {
				return clone(c), err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return clone(c), err
			}
			if err := fileio.WriteAtomicChecked(target, f.Data, os.FileMode(f.Mode), nil); err != nil {
				return clone(c), err
			}
			if err := os.Chmod(target, os.FileMode(f.Mode)); err != nil {
				return clone(c), err
			}
		}
	}
	c.Ready = true
	if err := s.save(c); err != nil {
		return clone(c), err
	}
	return clone(c), nil
}

func nulPaths(data []byte) []string {
	parts := strings.Split(string(data), "\x00")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func safePath(root, path string) (string, error) {
	if err := safeRelative(path); err != nil {
		return "", err
	}
	p := root
	for _, part := range strings.Split(path, "/") {
		p = filepath.Join(p, part)
		fi, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink state unsupported: %s", path)
		}
	}
	return p, nil
}

func (s *Service) read(root, common, path string) (*File, error) {
	p, err := safePath(root, path)
	if err != nil {
		return nil, err
	}
	if common != "" && within(common, p) {
		return nil, errors.New("git administration excluded")
	}
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("nonregular state unsupported: %s", path)
	}
	if fi.Size() > s.options.MaxFileBytes {
		return nil, errors.New("file exceeds baseline budget")
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, s.options.MaxFileBytes+1))
	closeErr := f.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(data)) > s.options.MaxFileBytes {
		return nil, errors.New("file grew beyond baseline budget")
	}
	return &File{Type: "file", Mode: uint32(fi.Mode().Perm()), Hash: hash(data), Data: data}, nil
}

func (s *Service) capture(ctx context.Context, root, common string, docs map[string]Document, include []string, isGit bool) (map[string]File, error) {
	paths := map[string]bool{}
	if isGit {
		unmerged, err := s.git(ctx, root, "ls-files", "--unmerged", "-z")
		if err != nil {
			return nil, err
		}
		if len(unmerged) != 0 {
			return nil, errors.New("unresolved Git index baseline unsupported")
		}
		// The tree/index audit rejects unsupported submodules/symlinks even if
		// their disk state happens to be missing or replaced by a regular file.
		for _, args := range [][]string{{"ls-tree", "-r", "HEAD"}, {"ls-files", "--stage"}} {
			if args[0] == "ls-tree" {
				// A repository without commits has no tree yet; its index still
				// lists every staged entry.
				if _, err := s.git(ctx, root, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err != nil {
					continue
				}
			}
			data, err := s.git(ctx, root, args...)
			if err != nil {
				return nil, err
			}
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "160000 ") || strings.HasPrefix(line, "120000 ") {
					return nil, errors.New("submodule and symlink baselines unsupported")
				}
			}
		}
		data, err := s.git(ctx, root, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
		if err != nil {
			return nil, err
		}
		for _, p := range nulPaths(data) {
			paths[p] = true
		}
	} else {
		if err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if p == root {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if strings.EqualFold(d.Name(), ".git") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				rel, err := filepath.Rel(root, p)
				if err != nil {
					return err
				}
				paths[filepath.ToSlash(rel)] = true
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	for _, p := range include {
		paths[p] = true
	}
	for p := range docs {
		paths[p] = true
	}
	if len(paths) > s.options.MaxFiles {
		return nil, errors.New("baseline file-count budget exceeded")
	}
	files := map[string]File{}
	var total int64
	for p := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, err := s.read(root, common, p)
		if err != nil {
			return nil, err
		}
		if d, ok := docs[p]; ok {
			if d.Revision == 0 {
				return nil, errors.New("accepted document revision required")
			}
			if !d.Exists {
				f = nil
			} else {
				mode := d.Mode
				if mode == 0 {
					mode = defaultDocumentMode
					if f != nil {
						mode = f.Mode
					}
				}
				f = &File{Type: "file", Mode: mode, Hash: hash(d.Data), Data: append([]byte(nil), d.Data...), DocumentRevision: d.Revision}
			}
		}
		if f != nil {
			total += int64(len(f.Data))
			if int64(len(f.Data)) > s.options.MaxFileBytes || total > s.options.MaxBytes {
				return nil, errors.New("baseline byte budget exceeded")
			}
			files[p] = *f
		}
	}
	return files, nil
}
