package session

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"crude/logger"
	"crude/provider"
	"crude/sessiondb"
)

// A fork continues the same conversation as a new saved session: its first
// request carries the source's history, it has its own identity and name, the
// source is untouched, and runtime-owned state (usage, queued input) stays
// with the source.
func TestForkContinuesConversationAsNewSession(t *testing.T) {
	requests := make(chan string, 8)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		n := calls.Add(1)
		requests <- string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"reply %d\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n", n)
	}))
	t.Cleanup(server.Close)
	home, dir := t.TempDir(), t.TempDir()
	cfg := mediaAttributionConfig(server.URL, "fork-model", "openai")
	source, err := NewWithSessionDir("", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.Close)
	source.SetWorkDir(t.TempDir())
	sender := newMockEventSender()
	o, err := source.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !o.SubmitInput(provider.PlainInput("remember the word fern")) {
		t.Fatal("submit rejected")
	}
	<-requests
	waitTurnDone(t, sender, o.ID)
	sourceBefore, _, err := sessiondb.LoadMessages(source.db)
	if err != nil {
		t.Fatal(err)
	}

	id := logger.NewSessionID()
	if err := source.Fork(dir, id); err != nil {
		t.Fatal(err)
	}
	fork, err := ResumeSessionWithDir(filepath.Join(dir, id+".db"), cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fork.Close)
	if fork.SessionID() != id || fork.AgentName() == "" || fork.AgentName() == source.AgentName() {
		t.Fatalf("fork identity = %q/%q, source %q/%q", fork.SessionID(), fork.AgentName(), source.SessionID(), source.AgentName())
	}
	var usage int
	if err := fork.db.QueryRow(`SELECT COUNT(*) FROM usage_requests`).Scan(&usage); err != nil || usage != 0 {
		t.Fatalf("fork inherited the source's billed usage: %d %v", usage, err)
	}
	fork.SetWorkDir(t.TempDir())
	forkSender := newMockEventSender()
	fo, err := fork.NewOrchestrator(forkSender)
	if err != nil {
		t.Fatal(err)
	}
	if !fo.SubmitInput(provider.PlainInput("which word?")) {
		t.Fatal("fork submit rejected")
	}
	body := <-requests
	waitTurnDone(t, forkSender, fo.ID)
	if !strings.Contains(body, "remember the word fern") || !strings.Contains(body, "reply 1") || !strings.Contains(body, "which word?") {
		t.Fatalf("fork request lost the source conversation: %s", body)
	}
	sourceAfter, _, err := sessiondb.LoadMessages(source.db)
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceAfter) != len(sourceBefore) {
		t.Fatalf("fork changed the source transcript: %d -> %d messages", len(sourceBefore), len(sourceAfter))
	}
}

// Nothing mid-flight may be duplicated: a fork is refused while a turn runs.
func TestForkRefusesWhileTurnRuns(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	home, dir := t.TempDir(), t.TempDir()
	source, err := NewWithSessionDir("", mediaAttributionConfig(server.URL, "fork-model", "openai"), home, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.Close)
	o, err := source.NewOrchestrator(newMockEventSender())
	if err != nil {
		t.Fatal(err)
	}
	if !o.SubmitInput(provider.PlainInput("work")) {
		t.Fatal("submit rejected")
	}
	<-started
	id := logger.NewSessionID()
	if err := source.Fork(dir, id); err == nil || !strings.Contains(err.Error(), "stop the running turn") {
		t.Fatalf("fork during a turn = %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, id+".db*")); len(matches) != 0 {
		t.Fatalf("refused fork left files: %v", matches)
	}
}
