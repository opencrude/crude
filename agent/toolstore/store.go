// Package toolstore owns the bytes of acquired code-intelligence tools.
//
// Each tool lives in one read-only directory per package, version and artifact
// digest under a root that the caller derives from CRUDE_HOME. The directory
// holds an immutable manifest.json and the extracted files. Mutable accounting
// (the workspaces that use a tool and when it was last used) lives in one
// separate usage.json that is replaced atomically. Every install, failed
// install, first use by a workspace and removal is appended to history.jsonl.
//
// The store is not an approval owner: callers pass an approval reference that
// the store records. It knows nothing about languages. It never deletes a tool
// on its own: removal needs a preview, and Remove fails closed when anything in
// the preview changed.
//
// Concurrency: one process owns a store root (the per-user core). Installs of
// the same package and version are serialized by an in-process lock, so they
// download once. The store does not lock across processes; two processes that
// open the same root at the same time can race. Store methods do blocking file
// and network I/O and must not run on a UI goroutine.
package toolstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	manifestName           = "manifest.json"
	contentDir             = "files"
	usageName              = "usage.json"
	historyName            = "history.jsonl"
	stagingName            = ".staging"
	evaluationPendingName  = ".evaluation-pending"
	preparationPendingName = ".preparation-pending"
	usageTemp              = ".usage-"

	maxManifestBytes = 1 << 20
	maxNameLength    = 128
)

// Archive kinds accepted by Request.Archive.
const (
	ArchiveZip   = "zip"
	ArchiveTarGz = "tar.gz"
	ArchiveTarXz = "tar.xz"
	ArchiveGz    = "gz"
	ArchiveRaw   = "raw"
	// ArchivePrepared is a tree produced by a caller-owned preparation process.
	ArchivePrepared = "prepared"
)

// Digest origins recorded in Tool.DigestOrigin.
const (
	DigestExpected      = "expected"
	DigestFirstDownload = "first-download"
	DigestPreparedTree  = "prepared-tree"
)

// Event kinds recorded in the history.
const (
	EventInstall       = "install"
	EventInstallFailed = "install-failed"
	EventRemove        = "remove"
	EventUse           = "use"
)

// DefaultMaxEntries bounds the number of archive entries when
// Request.MaxEntries is zero.
const DefaultMaxEntries = 65536

// Request describes one tool artifact to install.
type Request struct {
	Package, Version, URL string
	// Archive is "zip", "tar.gz", "tar.xz", "gz" (one compressed file),
	// "raw" (the download is the executable), or "prepared".
	Archive string
	// Bin is the slash-separated executable path inside the extracted tree.
	// For "gz" and "raw" it is the file name that the store writes.
	Bin string
	// Command is the catalog launch command bound to this artifact.
	Command string
	// ExpectedSHA256 is the downloaded artifact digest in hex. When it is
	// empty, the store records the first download or prepared-tree digest.
	ExpectedSHA256 string
	// MaxDownloadBytes and MaxExtractedBytes are required budgets.
	MaxDownloadBytes, MaxExtractedBytes int64
	// MaxEntries bounds archive entries; zero means DefaultMaxEntries.
	MaxEntries int
	Source     string
	Licenses   []string
	// InstalledBy names who asked for the install, for example "user" or
	// "automatic". Approval references the caller's approval record. Both are
	// required.
	InstalledBy, Approval string
	// Prepare produces files in output and uses scratch for temporary work.
	// It must join every owned process on every return. If teardown is
	// uncertain it returns PreparationUnverifiedError or
	// fileio.CommandTreeUnverifiedError, so the store retains the staging tree.
	// It owns source/network admission and download-budget enforcement and
	// must never run without authority.
	// Required only when Archive is ArchivePrepared.
	Prepare func(ctx context.Context, output, scratch string) error
	// Evaluate checks the exact sealed staged executable before publication.
	// It runs off mu, under the install's exclusive package/version and staging
	// ownership. It must join all owned processes before returning, including
	// on failure/cancellation. EvaluationUnverifiedError retains its staging
	// folder for inspection instead of deleting possibly live executable bytes.
	// Existing artifacts do not invoke this callback.
	Evaluate func(context.Context, Tool) error
	// PreparePublication runs immediately before acquiring the publication
	// lock. It may perform expensive caller-owned project/root/catalog checks.
	// It must honor ctx. It does not replace the short AdmitPublication fence.
	// Existing artifacts are returned without calling this function.
	PreparePublication func(context.Context) error
	// AdmitPublication is an optional caller-owned fence for a new artifact.
	// It runs once, after staging and immediately before the rename, without
	// performing I/O. The caller checks ctx while holding its authority gate.
	// A successful return admits that publication even if the caller's
	// authority changes afterward. It does not authorize tool use.
	// Existing artifacts are returned without calling this function.
	AdmitPublication func(context.Context) error
}

