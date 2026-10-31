package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"crude/fileio"
	"crude/insight/project"
	"crude/insight/project/cmake"
)

const (
	projectContextMaxArtifacts = 128
	projectContextMaxBytes     = 16 << 20
	projectContextMaxPathBytes = 4096
)

// ProjectArtifactSet selects inert metadata, not a command to run or a read or
// execution grant. Paths are workspace-relative, native, regular-file operands.
// A CMake set names its index and every reply artifact explicitly; JSON references
// never authorize another read. CompilationDatabase requires exactly one path.
type ProjectArtifactSet struct {
	ID                  string
	CompilationDatabase string
	CMakeIndex          string
	CMakeReplies        []string
}

type ProjectArtifactIdentity struct {
	Path   string
	SHA256 string
	Bytes  int64
}

// ProjectContextSnapshot is small detached evidence. Ready means the selected
// inert artifacts were observed, never complete build context or valid semantics.
// Pending/failure retains the former accepted artifact identities as stale.
type ProjectContextSnapshot struct {
	Owner              string
	Generation         uint64
	ObservedGeneration uint64
	State              string
	RootIdentity       string
	Artifacts          []ProjectArtifactIdentity
	UnknownInputs      []string
	Detail             string
}

type projectContextData struct {
	compilation map[string]project.CompilationDatabase
	cmake       map[string]cmake.Observation
	identities  []ProjectArtifactIdentity
}

// ProjectContextService is one workspace's retained artifact observation owner.
// It never reads source documents, executes tools, resolves compiler arguments,
// infers configurations or duplicates an engine's semantic index. One worker
// coalesces selections/invalidations; cancellation never abandons that worker.
// Construction, Select, Invalidate, Snapshot and Changed do no filesystem I/O.
type ProjectContextService struct {
	mu         sync.Mutex
	root       string
	owner      string
	selection  []ProjectArtifactSet
	generation uint64
	pending    bool
	closed     bool
	idleFenced bool
	snapshot   ProjectContextSnapshot
	accepted   projectContextData
	cancel     context.CancelFunc
	wake       chan struct{}
	changed    chan struct{}
	done       chan struct{}
	read       func(context.Context, string, []ProjectArtifactSet) (projectContextData, string, error)
}

// NewProjectContextService creates an idle owner. Selection is explicit: merely
// enabling intelligence must not recursively scan or execute project metadata.
func NewProjectContextService(root, owner string) *ProjectContextService {
	return newProjectContextService(root, owner, readProjectContext)
}

func newProjectContextService(root, owner string, read func(context.Context, string, []ProjectArtifactSet) (projectContextData, string, error)) *ProjectContextService {
	p := &ProjectContextService{root: filepath.Clean(root), owner: owner,
		wake: make(chan struct{}, 1), changed: make(chan struct{}), done: make(chan struct{}),
		read: read}
	p.snapshot = ProjectContextSnapshot{Owner: owner, State: "unavailable",
		UnknownInputs: projectContextUnknown(), Detail: "No project artifact selection has been observed."}
	go p.run()
	return p
}

var (
	ErrProjectContextSelection = errors.New("invalid project artifact selection")
	ErrProjectContextChanged   = errors.New("project artifact identity changed during observation")
	ErrProjectContextClosed    = errors.New("project context owner closed")
	ErrProjectContextReserved  = errors.New("project context owner reserved for idle shutdown")
)

// Select detaches a complete selection. Invalid input has no effect; an equal
// selection does not invalidate or re-read. Empty explicitly clears selection,
// while preserving formerly accepted evidence as stale, not a clean answer.
func (p *ProjectContextService) Select(selection []ProjectArtifactSet) error {
	selected, err := normalizeProjectSelection(selection)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrProjectContextClosed
	}
	if p.idleFenced {
		return ErrProjectContextReserved
	}
	if !filepath.IsAbs(p.root) || !validAnalysisOwnerID(p.owner) {
		return ErrProjectContextSelection
	}
	if equalProjectSelection(p.selection, selected) {
		return nil
	}
	p.selection = selected
	p.invalidateLocked("Project artifact selection changed.")
	return nil
}

// Invalidate is coarse passive retained-core ingress. The workspace's watcher
// owns event scope. A delivered spelling is not native identity (case, Unicode,
// short names and aliases differ by filesystem), so no selected input is kept
// current merely because its string did not match that event. No event path
// grants a read: only the already selected regular files can be re-observed.
func (p *ProjectContextService) Invalidate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || len(p.selection) == 0 {
		return
	}
	p.invalidateLocked("Selected project artifacts may have changed; retained evidence is stale.")
}

func (p *ProjectContextService) invalidateLocked(detail string) {
	p.generation++
	p.pending = true
	if p.cancel != nil {
		p.cancel()
	}
	p.snapshot.Generation, p.snapshot.State, p.snapshot.Detail = p.generation, "pending", detail
	p.publishLocked()
	if !p.idleFenced {
		p.wakeLocked()
	}
}

