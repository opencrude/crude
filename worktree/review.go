package worktree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"crude/fileio"
)

func equal(a, b *File) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Type == b.Type && a.Mode == b.Mode && a.Hash == b.Hash
}

func planDigest(p PlanDelta) string {
	p.Digest = ""
	p.DiscardDigest = ""
	p.HeadCommit, p.IgnoredPaths = "", nil
	data, _ := json.Marshal(p)
	return hash(data)
}

// Review compares bytes/types/modes against the immutable accepted baseline,
// not Git HEAD, so inherited human changes never become the child's delta.
func (s *Service) Review(ctx context.Context, id string) (PlanDelta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.review(ctx, id)
}

func (s *Service) review(ctx context.Context, id string) (PlanDelta, error) {
	c, ok := s.contexts[id]
	if !ok || c.Removed || !c.Ready {
		return PlanDelta{}, errors.New("execution context unavailable or preparation incomplete")
	}
	if err := s.verifyCheckout(ctx, c); err != nil {
		return PlanDelta{}, err
	}
	include := make([]string, 0, len(c.Baseline))
	for p := range c.Baseline {
		include = append(include, p)
	}
	current, err := s.capture(ctx, c.Root, c.CommonGitDir, nil, include, c.CommonGitDir != "")
	if err != nil {
		return PlanDelta{}, err
	}
	return sourceDelta(c, current), nil
}

func sourceDelta(c Context, current map[string]File) PlanDelta {
	paths := map[string]bool{}
	for p := range c.Baseline {
		paths[p] = true
	}
	for p := range current {
		paths[p] = true
	}
	ordered := make([]string, 0, len(paths))
	for p := range paths {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)
	plan := PlanDelta{ContextID: c.ID, Owner: c.Owner, Changes: []Change{}}
	for _, p := range ordered {
		var before, after *File
		if f, ok := c.Baseline[p]; ok {
			f = clone(f)
			before = &f
		}
		if f, ok := current[p]; ok {
			f = clone(f)
			after = &f
		}
		if !equal(before, after) {
			plan.Changes = append(plan.Changes, Change{Path: p, Before: before, After: after})
		}
	}
	plan.Digest = planDigest(plan)
	return plan
}

// ReviewDiscard includes ignored bytes and commit identity only for explicit
// discard review. Ordinary integration never scans disposable build output.
func (s *Service) ReviewDiscard(ctx context.Context, id string) (PlanDelta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reviewDiscard(ctx, id)
}