// Tool is one installed tool. The immutable fields come from the manifest;
// Workspaces and LastUsed come from usage.json.
type Tool struct {
	ID, Package, Version string
	// Dir is the tool directory. Executable is an absolute path inside it.
	Dir, Executable string
	Bin, Command    string
	// SHA256 is the digest of the downloaded artifact or prepared tree.
	SHA256 string
	// Size is the number of extracted content bytes.
	Size        int64
	Files       int
	Source, URL string
	Archive     string
	Licenses    []string
	// DigestOrigin is "expected", "first-download" or "prepared-tree".
	DigestOrigin          string
	InstalledAt           time.Time
	InstalledBy, Approval string
	Workspaces            []string
	LastUsed              time.Time
}

// Store is a tool store rooted at one directory.
type Store struct {
	root string

	// mu guards index, usage, usage.json and history.jsonl.
	mu    storeMutex
	index map[string]Tool
	usage usageFile
	// Retain a same-process refusal if a marker's removal could not be synced.
	unconfirmed map[string]bool
	// A reservation that could not be confirmed remains a same-process fence.
	preparationUnconfirmed map[string]bool
	// executableLeases pins exact tool identities through joined process stop.
	// Acquisition and removal share mu; callers never hold their own manager
	// lock while acquiring or releasing a lease.
	executableLeases map[string]int

	keysMu sync.Mutex
	keys   map[string]*keyLock

	// Test seams. Production code never changes them.
	allowLoopbackHTTP bool
	stallTimeout      time.Duration
	now               func() time.Time
	syncHistoryDir    func(string) error
}

// storeMutex supports cancellation without leaving a waiter goroutine behind.
type storeMutex struct{ ch chan struct{} }

func (m *storeMutex) Lock()   { m.ch <- struct{}{} }
func (m *storeMutex) Unlock() { <-m.ch }
func (m *storeMutex) TryLock() bool {
	select {
	case m.ch <- struct{}{}:
		return true
	default:
		return false
	}
}
func (m *storeMutex) LockContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case m.ch <- struct{}{}:
		if err := ctx.Err(); err != nil {
			m.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type keyLock struct {
	ch   chan struct{}
	refs int
}

type manifest struct {
	ID           string    `json:"id"`
	Package      string    `json:"package"`
	Version      string    `json:"version"`
	SHA256       string    `json:"sha256"`
	DigestOrigin string    `json:"digest_origin"`
	Executable   string    `json:"executable"`
	Command      string    `json:"command,omitempty"`
	Size         int64     `json:"size"`
	Files        int       `json:"files"`
	Archive      string    `json:"archive"`
	Source       string    `json:"source,omitempty"`
	URL          string    `json:"url"`
	Licenses     []string  `json:"licenses,omitempty"`
	InstalledAt  time.Time `json:"installed_at"`
	InstalledBy  string    `json:"installed_by"`
	Approval     string    `json:"approval"`
}

// Open opens or creates a store at root. root must be an absolute path that
// the caller derives from CRUDE_HOME. Open removes stale installation scratch
// but retains interrupted removals for inspection.
func Open(root string) (*Store, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("tool store folder %q is not an absolute path", root)
	}
	root = filepath.Clean(root)
	if err := os.MkdirAll(filepath.Join(root, stagingName), 0o700); err != nil {
		return nil, fmt.Errorf("create tool store folder: %w", err)
	}
	s := &Store{
		root:                   root,
		mu:                     storeMutex{ch: make(chan struct{}, 1)},
		unconfirmed:            make(map[string]bool),
		preparationUnconfirmed: make(map[string]bool),
		executableLeases:       make(map[string]int),
		keys:                   make(map[string]*keyLock),
		stallTimeout:           defaultStallTimeout,
		now:                    time.Now,
		syncHistoryDir:         syncDir,
	}
	if err := s.removeStaleStaging(); err != nil {
		return nil, err
	}
	usage, err := readUsage(filepath.Join(root, usageName))
	if err != nil {
		return nil, err
	}
	s.usage = usage
	if err := sealHistoryTail(filepath.Join(root, historyName)); err != nil {
		return nil, err
	}
	index, _, err := s.scan(context.Background())
	if err != nil {
		return nil, err
	}
	s.index = index
	return s, nil
}