func (p *ProjectContextService) wakeLocked() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *ProjectContextService) publishLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
}

func (p *ProjectContextService) Snapshot() ProjectContextSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.snapshot
	out.Artifacts = slices.Clone(out.Artifacts)
	out.UnknownInputs = slices.Clone(out.UnknownInputs)
	return out
}

// Changed captures the exact owner's current generation and next publication.
// A ready/failed publication can change state without another input generation.
func (p *ProjectContextService) Changed() (uint64, <-chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.generation, p.changed
}

// CompilationDatabase returns only current, immutable parser output, qualified
// by this owner's generation. A stale observation is not returned as current.
func (p *ProjectContextService) CompilationDatabase(generation uint64, id string) (project.CompilationDatabase, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.snapshot.State != "ready" || generation != p.generation {
		return project.CompilationDatabase{}, false
	}
	value, ok := p.accepted.compilation[id]
	return value, ok
}

func (p *ProjectContextService) CMake(generation uint64, id string) (cmake.Observation, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.snapshot.State != "ready" || generation != p.generation {
		return cmake.Observation{}, false
	}
	value, ok := p.accepted.cmake[id]
	return value, ok
}

func (p *ProjectContextService) run() {
	defer close(p.done)
	for range p.wake {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return
		}
		if !p.pending || p.idleFenced {
			p.mu.Unlock()
			continue
		}
		selection, generation := p.selection, p.generation // immutable detached input
		if len(selection) == 0 {
			p.snapshot.State, p.snapshot.Detail = "unavailable", "No project artifacts selected; former observations are stale."
			p.pending = false
			p.publishLocked()
			p.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		p.cancel, p.pending = cancel, false
		p.mu.Unlock()
		data, rootIdentity, err := p.read(ctx, p.root, selection)
		p.mu.Lock()
		if generation == p.generation && !p.closed {
			if err == nil && ctx.Err() != nil {
				err = ctx.Err()
			}
			if p.snapshot.RootIdentity != "" && rootIdentity != "" && rootIdentity != p.snapshot.RootIdentity {
				err = ErrProjectContextChanged
			}
			p.snapshot.State, p.snapshot.Detail = "failed", projectContextFailure(err)
			if err == nil {
				p.accepted = data
				p.snapshot.RootIdentity, p.snapshot.Artifacts = rootIdentity, data.identities
				p.snapshot.ObservedGeneration = generation
				p.snapshot.State, p.snapshot.Detail = "ready", "Selected artifacts observed; build context remains incomplete."
			}
			p.publishLocked()
		}
		p.cancel = nil
		cancel()
		p.mu.Unlock()
	}
}

func (p *ProjectContextService) beginIdleLease() (func(), error) {
	if !p.mu.TryLock() {
		return nil, errors.New("project context publication is still running")
	}
	defer p.mu.Unlock()
	if p.closed || p.idleFenced || p.pending || p.cancel != nil {
		return nil, errors.New("project context observation is running, queued or already reserved")
	}
	p.idleFenced = true
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.idleFenced = false
			if p.pending && !p.closed {
				p.wakeLocked() // replay metadata intent, never filesystem effects
			}
		})
	}, nil
}

func (p *ProjectContextService) Close() {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		p.generation++
		p.snapshot.Generation, p.snapshot.State = p.generation, "closed"
		p.snapshot.Detail = "Project context owner closed; retained evidence is stale."
		if p.cancel != nil {
			p.cancel()
		}
		p.publishLocked()
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}
	p.mu.Unlock()
	<-p.done
}

func projectContextUnknown() []string {
	return []string{"Selected artifacts do not establish the complete dependency or build graph.",
		"Producer authenticity, generated-input freshness, toolchain and environment identities are unverified.",
		"Filesystem notifications are invalidation hints; missed external changes are not ruled out."}
}

func projectContextFailure(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "Project artifact observation cancelled; former evidence is stale."
	case errors.Is(err, context.DeadlineExceeded):
		return "Project artifact observation exceeded its cooperative deadline; former evidence is stale."
	case errors.Is(err, ErrProjectContextChanged):
		return "Project root or artifact identity changed; former evidence is stale."
	default:
		// Errors may contain artifact contents, compiler argv or private paths.
		return "Selected project artifacts are unavailable, invalid or exceed observation bounds."
	}
}

