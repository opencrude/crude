package session

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
)

// A turn that is still open when Crude exits after an earlier resume must not
// make the session unopenable. Resume once rebuilt history from the lossy
// transcript projection, so the next recovery checkpoint dropped provider replay
// facts and conflicted with the immutable identities on the following restart.
func TestResumedSessionReopensAfterInterruptedTurn(t *testing.T) {
	var calls atomic.Int32
	arrived, release := make(chan struct{}, 4), make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 2 {
			arrived <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"accepted answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	home, directory, project := t.TempDir(), t.TempDir(), t.TempDir()
	cfg := modelAttributionConfig(server.URL, "resume-interrupted-turn")
	submit := func(s *Session, o *Orchestrator, input provider.UserInput) {
		t.Helper()
		intent, err := s.PrepareInputAdmission(o.ID, input, 1)
		if err != nil {
			t.Fatal(err)
		}
		if result := s.CommitInputAdmission(t.Context(), intent); result.Status != InputAdmissionAccepted {
			t.Fatalf("input was not accepted: %+v", result)
		}
	}

	s, err := NewWithSessionDir("resume-interrupted-turn", cfg, home, directory)
	if err != nil {
		t.Fatal(err)
	}
	s.SetWorkDir(project)
	sender := newMockEventSender()
	o, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	submit(s, o, provider.UserInput{InternalID: "first-input", Text: "first"})
	waitControlJourneyTurn(t, sender, o, "completed")
	path := s.dbPath
	s.Close()

	resumed, err := ResumeSessionWithDir(path, cfg, home, directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resumed.Close)
	resumed.SetWorkDir(project)
	var answer provider.ChatMessage
	for _, message := range resumed.msgs {
		if message.Role == "assistant" {
			answer = message
		}
	}
	if answer.InternalID == "" || answer.ReplayScope == "" || answer.ReplaySourceModel == "" {
		t.Fatalf("resume rebuilt the accepted answer without its provider replay facts: %+v", answer)
	}
	o, err = resumed.NewOrchestrator(newMockEventSender())
	if err != nil {
		t.Fatal(err)
	}
	submit(resumed, o, provider.UserInput{InternalID: "second-input", Text: "second"})
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("interrupted turn request did not arrive")
	}
	// Capture the committed state while the turn is open, as a crash or forced
	// exit leaves it, then reopen that image.
	crashed := filepath.Join(t.TempDir(), "crashed.db")
	if _, err := resumed.db.Exec(`VACUUM INTO ?`, crashed); err != nil {
		t.Fatal(err)
	}
	if _, open, err := sessiondb.LoadRecoveryCheckpoint(resumed.db, resumed.sessionID, o.ID); err != nil || !open {
		t.Fatalf("interrupted turn has no open recovery checkpoint: open=%v err=%v", open, err)
	}
	reopened, err := ResumeSessionWithDir(crashed, cfg, home, directory)
	if err != nil {
		t.Fatalf("session could not be opened after interrupted turn: %v", err)
	}
	defer reopened.Close()
	rows, err := sessiondb.LoadMessagesWithMeta(reopened.db)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, row := range rows {
		ids = append(ids, row.Msg.InternalID)
	}
	if len(ids) < 3 || ids[0] != "first-input" || ids[1] != answer.InternalID || !slices.Contains(ids[2:], "second-input") {
		t.Fatalf("reopened transcript lost accepted history: %v", ids)
	}
}
