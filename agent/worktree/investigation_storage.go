package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"crude/workspace/fileio"
)

// A Plan investigation's ledger contains mutable state only. Its immutable
// baseline is published before the first ledger write; an archived result is
// published and verified before its ledger reference or any checkout removal.
// All three files share the store directory's atomic-write durability boundary.
type investigationManifest struct {
	Context
	ResultDigest string `json:"investigation_result_digest,omitempty"`
}

type investigationArtifacts struct {
	baselineBytes int64
	resultBytes   int64
	resultDigest  string
}

type investigationBaseline struct {
	Files     map[string]File   `json:"files"`
	Revisions map[string]uint64 `json:"document_revisions,omitempty"`
}

func (s *Service) baselinePath(id string) string {
	return filepath.Join(s.store, id+".plan-baseline")
}

func (s *Service) resultPath(id, digest string) string {
	return filepath.Join(s.store, id+"."+digest+".plan-result")
}

func (s *Service) readInvestigationArtifact(path string) ([]byte, error) {
	return fileio.ReadRegularNoFollow(context.Background(), path, s.options.MaxBytes*2+(16<<20))
}

func (s *Service) validateInvestigationFiles(files map[string]File) error {
	if len(files) > s.options.MaxFiles {
		return errors.New("plan investigation file budget exceeded")
	}
	var total int64
	for path, f := range files {
		if err := safeRelative(path); err != nil {
			return err
		}
		total += int64(len(f.Data))
		if int64(len(f.Data)) > s.options.MaxFileBytes || total > s.options.MaxBytes {
			return errors.New("plan investigation byte budget exceeded")
		}
		if f.Type != "file" || f.Hash != hash(f.Data) {
			return errors.New("plan investigation file identity mismatch")
		}
	}
	return nil
}

func (s *Service) readInvestigationBaseline(c Context) (investigationBaseline, int64, error) {
	data, err := s.readInvestigationArtifact(s.baselinePath(c.ID))
	if err != nil {
		return investigationBaseline{}, 0, fmt.Errorf("read Plan investigation baseline: %w", err)
	}
	var snapshot investigationBaseline
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return investigationBaseline{}, 0, err
	}
	if err := s.validateInvestigationFiles(snapshot.Files); err != nil {
		return investigationBaseline{}, 0, err
	}
	for path, revision := range snapshot.Revisions {
		if err := safeRelative(path); err != nil {
			return investigationBaseline{}, 0, err
		}
		if revision == 0 {
			return investigationBaseline{}, 0, errors.New("invalid accepted document revision")
		}
	}
	for path, f := range snapshot.Files {
		if f.DocumentRevision != snapshot.Revisions[path] {
			return investigationBaseline{}, 0, errors.New("plan investigation document revision mismatch")
		}
	}
	c.Baseline, c.DocumentRevisions = snapshot.Files, snapshot.Revisions
	if snapshot.Files == nil || c.BaselineDigest != investigationBaselineDigest(c) {
		return investigationBaseline{}, 0, errors.New("plan investigation baseline identity mismatch")
	}
	return snapshot, int64(len(data)), nil
}

func (s *Service) readInvestigationResult(id, digest string) (map[string]File, int64, error) {
	data, err := s.readInvestigationArtifact(s.resultPath(id, digest))
	if err != nil {
		return nil, 0, fmt.Errorf("read archived Plan investigation: %w", err)
	}
	if digest != hash(data) {
		return nil, 0, errors.New("archived Plan investigation identity mismatch")
	}
	var files map[string]File
	if err := json.Unmarshal(data, &files); err != nil {
		return nil, 0, err
	}
	if err := s.validateInvestigationFiles(files); err != nil {
		return nil, 0, err
	}
	return files, int64(len(data)), nil
}

func validInvestigationDigest(digest string) bool {
	// The reference is also a path component. Accept only the SHA-256 spelling.
	return len(digest) == 64 && validID(digest[:32]) && validID(digest[32:])
}

func (s *Service) openInvestigation(c *Context, digest string) error {
	if c.Mode != Isolated || c.Investigation.SessionID == "" || c.Investigation.Cycle == "" || c.Integration != nil ||
		c.Baseline != nil || c.DocumentRevisions != nil || c.ArchivedResult != nil ||
		!validInvestigationDigest(c.BaselineDigest) || digest != "" && !validInvestigationDigest(digest) {
		return errors.New("invalid Plan investigation manifest")
	}
	artifacts := investigationArtifacts{resultDigest: digest}
	if !c.Removed {
		snapshot, size, err := s.readInvestigationBaseline(*c)
		if err != nil {
			return err
		}
		c.Baseline, c.DocumentRevisions = snapshot.Files, snapshot.Revisions
		artifacts.baselineBytes = size
		if digest != "" {
			if _, artifacts.resultBytes, err = s.readInvestigationResult(c.ID, digest); err != nil {
				return err
			}
		}
	}
	s.investigationArtifacts[c.ID] = artifacts
	return nil
}