func normalizeProjectSelection(selection []ProjectArtifactSet) ([]ProjectArtifactSet, error) {
	if len(selection) > projectContextMaxArtifacts {
		return nil, ErrProjectContextSelection
	}
	ids, paths := map[string]bool{}, map[string]bool{}
	out := make([]ProjectArtifactSet, 0, len(selection))
	for _, set := range selection {
		if !validAnalysisOwnerID(set.ID) || ids[set.ID] || (set.CompilationDatabase == "") == (set.CMakeIndex == "") ||
			set.CompilationDatabase != "" && len(set.CMakeReplies) != 0 {
			return nil, ErrProjectContextSelection
		}
		ids[set.ID] = true
		// Bound nested input before allocating or sorting it. An invalid caller
		// may own an arbitrarily large slice; intake must not copy that tail.
		remaining := projectContextMaxArtifacts - len(paths)
		if remaining == 0 || len(set.CMakeReplies) > remaining-1 {
			return nil, ErrProjectContextSelection
		}
		for _, path := range projectSetPaths(set) {
			if !validProjectArtifactPath(path) {
				return nil, ErrProjectContextSelection
			}
		}
		set.CMakeReplies = slices.Clone(set.CMakeReplies)
		sort.Strings(set.CMakeReplies)
		for _, path := range projectSetPaths(set) {
			if !validProjectArtifactPath(path) || paths[path] || len(paths) == projectContextMaxArtifacts {
				return nil, ErrProjectContextSelection
			}
			paths[path] = true
			if set.CMakeIndex != "" && filepath.Dir(path) != filepath.Dir(set.CMakeIndex) {
				return nil, ErrProjectContextSelection
			}
		}
		out = append(out, set)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func validProjectArtifactPath(path string) bool {
	return path != "." && len(path) <= projectContextMaxPathBytes && !strings.ContainsAny(path, "\x00\r\n") &&
		filepath.IsLocal(path) && filepath.Clean(path) == path
}

func projectSetPaths(set ProjectArtifactSet) []string {
	if set.CompilationDatabase != "" {
		return []string{set.CompilationDatabase}
	}
	return append([]string{set.CMakeIndex}, set.CMakeReplies...)
}

func equalProjectSelection(a, b []ProjectArtifactSet) bool {
	return slices.EqualFunc(a, b, func(a, b ProjectArtifactSet) bool {
		return a.ID == b.ID && a.CompilationDatabase == b.CompilationDatabase &&
			a.CMakeIndex == b.CMakeIndex && slices.Equal(a.CMakeReplies, b.CMakeReplies)
	})
}

func readProjectContext(ctx context.Context, root string, selection []ProjectArtifactSet) (projectContextData, string, error) {
	return readProjectContextWith(ctx, root, selection, readProjectArtifact)
}

func readProjectContextWith(ctx context.Context, root string, selection []ProjectArtifactSet,
	read func(context.Context, string, string, int64) ([]byte, projectArtifactStamp, error)) (projectContextData, string, error) {
	data := projectContextData{compilation: map[string]project.CompilationDatabase{}, cmake: map[string]cmake.Observation{}}
	identity, err := fileio.DirectoryIdentity(root)
	if err != nil {
		return data, "", err
	}
	budget := int64(projectContextMaxBytes)
	accountedBudget := int64(projectContextMaxBytes)
	stamps := map[string]projectArtifactStamp{}
	for _, set := range selection {
		inputs := make([]cmake.Artifact, 0, len(set.CMakeReplies)+1)
		for _, path := range projectSetPaths(set) {
			content, stamp, err := read(ctx, root, path, budget)
			if err != nil {
				return projectContextData{}, identity, err
			}
			budget -= int64(len(content))
			stamps[path] = stamp
			for other, previous := range stamps {
				if other != path && os.SameFile(stamp.info, previous.info) {
					return projectContextData{}, identity, ErrProjectContextSelection
				}
			}
			digest := sha256.Sum256(content)
			data.identities = append(data.identities, ProjectArtifactIdentity{
				Path: path, Bytes: int64(len(content)), SHA256: hex.EncodeToString(digest[:])})
			if set.CompilationDatabase != "" {
				if accountedBudget <= 0 {
					return projectContextData{}, identity, ErrProjectContextSelection
				}
				observation, err := project.ParseCompilationDatabase(ctx, bytes.NewReader(content), project.Limits{MaxAccountedBytes: accountedBudget})
				if err != nil {
					return projectContextData{}, identity, err
				}
				data.compilation[set.ID] = observation
				accountedBudget -= observation.AccountedBytes()
			} else {
				inputs = append(inputs, cmake.Artifact{Name: filepath.Base(path), Reader: bytes.NewReader(content)})
			}
		}
		if set.CMakeIndex != "" {
			observation, err := cmake.Import(ctx, filepath.Base(set.CMakeIndex), inputs, cmake.Limits{})
			if err != nil {
				return projectContextData{}, identity, err
			}
			data.cmake[set.ID] = observation
		}
	}
	// A multi-file observation must not accept an earlier file which changed
	// while a later reply was being read/imported. Revalidate the entire set
	// after derivation, before publishing any of it.
	for path, stamp := range stamps {
		if err := validateProjectArtifact(ctx, root, path, stamp); err != nil {
			return projectContextData{}, identity, err
		}
	}
	again, err := fileio.DirectoryIdentity(root)
	if err != nil || again != identity {
		return projectContextData{}, identity, ErrProjectContextChanged
	}
	if err := ctx.Err(); err != nil {
		return projectContextData{}, identity, err
	}
	sort.Slice(data.identities, func(i, j int) bool { return data.identities[i].Path < data.identities[j].Path })
	return data, identity, nil
}
