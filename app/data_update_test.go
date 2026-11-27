package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/agent/sessiondb"
	"crude/core"
	"crude/core/coreclient"
	"crude/core/coreprotocol"
	"crude/internal/dataformat"
	"crude/workspace/localhistory"
)

func fixtureSQL(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeDatabase(t *testing.T, path, ddl string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
}

func databaseVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := openDataFile(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	version, err := dataformat.Version(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

// oldHome is a data folder as Crude v0.8.15 to v0.8.17 left it: format 1
// sessions with unfinished-run messages, a format 1 Local History store, a
// session older than v0.7.3, and an unrelated browser cache database.
func oldHome(t *testing.T) (string, map[string]string) {
	t.Helper()
	dataDir := t.TempDir()
	sessionV1 := fixtureSQL(t, filepath.Join("..", "agent", "sessiondb", "testdata", "format-1.sql"))
	historyV1 := fixtureSQL(t, filepath.Join("..", "workspace", "localhistory", "testdata", "format-1.sql"))
	files := map[string]string{
		"session":   filepath.Join(dataDir, "workspaces", "ws", "sessions", "s.db"),
		"workspace": filepath.Join(dataDir, "workspaces", "ws", "workspace.db"),
		"deleted":   filepath.Join(dataDir, "deleted-workspaces", "old-1", "data", "sessions", "d.db"),
		"history":   filepath.Join(dataDir, "workspaces", "ws", "local-history", "history.db"),
		"legacy":    filepath.Join(dataDir, "sessions", "legacy.db"),
		"cache":     filepath.Join(dataDir, "workspaces", "ws", "sessions", "s", "tmp", "agent", "browser", "cache.db"),
	}
	writeDatabase(t, files["session"], sessionV1+`
		INSERT INTO sessions(id, project_path, project_name, created_at) VALUES ('s', '/p', 'p', 1);
		INSERT INTO agent_recoveries(incident_id, session_id, agent_id, generation, state, pending_input_json, archive_json, active_context_json, context_owner_id, created_at, updated_at)
		VALUES ('i', 's', 'agent', 1, 'needs_attention', '{}', '[]', '[]', 'session', 1, 1);
		INSERT INTO agent_recovery_lists(incident_id, field, version) VALUES ('i', 'archive', 3);
		INSERT INTO agent_recovery_tail(incident_id, field, position, message_json) VALUES ('i', 'archive', 0, '{"role":"user","content":"keep me"}');`)
	writeDatabase(t, files["workspace"], sessionV1)
	writeDatabase(t, files["deleted"], sessionV1)
	m := localhistory.Manifest{Version: 1, CreatedAt: 1, Kind: "checkpoint", Entries: map[string]localhistory.Entry{"main.go": {Type: localhistory.EntryFile, Mode: 420}}}
	payload, _ := json.Marshal(m)
	m.ID = fmt.Sprintf("%x", sha256.Sum256(payload))
	payload, _ = json.Marshal(m)
	dir := filepath.Join(filepath.Dir(files["history"]), "manifests")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, m.ID+".json"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	writeDatabase(t, files["history"], historyV1+strings.ReplaceAll(`
		INSERT INTO history_snapshots(snapshot_id, created_at, kind) VALUES ('snap', 1, 'checkpoint');
		INSERT INTO history_snapshot_entries(snapshot_id, path, entry_type, mode) VALUES ('snap', 'main.go', 'file', 420);`, "'snap'", "'"+m.ID+"'"))
	writeDatabase(t, files["legacy"], fixtureSQL(t, filepath.Join("..", "agent", "sessiondb", "testdata", "release-v0.1.1.sql")))
	writeDatabase(t, files["cache"], `CREATE TABLE cache (key TEXT)`)
	return dataDir, files
}

type recordedLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *recordedLog) log(level, msg string, pairs ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprint(level, " ", msg, pairs))
}

// A shell that connects while the core updates sees every phase over the real
// endpoint, can ask for a retry after it freed space, and the core then has
// current data only. A second start has nothing to do and shows nothing.
func TestDataUpdateUpdatesAnOldHomeAndShowsItsProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	dataDir, files := oldHome(t)
	listener, err := listenCore(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := core.NewHandoffListener(listener)
	defer endpoint.Close()
	server := core.NewDataUpdateServer("test-owner")
	serving, stopServing := context.WithCancel(ctx)
	defer stopServing()
	served := make(chan error, 1)
	go func() { served <- server.Serve(serving, endpoint) }()
	var free sync.Mutex
	freeBytes := int64(0)
	logs := &recordedLog{}
	runner := &dataUpdateRunner{dataDir: dataDir, stores: dataStores(), server: server, log: logs.log, grace: time.Second,
		freeSpace: func(context.Context, string) (int64, error) { free.Lock(); defer free.Unlock(); return freeBytes, nil }}
	result := make(chan error, 1)
	var final coreprotocol.DataUpdate
	go func() {
		var err error
		final, err = runner.run(ctx)
		result <- err
	}()

	carrier := localCarrier(dataDir)
	var phases []coreprotocol.DataUpdatePhase
	var last coreprotocol.DataUpdate
	retried := false
	err = coreclient.WatchDataUpdate(ctx, carrier, "shell-watch", false, func(u coreprotocol.DataUpdate) {
		if len(phases) == 0 || phases[len(phases)-1] != u.Phase {
			phases = append(phases, u.Phase)
		}
		last = u
		if u.Phase == coreprotocol.DataUpdateNoSpace && !retried {
			retried = true
			if u.Needed <= 0 || u.Free != 0 {
				t.Errorf("no space status %+v", u)
			}
			free.Lock()
			freeBytes = 1 << 40
			free.Unlock()
			go func() {
				retry, done := context.WithTimeout(ctx, 10*time.Second)
				defer done()
				_ = coreclient.WatchDataUpdate(retry, carrier, "shell-retry", true, func(coreprotocol.DataUpdate) { done() })
			}()
		}
	})
	if err != nil {
		t.Fatalf("watch: %v (phases %v)", err, phases)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	stopServing()
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	// Progress coalesces, so a fast update can go from one phase to the
	// final status; the order of the phases a watcher sees never changes.
	seen := strings.Join(func() []string {
		out := make([]string, len(phases))
		for i, p := range phases {
			out[i] = string(p)
		}
		return out
	}(), ",")
	if seen != "checking,no_space,updating,finished" && seen != "checking,no_space,finished" {
		t.Fatalf("phases %s", seen)
	}
	if !last.Final || last.Files != 5 || len(last.Problems) != 0 || last.Done != last.Total || last.Backup == "" || last.Quiet() {
		t.Fatalf("final status %+v", last)
	}
	if fmt.Sprint(final.Stores) != fmt.Sprint(last.Stores) {
		t.Fatalf("watcher and core disagree: %+v %+v", final.Stores, last.Stores)
	}
	for name, want := range map[string]int{"session": sessiondb.Format.Current, "workspace": sessiondb.Format.Current, "deleted": sessiondb.Format.Current, "history": localhistory.Format.Current, "legacy": sessiondb.Format.Current, "cache": 0} {
		if got := databaseVersion(t, files[name]); got != want {
			t.Errorf("%s has format %d, want %d", name, got, want)
		}
	}
	db, err := sessiondb.OpenAndMigrate(files["session"])
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, found, err := sessiondb.LoadRecoveryIncident(db, "s", "agent", "i")
	_ = db.Close()
	if err != nil || !found || len(checkpoint.Archive) != 1 || checkpoint.Archive[0].Content.String() != "keep me" {
		t.Fatalf("unfinished run lost its message: %+v %v %v", checkpoint.Archive, found, err)
	}
	// Restore the backup independently. SQLite can change header counters
	// in a backup; the rows and the old format are the recovery contract.
	backup, err := openDataFile(filepath.Join(last.Backup, "workspaces", "ws", "sessions", "s.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var body string
	if err := backup.QueryRow(`SELECT message_json FROM agent_recovery_tail WHERE incident_id='i' AND field='archive' AND position=0`).Scan(&body); err != nil || body != `{"role":"user","content":"keep me"}` {
		t.Fatalf("backup lost the recovery message: %s %v", body, err)
	}

	// The next start finds current data and changes nothing.
	again := &dataUpdateRunner{dataDir: dataDir, stores: dataStores(), server: core.NewDataUpdateServer("test-owner"), log: logs.log, grace: time.Second,
		freeSpace: func(context.Context, string) (int64, error) { return 1 << 40, nil }}
	status, err := again.run(ctx)
	if err != nil || !status.Quiet() || status.Backup != "" {
		t.Fatalf("second start: %+v %v", status, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dataDir, "backups"))
	if len(entries) != 1 {
		t.Fatalf("second start made another backup: %d", len(entries))
	}
}

// One database that cannot be updated is reported and left exactly as it
// was; the others are updated and Crude still starts.
func TestDataUpdateRefusesToServeAfterAFailedStep(t *testing.T) {
	dataDir, files := oldHome(t)
	broken := filepath.Join(dataDir, "workspaces", "ws", "sessions", "broken.db")
	writeDatabase(t, broken, fixtureSQL(t, filepath.Join("..", "agent", "sessiondb", "testdata", "format-1.sql"))+`
		PRAGMA foreign_keys=OFF;
		INSERT INTO agent_recovery_tail(incident_id, field, position, message_json) VALUES ('missing', 'archive', 0, '{}');`)
	runner := &dataUpdateRunner{dataDir: dataDir, stores: dataStores(), server: core.NewDataUpdateServer("test-owner"), log: (&recordedLog{}).log, grace: time.Second,
		freeSpace: func(context.Context, string) (int64, error) { return 1 << 40, nil }}
	status, err := runner.run(t.Context())
	if err == nil || status.Phase != coreprotocol.DataUpdateBlocked {
		t.Fatalf("failed update served sessions: %+v %v", status, err)
	}
	if len(status.Problems) != 1 || status.Problems[0].Path != "workspaces/ws/sessions/broken.db" || !strings.Contains(status.Problems[0].Detail, "belong to no recovery incident") {
		t.Fatalf("problems %+v", status.Problems)
	}
	if got := databaseVersion(t, broken); got != 0 {
		t.Fatalf("the broken database changed to format %d", got)
	}
	if got := databaseVersion(t, files["session"]); got != 0 {
		t.Fatalf("a later store changed after failure: %d", got)
	}
}

// Data from a newer Crude stops the start before anything changes.
func TestDataUpdateRefusesDataFromANewerCrude(t *testing.T) {
	dataDir, files := oldHome(t)
	newer := filepath.Join(dataDir, "workspaces", "other", "workspace.db")
	writeDatabase(t, newer, fmt.Sprintf(`CREATE TABLE sessions (id TEXT); PRAGMA user_version = %d;`, sessiondb.Format.Current+1))
	runner := &dataUpdateRunner{dataDir: dataDir, stores: dataStores(), server: core.NewDataUpdateServer("test-owner"), log: (&recordedLog{}).log, grace: 0,
		freeSpace: func(context.Context, string) (int64, error) { return 1 << 40, nil }}
	status, err := runner.run(t.Context())
	if !errors.Is(err, errDataNewer) || status.Phase != coreprotocol.DataUpdateNewer || !status.Final || len(status.Newer) != 1 || status.Newer[0].Path != "workspaces/other/workspace.db" {
		t.Fatalf("newer data: %+v %v", status, err)
	}
	if got := databaseVersion(t, files["session"]); got != 0 {
		t.Fatalf("an old database changed beside newer data: %d", got)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "backups")); !os.IsNotExist(err) {
		t.Fatalf("a refused update made backups: %v", err)
	}
}

func TestDataUpdateRefusesUnknownLayoutBeforeWriting(t *testing.T) {
	dir, files := oldHome(t)
	bad := filepath.Join(dir, "workspaces", "ws", "sessions", "unknown.db")
	writeDatabase(t, bad, `CREATE TABLE agent_message_lists(id);CREATE TABLE evidence(v);INSERT INTO evidence VALUES('keep')`)
	runner := &dataUpdateRunner{dataDir: dir, stores: dataStores(), server: core.NewDataUpdateServer("test"), log: (&recordedLog{}).log}
	s, err := runner.run(t.Context())
	if err == nil || s.Phase != coreprotocol.DataUpdateBlocked || len(s.Problems) != 1 || !strings.HasSuffix(s.Problems[0].Path, "unknown.db") {
		t.Fatalf("unrecognized database not reported: %+v %v", s, err)
	}
	for _, p := range files {
		if databaseVersion(t, p) != 0 {
			t.Fatal("changed another store", p)
		}
	}
}

// A real detached core updates first, accepts a client on the same endpoint,
// and keeps a fast update's result for the first shell that asks for it.
func TestDetachedCoreUpdatesBeforeAttachment(t *testing.T) {
	home := coreTestHome(t)
	root := filepath.Join(home, "project")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(DataDir(home), "workspaces", "fixture", "sessions", "s.db")
	writeDatabase(t, path, fixtureSQL(t, filepath.Join("..", "agent", "sessiondb", "testdata", "release-v0.1.1.sql")))
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	client, err := ConnectCore(ctx, home, core.NewClientID())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if databaseVersion(t, path) != sessiondb.Format.Current {
		t.Fatal("session admitted before upgrade")
	}
	for _, expect := range []struct{ claim, shown bool }{{false, false}, {true, true}, {true, false}} {
		var out coreprotocol.StartupSnapshot
		if err := client.Call(ctx, "", coreprotocol.StartupInspect, &coreprotocol.StartupInspectRequest{Root: root, Explicit: true, TakeDataUpdate: expect.claim}, &out); err != nil {
			t.Fatal(err)
		}
		if (out.DataUpdate != nil) != expect.shown {
			t.Fatalf("claim=%v, report=%+v", expect.claim, out.DataUpdate)
		}
		if expect.shown && (!out.DataUpdate.Final || out.DataUpdate.Done != out.DataUpdate.Total || out.DataUpdate.Files != 1) {
			t.Fatal("incomplete update report", out.DataUpdate)
		}
	}
}
