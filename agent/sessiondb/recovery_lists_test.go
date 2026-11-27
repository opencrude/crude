package sessiondb

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"crude/agent/provider"
	recoverytypes "crude/types/recovery"
)

// recoveryListFixture is one session database whose list rows are counted as
// they are written.
type recoveryListFixture struct {
	t       *testing.T
	db      *sql.DB
	history []provider.ChatMessage
}

func newRecoveryListFixture(t *testing.T, history int) *recoveryListFixture {
	t.Helper()
	db := OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { _ = db.Close() })
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE written_rows(list_id INTEGER);
		CREATE TRIGGER count_written_rows AFTER INSERT ON agent_message_list_rows BEGIN INSERT INTO written_rows VALUES(NEW.list_id); END`); err != nil {
		t.Fatal(err)
	}
	f := &recoveryListFixture{t: t, db: db}
	for i := range history {
		message := provider.NewTextMessage("user", fmt.Sprintf("history message %d", i))
		message.InternalID = fmt.Sprintf("h%d", i)
		f.history = append(f.history, message)
	}
	return f
}

// save checkpoints a root run: its active context is the session's context.
func (f *recoveryListFixture) save(incident string, state recoverytypes.State, archive, active []provider.ChatMessage) {
	f.t.Helper()
	current, _, err := LoadContextState(f.db, "session", "session")
	if err != nil {
		f.t.Fatal(err)
	}
	checkpoint := RecoveryCheckpoint{Status: recoverytypes.Status{ID: incident, AgentID: "root", Generation: 1, State: state},
		PendingInput: provider.PlainInput("next"), InputAdmitted: true, Archive: archive, ActiveContext: active,
		ContextOwnerID: "session", ContextRevision: current.Revision, ContextGeneration: 1, ContextReason: "recovery"}
	if err := SaveRecoveryCheckpoint(f.db, "session", checkpoint); err != nil {
		f.t.Fatal(err)
	}
}

// written returns the list rows written since the last call.
func (f *recoveryListFixture) written() int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM written_rows`).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.db.Exec(`DELETE FROM written_rows`); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *recoveryListFixture) expectLists(incident string, archive, active []provider.ChatMessage) {
	f.t.Helper()
	loaded, found, err := LoadRecoveryIncident(f.db, "session", "root", incident)
	if err != nil || !found {
		f.t.Fatalf("load %s: found=%v err=%v", incident, found, err)
	}
	if !reflect.DeepEqual(loaded.Archive, archive) || !reflect.DeepEqual(loaded.ActiveContext, active) {
		f.t.Fatalf("%s lists differ:\n archive=%d want %d\n active=%d want %d", incident, len(loaded.Archive), len(archive), len(loaded.ActiveContext), len(active))
	}
}

func extend(messages []provider.ChatMessage, id string) []provider.ChatMessage {
	message := provider.NewTextMessage("user", "new input "+id)
	message.InternalID = id
	return append(messages[:len(messages):len(messages)], message)
}

// A turn starts a new incident whose archive is the whole history plus the new
// input. It must write the message it adds, not the history again, and the
// run that ended cleanly must not take its rows with it.
func TestNewRunExtendsItsAgentsRecoveryList(t *testing.T) {
	f := newRecoveryListFixture(t, 200)
	first := extend(f.history, "turn-1")
	f.save("incident-1", "", first, first[150:])
	if got := f.written(); got != 201+51 {
		t.Fatalf("first run wrote %d rows, want its whole lists (252)", got)
	}
	if err := DeleteRecoveryCheckpoint(f.db, "incident-1"); err != nil {
		t.Fatal(err)
	}

	second := extend(first, "turn-2")
	f.save("incident-2", "", second, second[150:])
	if got := f.written(); got != 2 {
		t.Fatalf("a new turn wrote %d rows, want only its new message in each list (2)", got)
	}
	f.expectLists("incident-2", second, second[150:])
	var rows int
	if err := f.db.QueryRow(`SELECT count(*) FROM agent_message_list_rows`).Scan(&rows); err != nil || rows != len(second)+len(second[150:]) {
		t.Fatalf("stored %d rows (err %v), want one copy of each list (%d)", rows, err, len(second)+len(second[150:]))
	}

	// A new process knows nothing of the stored rows (the first turn after a
	// restart). It compares them with the messages and still writes only the
	// new one.
	forgetRecoveryLists()
	if err := DeleteRecoveryCheckpoint(f.db, "incident-2"); err != nil {
		t.Fatal(err)
	}
	third := extend(second, "turn-3")
	f.save("incident-3", "", third, third[150:])
	if got := f.written(); got != 2 {
		t.Fatalf("after a restart a new turn wrote %d rows, want only its new message in each list (2)", got)
	}
	f.expectLists("incident-3", third, third[150:])
	if err := f.db.QueryRow(`SELECT count(*) FROM agent_message_list_rows`).Scan(&rows); err != nil || rows != len(third)+len(third[150:]) {
		t.Fatalf("stored %d rows (err %v) after a restart, want %d", rows, err, len(third)+len(third[150:]))
	}

	// Stored rows that differ from the messages (the same message with other
	// display fields after a resume) are written from the first difference.
	forgetRecoveryLists()
	fourth := extend(third, "turn-4")
	fourth[120].DisplayContent = "shown differently"
	f.save("incident-3", "", fourth, fourth[150:])
	if got, want := f.written(), len(fourth)-120+1; got != want {
		t.Fatalf("a list differing at message 120 wrote %d rows, want %d", got, want)
	}
	f.expectLists("incident-3", fourth, fourth[150:])
}

