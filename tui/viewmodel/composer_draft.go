package viewmodel

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
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
	Text   string                 `json:"text"`
	RefIDs []string               `json:"ref_ids"`
	Cursor int                    `json:"cursor"`
	Spans  []composerDraftRefSpan `json:"spans,omitempty"`
}

type composerDraftRefSpan struct {
	ID    string `json:"id"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// composerDraftLoadedMsg carries a hydrated draft back to Update.
type composerDraftLoadedMsg struct {
	session *workspace.Session
	text    string
	cursor  int
	refIDs  []string
	spans   []composerDraftRefSpan
}

// composerDraftSavedMsg closes one in-flight save; a coalesced dirty flag
// re-arms the next save from Update (single-writer, ordered).
type composerDraftSavedMsg struct{}

// maybeHydrateComposerDraft runs once per frame from View(). When the active
// session pointer changed, the composer clears immediately (no cross-session
// draft leakage) and the incoming session's persisted draft loads
// asynchronously. The outgoing session's draft was already saved continuously.
func (m *App) maybeHydrateComposerDraft() {
	if m.ActiveSession == m.composerHydratedFor {
		return
	}
	m.composerHydratedFor = m.ActiveSession
	m.composerSavedDraft = ""
	m.Input.ClearInput()
	m.Input.ClearInputSelection()
	if m.RefStore() != nil {
		m.Input.SetReferenceStore(m.RefStore())
	}
	sess := m.ActiveSession
	if sess == nil || sess.DB == nil {
		return
	}
	m.runDetachedAsyncCmd(func() tea.Msg {
		return m.loadComposerDraftMsg(sess)
	})
}

// loadComposerDraftMsg reads one session's persisted draft. Split from the
// async wrapper so tests can exercise the DB round-trip synchronously.
func (m *App) loadComposerDraftMsg(sess *workspace.Session) tea.Msg {
	if sess == nil || sess.DB == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), composerDraftIOTimeout)
	defer cancel()
	raw, err := sessiondb.LoadRuntimeState(ctx, sess.DB, composerDraftStateKey)
	if err != nil || len(raw) == 0 {
		return nil
	}
	var snap composerDraftSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil
	}
	return composerDraftLoadedMsg{
		session: sess,
		text:    snap.Text,
		cursor:  snap.Cursor,
		refIDs:  snap.RefIDs,
		spans:   snap.Spans,
	}
}

// applyComposerDraft restores a loaded draft for the still-active session.
// Chip spans whose backing reference no longer exists are removed from the
// text (highest offset first); surviving tokens re-resolve against the live
// reference store, and the caret clamps into the restored text.
func (m *App) applyComposerDraft(msg composerDraftLoadedMsg) {
	if msg.session == nil || m.ActiveSession != msg.session {
		return
	}
	store := m.RefStore()
	if store == nil {
		return
	}
	runes := []rune(msg.text)
	var dead []input.RefRange
	for _, sp := range msg.spans {
		if ref, ok := store.GetReference(sp.ID); !ok || !ref.Active() {
			dead = append(dead, input.RefRange{ID: sp.ID, Start: sp.Start, End: sp.End})
		}
	}
	sort.Slice(dead, func(i, j int) bool { return dead[i].Start > dead[j].Start })
	for _, rr := range dead {
		n := len(runes)
		if rr.Start < 0 || rr.End > n || rr.Start >= rr.End {
			continue
		}
		// Also consume one neighboring space so words don't glue together.
		cutStart, cutEnd := rr.Start, rr.End
		if n > 0 && cutEnd < n && runes[cutEnd] == ' ' {
			cutEnd++
		} else if cutStart > 0 && runes[cutStart-1] == ' ' {
			cutStart--
		}
		runes = append(runes[:cutStart], runes[cutEnd:]...)
	}
	text := string(runes)
	m.composerSavedDraft = text
	m.Input.SetReferenceStore(store)
	m.Input.SetValuePreserveReferences(text)
	cursor := msg.cursor
	if n := len([]rune(text)); cursor < 0 || cursor > n {
		cursor = n
	}
	m.Input.SetCursorFlat(cursor)
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
	text := m.Input.Value()
	if m.composerSaving {
		if text != m.composerSavedDraft {
			m.composerDirtySinceSave = true
		}
		return
	}
	if text == m.composerSavedDraft {
		return
	}
	m.composerSavedDraft = text
	snap, err := json.Marshal(composerDraftSnapshot{
		Text:   text,
		RefIDs: m.Input.ReferencedIDs(),
		Cursor: m.Input.CursorFlat(),
		Spans:  composerDraftRefSpans(m.Input.RefRanges()),
	})
	if err != nil {
		return
	}
	db := sess.DB
	m.composerSaving = true
	m.runDetachedAsyncCmd(func() tea.Msg {
		return m.saveComposerDraftMsg(db, snap)
	})
}

// saveComposerDraftMsg writes one draft snapshot. Split from the async
// wrapper so tests can exercise the write synchronously.
func (m *App) saveComposerDraftMsg(db *sql.DB, snap []byte) tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), composerDraftIOTimeout)
	defer cancel()
	_ = sessiondb.SaveRuntimeState(ctx, db, composerDraftStateKey, snap)
	return composerDraftSavedMsg{}
}

func (m *App) onComposerDraftSaved() {
	m.composerSaving = false
	if !m.composerDirtySinceSave {
		return
	}
	m.composerDirtySinceSave = false
	m.persistComposerDraftIfChanged()
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
