package sessiondb

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"crude/agent/memory"
	"crude/agent/provider"
	"crude/internal/dataformat"
	"crude/types/recovery"
)

// Each released format keeps its exact schema as frozen test data, as
// Chromium keeps components/test/data/history/history.N.sql. A database of
// every older version must reach the schema that a new database gets.
//
//go:embed testdata/format-1.sql
var formatOneSchema string

//go:embed testdata/format-2.sql
var formatTwoSchema string

func openFormatFixture(t *testing.T, ddl string) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if ddl != "" {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	return db, path
}

// schemaShape describes every table by its columns, indexes and foreign keys,
// independent of the text and comments of the statements that created them.
func schemaShape(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	collect := func(query string, args ...any) string {
		rows, err := db.Query(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, _ := rows.Columns()
		var lines []string
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, fmt.Sprint(values...))
		}
		sort.Strings(lines)
		return strings.Join(lines, "\n")
	}
	shape := map[string]string{}
	for _, table := range tables {
		shape[table] = collect(`SELECT name, type, "notnull", dflt_value, pk FROM pragma_table_info(?)`, table) + "\n--\n" +
			collect(`SELECT l.name, l."unique", group_concat(i.name) FROM pragma_index_list(?) l, pragma_index_info(l.name) i GROUP BY l.name`, table) + "\n--\n" +
			collect(`SELECT "table", "from", "to", on_delete FROM pragma_foreign_key_list(?)`, table)
	}
	return shape
}

func message(role, text string) []byte {
	data, err := json.Marshal(provider.ChatMessage{Role: role, Content: provider.TextContent(text)})
	if err != nil {
		panic(err)
	}
	return data
}

