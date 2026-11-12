package memory

import (
	"context"
	"errors"
	"testing"
)

func TestDeleteRemovesTheMemoryWithItsHistoryAndSearchEntries(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	kept, err := st.Remember(ctx, Record{Kind: KindFact, Title: "Kept note", Content: "Gamma deployment uses blue slots"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.Remember(ctx, Record{Kind: KindFact, Title: "Private note", Content: "Alpha secret phrase zebra"})
	if err != nil {
		t.Fatal(err)
	}
	first.Content = "Alpha secret phrase zebra, corrected"
	second, err := st.Update(ctx, first.ID, first, ActorUser, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Expose(ctx, []RevisionRef{{ID: second.ID, Revision: second.Revision}}, "session-1", "recall"); err != nil {
		t.Fatal(err)
	}

	if _, err := st.db.ExecContext(ctx, `DELETE FROM memory_revisions WHERE memory_id=?`, kept.ID); err == nil {
		t.Fatal("history of an existing memory must stay immutable")
	}
	if err := st.Delete(ctx, second.ID, first.Revision); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("a revision the user has not seen must not be deleted: %v", err)
	}
	if err := st.Delete(ctx, second.ID, second.Revision); err != nil {
		t.Fatal(err)
	}

	for table, column := range map[string]string{"workspace_memories": "id", "memory_revisions": "memory_id", "memory_exposures": "memory_id", "memory_feedback": "memory_id", "memory_journal": "memory_id", "workspace_memories_fts": "id"} {
		var count int
		if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE `+column+`=?`, second.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s keeps %d rows for the deleted memory (%v)", table, count, err)
		}
	}
	found, err := st.List(ctx, ListQuery{Text: "zebra", Status: "all"})
	if err != nil || len(found) != 0 {
		t.Fatalf("search still finds the deleted memory: %+v %v", found, err)
	}
	if record, err := st.Get(ctx, kept.ID); err != nil || record.Content != kept.Content {
		t.Fatalf("another memory changed: %+v %v", record, err)
	}
	if err := st.Delete(ctx, second.ID, second.Revision); err != nil {
		t.Fatalf("deleting a removed memory again must succeed: %v", err)
	}
}

func TestDeleteEpisodeRemovesItCompletely(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := st.db.ExecContext(ctx, `INSERT INTO memory_episodes(id, session_id, turn, episode, created_at) VALUES ('ep-1', 's', 1, '{"ID":"ep-1"}', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteEpisode(ctx, "ep-1"); err != nil {
		t.Fatal(err)
	}
	if episodes, err := st.Episodes(ctx, "", 10); err != nil || len(episodes) != 0 {
		t.Fatalf("deleted episode still listed: %+v %v", episodes, err)
	}
}
