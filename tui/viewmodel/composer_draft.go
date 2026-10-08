package viewmodel

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/tui/input"
	"crude/workspace"
)

const composerDraftStateKey = "composer_draft"

const composerDraftIOTimeout = 2 * time.Second

// composerDraftSnapshot is the persisted per-session composer state. It lives
// in the session's own SQLite DB (session_runtime_state), so the key needs no
// session qualifier. RefSpans records where attachment chips sat in the text;
// on restore, only spans whose backing reference is gone are removed, so
// user-typed bracketed prose (e.g. array[0]) is never touched.
type composerDraftSnapshot struct {
	Text          string                 `json:"text"`
	RefIDs        []string               `json:"ref_ids"`
	Cursor        int                    `json:"cursor"`
	SelectionFrom int                    `json:"selection_from,omitempty"`
	SelectionTo   int                    `json:"selection_to,omitempty"`
	Scroll        int                    `json:"scroll,omitempty"`
	Revision      uint64                 `json:"revision"`
	Spans         []composerDraftRefSpan `json:"spans,omitempty"`
}

type composerDraftRefSpan struct {
	ID    string `json:"id"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type composerDraftOwner struct {
	epoch        uint64
	loadRevision uint64
	persisted    []byte
	inFlight     []byte
	queued       []byte
}

// composerDraftLoadedMsg carries a hydrated draft back to Update.
type composerDraftLoadedMsg struct {
	session         *workspace.Session
	epoch           uint64
	err             error
	text            string
	cursor          int
	selectionFrom   int
	selectionTo     int
	scroll          int
	revision        uint64
	requestRevision uint64
	refIDs          []string
	spans           []composerDraftRefSpan
}

// composerDraftSavedMsg closes one in-flight save; a coalesced dirty flag
// re-arms the next save from Update (single-writer, ordered).
type composerDraftSavedMsg struct {
	session *workspace.Session
	epoch   uint64
	err     error
	raw     []byte
}

// maybeHydrateComposerDraft runs from Update/Init lifecycle boundaries. When the active
// session pointer changed, the composer clears immediately (no cross-session
// draft leakage) and the incoming session's persisted draft loads
// asynchronously. The outgoing session's draft was already saved continuously.
func (m *App) maybeHydrateComposerDraft() {
	m.runDetachedAsyncCmd(m.hydrateComposerDraftCmd())
}

func (m *App) hydrateComposerDraftCmd() tea.Cmd {
	if m.ActiveSession == m.composerHydratedFor {
		return nil
	}
	m.composerDraftEpoch++
	epoch := m.composerDraftEpoch
	m.composerHydratedFor = m.ActiveSession
	m.Input.ClearInput()
	m.Input.ClearInputSelection()
	if m.RefStore() != nil {
		m.Input.SetReferenceStore(m.RefStore())
	}
	sess := m.ActiveSession
	if sess == nil || sess.DB == nil {
		return nil
	}
	owner := m.draftOwner(sess)
	owner.epoch = epoch
	owner.loadRevision = m.Input.DraftRevision()
	requestRevision := owner.loadRevision
	return func() tea.Msg { return m.loadComposerDraftMsg(sess, epoch, requestRevision) }
}

func (m *App) draftOwner(sess *workspace.Session) *composerDraftOwner {
	if m.composerDraftOwners == nil {
		m.composerDraftOwners = make(map[*workspace.Session]*composerDraftOwner)
	}
	owner := m.composerDraftOwners[sess]
	if owner == nil {
		owner = &composerDraftOwner{}
		m.composerDraftOwners[sess] = owner
	}
	return owner
}

// loadComposerDraftMsg reads one session's persisted draft. Split from the
// async wrapper so tests can exercise the DB round-trip synchronously.
func (m *App) loadComposerDraftMsg(sess *workspace.Session, epoch, requestRevision uint64) tea.Msg {
	if sess == nil || sess.DB == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), composerDraftIOTimeout)
	defer cancel()
	raw, err := sessiondb.LoadRuntimeState(ctx, sess.DB, composerDraftStateKey)
	if err != nil {
		return composerDraftLoadedMsg{session: sess, epoch: epoch, requestRevision: requestRevision, err: err}
	}
	if len(raw) == 0 {
		return composerDraftLoadedMsg{session: sess, epoch: epoch, requestRevision: requestRevision}
	}
	var snap composerDraftSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return composerDraftLoadedMsg{session: sess, epoch: epoch, err: err}
	}
	return composerDraftLoadedMsg{
		session:         sess,
		epoch:           epoch,
		text:            snap.Text,
		cursor:          snap.Cursor,
		selectionFrom:   snap.SelectionFrom,
		selectionTo:     snap.SelectionTo,
		scroll:          snap.Scroll,
		revision:        snap.Revision,
		refIDs:          snap.RefIDs,
		spans:           snap.Spans,
		requestRevision: requestRevision,
	}
}

// applyComposerDraft restores a loaded draft for the still-active session.
// Chip spans whose backing reference no longer exists are removed from the
// text (highest offset first); surviving tokens re-resolve against the live
// reference store, and the caret clamps into the restored text.
func (m *App) applyComposerDraft(msg composerDraftLoadedMsg) {
	if msg.session == nil || m.ActiveSession != msg.session || msg.epoch != m.composerDraftEpoch {
		return
	}
	owner := m.draftOwner(msg.session)
	if owner.epoch != msg.epoch || owner.loadRevision != msg.requestRevision || m.Input.DraftRevision() != msg.requestRevision {
		return
	}
	if msg.err != nil {
		m.composerDraftErr = msg.err
		return
	}
	store := m.RefStore()
	text := msg.text
	if store != nil {
		m.Input.SetReferenceStore(store)
	}
	m.Input.SetValuePreserveReferences(text)
	m.Input.UseReferenceIDs(msg.refIDs)
	cursor := msg.cursor
	if n := len([]rune(text)); cursor < 0 || cursor > n {
		cursor = n
	}
	m.Input.SetCursorFlat(cursor)
	m.Input.SetSelection(msg.selectionFrom, msg.selectionTo)
	m.Input.SetScrollOffset(msg.scroll)
	if raw, err := json.Marshal(composerDraftSnapshot{Text: msg.text, RefIDs: msg.refIDs, Cursor: msg.cursor, SelectionFrom: msg.selectionFrom, SelectionTo: msg.selectionTo, Scroll: msg.scroll, Revision: msg.revision, Spans: msg.spans}); err == nil {
		owner.persisted = raw
	}
}

// persistComposerDraftIfChanged saves the draft asynchronously whenever the
// composer text diverges from the last persisted snapshot. At most one save
// is in flight; edits during flight coalesce into one follow-up save, so
// writes stay ordered without ever blocking the UI thread on SQLite.
func (m *App) persistComposerDraftIfChanged() {
	sess := m.ActiveSession
	if sess == nil || sess.DB == nil {
		return
	}
	selectionFrom, selectionTo := m.Input.InputSelectionRange()
	revision := m.Input.DraftRevision()
	snap, err := json.Marshal(composerDraftSnapshot{
		Text:          m.Input.Value(),
		RefIDs:        m.Input.SnapshotReferenceIDs(),
		Cursor:        m.Input.CursorFlat(),
		SelectionFrom: selectionFrom,
		SelectionTo:   selectionTo,
		Scroll:        m.Input.ScrollOffset(),
		Revision:      revision,
		Spans:         composerDraftRefSpans(m.Input.RefRanges()),
	})
	if err != nil {
		m.composerDraftErr = err
		return
	}
	owner := m.draftOwner(sess)
	if bytes.Equal(snap, owner.persisted) || bytes.Equal(snap, owner.inFlight) {
		return
	}
	if len(owner.inFlight) != 0 {
		owner.queued = snap
		return
	}
	if bytes.Equal(snap, owner.queued) {
		owner.queued = nil
	}
	m.startComposerDraftSave(sess, owner, snap)
}

func (m *App) startComposerDraftSave(sess *workspace.Session, owner *composerDraftOwner, snap []byte) {
	if m.composerDraftStopping.Load() {
		owner.queued = append([]byte(nil), snap...)
		return
	}
	owner.inFlight = append([]byte(nil), snap...)
	db, epoch := sess.DB, owner.epoch
	m.composerDraftWrites.Add(1)
	m.runDetachedAsyncCmd(func() tea.Msg {
		defer m.composerDraftWrites.Done()
		return m.saveComposerDraftMsg(sess, db, epoch, snap)
	})
}

// saveComposerDraftMsg writes one draft snapshot. Split from the async
// wrapper so tests can exercise the write synchronously.
func (m *App) saveComposerDraftMsg(sess *workspace.Session, db *sql.DB, epoch uint64, snap []byte) tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), composerDraftIOTimeout)
	defer cancel()
	err := sessiondb.SaveRuntimeState(ctx, db, composerDraftStateKey, snap)
	return composerDraftSavedMsg{session: sess, epoch: epoch, raw: snap, err: err}
}

func (m *App) onComposerDraftSaved(msg composerDraftSavedMsg) {
	owner := m.composerDraftOwners[msg.session]
	if owner == nil || owner.epoch != msg.epoch || !bytes.Equal(owner.inFlight, msg.raw) {
		return
	}
	owner.inFlight = nil
	m.composerDraftErr = msg.err
	if msg.err != nil {
		owner.queued = append([]byte(nil), msg.raw...)
		return
	}
	owner.persisted = append(owner.persisted[:0], msg.raw...)
	next := owner.queued
	owner.queued = nil
	if len(next) != 0 && !bytes.Equal(next, owner.persisted) {
		m.startComposerDraftSave(msg.session, owner, next)
	}
}

// flushComposerDraft is the teardown barrier. It waits for ordered async
// writes, then commits the latest full editor snapshot before the DB closes.
func (m *App) flushComposerDraft() {
	sess := m.ActiveSession
	if sess == nil || sess.DB == nil {
		return
	}
	m.composerDraftStopping.Store(true)
	defer m.composerDraftStopping.Store(false)
	m.composerDraftWrites.Wait()
	selectionFrom, selectionTo := m.Input.InputSelectionRange()
	snap, err := json.Marshal(composerDraftSnapshot{Text: m.Input.Value(), RefIDs: m.Input.SnapshotReferenceIDs(), Cursor: m.Input.CursorFlat(), SelectionFrom: selectionFrom, SelectionTo: selectionTo, Scroll: m.Input.ScrollOffset(), Revision: m.Input.DraftRevision(), Spans: composerDraftRefSpans(m.Input.RefRanges())})
	if err != nil {
		m.composerDraftErr = err
		return
	}
	owner := m.draftOwner(sess)
	if bytes.Equal(snap, owner.persisted) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), composerDraftIOTimeout)
	defer cancel()
	if err := sessiondb.SaveRuntimeState(ctx, sess.DB, composerDraftStateKey, snap); err != nil {
		m.composerDraftErr = err
		return
	}
	owner.persisted = append(owner.persisted[:0], snap...)
	owner.inFlight, owner.queued = nil, nil
	m.composerDraftErr = nil
}

// composerDraftRefSpans converts live chip ranges into persisted spans.
func composerDraftRefSpans(ranges []input.RefRange) []composerDraftRefSpan {
	if len(ranges) == 0 {
		return nil
	}
	out := make([]composerDraftRefSpan, len(ranges))
	for i, rr := range ranges {
		out[i] = composerDraftRefSpan{ID: rr.ID, Start: rr.Start, End: rr.End}
	}
	return out
}