// A v0.7.3–v0.8.17 session database with unfinished-run messages reaches the
// current schema, keeps every message of every incident exactly, and opens.
func TestFormatUpdatesAFormatOneSessionDatabase(t *testing.T) {
	db, path := openFormatFixture(t, formatOneSchema)
	ctx := t.Context()
	now := time.Now().UnixNano()
	if _, err := db.Exec(`INSERT INTO sessions(id, project_path, project_name, created_at) VALUES ('s', '/p', 'p', 1)`); err != nil {
		t.Fatal(err)
	}
	incidents := []struct {
		id, agent string
		updated   int64
		state     recovery.State
		archive   []string
		active    []string
		version   int64
	}{
		{"older", "agent", now - 2, recovery.Recovered, []string{"first", "second"}, []string{"second"}, 4},
		{"newer", "agent", now - 1, recovery.NeedsAttention, []string{"first", "second", "third"}, []string{"third"}, 9},
		{"child", "child", now, recovery.Interrupted, []string{"child task"}, nil, 1},
	}
	for _, incident := range incidents {
		if _, err := db.Exec(`INSERT INTO agent_recoveries(incident_id, session_id, agent_id, generation, state, pending_input_json, archive_json, active_context_json, context_owner_id, created_at, updated_at)
			VALUES (?, 's', ?, 1, ?, '{}', '[]', '[]', 'session', ?, ?)`, incident.id, incident.agent, string(incident.state), incident.updated, incident.updated); err != nil {
			t.Fatal(err)
		}
		for field, texts := range map[string][]string{"archive": incident.archive, "active": incident.active} {
			if _, err := db.Exec(`INSERT INTO agent_recovery_lists(incident_id, field, version) VALUES (?, ?, ?)`, incident.id, field, incident.version); err != nil {
				t.Fatal(err)
			}
			for position, text := range texts {
				if _, err := db.Exec(`INSERT INTO agent_recovery_tail(incident_id, field, position, message_json) VALUES (?, ?, ?, ?)`, incident.id, field, position, message("user", text)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if _, err := Format.Require(ctx, db); err == nil {
		t.Fatal("product code would open a format 1 database")
	}
	var fractions []float64
	from, err := Format.Update(ctx, db, path, filepath.Join(t.TempDir(), "backup.db"), func(f float64) { fractions = append(fractions, f) })
	if err != nil || from != 1 {
		t.Fatalf("update from %d: %v", from, err)
	}
	if len(fractions) == 0 || fractions[len(fractions)-1] != 1 || !sort.Float64sAreSorted(fractions) {
		t.Fatalf("progress %v", fractions)
	}
	if version, _ := dataformat.Version(ctx, db); version != Format.Current {
		t.Fatalf("version %d", version)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	fresh, _ := openFormatFixture(t, "")
	if err := Migrate(fresh); err != nil {
		t.Fatal(err)
	}
	if got, want := schemaShape(t, db), schemaShape(t, fresh); !reflect.DeepEqual(got, want) {
		for table := range want {
			if got[table] != want[table] {
				t.Errorf("table %s after the update:\n%s\nnew database:\n%s", table, got[table], want[table])
			}
		}
		for table := range got {
			if _, ok := want[table]; !ok {
				t.Errorf("the update left table %s that a new database does not have", table)
			}
		}
		t.FailNow()
	}
	for _, incident := range incidents {
		checkpoint, found, err := LoadRecoveryIncident(db, "s", incident.agent, incident.id)
		if err != nil || !found {
			t.Fatalf("incident %s: found=%v %v", incident.id, found, err)
		}
		texts := func(messages []provider.ChatMessage) []string {
			out := []string{}
			for _, m := range messages {
				out = append(out, m.Content.String())
			}
			return out
		}
		if got := texts(checkpoint.Archive); strings.Join(got, ",") != strings.Join(incident.archive, ",") {
			t.Fatalf("incident %s archive %v", incident.id, got)
		}
		if got := texts(checkpoint.ActiveContext); strings.Join(got, ",") != strings.Join(incident.active, ",") {
			t.Fatalf("incident %s active context %v", incident.id, got)
		}
	}
	// The agent's newest incident holds its agent's newest list.
	var newest string
	if err := db.QueryRow(`SELECT r.incident_id FROM agent_recovery_lists r JOIN agent_message_lists l ON l.list_id=r.list_id
		WHERE l.agent_id='agent' AND l.field='archive' ORDER BY l.list_id DESC LIMIT 1`).Scan(&newest); err != nil || newest != "newer" {
		t.Fatalf("newest archive list belongs to %q: %v", newest, err)
	}
}

// A failed step changes nothing: the old tables, rows and version stay.
func TestFormatUpdateFailureLeavesTheDatabaseUnchanged(t *testing.T) {
	db, path := openFormatFixture(t, formatOneSchema)
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF; INSERT INTO agent_recovery_tail(incident_id, field, position, message_json) VALUES ('missing', 'archive', 0, '{}'); PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := Format.Update(t.Context(), db, path, filepath.Join(t.TempDir(), "backup.db"), nil); err == nil || !strings.Contains(err.Error(), "belong to no recovery incident") {
		t.Fatalf("update accepted orphaned messages: %v", err)
	}
	var version, rows int
	var lists bool
	if err := db.QueryRow(`SELECT (SELECT user_version FROM pragma_user_version), (SELECT count(*) FROM agent_recovery_tail),
		EXISTS(SELECT 1 FROM sqlite_master WHERE name='agent_message_lists')`).Scan(&version, &rows, &lists); err != nil || version != 0 || rows != 1 || lists {
		t.Fatalf("failed update changed the database: version=%d rows=%d lists=%v %v", version, rows, lists, err)
	}
}

// Unversioned current databases only get their version; databases older than
// v0.7.3 and databases from a newer Crude stay unchanged.
func TestFormatClassifiesUnversionedAndRefusesNewerSessionDatabases(t *testing.T) {
	ctx := t.Context()
	current, path := openFormatFixture(t, formatTwoSchema)
	if found, err := Format.Inspect(ctx, current); err != nil || found.Version != 2 || found.Stamped || !found.Pending(Format) {
		t.Fatalf("unversioned format 2 database: %+v %v", found, err)
	}
	if from, err := Format.Update(ctx, current, path, filepath.Join(t.TempDir(), "backup.db"), nil); err != nil || from != 2 {
		t.Fatalf("update from %d: %v", from, err)
	}
	if found, err := Format.Inspect(ctx, current); err != nil || found.Version != Format.Current || found.Pending(Format) {
		t.Fatalf("after update: %+v %v", found, err)
	}

	old, oldPath := openFormatFixture(t, `CREATE TABLE sessions (id TEXT PRIMARY KEY); CREATE TABLE messages (id INTEGER PRIMARY KEY)`)
	if found, err := Format.Inspect(ctx, old); err != nil || found.Version != 0 || found.Pending(Format) {
		t.Fatalf("pre-v0.7.3 database: %+v %v", found, err)
	}
	if _, err := Format.Update(ctx, old, oldPath, filepath.Join(t.TempDir(), "backup.db"), nil); !errors.Is(err, dataformat.ErrUnsupported) {
		t.Fatalf("pre-v0.7.3 database: %v", err)
	}

	newer, newerPath := openFormatFixture(t, schema+fmt.Sprintf(`PRAGMA user_version = %d;`, Format.Current+1))
	var refused *dataformat.ErrNewer
	if _, err := Format.Update(ctx, newer, newerPath, filepath.Join(t.TempDir(), "backup.db"), nil); !errors.As(err, &refused) {
		t.Fatalf("newer database: %v", err)
	}
	if version, _ := dataformat.Version(ctx, newer); version != Format.Current+1 {
		t.Fatalf("newer database changed to version %d", version)
	}
}

// Maintenance used to archive memories only for the size of the store or for
// their age. The update returns exactly those to recall, as new revisions.
func TestFormatReturnsMemoriesArchivedByRetiredMaintenance(t *testing.T) {
	ctx := t.Context()
	db, path := openFormatFixture(t, formatTwoSchema)
	store, err := memory.Attach(db)
	if err != nil {
		t.Fatal(err)
	}
	remember := func(title string) memory.Record {
		t.Helper()
		record, err := store.Remember(ctx, memory.Record{Kind: memory.KindPreference, Title: title, Content: title + " content."})
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	capped, decayed, other, changed, active := remember("Capped"), remember("Decayed"), remember("Other"), remember("Changed"), remember("Active")
	archive := func(record memory.Record, reason string) {
		t.Helper()
		if err := store.Archive(ctx, []memory.RevisionRef{{ID: record.ID, Revision: record.Revision}}, memory.ActorConsolidation, reason); err != nil {
			t.Fatal(err)
		}
	}
	archive(capped, "capped: active set over limit")
	archive(decayed, "decayed: no recent usefulness evidence")
	archive(other, "user archived it")
	archive(changed, "capped: active set over limit")
	if _, err := db.Exec(`INSERT INTO memory_journal(at, actor, op, memory_id, session_id, detail) VALUES (?, 'user', 'note', ?, '', 'looked at it')`, time.Now().UnixNano(), changed.ID); err != nil {
		t.Fatal(err)
	}
	if from, err := Format.Update(ctx, db, path, filepath.Join(t.TempDir(), "backup.db"), nil); err != nil || from != 2 {
		t.Fatalf("update from %d: %v", from, err)
	}
	status := func(record memory.Record) (string, int64) {
		t.Helper()
		got, err := store.Get(ctx, record.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Status, got.Revision
	}
	for _, check := range []struct {
		record memory.Record
		want   string
	}{{capped, memory.StatusActive}, {decayed, memory.StatusActive}, {other, memory.StatusArchived}, {changed, memory.StatusArchived}, {active, memory.StatusActive}} {
		if got, _ := status(check.record); got != check.want {
			t.Fatalf("%s is %s, want %s", check.record.Title, got, check.want)
		}
	}
	if _, revision := status(capped); revision != capped.Revision+2 {
		t.Fatalf("returned memory has revision %d", revision)
	}
	revisions, err := store.Revisions(ctx, capped.ID)
	if err != nil || len(revisions) != 3 || revisions[2].Status != memory.StatusActive || revisions[2].Op != "restore" {
		t.Fatalf("return is not a recorded revision: %+v %v", revisions, err)
	}
	recalled, err := store.Recall(ctx, memory.RecallQuery{Text: "Capped content"})
	if err != nil || len(recalled) == 0 || recalled[0].ID != capped.ID {
		t.Fatalf("returned memory does not recall: %+v %v", recalled, err)
	}
}

func TestAllReleasedSessionLayoutsKeepBaseCheckpoint(t *testing.T) {
	fixtures, err := filepath.Glob("testdata/release-*.sql")
	if err != nil || len(fixtures) != 3 {
		t.Fatal(fixtures, err)
	}
	for _, fixture := range fixtures {
		t.Run(filepath.Base(fixture), func(t *testing.T) {
			raw, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			db, path := openFormatFixture(t, string(raw))
			if err := CreateSessionRow(db, "s"); err != nil {
				t.Fatal(err)
			}
			const body = `[{"role":"user","content":"exact original request"}]`
			_, err = db.Exec(`INSERT INTO agent_recoveries(incident_id,session_id,agent_id,generation,state,pending_input_json,archive_json,active_context_json,context_owner_id,created_at,updated_at) VALUES('i','s','a',1,'needs_attention','{}',?,?,'s',1,1)`, body, body)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Format.Update(t.Context(), db, path, filepath.Join(t.TempDir(), "backup.db"), nil); err != nil {
				t.Fatal(err)
			}
			cp, found, err := LoadRecoveryIncident(db, "s", "a", "i")
			if err != nil || !found || len(cp.Archive) != 1 || len(cp.ActiveContext) != 1 || cp.Archive[0].ContentString() != "exact original request" {
				t.Fatal("lost pre-tail checkpoint", err)
			}
			fresh, _ := openFormatFixture(t, "")
			if err := Migrate(fresh); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(schemaShape(t, db), schemaShape(t, fresh)) {
				t.Fatal("updated schema differs from a fresh session")
			}
		})
	}
}
