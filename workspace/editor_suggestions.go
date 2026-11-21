package workspace

import (
	"context"
	"errors"
	"path/filepath"

	"crude/insight/textindex"
	"crude/types"
	"crude/workspace/document"
)

// installSuggestions creates the text index and the suggestion service for a
// project. The core starts the index; nothing here reads the disk.
func (w *Workspace) installSuggestions(root string, db *WorkspaceDB) {
	w.TextIndex = textindex.New(root, 0)
	w.suggestions = nil
	if db == nil || w.LiveDocuments == nil {
		return
	}
	service, err := newEditorPredictionService(root, w.LiveDocuments, w.TextIndex, db.DB)
	if err != nil {
		if w.Logger != nil {
			w.Logger.Warn("SUGGEST", "service_unavailable", "error", err.Error())
		}
		return
	}
	w.suggestions = service
}

func (w *Workspace) closeSuggestions() {
	if w.suggestions != nil {
		_ = w.suggestions.Close()
		w.suggestions = nil
	}
	if w.TextIndex != nil {
		w.TextIndex.Close()
	}
}

// configureSuggestions binds the effective workspace choice and the current
// connection. It runs on the workspace owner and does no I/O.
func (w *Workspace) configureSuggestions(effective types.AppSettings) {
	if w.suggestions == nil {
		return
	}
	overrides := w.LoadWorkspaceOverrides()
	policy := predictionRuntimePolicy{settings: effective.Prediction, rootIdentity: overrides.PredictionRootIdentity,
		grant: overrides.PredictionGrantID, consented: overrides.PredictionConnectionIdentity}
	if effective.Prediction.Mode != types.PredictionOff {
		if w.sessionCfg == nil {
			policy.completerErr = errors.New("the Errand connection is not ready")
		} else {
			policy.completer, policy.completerErr = w.sessionCfg.SuggestionCompleter()
		}
	}
	w.suggestions.Configure(policy)
}

var errSuggestionsUnavailable = errors.New("suggestions are not available in this workspace")

// RequestEditorSuggestion answers one editor suggestion request off the
// workspace owner. An empty result means there is nothing to show.
func (w *Workspace) RequestEditorSuggestion(ctx context.Context, owner document.Owner, request types.EditorPredictionRequest) (types.EditorPredictionResult, error) {
	if w == nil || w.suggestions == nil {
		return types.EditorPredictionResult{}, errSuggestionsUnavailable
	}
	return w.suggestions.Request(ctx, owner, request)
}

// ReleaseEditorSuggestion tells the core that a view no longer wants an
// answer for sequence or older.
func (w *Workspace) ReleaseEditorSuggestion(owner document.Owner, viewID string, sequence uint64) {
	if w != nil && w.suggestions != nil {
		w.suggestions.Release(owner, viewID, sequence)
	}
}

// EditorSuggestionStatus returns the suggestion status, refreshing the hourly
// meter first. It reads the ledger and must run off the workspace owner.
func (w *Workspace) EditorSuggestionStatus(ctx context.Context) types.EditorPredictionStatus {
	if w == nil || w.suggestions == nil {
		return types.EditorPredictionStatus{Reason: "Suggestions are not available in this workspace."}
	}
	_ = w.suggestions.RefreshMeter(ctx)
	return w.suggestions.Snapshot()
}

// RecentEdits returns up to limit recent edits in one file (all files when
// path is empty), newest last, with authors relative to owner. Paths are
// workspace-relative with forward slashes.
func (w *Workspace) RecentEdits(owner document.Owner, path string, limit int) []types.RecentEdit {
	if w == nil || w.LiveDocuments == nil {
		return nil
	}
	if path != "" && filepath.IsAbs(path) {
		path = filepath.Clean(path)
	} else if path != "" {
		path = filepath.Join(w.RootPath, filepath.FromSlash(path))
	}
	s := w.LiveDocuments
	s.mu.Lock()
	defer s.mu.Unlock()
	return allowedEdits(w.RootPath, s.recentEditsLocked(owner, limit, path))
}
