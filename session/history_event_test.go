package session

import (
	"testing"

	"crude/provider"
)

func TestWorkspaceHistoryEventAppendsWithoutMutatingPrefix(t *testing.T) {
	original := provider.NewTextMessage("user", "original")
	s := &Session{sessionID: "s", msgs: []provider.ChatMessage{original}, activeContext: []provider.ChatMessage{original}, msgTurns: []int{1}, turnNumber: 1}
	if err := s.AppendWorkspaceHistoryEvent("<workspace_history_event>restored</workspace_history_event>", "Restored one file"); err != nil {
		t.Fatal(err)
	}
	if len(s.msgs) != 2 || len(s.activeContext) != 2 || s.msgs[0].Content.String() != "original" || s.activeContext[0].Content.String() != "original" {
		t.Fatalf("archive=%+v context=%+v", s.msgs, s.activeContext)
	}
	if s.displayMetas[0].ContextOnly != true || len(s.displayMetas[0].Snapshots) != 1 {
		t.Fatalf("meta=%+v", s.displayMetas)
	}
}
