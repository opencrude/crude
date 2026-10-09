package editor

import (
	"errors"
	"path/filepath"
)

// RetargetDocumentPath installs canonical path metadata without reattaching the
// document, changing pending text/undo ownership, or creating a local watcher.
// The shell owns rekeying its tab and BufferManager indexes after this succeeds.
func (e *Editor) RetargetDocumentPath(path string) error {
	if e.documentProjection == nil || e.buffer == nil {
		return errors.New("canonical document attachment required")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("canonical document path must be absolute and clean")
	}
	e.filePath, e.title = path, filepath.Base(path)
	e.buffer.Path, e.buffer.Title = path, e.title
	e.buf.filePath = path
	e.documentProjection.snapshot.Path = path
	return nil
}
