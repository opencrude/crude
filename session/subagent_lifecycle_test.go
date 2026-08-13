package session

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"crude/agent"
	"crude/provider"
	"crude/sessiondb"
)

func TestSubAgentCompletionWakeWaitsForQueueCapacity(t *testing.T) {
	wakeCh := make(chan WakeEvent, 1)
	wakeCh <- WakeEvent{Type: WakeSubAgentDone, SourceID: "existing"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &SubAgentRunner{wakeCh: wakeCh, sessionCtx: ctx}
	delivered := make(chan bool, 1)
	go func() {
		delivered <- runner.deliverCompletionWake(agent.SubAgentResult{
			AgentID: "new-result", Status: agent.SubAgentCompleted,
		})
	}()

	select {
	case <-delivered:
		t.Fatal("completion wake was dropped instead of applying backpressure")
	case <-time.After(20 * time.Millisecond):
	}
	<-wakeCh
	select {
	case ok := <-delivered:
		if !ok {
			t.Fatal("completion wake was not delivered")
		}
	case <-time.After(time.Second):
		t.Fatal("completion wake did not unblock after queue capacity became available")
	}
	event := <-wakeCh
	if event.SourceID != "new-result" {
		t.Fatalf("wake event = %+v", event)
	}
}

func TestSubAgentTranscriptIsReplayable(t *testing.T) {
	db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "child.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	defer db.Close()
	if err := sessiondb.Create(db, sessiondb.SessionMeta{
		ID: "child", ProjectPath: "/project", ProjectName: "project",
		CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	handle := &agent.SubAgentHandle{DB: db, SessionID: "child"}
	if err := persistSubAgentTranscript(handle, []provider.ChatMessage{
		provider.NewTextMessage("system", "system"),
		provider.NewTextMessage("user", "inspect lifecycle"),
		provider.NewTextMessage("assistant", "found the bug"),
	}); err != nil {
		t.Fatalf("persistSubAgentTranscript: %v", err)
	}
	messages, _, err := sessiondb.LoadMessages(db)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(messages) != 2 || messages[0].ContentString() != "inspect lifecycle" ||
		messages[1].ContentString() != "found the bug" {
		t.Fatalf("messages = %+v", messages)
	}
}
