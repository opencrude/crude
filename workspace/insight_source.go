package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"unicode/utf8"

	"crude/document"
	"crude/fileio"
	"crude/types"
)

// An analysis lease borrows one immutable source, not an editor view. Canonical
// retained work wins even with zero views or no remaining disk path. A disk-only
// request cannot silently move to a later editor buffer or newer disk bytes.
type analysisSourceLease struct {
	documents *DocumentService
	canonical *liveDocument
	snapshot  document.Snapshot
	disk      os.FileInfo
	released  atomic.Bool
	ref       types.InsightSource
}

func (s *DocumentService) analysisIdentityLocked(d *liveDocument) types.InsightSource {
	if s.Insight == nil {
		return types.InsightSource{}
	}
	if d.snapshot.AnalysisGeneration != s.Insight.generation || d.snapshot.AnalysisLifetime == "" {
		d.snapshot.AnalysisGeneration, d.snapshot.AnalysisLifetime = s.Insight.generation, rand.Text()
	}
	return types.InsightSource{WorkspaceGeneration: d.snapshot.AnalysisGeneration,
		Lifetime: d.snapshot.AnalysisLifetime, DocumentID: d.snapshot.ID, Path: d.snapshot.Path,
		Revision: d.snapshot.Revision, Kind: "canonical"}
}

func (s *DocumentService) observeAnalysisLocked(d *liveDocument) {
	ref := s.analysisIdentityLocked(d)
	changed := false
	if s.Insight != nil {
		changed = s.Insight.diagnostics.InvalidateSource(ref)
	}
	if s.lsp != nil && d.lspOpen {
		s.lsp.QueueFileUpdate(ref, string(d.snapshot.Text))
	} else if changed && s.lsp != nil {
		s.lsp.mu.Lock()
		s.lsp.bumpVersionLocked()
		s.lsp.mu.Unlock()
	}
}

func (s *DocumentService) analysisSourceLocked(path string) (*analysisSourceLease, error) {
	if s.closed || s.idleFenced {
		return nil, context.Canceled
	}
	if s.stateErr != nil {
		return nil, s.stateErr
	}
	if d := s.documentAtPath(path); d != nil {
		ref := s.analysisIdentityLocked(d)
		snapshot := d.snapshot
		snapshot.Text, snapshot.Saved = bytes.Clone(snapshot.Text), nil
		return &analysisSourceLease{documents: s, canonical: d, snapshot: snapshot, ref: ref}, nil
	}
	if s.docs[path] != nil {
		return nil, errors.New("document identity is retained at another path")
	}
	return nil, nil
}

func (s *DocumentService) acquireAnalysisSource(ctx context.Context, path string) (*analysisSourceLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path == "" || len(path) > 4096 {
		return nil, document.ErrLimit
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.root, path)
	}
	path = filepath.Clean(path)
	s.mu.Lock()
	source, err := s.analysisSourceLocked(path)
	s.mu.Unlock()
	if source != nil || err != nil {
		return source, err
	}
	// This explicit off-thread disk read never holds the document authority and
	// cannot erase a canonical source that appears while the read is in flight.
	text, info, err := readAnalysisDisk(path)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source, err = s.analysisSourceLocked(path)
	if source != nil || err != nil {
		return source, err
	}
	return &analysisSourceLease{documents: s, disk: info,
		ref: types.InsightSource{WorkspaceGeneration: s.Insight.generation,
			DocumentID: path, Path: path, Lifetime: rand.Text(), Revision: 1, Kind: "disk"},
		snapshot: document.Snapshot{ID: path, Path: path, Revision: 1, Text: text, DiskExists: true}}, nil
}

func (s *analysisSourceLease) release() { s.released.Store(true) }

// The caller holds the document authority through registration/publication.
func (s *analysisSourceLease) validateLocked() error {
	if s.released.Load() || s.documents.closed || s.documents.idleFenced {
		return document.ErrOwner
	}
	current := s.documents.documentAtPath(s.snapshot.Path)
	if s.canonical == nil {
		if current != nil {
			return document.ErrRevision
		}
		return nil
	}
	if current != s.canonical || s.documents.analysisIdentityLocked(current) != s.ref {
		return document.ErrRevision
	}
	return nil
}

func (s *analysisSourceLease) validate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.released.Load() {
		return document.ErrOwner
	}
	s.documents.mu.Lock()
	err := s.validateLocked()
	s.documents.mu.Unlock()
	if err != nil {
		return err
	}
	if s.canonical != nil {
		return ctx.Err()
	}
	text, info, err := readAnalysisDisk(s.snapshot.Path)
	if err != nil {
		return err
	}
	if !sameAnalysisDisk(s.disk, info) || !bytes.Equal(text, s.snapshot.Text) {
		return document.ErrRevision
	}
	// Opening a view during validation must not bind disk answers to that view.
	s.documents.mu.Lock()
	err = s.validateLocked()
	s.documents.mu.Unlock()
	if err != nil {
		return err
	}
	return ctx.Err()
}

func sameAnalysisDisk(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() &&
		a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime())
}

// Validate both the opened handle and its path before returning bounded bytes.
// A symlink/replacement or write during observation is not an immutable source.
func readAnalysisDisk(path string) ([]byte, os.FileInfo, error) {
	if err := fileio.CheckDirectoryNoSymlinks(filepath.Dir(path)); err != nil {
		return nil, nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, before, errors.New("document is not a regular file")
	}
	if before.Size() > document.MaxBytes {
		return nil, before, document.ErrLimit
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, before, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil {
		return nil, before, err
	}
	if !sameAnalysisDisk(before, opened) {
		return nil, before, document.ErrRevision
	}
	text, err := io.ReadAll(io.LimitReader(file, document.MaxBytes+1))
	if err != nil {
		return nil, before, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, before, err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return nil, before, err
	}
	if !sameAnalysisDisk(opened, after) || !sameAnalysisDisk(after, current) {
		return nil, before, document.ErrRevision
	}
	if err := fileio.CheckDirectoryNoSymlinks(filepath.Dir(path)); err != nil {
		return nil, before, err
	}
	if len(text) > document.MaxBytes {
		return nil, before, document.ErrLimit
	}
	if !utf8.Valid(text) {
		return nil, before, errors.New("document is not UTF-8")
	}
	return text, after, nil
}