// An incident written before lists left its base may still hold messages in
// it. A write that compares stored rows replaces that base, so the list is
// never the old base followed by the whole history again.
func TestStoredListComparisonEmptiesAnOlderBase(t *testing.T) {
	f := newRecoveryListFixture(t, 30)
	f.save("incident", "", f.history, f.history[:5])
	raw, err := json.Marshal(f.history[:10])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE agent_recoveries SET archive_json=? WHERE incident_id='incident'`, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`DELETE FROM agent_message_list_rows WHERE position<10 AND list_id=(SELECT list_id FROM agent_recovery_lists WHERE incident_id='incident' AND field='archive')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE agent_message_list_rows SET position=position-10 WHERE list_id=(SELECT list_id FROM agent_recovery_lists WHERE incident_id='incident' AND field='archive')`); err != nil {
		t.Fatal(err)
	}
	forgetRecoveryLists()
	f.expectLists("incident", f.history, f.history[:5])
	forgetRecoveryLists()
	next := extend(f.history, "next")
	f.save("incident", "", next, f.history[:5])
	f.expectLists("incident", next, f.history[:5])
}

// A parent database mirrors each child's incident without a context owner.
// The child never runs again in the parent's database, so a finished
// child's mirrored lists must not stay there as a copy of its history.
func TestFinishedMirrorTakesItsListsAlong(t *testing.T) {
	f := newRecoveryListFixture(t, 40)
	mirror := RecoveryCheckpoint{Status: recoverytypes.Status{ID: "child-incident", AgentID: "child", Generation: 1},
		PendingInput: provider.PlainInput("assignment"), InputAdmitted: true, Archive: f.history, ActiveContext: f.history}
	if err := SaveRecoveryCheckpoint(f.db, "session", mirror); err != nil {
		t.Fatal(err)
	}
	if err := DeleteRecoveryCheckpoint(f.db, "child-incident"); err != nil {
		t.Fatal(err)
	}
	var lists, rows int
	if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM agent_message_lists), (SELECT count(*) FROM agent_message_list_rows)`).Scan(&lists, &rows); err != nil || lists != 0 || rows != 0 {
		t.Fatalf("a finished mirror left %d lists and %d rows (err %v)", lists, rows, err)
	}
}

func forgetRecoveryLists() {
	recoveryLists = newByteLRU[recoveryListKey, recoveryListEntry](messageCacheBytes)
}

// An unfinished incident keeps its lists. Another incident of the same agent
// starts its own lists instead of rewriting them.
func TestRecoveryListOfUnfinishedIncidentIsNeverTaken(t *testing.T) {
	f := newRecoveryListFixture(t, 20)
	parked := extend(f.history, "parked")
	f.save("parked", recoverytypes.NeedsAttention, parked, parked)
	f.written()
	other := extend(f.history[:5], "other")
	f.save("other", "", other, other)
	if got := f.written(); got != 2*len(other) {
		t.Fatalf("second incident wrote %d rows, want its own lists (%d)", got, 2*len(other))
	}
	f.expectLists("parked", parked, parked)
	f.expectLists("other", other, other)
}

// A later run may take over the lists of a settled incident. The settled
// incident then reports that its lists are gone; it never returns the later
// run's messages as its own.
func TestSettledIncidentWhoseListWasTakenRefusesToLoad(t *testing.T) {
	f := newRecoveryListFixture(t, 20)
	settled := extend(f.history, "settled")
	f.save("settled", recoverytypes.Recovered, settled, settled)
	later := extend(f.history[:3], "later")
	f.save("later", "", later, later)
	f.expectLists("later", later, later)
	if _, _, err := LoadRecoveryIncident(f.db, "session", "root", "settled"); !errors.Is(err, errRecoveryListReplaced) {
		t.Fatalf("settled incident with replaced lists loaded: %v", err)
	}
}
