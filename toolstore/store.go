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
	manifestName = "manifest.json"
	contentDir   = "files"
	usageName    = "usage.json"
	historyName  = "history.jsonl"
	stagingName  = ".staging"
	usageTemp    = ".usage-"

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
)

// Digest origins recorded in Tool.DigestOrigin.
const (
	DigestExpected      = "expected"
	DigestFirstDownload = "first-download"
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
	// Archive is one of "zip", "tar.gz", "tar.xz", "gz" (one compressed
	// file) or "raw" (the download is the executable).
	Archive string
	// Bin is the slash-separated executable path inside the extracted tree.
	// For "gz" and "raw" it is the file name that the store writes.
	Bin string
	// ExpectedSHA256 is the artifact digest in hex. When it is empty, the
	// store records the digest of the first download.
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
}

// Tool is one installed tool. The immutable fields come from the manifest;
// Workspaces and LastUsed come from usage.json.
type Tool struct {
	ID, Package, Version string
	// Dir is the tool directory. Executable is an absolute path inside it.
	Dir, Executable string
	// SHA256 is the digest of the downloaded artifact.
	SHA256 string
	// Size is the number of extracted content bytes.
	Size        int64
	Files       int
	Source, URL string
	Archive     string
	Licenses    []string
	// DigestOrigin is "expected" or "first-download".
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
	mu    sync.Mutex
	index map[string]Tool
	usage usageFile

	keysMu sync.Mutex
	keys   map[string]*keyLock

	// Test seams. Production code never changes them.
	allowLoopbackHTTP bool
	stallTimeout      time.Duration
	now               func() time.Time
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
// the caller derives from CRUDE_HOME. Open removes stale staging directories
// left by an interrupted install or removal.
func Open(root string) (*Store, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("tool store folder %q is not an absolute path", root)
	}
	root = filepath.Clean(root)
	if err := os.MkdirAll(filepath.Join(root, stagingName), 0o700); err != nil {
		return nil, fmt.Errorf("create tool store folder: %w", err)
	}
	s := &Store{
		root:         root,
		keys:         make(map[string]*keyLock),
		stallTimeout: defaultStallTimeout,
		now:          time.Now,
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
	index, _, err := s.scan()
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
	entries, err := os.ReadDir(staging)
	if err != nil {
		return fmt.Errorf("read tool store staging folder: %w", err)
	}
	for _, entry := range entries {
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
func (s *Store) List() ([]Tool, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(); err != nil {
		return nil, 0, err
	}
	tools := make([]Tool, 0, len(s.index))
	var total int64
	for _, tool := range s.index {
		tools = append(tools, s.withUsageLocked(tool))
		total += tool.Size
	}
	sortTools(tools)
	return tools, total, nil
}

// Unrecognized lists entries under the store root that are not tools or store
// files, for example a folder without a valid manifest. The store never
// removes them.
func (s *Store) Unrecognized() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, unknown, err := s.scan()
	return unknown, err
}

// MarkUsed records that workspace uses the tool now. The first use by a
// workspace is also recorded in the history.
func (s *Store) MarkUsed(id, workspace string) error {
	if strings.TrimSpace(workspace) == "" {
		return errors.New("workspace is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tool, ok := s.index[id]
	if ok {
		if _, err := os.Lstat(filepath.Join(tool.Dir, manifestName)); err != nil {
			delete(s.index, id)
			ok = false
		}
	}
	if !ok {
		return fmt.Errorf("no installed tool has the ID %q", id)
	}
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

func (s *Store) refreshLocked() error {
	index, _, err := s.scan()
	if err != nil {
		return err
	}
	s.index = index
	return nil
}

// scan reads every tool manifest under the root. It returns the valid tools
// and the root-relative paths it does not recognize.
func (s *Store) scan() (map[string]Tool, []string, error) {
	index := make(map[string]Tool)
	var unknown []string
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, nil, fmt.Errorf("read tool store folder: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		switch name {
		case stagingName, usageName, historyName:
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
			rel := name + "/" + version.Name()
			if !version.IsDir() {
				unknown = append(unknown, rel)
				continue
			}
			tool, err := s.readTool(name, version.Name())
			if err != nil {
				unknown = append(unknown, rel)
				continue
			}
			index[tool.ID] = tool
		}
	}
	sort.Strings(unknown)
	return index, unknown, nil
}

// readTool reads and validates the manifest of <root>/<pkg>/<dir>.
func (s *Store) readTool(pkg, dir string) (Tool, error) {
	toolDir := filepath.Join(s.root, pkg, dir)
	data, err := readBounded(filepath.Join(toolDir, manifestName), maxManifestBytes)
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
	if m.DigestOrigin != DigestExpected && m.DigestOrigin != DigestFirstDownload {
		return errors.New("digest origin is not valid")
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
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
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
