package session

import (
	"math/big"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"crude/provider"
	"crude/sessiondb"
)

func TestAgentNameCatalogAndTierBoundaries(t *testing.T) {
	if len(agentFirstNames) < 512 || len(agentSurnames) < 512 {
		t.Fatalf("catalog sizes: %d given names, %d surnames", len(agentFirstNames), len(agentSurnames))
	}
	first64 := []string{
		"Jeff", "Tom", "Jake", "Ava", "Maya", "Leo", "Nora", "Sam", "Ella", "Noah", "Mia", "Owen", "Ivy", "Finn", "Zoe", "Liam",
		"Ada", "Max", "Ruby", "Eli", "Luna", "Theo", "Rose", "Alex", "Iris", "Jack", "Lily", "Ben", "Esme", "Arlo", "June", "Luke",
		"Clara", "Jude", "Nina", "Hugo", "Sara", "Miles", "Alice", "Oscar", "Grace", "Felix", "Emma", "Levi", "Lucy", "Ezra", "Cora", "Adam",
		"Wren", "Dean", "Jade", "Cole", "Tess", "Joel", "Hope", "Paul", "Vera", "Seth", "Ruth", "Neil", "Faye", "Eric", "Dawn", "Ryan",
	}
	for position, expected := range first64 {
		if actual := agentNameAt(big.NewInt(int64(position))); actual != expected {
			t.Fatalf("original position %d: %q, want %q", position, actual, expected)
		}
	}
	if actual := agentNameAt(big.NewInt(64)); actual == "Jeff 2" || strings.Contains(actual, " ") {
		t.Fatalf("first new given name: %q", actual)
	}

	validToken := regexp.MustCompile(`^[A-Za-z]+$`)
	for catalogName, catalog := range map[string][]string{"given": agentFirstNames, "surname": agentSurnames} {
		seen := make(map[string]bool, len(catalog))
		for _, entry := range catalog {
			if seen[entry] || !validToken.MatchString(entry) {
				t.Fatalf("invalid or duplicate %s catalog entry %q", catalogName, entry)
			}
			seen[entry] = true
		}
	}

	firstCount := int64(len(agentFirstNames))
	surnameCount := int64(len(agentSurnames))
	boundaries := []struct {
		position int64
		want     string
	}{
		{firstCount - 1, agentFirstNames[firstCount-1]},
		{firstCount, "Jeff " + agentSurnames[0]},
		{firstCount + 1, "Tom " + agentSurnames[0]},
		{firstCount + firstCount*surnameCount - 1, agentFirstNames[firstCount-1] + " " + agentSurnames[surnameCount-1]},
		{firstCount + firstCount*surnameCount, "Jeff " + agentSurnames[0] + " " + agentSurnames[0]},
		{firstCount + firstCount*surnameCount*(surnameCount+1) - 1, agentFirstNames[firstCount-1] + " " + agentSurnames[surnameCount-1] + " " + agentSurnames[surnameCount-1]},
		{firstCount + firstCount*surnameCount*(surnameCount+1), "Jeff " + agentSurnames[0] + " " + agentSurnames[0] + " " + agentSurnames[0]},
	}
	for _, boundary := range boundaries {
		if actual := agentNameAt(big.NewInt(boundary.position)); actual != boundary.want {
			t.Fatalf("tier boundary %d: %q, want %q", boundary.position, actual, boundary.want)
		}
	}
}

func TestAgentNamePoolUniqueAndUnlimited(t *testing.T) {
	seen := make(map[string]bool)
	for position := int64(0); position < int64(len(agentFirstNames))*(int64(len(agentSurnames))+1)+1024; position++ {
		name := agentNameAt(big.NewInt(position))
		if seen[name] || strings.ContainsAny(name, "0123456789-") {
			t.Fatalf("duplicate or numeric name at %d: %q", position, name)
		}
		seen[name] = true
	}

	huge := new(big.Int).Exp(big.NewInt(10), big.NewInt(200), nil)
	for _, offset := range []int64{-1, 0, 1, 527, 528, 529} {
		position := new(big.Int).Add(huge, big.NewInt(offset))
		name := agentNameAt(position)
		if seen[name] || strings.ContainsAny(name, "0123456789-") || len(strings.Fields(name)) < 3 {
			t.Fatalf("huge position %s generated invalid name %q", position, name)
		}
		seen[name] = true
	}
}

func TestAgentNameAllocatorIsDurableAndConcurrent(t *testing.T) {
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
	var waitGroup sync.WaitGroup
	for allocation := 0; allocation < 80; allocation++ {
		waitGroup.Go(func() {
			name, err := allocateAgentName(home)
			if err != nil {
				t.Error(err)
				return
			}
			names <- name
		})
	}
	waitGroup.Wait()
	close(names)
	seen := map[string]bool{first: true, second: true}
	for name := range names {
		if seen[name] {
			t.Fatalf("reused name %q", name)
		}
		seen[name] = true
	}
	if len(seen) != 82 || !seen[agentFirstNames[64]] || seen["Jeff 2"] {
		t.Fatalf("durable concurrent pool: %d names", len(seen))
	}
	reopened, err := allocateAgentName(home)
	if err != nil || reopened != agentNameAt(big.NewInt(82)) {
		t.Fatalf("reopened sequence: %q, err=%v", reopened, err)
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
