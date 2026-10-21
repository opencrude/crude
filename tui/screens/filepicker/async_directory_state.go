package filepicker

import (
	"path/filepath"

	"crude/tui/filetree"
)

const asyncDirectoryLoadingText = "Loading…"

type asyncDirectoryLoad struct {
	intent   IntentToken
	path     string
	expand   bool
	snapshot pickerSnapshot
}

// pickerSnapshot is kept separate from the async record so restoring a failed
// navigation never has to reconstruct a tree from disk.
type pickerSnapshot struct {
	current      string
	model        *filetree.FileTreeModel
	browseModel  *filetree.FileTreeModel
	entries      []Entry
	searchActive bool
	searchQuery  string
	filter       string
	selected     map[string]bool
}

// BeginAsyncDirectoryLoad enters a loading state for a root navigation. It
// performs no filesystem I/O.
func (s *Screen) BeginAsyncDirectoryLoad(path string) IntentToken {
	if s == nil || !s.active {
		return IntentToken{}
	}
	intent := IntentToken{Epoch: s.openEpoch, Sequence: s.intentSeq}
	if !s.beginAsyncDirectoryLoad(path, intent) {
		return IntentToken{}
	}
	return intent
}

// BeginAsyncDirectoryLoadFor starts a root navigation only for the current
// picker lifecycle and intent.
func (s *Screen) BeginAsyncDirectoryLoadFor(path string, intent IntentToken) bool {
	if s == nil || !s.active || intent.Epoch != s.openEpoch {
		return false
	}
	if intent.Sequence != 0 && intent.Sequence != s.intentSeq {
		return false
	}
	return s.beginAsyncDirectoryLoad(path, intent)
}

func (s *Screen) beginAsyncDirectoryLoad(path string, intent IntentToken) bool {
	if s == nil || !s.active || intent.Epoch != s.openEpoch {
		return false
	}
	snapshot := s.currentSnapshot()
	if s.asyncLoad != nil {
		snapshot = s.asyncLoad.snapshot
	}
	s.asyncLoad = &asyncDirectoryLoad{
		intent: intent, path: cleanAbs(path), snapshot: snapshot,
	}
	s.current = cleanAbs(path)
	s.errText = asyncDirectoryLoadingText
	s.filter.field.SetValue("")
	s.searchActive = false
	s.searchQuery = ""
	s.installModel(filetree.NewPickerFileTreeModel(s.current, s.mode == ModeFolder))
	s.sync()
	return true
}

// BeginAsyncDirectoryExpandFor marks one existing tree directory as loading
// while keeping all other rows and the current breadcrumb intact.
func (s *Screen) BeginAsyncDirectoryExpandFor(path string, intent IntentToken) bool {
	if s == nil || !s.active || intent.Epoch != s.openEpoch {
		return false
	}
	if intent.Sequence != 0 && intent.Sequence != s.intentSeq {
		return false
	}
	path = cleanAbs(path)
	if s.model == nil || !s.model.MarkDirectoryLoading(path) {
		return false
	}
	snapshot := s.currentSnapshot()
	if s.asyncLoad != nil {
		snapshot = s.asyncLoad.snapshot
	}
	s.asyncLoad = &asyncDirectoryLoad{intent: intent, path: path, expand: true, snapshot: snapshot}
	s.pendingChildPath = ""
	s.errText = asyncDirectoryLoadingText
	s.sync()
	return true
}

func (s *Screen) CompleteAsyncDirectoryLoad(path string, entries []Entry) bool {
	if s == nil || s.asyncLoad == nil {
		return false
	}
	return s.CompleteAsyncDirectoryLoadFor(s.asyncLoad.intent, path, entries)
}

