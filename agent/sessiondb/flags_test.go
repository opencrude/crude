package sessiondb

import "testing"

func TestSessionFlagsAreScopedAndAtomic(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	for _, id := range []string{"one", "two"} {
		if err := CreateSessionRow(db, id); err != nil {
			t.Fatal(err)
		}
	}
	on, off := true, false
	if err := SaveSessionFlags(db, "one", SessionFlags{TurnLogs: &on}); err != nil {
		t.Fatal(err)
	}
	if err := SaveSessionFlags(db, "two", SessionFlags{TurnLogs: &off}); err != nil {
		t.Fatal(err)
	}
	if err := ClearSessionFlags(db, "two"); err != nil {
		t.Fatal(err)
	}
	one, err := LoadSessionFlags(db, "one")
	if err != nil || one.TurnLogs == nil || !*one.TurnLogs {
		t.Fatal("another session reset changed logging", err)
	}
	two, err := LoadSessionFlags(db, "two")
	if err != nil || two.TurnLogs != nil {
		t.Fatal("reset kept logging", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_flags BEFORE INSERT ON session_flags BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := SaveSessionFlags(db, "one", SessionFlags{MultiAgent: &off, TurnLogs: &off}); err == nil {
		t.Fatal("fixture must reject")
	}
	one, err = LoadSessionFlags(db, "one")
	if err != nil || one.TurnLogs == nil || !*one.TurnLogs || one.MultiAgent != nil {
		t.Fatal("failed flags transaction partly changed logging", err)
	}
}