func (s *Service) saveInvestigation(c Context) error {
	artifacts, exists := s.investigationArtifacts[c.ID]
	if exists {
		// The service owns these immutable maps. A state update cannot replace
		// evidence, even when its caller has previously requested a full copy.
		prior := s.contexts[c.ID]
		if c.BaselineDigest != prior.BaselineDigest || c.BaseCommit != prior.BaseCommit {
			return errors.New("plan investigation baseline cannot change")
		}
		c.Baseline, c.DocumentRevisions = prior.Baseline, prior.DocumentRevisions
	} else {
		if err := os.MkdirAll(s.store, 0700); err != nil {
			return err
		}
		data, err := json.Marshal(investigationBaseline{Files: c.Baseline, Revisions: c.DocumentRevisions})
		if err != nil {
			return err
		}
		if err := fileio.WriteAtomicNewChecked(s.baselinePath(c.ID), data, 0600, nil); err != nil {
			return err
		}
		artifacts.baselineBytes = int64(len(data))
	}
	return s.publishInvestigation(c, artifacts, !exists)
}

func (s *Service) publishInvestigation(c Context, artifacts investigationArtifacts, newContext bool) error {
	ledger := c
	ledger.Baseline, ledger.DocumentRevisions, ledger.ArchivedResult = nil, nil, nil
	data, err := json.Marshal(investigationManifest{Context: ledger, ResultDigest: artifacts.resultDigest})
	if err != nil {
		return err
	}
	path := s.manifestPath(c.ID)
	if newContext {
		err = fileio.WriteAtomicNewChecked(path, data, 0600, nil)
	} else {
		err = fileio.WriteAtomicChecked(path, data, 0600, nil)
	}
	if err != nil && !fileio.WriteCommitted(err) {
		return err
	}
	prior, existed := s.contexts[c.ID]
	switch {
	case !existed && !c.Removed:
		s.countPlanInvestigation(c.Investigation.SessionID, 1)
	case existed && !prior.Removed && c.Removed:
		s.countPlanInvestigation(c.Investigation.SessionID, -1)
	}
	s.investigationArtifacts[c.ID] = artifacts
	s.sizes[c.ID] = int64(len(data))
	if c.Removed {
		c = ledger
	}
	s.contexts[c.ID] = c
	return err
}

func (s *Service) saveArchive(c *Context, files map[string]File) error {
	if c.Investigation == nil {
		c.ArchivedResult = files
		return s.save(*c)
	}
	data, err := json.Marshal(files)
	if err != nil {
		return err
	}
	artifacts := s.investigationArtifacts[c.ID]
	artifacts.resultDigest = hash(data)
	path := s.resultPath(c.ID, artifacts.resultDigest)
	// A crash can leave this exact artifact staged but not yet referenced.
	// Accept the existing file only after verifying its bytes, never replace it.
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if err := fileio.WriteAtomicNewChecked(path, data, 0600, nil); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, artifacts.resultBytes, err = s.readInvestigationResult(c.ID, artifacts.resultDigest); err != nil {
		return err
	}
	return s.publishInvestigation(*c, artifacts, false)
}

func (s *Service) inspectInvestigation(c Context, resultDigest string) (Context, error) {
	out := compactContext(c)
	if c.Removed {
		snapshot, _, err := s.readInvestigationBaseline(c)
		if err != nil {
			return Context{}, err
		}
		out.Baseline, out.DocumentRevisions = snapshot.Files, snapshot.Revisions
	} else {
		out.Baseline = maps.Clone(c.Baseline)
		for path, f := range out.Baseline {
			f.Data = bytes.Clone(f.Data)
			out.Baseline[path] = f
		}
		out.DocumentRevisions = maps.Clone(c.DocumentRevisions)
	}
	if resultDigest != "" {
		var err error
		if out.ArchivedResult, _, err = s.readInvestigationResult(c.ID, resultDigest); err != nil {
			return Context{}, err
		}
	}
	return out, nil
}
