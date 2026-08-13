package session

import (
	"context"
	"strings"
	"testing"
)

type memoryStoreStub struct {
	records []MemoryRecord
}

func (s *memoryStoreStub) RememberMemory(_ context.Context, record MemoryRecord) (MemoryRecord, error) {
	record.ID = "mem-1"
	s.records = append(s.records, record)
	return record, nil
}
func (s *memoryStoreStub) RecallMemories(_ context.Context, _ string, _ int) ([]MemoryRecord, error) {
	return append([]MemoryRecord(nil), s.records...), nil
}
func (s *memoryStoreStub) ForgetMemory(_ context.Context, id string) error {
	for index, record := range s.records {
		if record.ID == id {
			s.records = append(s.records[:index], s.records[index+1:]...)
			break
		}
	}
	return nil
}

func TestMemoryToolsAndAutomaticRecall(t *testing.T) {
	store := &memoryStoreStub{}
	s := &Session{sessionID: "session-1", memoryStore: store}
	result, handled, err := s.executeMemoryTool(context.Background(), "remember", map[string]any{
		"title": "Testing", "content": "Use race tests.", "kind": "convention",
	})
	if err != nil || !handled || result.Content == "" {
		t.Fatalf("remember result=%+v handled=%v err=%v", result, handled, err)
	}
	if len(store.records) != 1 || store.records[0].SourceSessionID != "session-1" {
		t.Fatalf("stored memories = %+v", store.records)
	}
	contextText := s.recallMemoryContext(context.Background(), "testing")
	if contextText == "" || !containsAll(contextText, "Workspace Memory", "Use race tests.") {
		t.Fatalf("memory context = %q", contextText)
	}
}

func containsAll(value string, wants ...string) bool {
	for _, want := range wants {
		if !strings.Contains(value, want) {
			return false
		}
	}
	return true
}
