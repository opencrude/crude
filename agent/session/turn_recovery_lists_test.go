package session

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"crude/agent/provider"
	"crude/internal/providersim"
)

// Each human message starts a new recovery incident whose archive is the whole
// history. Sending must not write that history again: dense sessions spent
// over a second in this commit before the composer was released. Every turn
// after the first writes only the messages it adds, so the rows written per
// turn stay constant while the history grows.
func TestNewTurnsWriteOnlyTheirOwnMessagesToRecoveryLists(t *testing.T) {
	server := providersim.New(t)
	server.Respond(func(providersim.Request) providersim.Turn {
		return providersim.Text("accepted answer").WithoutUsage()
	})
	home := t.TempDir()
	s, err := NewWithSessionDir("", modelAttributionConfig(server.BaseURL(providersim.Chat), "turn-lists-model"), home, filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`CREATE TABLE written_rows(list_id INTEGER);
		CREATE TRIGGER count_written_rows AFTER INSERT ON agent_message_list_rows BEGIN INSERT INTO written_rows VALUES(NEW.list_id); END`); err != nil {
		t.Fatal(err)
	}
	written := func() int {
		t.Helper()
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM written_rows`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`DELETE FROM written_rows`); err != nil {
			t.Fatal(err)
		}
		return n
	}
	sender := newMockEventSender()
	o, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	const turns = 8
	perTurn := make([]int, 0, turns)
	for turn := 1; turn <= turns; turn++ {
		intent, err := s.PrepareInputAdmission(o.ID, provider.PlainInput(fmt.Sprintf("request %d", turn)), uint64(turn))
		if err != nil {
			t.Fatal(err)
		}
		if result := s.CommitInputAdmission(context.Background(), intent); !result.Accepted() {
			t.Fatalf("turn %d was not admitted: %+v", turn, result)
		}
		waitTurnDone(t, sender, o.ID)
		perTurn = append(perTurn, written())
	}
	s.msgsMu.Lock()
	history := len(s.msgs)
	s.msgsMu.Unlock()
	for turn := 2; turn < turns; turn++ {
		if perTurn[turn] != perTurn[1] || perTurn[turn] >= history {
			t.Fatalf("rows written per turn %v grow with the %d-message history; a turn must write only what it adds", perTurn, history)
		}
	}
	var stored, messages int
	if err := s.db.QueryRow(`SELECT count(*) FROM agent_message_list_rows`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM agent_message_lists`).Scan(&messages); err != nil || messages != 2 {
		t.Fatalf("turns kept %d lists (err %v), want one archive and one context list", messages, err)
	}
	if stored > 2*history {
		t.Fatalf("lists hold %d rows for a %d-message history: an old copy was kept", stored, history)
	}

	// After a restart the session is resumed from its transcript and the
	// stored lists are unknown to the new process. The context list is
	// compared with the resumed context and only extended. The resumed
	// archive is the transcript, without the hidden context messages the live
	// archive held, so it may be stored once as a new list; from the next turn
	// on, turns write only what they add again.
	path, sessionsDir := s.dbPath, filepath.Join(home, "sessions")
	s.Close()
	resumed, err := ResumeSessionWithDir(path, modelAttributionConfig(server.BaseURL(providersim.Chat), "turn-lists-model"), home, sessionsDir)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	s = resumed
	sender = newMockEventSender()
	previous := o.ID
	if o, err = resumed.NewOrchestrator(sender); err != nil {
		t.Fatal(err)
	}
	if o.ID != previous {
		t.Fatalf("resumed orchestrator %s, was %s: lists are owned by a stable agent", o.ID, previous)
	}
	written()
	for turn := turns + 1; turn <= turns+2; turn++ {
		resumed.msgsMu.Lock()
		archive, active := len(resumed.msgs), len(resumed.activeContext)
		resumed.msgsMu.Unlock()
		intent, err := resumed.PrepareInputAdmission(o.ID, provider.PlainInput(fmt.Sprintf("request %d after restart", turn)), uint64(turn))
		if err != nil {
			t.Fatal(err)
		}
		if result := resumed.CommitInputAdmission(context.Background(), intent); !result.Accepted() {
			t.Fatalf("turn %d after restart was not admitted: %+v", turn, result)
		}
		waitTurnDone(t, sender, o.ID)
		got := written()
		if turn == turns+1 && got > archive+perTurn[1] {
			t.Fatalf("the first turn after a restart wrote %d rows; the %d-message context must not be written again (archive %d, a turn adds %d)", got, active, archive, perTurn[1])
		}
		if turn == turns+2 && got != perTurn[1] {
			t.Fatalf("the second turn after a restart wrote %d rows, want %d", got, perTurn[1])
		}
	}
}