func (s *Service) reviewDiscard(ctx context.Context, id string) (PlanDelta, error) {
	c, ok := s.contexts[id]
	if !ok || c.Removed {
		return PlanDelta{}, errors.New("execution context unavailable")
	}
	if !c.Ready {
		files, err := s.preparationRemnants(ctx, c)
		if err != nil {
			return PlanDelta{}, err
		}
		plan := sourceDelta(c, files)
		data, err := json.Marshal(struct {
			ID                 string
			Baseline, Remnants map[string]File
		}{c.ID, c.Baseline, files})
		if err != nil {
			return PlanDelta{}, err
		}
		plan.DiscardDigest = hash(data)
		return plan, nil
	}
	plan, err := s.review(ctx, id)
	if err != nil {
		return plan, err
	}
	if c.Mode == Isolated {
		head, err := s.git(ctx, c.Root, "rev-parse", "HEAD")
		if err != nil {
			return PlanDelta{}, err
		}
		plan.HeadCommit = strings.TrimSpace(string(head))
		ignored, err := s.git(ctx, c.Root, "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
		if err != nil {
			return PlanDelta{}, err
		}
		if len(ignored) != 0 {
			plan.IgnoredPaths = nulPaths(ignored)
		}
		sort.Strings(plan.IgnoredPaths)
		ignoredFiles := make(map[string]*File, len(plan.IgnoredPaths))
		for _, path := range plan.IgnoredPaths {
			file, err := s.read(c.Root, c.CommonGitDir, path)
			if err != nil {
				return PlanDelta{}, err
			}
			ignoredFiles[path] = file
		}
		plan.Digest = planDigest(plan)
		data, err := json.Marshal(struct {
			ID, Digest, Head string
			Ignored          map[string]*File
		}{c.ID, plan.Digest, plan.HeadCommit, ignoredFiles})
		if err != nil {
			return PlanDelta{}, err
		}
		plan.DiscardDigest = hash(data)
	}
	return plan, nil
}

func (s *Service) verifyCheckout(ctx context.Context, c Context) error {
	r, err := canonical(c.Root)
	if err != nil || r != c.Root {
		return errors.New("checkout missing or replaced by symlink")
	}
	if c.CommonGitDir == "" {
		return nil
	}
	g, err := s.git(ctx, c.Root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	r, err = canonical(strings.TrimSpace(string(g)))
	if err != nil || r != c.CommonGitDir {
		return errors.New("checkout Git ownership changed")
	}
	if c.Mode == Isolated {
		fi, err := os.Lstat(filepath.Join(c.Root, ".git"))
		if err != nil || !fi.Mode().IsRegular() {
			return errors.New("managed checkout must have regular Git pointer")
		}
	}
	return nil
}

// ValidateDestination is read-only. Callers must hold their document authority
// and the shared fileio mutation lease through validation AND commit. Revalidate
// every accepted revision immediately before transaction installation. Newer
// nonoverlapping files are untouched; overlapping changes fail closed (no merge).
func (s *Service) ValidateDestination(ctx context.Context, plan PlanDelta, documents map[string]Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.contexts[plan.ContextID]
	if !ok || c.Owner != plan.Owner || planDigest(plan) != plan.Digest {
		return errors.New("invalid review plan identity")
	}
	current, err := s.review(ctx, c.ID)
	if err != nil {
		return err
	}
	if current.Digest != sourceDigest(plan) {
		return errors.New("source changed after review")
	}
	if plan.SourceDigest != "" {
		resolved, err := s.resolve(ctx, c.ID, documents)
		if err != nil {
			return err
		}
		if resolved.Digest != plan.Digest {
			return errors.New("destination changed after review")
		}
	}
	for _, change := range plan.Changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := s.read(s.root, c.CommonGitDir, change.Path)
		if err != nil {
			return err
		}
		if revision := c.DocumentRevisions[change.Path]; revision != 0 && plan.SourceDigest == "" {
			d, ok := documents[change.Path]
			if !ok || d.Revision != revision {
				return fmt.Errorf("accepted destination document revision conflict: %s", change.Path)
			}
		}
		if d, ok := documents[change.Path]; ok {
			if d.Revision == 0 {
				return errors.New("accepted destination revision required")
			}
			if change.Before != nil && change.Before.DocumentRevision != 0 && d.Revision != change.Before.DocumentRevision {
				return fmt.Errorf("destination document revision conflict: %s", change.Path)
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
				f = &File{Type: "file", Mode: mode, Hash: hash(d.Data), Data: d.Data, DocumentRevision: d.Revision}
			}
		} else if change.Before != nil && change.Before.DocumentRevision != 0 {
			return fmt.Errorf("accepted destination document snapshot required: %s", change.Path)
		}
		if !equal(f, change.Before) {
			return fmt.Errorf("destination conflict: %s", change.Path)
		}
	}
	return nil
}

// Apply intentionally returns a validated plan, not a disk mutation. Only the
// existing document/file + Local History transaction owner can safely commit
// mixed live/disk changes and publish operation-scoped undo evidence.
func (s *Service) Apply(ctx context.Context, id string, documents map[string]Document) (PlanDelta, error) {
	p, err := s.Review(ctx, id)
	if err != nil {
		return PlanDelta{}, err
	}
	if err := s.ValidateDestination(ctx, p, documents); err != nil {
		return PlanDelta{}, err
	}
	return p, nil
}

func (s *Service) Pin(id string, owner Owner) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validOwner(owner) {
		return errors.New("exact pin owner required")
	}
	c, ok := s.contexts[id]
	if !ok || c.Removed || !c.Ready {
		return errors.New("context unavailable")
	}
	for _, p := range c.Pins {
		if p == owner {
			return nil
		}
	}
	c.Pins = append(c.Pins, owner)
	return s.save(c)
}
func (s *Service) Unpin(id string, owner Owner) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.contexts[id]
	if !ok {
		return errors.New("unknown context")
	}
	pins := make([]Owner, 0, len(c.Pins))
	found := false
	for _, p := range c.Pins {
		if p == owner {
			found = true
		} else {
			pins = append(pins, p)
		}
	}
	if !found {
		return errors.New("pin owner mismatch")
	}
	c.Pins = pins
	return s.save(c)
}
func (s *Service) Keep(id string, owner Owner, keep ...bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.contexts[id]
	if !ok || c.Owner != owner {
		return errors.New("context owner mismatch")
	}
	c.Kept = len(keep) == 0 || keep[0]
	return s.save(c)
}

