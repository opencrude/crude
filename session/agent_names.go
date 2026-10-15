package session

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"crude/buildinfo"
	"crude/collaboration"
)

// Agent names come from a durable app-wide arbitrary-precision counter, so the
// namespace is never exhausted and no name is ever handed out twice across
// workspaces, restarts and concurrent processes. Each counter value is mapped
// through a keyed permutation of its tier (one given name, then given name plus
// one, two, ... surnames), seeded once per installation: consecutive agents get
// unrelated names instead of walking the catalog in order, without any
// retries, reservations or wasted values.
func allocateAgentName(home string) (string, error) {
	if home == "" {
		var err error
		home, err = buildinfo.Home()
		if err != nil {
			return "", err
		}
	}
	directory := filepath.Join(home, buildinfo.DataDir())
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	db, err := sql.Open("sqlite", filepath.Join(directory, "agent-names.db"))
	if err != nil {
		return "", err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA busy_timeout=10000; CREATE TABLE IF NOT EXISTS name_sequence (id INTEGER PRIMARY KEY CHECK(id=1), next TEXT NOT NULL); INSERT OR IGNORE INTO name_sequence(id,next) VALUES(1,'0'); CREATE TABLE IF NOT EXISTS name_key (id INTEGER PRIMARY KEY CHECK(id=1), key BLOB NOT NULL);"); err != nil {
		return "", err
	}
	if _, err = db.Exec("BEGIN IMMEDIATE"); err != nil {
		return "", err
	}
	defer func() { _, _ = db.Exec("ROLLBACK") }()
	var key []byte
	if err = db.QueryRow("SELECT key FROM name_key WHERE id=1").Scan(&key); errors.Is(err, sql.ErrNoRows) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return "", err
		}
		if _, err = db.Exec("INSERT INTO name_key(id,key) VALUES(1,?)", key); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	var raw string
	if err = db.QueryRow("SELECT next FROM name_sequence WHERE id=1").Scan(&raw); err != nil {
		return "", err
	}
	index, ok := new(big.Int).SetString(raw, 10)
	if !ok || index.Sign() < 0 {
		return "", fmt.Errorf("invalid agent name sequence")
	}
	name := shuffledAgentName(index, key)
	next := new(big.Int).Add(index, big.NewInt(1))
	if _, err = db.Exec("UPDATE name_sequence SET next=? WHERE id=1", next.String()); err != nil {
		return "", err
	}
	if _, err = db.Exec("COMMIT"); err != nil {
		return "", err
	}
	return name, nil
}

// agentNameTier locates index within its tier: tier 0 holds single given
// names, tier k >= 1 a given name followed by k surnames.
func agentNameTier(index *big.Int) (tier int, offset, size *big.Int) {
	firstCount := big.NewInt(int64(len(agentFirstNames)))
	surnameCount := big.NewInt(int64(len(agentSurnames)))
	offset = new(big.Int).Set(index)
	size = new(big.Int).Set(firstCount)
	for offset.Cmp(size) >= 0 {
		offset.Sub(offset, size)
		if tier == 0 {
			size = new(big.Int).Mul(firstCount, surnameCount)
		} else {
			size = new(big.Int).Mul(size, surnameCount)
		}
		tier++
	}
	return tier, offset, size
}

// composeAgentName renders the offset-th name of a tier.
func composeAgentName(tier int, offset *big.Int) string {
	firstCount := big.NewInt(int64(len(agentFirstNames)))
	surnameCount := big.NewInt(int64(len(agentSurnames)))
	rest, firstSlot := new(big.Int).QuoRem(offset, firstCount, new(big.Int))
	words := make([]string, tier+1)
	words[0] = agentFirstNames[firstSlot.Int64()]
	for slot := tier; slot > 0; slot-- {
		digit := new(big.Int)
		rest.QuoRem(rest, surnameCount, digit)
		words[slot] = agentSurnames[digit.Int64()]
	}
	return strings.Join(words, " ")
}

// agentNameAt is the unshuffled catalog order.
func agentNameAt(index *big.Int) string {
	tier, offset, _ := agentNameTier(index)
	return composeAgentName(tier, offset)
}

// shuffledAgentName maps index through a keyed bijection of its own tier.
func shuffledAgentName(index *big.Int, key []byte) string {
	tier, offset, size := agentNameTier(index)
	return composeAgentName(tier, permuteBelow(offset, size, key, tier))
}

// permuteBelow is a keyed bijection of [0, size): a balanced Feistel network
// over the smallest even-width binary domain covering size, with cycle walking
// back into range (fewer than four steps expected).
func permuteBelow(value, size *big.Int, key []byte, tier int) *big.Int {
	if size.Cmp(big.NewInt(2)) < 0 {
		return new(big.Int).Set(value)
	}
	half := (size.BitLen() + 1) / 2
	mask := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(half)), big.NewInt(1))
	round := func(right *big.Int, step int) *big.Int {
		var digest []byte
		for block := 0; len(digest)*8 < half; block++ {
			mac := hmac.New(sha256.New, key)
			fmt.Fprintf(mac, "%d/%d/%d/%d/", tier, half, step, block)
			mac.Write(right.Bytes())
			digest = mac.Sum(digest)
		}
		return new(big.Int).And(new(big.Int).SetBytes(digest), mask)
	}
	current := new(big.Int).Set(value)
	for {
		left := new(big.Int).Rsh(current, uint(half))
		right := new(big.Int).And(current, mask)
		for step := 0; step < 4; step++ {
			left, right = right, new(big.Int).Xor(left, round(right, step))
		}
		current = new(big.Int).Or(new(big.Int).Lsh(left, uint(half)), right)
		if current.Cmp(size) < 0 {
			return current
		}
	}
}

func (s *Session) AgentName() string { return s.agentName }

// validChildName accepts a name a parent gives its child: a short human name in
// any script, never markup or control text.
func validChildName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if count := utf8.RuneCountInString(name); count == 0 || count > 40 {
		return "", errors.New("agent_start nickname must be 1-40 characters; omit it for a generated one")
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) && !strings.ContainsRune(" '-.", r) {
			return "", fmt.Errorf("agent_start nickname %q may use letters, digits, spaces and ' - . only; omit it for a generated one", name)
		}
	}
	if strings.Contains(strings.ToLower(name), " sub ") {
		return "", errors.New("agent_start nickname cannot contain \" sub \"; lineage is added automatically")
	}
	return name, nil
}

// distinctChildName keeps a chosen name unique in this workspace the way
// families do: a repeated name gets the next numeral (Nova, Nova II, ...).
func (r *SubAgentRunner) distinctChildName(ctx context.Context, name string) string {
	suffix := " sub " + r.session.AgentName()
	taken := make(map[string]bool)
	if store := r.session.CollaborationStore(); store != nil {
		if agents, err := store.ListAgents(ctx, collaboration.AgentQuery{Query: name, IncludeClosed: true, Limit: collaboration.MaxAgentLimit}); err == nil {
			for _, existing := range agents {
				taken[strings.ToLower(existing.Name)] = true
			}
		}
	}
	for generation := 1; ; generation++ {
		candidate := name
		if generation > 1 {
			candidate += " " + romanNumeral(generation)
		}
		if !taken[strings.ToLower(candidate+suffix)] {
			return candidate
		}
	}
}

func romanNumeral(value int) string {
	numerals := []struct {
		value  int
		symbol string
	}{{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"}, {100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"}, {10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"}}
	var out strings.Builder
	for _, numeral := range numerals {
		for value >= numeral.value {
			out.WriteString(numeral.symbol)
			value -= numeral.value
		}
	}
	return out.String()
}
