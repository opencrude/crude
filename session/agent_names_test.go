package session

import (
	"database/sql"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestAgentNameAllocatorIsDurableConcurrentAndShuffled(t *testing.T) {
	home := t.TempDir()
	first, err := allocateAgentName(home)
	if err != nil || first == "" {
		t.Fatalf("first=%q err=%v", first, err)
	}
	second, err := allocateAgentName(home)
	if err != nil || second == "" || second == first {
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
	inOrder := 0
	for name := range names {
		if seen[name] {
			t.Fatalf("reused name %q", name)
		}
		seen[name] = true
	}
	for position := 0; position < 82; position++ {
		if seen[agentNameAt(big.NewInt(int64(position)))] {
			inOrder++
		}
	}
	if len(seen) != 82 {
		t.Fatalf("durable concurrent pool: %d names", len(seen))
	}
	// The single-name tier is a permutation, so all 82 are given names; they
	// must not simply be the first 82 catalog entries.
	if inOrder == 82 {
		t.Fatal("allocation walks the catalog in order instead of shuffling")
	}
	db, err := sql.Open("sqlite", filepath.Join(home, ".crude", "agent-names.db"))
	if err != nil {
		t.Fatal(err)
	}
	var key []byte
	if err := db.QueryRow("SELECT key FROM name_key WHERE id=1").Scan(&key); err != nil || len(key) != 32 {
		t.Fatalf("installation key=%x err=%v", key, err)
	}
	_ = db.Close()
	reopened, err := allocateAgentName(home)
	if err != nil || reopened != shuffledAgentName(big.NewInt(82), key) {
		t.Fatalf("reopened sequence: %q, err=%v", reopened, err)
	}
}

func TestAgentNameAllocatorConcurrentInitialization(t *testing.T) {
	home := t.TempDir()
	const allocations = 320
	names := make(chan string, allocations)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range allocations {
		workers.Go(func() {
			<-start
			name, err := allocateAgentName(home)
			if err != nil {
				t.Error(err)
				return
			}
			names <- name
		})
	}
	began := time.Now()
	close(start)
	workers.Wait()
	close(names)
	allocated := make([]string, 0, allocations)
	for name := range names {
		allocated = append(allocated, name)
	}
	t.Logf("%d cold-start allocations in %s", len(allocated), time.Since(began))
	checkAgentNameSequence(t, home, allocations, allocated)
}

func TestAgentNameAllocatorConcurrentProcesses(t *testing.T) {
	home := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const processes, allocationsPerProcess = 4, 10
	names := make(chan string, processes*allocationsPerProcess)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range processes {
		workers.Go(func() {
			<-start
			cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestAgentNameAllocatorProcessHelper$", "-test.timeout=30s")
			cmd.Env = append(os.Environ(), "CRUDE_AGENT_NAME_HELPER_HOME="+home)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("allocator process: %v\n%s", err, out)
				return
			}
			for line := range strings.SplitSeq(string(out), "\n") {
				if name, ok := strings.CutPrefix(strings.TrimSpace(line), "allocated name: "); ok {
					names <- name
				}
			}
		})
	}
	close(start)
	workers.Wait()
	close(names)
	allocated := make([]string, 0, processes*allocationsPerProcess)
	for name := range names {
		allocated = append(allocated, name)
	}
	checkAgentNameSequence(t, home, processes*allocationsPerProcess, allocated)
}

func TestAgentNameAllocatorProcessHelper(t *testing.T) {
	home := os.Getenv("CRUDE_AGENT_NAME_HELPER_HOME")
	if home == "" {
		t.Skip("subprocess helper")
	}
	for range 10 {
		name, err := allocateAgentName(home)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("allocated name:", name)
	}
}

// Check the durable counter as well as returned names: a lost allocation or
// partial schema initialization must not be hidden by another process/reopen.
func checkAgentNameSequence(t *testing.T, home string, count int, names []string) {
	t.Helper()
	if len(names) != count {
		t.Fatalf("allocated %d names, want %d", len(names), count)
	}
	seen := make(map[string]bool, count)
	for _, name := range names {
		if name == "" || seen[name] {
			t.Fatalf("empty or reused agent name %q", name)
		}
		seen[name] = true
	}
	db, err := sql.Open("sqlite", filepath.Join(home, ".crude", "agent-names.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var next string
	if err := db.QueryRow("SELECT next FROM name_sequence WHERE id=1").Scan(&next); err != nil || next != fmt.Sprint(count) {
		t.Fatalf("durable sequence=%q, want %d: %v", next, count, err)
	}
	var key []byte
	if err := db.QueryRow("SELECT key FROM name_key WHERE id=1").Scan(&key); err != nil || len(key) != 32 {
		t.Fatalf("installation key=%x: %v", key, err)
	}
	for position := range count {
		if name := shuffledAgentName(big.NewInt(int64(position)), key); !seen[name] {
			t.Fatalf("durable sequence skipped allocation %d (%q)", position, name)
		}
	}
	name, err := allocateAgentName(home)
	if err != nil || name == "" || seen[name] {
		t.Fatalf("reopened sequence=%q: %v", name, err)
	}
}

func TestAgentNameAllocatorRollsBackInvalidSequence(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, ".crude")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(directory, "agent-names.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("CREATE TABLE name_sequence (id INTEGER PRIMARY KEY, next TEXT NOT NULL); INSERT INTO name_sequence VALUES(1,'invalid'); CREATE TABLE name_key (id INTEGER PRIMARY KEY, key BLOB NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if name, err := allocateAgentName(home); name != "" || err == nil || !strings.Contains(err.Error(), "invalid agent name sequence") {
		t.Fatalf("invalid sequence allocated %q: %v", name, err)
	}
	var keys int
	if err := db.QueryRow("SELECT COUNT(*) FROM name_key").Scan(&keys); err != nil || keys != 0 {
		t.Fatalf("failed allocation retained installation key: %d, %v", keys, err)
	}
	var next string
	if err := db.QueryRow("SELECT next FROM name_sequence WHERE id=1").Scan(&next); err != nil || next != "invalid" {
		t.Fatalf("failed allocation changed sequence: %q, %v", next, err)
	}
	if _, err := db.Exec("UPDATE name_sequence SET next='0' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	name, err := allocateAgentName(home)
	if err != nil {
		t.Fatal(err)
	}
	checkAgentNameSequence(t, home, 1, []string{name})
}

func TestAgentNameAllocatorWaitsForExternalWriter(t *testing.T) {
	home := t.TempDir()
	first, err := allocateAgentName(home)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, ".crude", "agent-names.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.ExecContext(t.Context(), "ROLLBACK") }()
	type allocation struct {
		name string
		err  error
	}
	result := make(chan allocation, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		name, err := allocateAgentName(home)
		result <- allocation{name, err}
	}()
	<-started
	select {
	case got := <-result:
		t.Fatalf("returned before external writer released: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := conn.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatal(got.err)
		}
		checkAgentNameSequence(t, home, 2, []string{first, got.name})
	case <-t.Context().Done():
		t.Fatal("allocator did not finish after writer released")
	}
}

func TestShuffledNamesAreABijectionOfEachTier(t *testing.T) {
	key := []byte("fixed test key for permutations")
	for _, size := range []int64{2, 3, 528, 1000, 4099} {
		seen := make(map[int64]bool, size)
		for value := int64(0); value < size; value++ {
			permuted := permuteBelow(big.NewInt(value), big.NewInt(size), key, 1)
			if permuted.Sign() < 0 || permuted.Int64() >= size || seen[permuted.Int64()] {
				t.Fatalf("size %d: %d -> %s is not a bijection", size, value, permuted)
			}
			seen[permuted.Int64()] = true
		}
	}
	huge := new(big.Int).Exp(big.NewInt(10), big.NewInt(200), nil)
	names := make(map[string]bool)
	for offset := int64(0); offset < 64; offset++ {
		name := shuffledAgentName(new(big.Int).Add(huge, big.NewInt(offset)), key)
		if names[name] || len(strings.Fields(name)) < 3 {
			t.Fatalf("huge-tier name %q is invalid or repeated", name)
		}
		names[name] = true
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
	if name == "" || !strings.Contains(s.systemPrompt(true), "Your name is "+name) {
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
	if other.AgentName() == "" || other.AgentName() == name {
		t.Fatalf("other workspace reused identity: %q", other.AgentName())
	}
}

func TestParentChosenChildNamesAreValidatedAndKeptDistinct(t *testing.T) {
	for _, bad := range []string{"", "   ", strings.Repeat("a", 41), "<b>Nova</b>", "Nova sub Root", "tab\x00null"} {
		if _, err := validChildName(bad); err == nil {
			t.Fatalf("name %q accepted", bad)
		}
	}
	for input, want := range map[string]string{"  Nova  ": "Nova", "Zoë Ödegaard": "Zoë Ödegaard", "Ana-María O'Neil": "Ana-María O'Neil", "R2 D2": "R2 D2"} {
		if got, err := validChildName(input); err != nil || got != want {
			t.Fatalf("name %q -> %q err=%v", input, got, err)
		}
	}
	for value, want := range map[int]string{2: "II", 4: "IV", 9: "IX", 14: "XIV", 1994: "MCMXCIV"} {
		if got := romanNumeral(value); got != want {
			t.Fatalf("numeral %d=%q", value, got)
		}
	}
}