// RecordIntegration requires exact reviewed source and checks destination bytes
// reflect its result. Receipt must come from the transactional Local History
// owner after commit; this method does not create or verify its backup artifact.
func (s *Service) RecordIntegration(ctx context.Context, plan PlanDelta, receipt Receipt, documents map[string]Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.contexts[plan.ContextID]
	if !ok || c.Owner != plan.Owner || planDigest(plan) != plan.Digest || receipt.PlanDigest != plan.Digest || receipt.OperationID == "" || receipt.SafetySnapshotID == "" {
		return errors.New("exact integration and safety receipt required")
	}
	p, err := s.review(ctx, c.ID)
	if err != nil {
		return err
	}
	if p.Digest != sourceDigest(plan) {
		return errors.New("source changed after integration review")
	}
	for _, change := range plan.Changes {
		f, err := s.read(s.root, c.CommonGitDir, change.Path)
		if err != nil {
			return err
		}
		if d, ok := documents[change.Path]; ok {
			if d.Revision == 0 {
				return errors.New("accepted document revision required")
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
				f = &File{Type: "file", Mode: mode, Hash: hash(d.Data), Data: d.Data}
			}
		} else if change.Before != nil && change.Before.DocumentRevision != 0 {
			return errors.New("accepted document result required")
		}
		if !equal(f, change.After) {
			return fmt.Errorf("integration result mismatch: %s", change.Path)
		}
	}
	c.Integration = &receipt
	c.IntegratedSource = sourceDigest(plan)
	return s.save(c)
}

