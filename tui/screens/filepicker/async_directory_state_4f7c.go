package filepicker

import (
	"path/filepath"
)

const asyncDirectoryLoadingText = "Loading…"

type asyncDirectorySnapshot struct {
	current    string
	rawEntries []Entry
	filter     string
}

type asyncDirectoryLoad struct {
	intent   IntentToken
	snapshot asyncDirectorySnapshot
}

// BeginAsyncDirectoryLoad enters a visible loading state without discarding the
// last successful listing. The snapshot lets a failed read restore the prior
// directory instead of leaving the picker pointed at an unreadable path.
//
// This compatibility entry point binds the load to the screen's current
// lifecycle and intent. NavigateMsg consumers should prefer
// BeginAsyncDirectoryLoadFor so delayed messages cannot start a stale load.
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

// BeginAsyncDirectoryLoadFor starts a load only when intent is the newest
// emitted intent from the current open lifecycle.
func (s *Screen) BeginAsyncDirectoryLoadFor(path string, intent IntentToken) bool {
	if s == nil || !s.IsCurrentIntent(intent.Epoch, intent.Sequence) {
		return false
	}
	return s.beginAsyncDirectoryLoad(path, intent)
}

func (s *Screen) beginAsyncDirectoryLoad(path string, intent IntentToken) bool {
	if s == nil || !s.active || intent.Epoch != s.openEpoch {
		return false
	}
	snapshot := asyncDirectorySnapshot{
		current:    s.current,
		rawEntries: append([]Entry(nil), s.rawEntries...),
	}
	if s.filter != nil && s.filter.field != nil {
		snapshot.filter = s.filter.field.Value()
	}
	if s.asyncLoad != nil {
		snapshot = s.asyncLoad.snapshot
	}
	s.asyncLoad = &asyncDirectoryLoad{intent: intent, snapshot: snapshot}
	s.current = filepath.Clean(path)
	s.errText = asyncDirectoryLoadingText
	if s.filter != nil && s.filter.field != nil {
		s.filter.field.SetValue("")
	}
	if s.list != nil {
		s.list.lastClick = -1
	}
	// sync keeps both visible rows and the interactive list empty for the
	// entire load, including filter OnChanged callbacks.
	s.sync()
	return true
}

func (s *Screen) CompleteAsyncDirectoryLoad(path string, entries []Entry) bool {
	if s == nil || s.asyncLoad == nil {
		return false
	}
	return s.CompleteAsyncDirectoryLoadFor(s.asyncLoad.intent, path, entries)
}

// CompleteAsyncDirectoryLoadFor applies a result only to its exact load.
func (s *Screen) CompleteAsyncDirectoryLoadFor(intent IntentToken, path string, entries []Entry) bool {
	if !s.matchesAsyncDirectoryLoad(intent) {
		return false
	}
	s.discardAsyncDirectoryLoad()
	s.SetDirectory(path, entries)
	return true
}

func (s *Screen) FailAsyncDirectoryLoad(err error) bool {
	if s == nil || s.asyncLoad == nil {
		return false
	}
	return s.FailAsyncDirectoryLoadFor(s.asyncLoad.intent, err)
}

// FailAsyncDirectoryLoadFor restores state only for its exact load.
func (s *Screen) FailAsyncDirectoryLoadFor(intent IntentToken, err error) bool {
	if !s.matchesAsyncDirectoryLoad(intent) {
		return false
	}
	s.restoreAsyncDirectorySnapshot()
	s.SetError(err)
	return true
}

// CancelAsyncDirectoryLoad cancels the current load and restores the last
// successful raw listing. It is safe to call repeatedly.
func (s *Screen) CancelAsyncDirectoryLoad() bool {
	if s == nil || s.asyncLoad == nil {
		return false
	}
	s.restoreAsyncDirectorySnapshot()
	return true
}

// CancelAsyncDirectoryLoadFor cancels only the exact matching load.
func (s *Screen) CancelAsyncDirectoryLoadFor(intent IntentToken) bool {
	if !s.matchesAsyncDirectoryLoad(intent) {
		return false
	}
	s.restoreAsyncDirectorySnapshot()
	return true
}

func (s *Screen) AsyncDirectoryLoading() bool {
	return s != nil && s.asyncLoad != nil
}

// AsyncDirectoryIntent returns the epoch/sequence currently bound to the
// pending directory read.
func (s *Screen) AsyncDirectoryIntent() (IntentToken, bool) {
	if s == nil || s.asyncLoad == nil {
		return IntentToken{}, false
	}
	return s.asyncLoad.intent, true
}

func (s *Screen) restoreAsyncDirectorySnapshot() {
	if s == nil || s.asyncLoad == nil {
		return
	}
	snapshot := s.asyncLoad.snapshot
	s.discardAsyncDirectoryLoad()
	s.current = snapshot.current
	s.errText = ""
	s.setEntries(append([]Entry(nil), snapshot.rawEntries...))
	if s.filter != nil && s.filter.field != nil {
		s.filter.field.SetValue(snapshot.filter)
	}
	if s.list != nil {
		s.list.lastClick = -1
	}
	s.sync()
}

func (s *Screen) matchesAsyncDirectoryLoad(intent IntentToken) bool {
	return s != nil &&
		s.active &&
		s.asyncLoad != nil &&
		s.asyncLoad.intent == intent &&
		intent.Epoch == s.openEpoch &&
		intent.Sequence == s.intentSeq
}

func (s *Screen) discardAsyncDirectoryLoad() {
	if s != nil {
		s.asyncLoad = nil
	}
}
