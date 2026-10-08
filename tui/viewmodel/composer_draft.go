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
// session qualifier. RefSpans and IDs retain exact attachment identity, including
// unresolved media; user-typed bracketed prose is never guessed as an attachment.
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
	loading      bool
	input        *input.Model
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

// maybeHydrateComposerDraft runs at session activation/Init, never from Paint.
// Warm editors are retained whole; cold drafts bind the destination reference
// store immediately and load persisted text off Update.
func (m *App) maybeHydrateComposerDraft() {
	m.runDetachedAsyncCmd(m.hydrateComposerDraftCmd())
}

func (m *App) hydrateComposerDraftCmd() tea.Cmd {
	if m.ActiveSession == m.composerHydratedFor {
		return nil
	}
	if previous := m.composerHydratedFor; previous != nil {
		m.persistComposerDraftFor(previous)
		m.Input.SuspendDraft()
		retained := m.Input
		m.draftOwner(previous).input = &retained
	}
	m.composerDraftEpoch++
	epoch := m.composerDraftEpoch
	m.composerHydratedFor = m.ActiveSession
	sess := m.ActiveSession
	if sess == nil {
		m.Input = m.Input.NewDraft()
		return nil
	}
	owner := m.draftOwner(sess)
	if owner.input != nil {
		m.Input = *owner.input
		owner.input = nil
		m.Input.SetTheme(m.Theme)
		if m.Prompts != nil {
			m.Input.SetHistory(m.Prompts.Entries())
		}
		if !owner.loading || m.Input.DraftRevision() != owner.loadRevision {
			owner.loading = false
			return nil
		}
	} else {
		m.Input = m.Input.NewDraft()
		if store := m.RefStore(); store != nil {
			m.Input.SetReferenceStore(store)
		}
	}
	if sess.DB == nil {
		return nil
	}
	if owner.epoch == 0 {
		owner.epoch = epoch // save lifetime, not each activation's load epoch
	}
	owner.loading = true
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
		return composerDraftLoadedMsg{session: sess, epoch: epoch, requestRevision: requestRevision, err: err}
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

// applyComposerDraft restores a loaded draft for the unchanged active owner.
// Missing references retain their exact IDs; no label-based repair is inferred.
func (m *App) applyComposerDraft(msg composerDraftLoadedMsg) {
	if msg.session == nil || m.ActiveSession != msg.session || msg.epoch != m.composerDraftEpoch {
		return
	}
	owner := m.draftOwner(msg.session)
	if owner.loadRevision != msg.requestRevision || m.Input.DraftRevision() != msg.requestRevision {
		return
	}
	if msg.err != nil {
		m.composerDraftErr = msg.err
		return
	}
	owner.loading = false
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
	m.persistComposerDraftFor(m.ActiveSession)
}

func (m *App) persistComposerDraftFor(sess *workspace.Session) {
	if sess == nil || sess.DB == nil {
		return
	}
	owner := m.draftOwner(sess)
	if owner.loading && m.Input.DraftRevision() == owner.loadRevision {
		return // an unmodified loading projection is not an empty saved draft
	}
	snap, err := marshalComposerDraft(m.Input)
	if err != nil {
		m.composerDraftErr = err
		return
	}
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
		if len(owner.queued) == 0 {
			owner.queued = append([]byte(nil), msg.raw...)
		}
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
	m.composerDraftStopping.Store(true)
	defer m.composerDraftStopping.Store(false)
	m.composerDraftWrites.Wait()
	if m.ActiveSession != nil {
		m.draftOwner(m.ActiveSession)
	}
	for sess, owner := range m.composerDraftOwners {
		model := owner.input
		if sess == m.ActiveSession {
			model = &m.Input
		}
		if sess.DB == nil || model == nil || owner.loading && model.DraftRevision() == owner.loadRevision {
			continue
		}
		snap, err := marshalComposerDraft(*model)
		if err != nil {
			m.composerDraftErr = err
			return
		}
		if bytes.Equal(snap, owner.persisted) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), composerDraftIOTimeout)
		err = sessiondb.SaveRuntimeState(ctx, sess.DB, composerDraftStateKey, snap)
		cancel()
		if err != nil {
			m.composerDraftErr = err
			return
		}
		owner.persisted = append(owner.persisted[:0], snap...)
		owner.inFlight, owner.queued = nil, nil
	}
	m.composerDraftErr = nil
}

func marshalComposerDraft(model input.Model) ([]byte, error) {
	from, to := model.InputSelectionRange()
	return json.Marshal(composerDraftSnapshot{Text: model.Value(), RefIDs: model.SnapshotReferenceIDs(),
		Cursor: model.CursorFlat(), SelectionFrom: from, SelectionTo: to, Scroll: model.ScrollOffset(),
		Revision: model.DraftRevision(), Spans: composerDraftRefSpans(model.RefRanges())})
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