// Cleanup is parent-managed, never a side effect of child completion. A reviewed
// discard archives unwanted results without merging them. Shared records retire
// only their metadata; the shared project directory is never removed.
func (s *Service) Cleanup(ctx context.Context, id string, owner Owner, options ...CleanupOptions) error {
	unlock, err := fileio.LockMutationContext(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	option := CleanupOptions{}
	if len(options) > 0 {
		option = options[0]
	}
	if option.Check != nil {
		if err := option.Check(ctx); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.contexts[id]
	if !ok || c.Owner != owner {
		return errors.New("context owner mismatch")
	}
	if c.Removed {
		return nil
	}
	if c.Kept || len(c.Pins) > 0 {
		return errors.New("context retained or pinned")
	}
	if c.Mode == Shared {
		c.Removed = true
		return s.save(c)
	}
	if !c.Ready {
		if !option.Discard {
			return errors.New("preparation incomplete; review with discard=true, then cleanup using discard_digest")
		}
		plan, err := s.reviewDiscard(ctx, id)
		if err != nil {
			return err
		}
		if option.Digest == "" || option.Digest != plan.DiscardDigest {
			return errors.New("preparation remnants changed; review discard again")
		}
		return s.cleanupPreparation(ctx, c)
	}
	for _, other := range s.contexts {
		if other.ID != c.ID && !other.Removed && other.Root == c.Root && len(other.Pins) > 0 {
			return errors.New("checkout retained by shared execution pins")
		}
	}
	var plan PlanDelta
	if option.Discard {
		plan, err = s.reviewDiscard(ctx, id)
	} else {
		plan, err = s.review(ctx, id)
	}
	if err != nil {
		return err
	}
	if option.Discard && (option.Digest == "" || option.Digest != plan.DiscardDigest) {
		return errors.New("discard requires the current execution.review discard_digest; review again")
	}
	if len(plan.Changes) > 0 && !option.Discard && (c.Integration == nil || c.IntegratedSource != plan.Digest) {
		return errors.New("unintegrated or newer dirty result retained")
	}
	head, err := s.git(ctx, c.Root, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(head)) != c.BaseCommit && c.Integration == nil && !option.Discard {
		return errors.New("new commits retained")
	}
	// Even status may run a tracked file's clean/process filter while comparing
	// it with the index. Refuse before that read, not only before staging.
	paths := append([]string(nil), plan.IgnoredPaths...)
	for _, change := range plan.Changes {
		paths = append(paths, change.Path)
	}
	if err := s.rejectAppliedFilters(ctx, c.Root, paths); err != nil {
		return err
	}
	status, err := s.git(ctx, c.Root, "status", "--porcelain=v1", "--untracked-files=all", "--ignored")
	if err != nil {
		return err
	}
	if len(status) > 0 || strings.TrimSpace(string(head)) != c.BaseCommit {
		if c.Integration == nil && !option.Discard && len(plan.Changes) != 0 {
			return errors.New("git dirty or ignored bytes retained")
		}
		include := make([]string, 0, len(c.Baseline))
		for p := range c.Baseline {
			include = append(include, p)
		}
		ignored, err := s.git(ctx, c.Root, "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
		if err != nil {
			return err
		}
		if len(ignored) != 0 && !option.Discard {
			return errors.New("ignored artifacts retained; review and explicitly discard to archive them")
		}
		include = append(include, nulPaths(ignored)...)
		archived, err := s.capture(ctx, c.Root, c.CommonGitDir, nil, include, true)
		if err != nil {
			return err
		}
		c.ArchivedResult = archived
		if err = s.save(c); err != nil {
			return err
		}
		// Verify durable archive before changing source index or removing bytes.
		data, err := os.ReadFile(filepath.Join(s.store, c.ID+".json"))
		if err != nil {
			return err
		}
		var verify Context
		if err = json.Unmarshal(data, &verify); err != nil {
			return err
		}
		if !equalArchive(verify.ArchivedResult, archived) || !equalArchive(verify.Baseline, c.Baseline) {
			return errors.New("archive verification failed")
		}
		if len(status) > 0 {
			archivePaths := make([]string, 0, len(archived))
			for path := range archived {
				archivePaths = append(archivePaths, path)
			}
			if err := s.rejectAppliedFilters(ctx, c.Root, archivePaths); err != nil {
				return err
			}
			if _, err = s.git(ctx, c.Root, "add", "--all", "--", "."); err != nil {
				return err
			}
			if len(ignored) != 0 {
				// Exact ignored files were archived above; no blanket force removal.
				for _, path := range nulPaths(ignored) {
					if _, err = s.git(ctx, c.Root, "add", "--force", "--", path); err != nil {
						return err
					}
				}
			}
			if _, err = s.git(ctx, c.Root, "-c", "user.name=Crude execution archive", "-c", "user.email=execution@crude.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "Archive execution "+c.ID); err != nil {
				return err
			}
		}
		head, err = s.git(ctx, c.Root, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		c.ArchiveCommit = strings.TrimSpace(string(head))
		if _, err = s.git(ctx, c.Root, "update-ref", "refs/crude/executions/"+c.ID, c.ArchiveCommit); err != nil {
			return err
		}
		if err = s.save(c); err != nil {
			return err
		}
		status, err = s.git(ctx, c.Root, "status", "--porcelain=v1", "--untracked-files=all", "--ignored")
		if err != nil {
			return err
		}
		if len(status) != 0 {
			return errors.New("source changed during archival; checkout retained")
		}
		actual, err := s.capture(ctx, c.Root, c.CommonGitDir, nil, include, true)
		if err != nil {
			return err
		}
		if !equalArchive(actual, archived) {
			return errors.New("source changed during archival; checkout retained")
		}
	}
	if _, err := s.git(ctx, s.root, "worktree", "remove", c.Root); err != nil {
		return err
	}
	c.Removed = true
	return s.save(c)
}

func equalArchive(a, b map[string]File) bool {
	if len(a) != len(b) {
		return false
	}
	for p, f := range a {
		other, ok := b[p]
		if !ok || !equal(&f, &other) {
			return false
		}
	}
	return true
}
