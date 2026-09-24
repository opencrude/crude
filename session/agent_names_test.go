package session

import (
	"math/big"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"crude/provider"
	"crude/sessiondb"
)

func TestAgentNameAllocatorIsDurableConcurrentAndUnbounded(t *testing.T) {
	home := t.TempDir()
	first, err := allocateAgentName(home)
	if err != nil || first != "Jeff" {
		t.Fatalf("first=%q err=%v", first, err)
	}
	second, err := allocateAgentName(home)
	if err != nil || second != "Tom" {
		t.Fatalf("reopened allocator=%q err=%v", second, err)
	}
	names := make(chan string, 80)
	var wg sync.WaitGroup
	for i := 0; i < 80; i++ {
		wg.Go(func() {
			name, err := allocateAgentName(home)
			if err != nil {
				t.Error(err)
				return
			}
			names <- name
		})
	}
	wg.Wait()
	close(names)
	seen := map[string]bool{first: true, second: true}
	for name := range names {
		if seen[name] {
			t.Fatalf("reused name %q", name)
		}
		seen[name] = true
	}
	if len(seen) != 82 || !seen["Jeff 2"] {
		t.Fatalf("pool did not extend uniquely: %d names", len(seen))
	}
	huge, _ := new(big.Int).SetString("999999999999999999999999999999999999999999999999", 10)
	a := agentNameAt(huge)
	b := agentNameAt(new(big.Int).Add(huge, big.NewInt(1)))
	if a == b || !strings.Contains(a, " ") {
		t.Fatalf("large sequence exhausted: %q %q", a, b)
	}
}

func TestRootAgentNamePersistsAcrossResumeAndWorkspaceSessions(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	s, err := NewWithSessionDir("", testConfig(), home, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	name := s.AgentName()
	if name != "Jeff" || !strings.Contains(s.systemPrompt(true), "Your name is Jeff") {
		t.Fatalf("root identity=%q", name)
	}
	meta, err := sessiondb.GetMeta(s.DB())
	if err != nil || meta.AgentName.String != name {
		t.Fatalf("persisted identity=%+v err=%v", meta, err)
	}
	// Empty sessions are intentionally pruned on close; seed a real durable
	// conversation before exercising resume identity.
	s.msgs = []provider.ChatMessage{provider.NewTextMessage("user", "keep this session")}
	if _, err := sessiondb.ReplaceMessagesWithMeta(s.DB(), s.SessionID(), s.msgs, []int{1}, []sessiondb.DisplayMeta{{DisplayRole: "user"}}); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(directory, s.SessionID()+".db")
	s.Close()
	resumed, err := ResumeSessionWithDir(dbPath, testConfig(), home, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if resumed.AgentName() != name {
		t.Fatalf("resume changed name=%q", resumed.AgentName())
	}
	other, err := NewWithSessionDir("", testConfig(), home, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if other.AgentName() != "Tom" {
		t.Fatalf("other workspace reused identity: %q", other.AgentName())
	}
}