func (s *Screen) CompleteAsyncDirectoryLoadFor(intent IntentToken, path string, entries []Entry) bool {
	if !s.matchesAsyncDirectoryLoad(intent) {
		return false
	}
	load := s.asyncLoad
	s.discardAsyncDirectoryLoad()
	if load.expand {
		return s.SetDirectoryChildren(path, entries)
	}
	s.current = cleanAbs(path)
	s.entries = append(s.entries[:0], entries...)
	s.errText = ""
	s.filter.field.SetValue("")
	s.searchActive = false
	s.searchQuery = ""
	s.installModel(filetree.NewPickerFileTreeModel(s.current, s.mode == ModeFolder))
	s.model.SetDirectoryEntries(s.current, toTreeEntries(entries))
	s.rebuildActions()
	s.sync()
	return true
}

func (s *Screen) FailAsyncDirectoryLoad(err error) bool {
	if s == nil || s.asyncLoad == nil {
		return false
	}
	return s.FailAsyncDirectoryLoadFor(s.asyncLoad.intent, err)
}

func (s *Screen) FailAsyncDirectoryLoadFor(intent IntentToken, err error) bool {
	if !s.matchesAsyncDirectoryLoad(intent) {
		return false
	}
	load := s.asyncLoad
	s.discardAsyncDirectoryLoad()
	if load.expand {
		if s.model != nil {
			s.model.SetDirectoryLoadError(load.path)
		}
		s.errText = errorText(err)
		s.rebuildActions()
		s.sync()
		return true
	}
	s.restoreSnapshot(load.snapshot)
	s.errText = errorText(err)
	s.rebuildActions()
	s.sync()
	return true
}

func (s *Screen) CancelAsyncDirectoryLoad() bool {
	if s == nil || s.asyncLoad == nil {
		return false
	}
	if s.asyncLoad.expand && s.model != nil {
		s.model.SetDirectoryLoadError(s.asyncLoad.path)
	}
	s.restoreSnapshot(s.asyncLoad.snapshot)
	return true
}

func (s *Screen) CancelAsyncDirectoryLoadFor(intent IntentToken) bool {
	if !s.matchesAsyncDirectoryLoad(intent) {
		return false
	}
	return s.CancelAsyncDirectoryLoad()
}

func (s *Screen) AsyncDirectoryLoading() bool {
	return s != nil && s.asyncLoad != nil
}

func (s *Screen) AsyncDirectoryIntent() (IntentToken, bool) {
	if s == nil || s.asyncLoad == nil {
		return IntentToken{}, false
	}
	return s.asyncLoad.intent, true
}

func (s *Screen) currentSnapshot() pickerSnapshot {
	filter := ""
	if s.filter != nil && s.filter.field != nil {
		filter = s.filter.field.Value()
	}
	return pickerSnapshot{
		current: s.current, model: s.model, filter: filter,
		browseModel: s.browseModel, entries: append([]Entry(nil), s.entries...),
		searchActive: s.searchActive, searchQuery: s.searchQuery,
		selected: cloneSelected(s.selected),
	}
}

func (s *Screen) restoreSnapshot(snapshot pickerSnapshot) {
	s.discardAsyncDirectoryLoad()
	s.current = snapshot.current
	s.model = snapshot.model
	s.browseModel = snapshot.browseModel
	s.entries = append(s.entries[:0], snapshot.entries...)
	s.searchActive = snapshot.searchActive
	s.searchQuery = snapshot.searchQuery
	s.selected = cloneSelected(snapshot.selected)
	if s.model != nil {
		s.configureModel()
	}
	s.filter.field.SetValue(snapshot.filter)
	s.errText = ""
	s.rebuildTree()
	s.rebuildActions()
	s.sync()
}

func (s *Screen) matchesAsyncDirectoryLoad(intent IntentToken) bool {
	return s != nil && s.active && s.asyncLoad != nil &&
		s.asyncLoad.intent == intent && intent.Epoch == s.openEpoch &&
		(intent.Sequence == 0 || intent.Sequence == s.intentSeq)
}

func (s *Screen) discardAsyncDirectoryLoad() {
	if s != nil {
		s.asyncLoad = nil
	}
}

func cloneSelected(src map[string]bool) map[string]bool {
	if len(src) == 0 {
		return make(map[string]bool)
	}
	dst := make(map[string]bool, len(src))
	for path, selected := range src {
		if selected {
			dst[filepath.Clean(path)] = true
		}
	}
	return dst
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