// Root returns the store root.
func (s *Store) Root() string { return s.root }

func (s *Store) removeStaleStaging() error {
	staging := filepath.Join(s.root, stagingName)
	retained, _, uncertain, err := preparationReservations(context.Background(), s.root)
	if err != nil {
		return fmt.Errorf("inspect tool preparation records: %w", err)
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		return fmt.Errorf("read tool store staging folder: %w", err)
	}
	for _, entry := range entries {
		// A removal staging folder can contain installed tools whose move
		// did not settle. Never delete those tools during startup.
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "install-") {
			continue
		}
		if uncertain || retained[entry.Name()] {
			continue
		}
		retained := false
		for _, marker := range []string{evaluationPendingName, preparationPendingName} {
			if _, err := os.Lstat(filepath.Join(staging, entry.Name(), marker)); err == nil {
				retained = true
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inspect staged tool operation: %w", err)
			}
		}
		if retained {
			continue
		}
		if err := removeTree(filepath.Join(staging, entry.Name())); err != nil {
			return fmt.Errorf("remove stale staging data: %w", err)
		}
	}
	rootEntries, err := os.ReadDir(s.root)
	if err != nil {
		return fmt.Errorf("read tool store folder: %w", err)
	}
	for _, entry := range rootEntries {
		if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), usageTemp) {
			if err := os.Remove(filepath.Join(s.root, entry.Name())); err != nil {
				return fmt.Errorf("remove stale usage file: %w", err)
			}
		}
	}
	return nil
}

// Lookup returns the installed tool for an exact package and version. It
// reports false when no tool or more than one build of that version exists.
func (s *Store) Lookup(pkg, version string) (Tool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	matches := s.matchesLocked(pkg, version)
	if len(matches) != 1 {
		return Tool{}, false
	}
	return s.withUsageLocked(matches[0]), true
}

// matchesLocked returns the indexed tools for pkg and version whose manifest
// is still on disk. It drops index entries whose directory disappeared.
func (s *Store) matchesLocked(pkg, version string) []Tool {
	var matches []Tool
	for id, tool := range s.index {
		if tool.Package != pkg || tool.Version != version {
			continue
		}
		pending, err := publicationPending(filepath.Dir(tool.Dir), filepath.Base(tool.Dir))
		if err != nil || pending || s.unconfirmed[id] {
			delete(s.index, id)
			continue
		}
		if _, err := os.Lstat(filepath.Join(tool.Dir, manifestName)); err != nil {
			delete(s.index, id)
			continue
		}
		matches = append(matches, tool)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
	return matches
}

// List reads the store from disk and returns every installed tool, sorted by
// package and version, with the sum of their content sizes.
func (s *Store) List(ctx context.Context) ([]Tool, int64, error) {
	if err := s.mu.LockContext(ctx); err != nil {
		return nil, 0, err
	}
	defer s.mu.Unlock()
	if err := s.refreshLocked(ctx); err != nil {
		return nil, 0, err
	}
	tools := make([]Tool, 0, len(s.index))
	var total int64
	for _, tool := range s.index {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		tools = append(tools, s.withUsageLocked(tool))
		total += tool.Size
	}
	sortTools(tools)
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	return tools, total, nil
}

// Unrecognized lists entries under the store root that are not tools or store
// files, for example a folder without a valid manifest. The store never
// removes them.
func (s *Store) Unrecognized(ctx context.Context) ([]string, error) {
	if err := s.mu.LockContext(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	_, unknown, err := s.scan(ctx)
	return unknown, err
}

// MarkUsed records that workspace uses the tool now. The first use by a
// workspace is also recorded in the history.
func (s *Store) MarkUsed(id, workspace string) error {
	return s.MarkUsedContext(context.Background(), id, workspace, nil)
}

// MarkUsedContext records use only after a cancellable store-lock wait and a
// short caller-owned admission check immediately before accounting. admit runs
// under the store lock; it must not perform I/O or call back into the store.
// Cancellation after successful admission does not undo the admitted effect.
func (s *Store) MarkUsedContext(ctx context.Context, id, workspace string, admit func(context.Context) error) error {
	if strings.TrimSpace(workspace) == "" {
		return errors.New("workspace is empty")
	}
	if err := s.mu.LockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	tool, ok := s.index[id]
	if ok {
		pending, err := publicationPending(filepath.Dir(tool.Dir), filepath.Base(tool.Dir))
		if err != nil || pending || s.unconfirmed[id] {
			return fmt.Errorf("the tool publication needs inspection: %s", id)
		}
		if _, err := os.Lstat(filepath.Join(tool.Dir, manifestName)); err != nil {
			delete(s.index, id)
			ok = false
		}
	}
	if !ok {
		return fmt.Errorf("no installed tool has the ID %q", id)
	}
	return s.markUsedLocked(ctx, tool, workspace, admit)
}

func (s *Store) markUsedLocked(ctx context.Context, tool Tool, workspace string, admit func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if admit != nil {
		if err := admit(ctx); err != nil {
			return err
		}
	}
	id := tool.ID
	previous, had := s.usage.Tools[id]
	entry := usageEntry{Workspaces: append([]string(nil), previous.Workspaces...), LastUsed: s.now().UTC()}
	first := !containsString(entry.Workspaces, workspace)
	if first {
		entry.Workspaces = append(entry.Workspaces, workspace)
		sort.Strings(entry.Workspaces)
	}
	s.usage.Tools[id] = entry
	if err := s.writeUsageLocked(); err != nil {
		if had {
			s.usage.Tools[id] = previous
		} else {
			delete(s.usage.Tools, id)
		}
		return err
	}
	if first {
		return s.appendLocked(Event{Kind: EventUse, ToolID: id, Package: tool.Package, Version: tool.Version, Detail: workspace})
	}
	return nil
}

func (s *Store) withUsageLocked(tool Tool) Tool {
	tool.Licenses = append([]string(nil), tool.Licenses...)
	entry := s.usage.Tools[tool.ID]
	tool.Workspaces = append([]string(nil), entry.Workspaces...)
	tool.LastUsed = entry.LastUsed
	return tool
}

func (s *Store) refreshLocked(ctx context.Context) error {
	index, _, err := s.scan(ctx)
	if err != nil {
		return err
	}
	s.index = index
	return nil
}

// scan reads every tool manifest under the root. It returns the valid tools
// and the root-relative paths it does not recognize.
func (s *Store) scan(ctx context.Context) (map[string]Tool, []string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	index := make(map[string]Tool)
	var unknown []string
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, nil, fmt.Errorf("read tool store folder: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		name := entry.Name()
		switch name {
		case stagingName, usageName, historyName, publicationPendingDir, preparationPendingDir:
			continue
		}
		if !entry.IsDir() || validComponent(name) != nil {
			unknown = append(unknown, name)
			continue
		}
		versions, err := os.ReadDir(filepath.Join(s.root, name))
		if err != nil {
			return nil, nil, fmt.Errorf("read tool store folder: %w", err)
		}
		for _, version := range versions {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			rel := name + "/" + version.Name()
			if s.unconfirmed[rel] {
				unknown = append(unknown, rel)
				continue
			}
			if !version.IsDir() {
				unknown = append(unknown, rel)
				continue
			}
			tool, err := s.readToolContext(ctx, name, version.Name())
			if err != nil {
				if ctx.Err() != nil {
					return nil, nil, ctx.Err()
				}
				unknown = append(unknown, rel)
				continue
			}
			index[tool.ID] = tool
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	pending, err := pendingPublications(ctx, s.root)
	if err != nil {
		return nil, nil, err
	}
	unknown = append(unknown, pending...)
	retainedStages, preparations, uncertain, err := preparationReservations(ctx, s.root)
	if err != nil {
		return nil, nil, err
	}
	unknown = append(unknown, preparations...)
	stagingEntries, err := os.ReadDir(filepath.Join(s.root, stagingName))
	if err != nil {
		return nil, nil, fmt.Errorf("read tool store staging folder: %w", err)
	}
	for _, entry := range stagingEntries {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		pending := false
		if entry.IsDir() {
			for _, marker := range []string{evaluationPendingName, preparationPendingName} {
				_, pendingErr := os.Lstat(filepath.Join(s.root, stagingName, entry.Name(), marker))
				if pendingErr == nil {
					pending = true
				} else if !errors.Is(pendingErr, os.ErrNotExist) {
					return nil, nil, pendingErr
				}
			}
		}
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "install-") || pending || uncertain || retainedStages[entry.Name()] {
			unknown = append(unknown, stagingName+"/"+entry.Name())
		}
	}
	for id := range s.unconfirmed {
		unknown = append(unknown, id)
	}
	sort.Strings(unknown)
	if len(unknown) > 1 {
		n := 1
		for _, name := range unknown[1:] {
			if name != unknown[n-1] {
				unknown[n] = name
				n++
			}
		}
		unknown = unknown[:n]
	}
	return index, unknown, nil
}

func (s *Store) readToolContext(ctx context.Context, pkg, dir string) (Tool, error) {
	toolDir := filepath.Join(s.root, pkg, dir)
	if pending, err := publicationPending(filepath.Dir(toolDir), dir); err != nil {
		return Tool{}, err
	} else if pending {
		return Tool{}, errors.New("the tool publication needs inspection")
	}
	data, err := readBoundedContext(ctx, filepath.Join(toolDir, manifestName), maxManifestBytes)
	if err != nil {
		return Tool{}, err
	}
	var m manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return Tool{}, fmt.Errorf("manifest %s: %w", filepath.Join(toolDir, manifestName), err)
	}
	if err := m.validate(pkg, dir); err != nil {
		return Tool{}, fmt.Errorf("manifest %s: %w", filepath.Join(toolDir, manifestName), err)
	}
	return toolFromManifest(toolDir, m), nil
}

func (m manifest) validate(pkg, dir string) error {
	if err := validComponent(m.Package); err != nil || m.Package != pkg {
		return errors.New("package does not match its folder")
	}
	if err := validComponent(m.Version); err != nil {
		return errors.New("version is not valid")
	}
	if !validDigest(m.SHA256) {
		return errors.New("digest is not a SHA-256 value")
	}
	if dir != versionDir(m.Version, m.SHA256) || m.ID != m.Package+"/"+dir {
		return errors.New("identity does not match its folder")
	}
	if _, err := cleanEntryName(m.Executable); err != nil || m.Executable == "" {
		return errors.New("executable path is not contained in the tool")
	}
	if m.DigestOrigin != DigestExpected && m.DigestOrigin != DigestFirstDownload && m.DigestOrigin != DigestPreparedTree {
		return errors.New("digest origin is not valid")
	}
	if (m.DigestOrigin == DigestPreparedTree) != (m.Archive == ArchivePrepared) {
		return errors.New("digest origin does not match the artifact")
	}
	return nil
}

func toolFromManifest(toolDir string, m manifest) Tool {
	return Tool{
		ID:           m.ID,
		Package:      m.Package,
		Version:      m.Version,
		Dir:          toolDir,
		Executable:   filepath.Join(toolDir, contentDir, filepath.FromSlash(m.Executable)),
		Bin:          m.Executable,
		Command:      m.Command,
		SHA256:       m.SHA256,
		Size:         m.Size,
		Files:        m.Files,
		Source:       m.Source,
		URL:          m.URL,
		Archive:      m.Archive,
		Licenses:     append([]string(nil), m.Licenses...),
		DigestOrigin: m.DigestOrigin,
		InstalledAt:  m.InstalledAt,
		InstalledBy:  m.InstalledBy,
		Approval:     m.Approval,
	}
}

func versionDir(version, digest string) string {
	return version + "-" + digest[:12]
}

func sortTools(tools []Tool) {
	sort.Slice(tools, func(i, j int) bool {
		if tools[i].Package != tools[j].Package {
			return tools[i].Package < tools[j].Package
		}
		if tools[i].Version != tools[j].Version {
			return tools[i].Version < tools[j].Version
		}
		return tools[i].ID < tools[j].ID
	})
}

// validComponent accepts package and version names that are one portable path
// component.
func validComponent(name string) error {
	if name == "" || len(name) > maxNameLength {
		return fmt.Errorf("name %q must have 1 to %d characters", name, maxNameLength)
	}
	if name[0] == '.' || name[0] == '-' {
		return fmt.Errorf("name %q must not start with a dot or a hyphen", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-', r == '+', r == '@':
		default:
			return fmt.Errorf("name %q contains the character %q, which is not permitted", name, r)
		}
	}
	if !filepath.IsLocal(name) {
		return fmt.Errorf("name %q is reserved on this system", name)
	}
	return nil
}

func validDigest(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	for _, r := range digest {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// cleanEntryName validates a slash-separated archive path and returns it
// without a leading "./" or a trailing "/". It returns "" for the archive root.
func cleanEntryName(name string) (string, error) {
	for strings.HasPrefix(name, "./") {
		name = name[2:]
	}
	name = strings.TrimSuffix(name, "/")
	if name == "" || name == "." {
		return "", nil
	}
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("archive path %q is absolute", name)
	}
	for _, r := range name {
		if r == '\\' || r == ':' || r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("archive path %q contains a character that is not permitted", name)
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("archive path %q goes outside the tool folder", name)
		}
	}
	if path.Clean(name) != name {
		return "", fmt.Errorf("archive path %q is not in its simplest form", name)
	}
	return name, nil
}

func readBounded(name string, limit int64) ([]byte, error) {
	return readBoundedContext(context.Background(), name, limit)
}

type boundedContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r boundedContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func readBoundedContext(ctx context.Context, name string, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(boundedContextReader{ctx, file}, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", name, limit)
	}
	return data, nil
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// lockKeys takes the in-process locks for the given install keys in sorted
// order. The returned function releases them.
func (s *Store) lockKeys(ctx context.Context, keys []string) (func(), error) {
	keys = append([]string(nil), keys...)
	sort.Strings(keys)
	var held []string
	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			s.unlockKey(held[i])
		}
	}
	for i, key := range keys {
		if i > 0 && keys[i-1] == key {
			continue
		}
		if err := s.lockKey(ctx, key); err != nil {
			release()
			return nil, err
		}
		held = append(held, key)
	}
	return release, nil
}

func (s *Store) lockKey(ctx context.Context, key string) error {
	s.keysMu.Lock()
	lock := s.keys[key]
	if lock == nil {
		lock = &keyLock{ch: make(chan struct{}, 1)}
		s.keys[key] = lock
	}
	lock.refs++
	s.keysMu.Unlock()
	select {
	case lock.ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		s.releaseKeyRef(key, lock)
		return ctx.Err()
	}
}

func (s *Store) unlockKey(key string) {
	s.keysMu.Lock()
	lock := s.keys[key]
	s.keysMu.Unlock()
	<-lock.ch
	s.releaseKeyRef(key, lock)
}

func (s *Store) releaseKeyRef(key string, lock *keyLock) {
	s.keysMu.Lock()
	lock.refs--
	if lock.refs == 0 {
		delete(s.keys, key)
	}
	s.keysMu.Unlock()
}

func installKey(pkg, version string) string { return pkg + "\x00" + version }
