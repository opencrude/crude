package session

import (
	"database/sql"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"crude/buildinfo"
)

// A durable app-wide arbitrary-precision counter gives names a never-exhausted
// namespace across workspaces, restarts, and concurrent processes.
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
	if _, err = db.Exec("PRAGMA busy_timeout=10000; CREATE TABLE IF NOT EXISTS name_sequence (id INTEGER PRIMARY KEY CHECK(id=1), next TEXT NOT NULL); INSERT OR IGNORE INTO name_sequence(id,next) VALUES(1,'0');"); err != nil {
		return "", err
	}
	if _, err = db.Exec("BEGIN IMMEDIATE"); err != nil {
		return "", err
	}
	defer func() { _, _ = db.Exec("ROLLBACK") }()
	var raw string
	if err = db.QueryRow("SELECT next FROM name_sequence WHERE id=1").Scan(&raw); err != nil {
		return "", err
	}
	index, ok := new(big.Int).SetString(raw, 10)
	if !ok || index.Sign() < 0 {
		return "", fmt.Errorf("invalid agent name sequence")
	}
	name := agentNameAt(index)
	next := new(big.Int).Add(index, big.NewInt(1))
	if _, err = db.Exec("UPDATE name_sequence SET next=? WHERE id=1", next.String()); err != nil {
		return "", err
	}
	if _, err = db.Exec("COMMIT"); err != nil {
		return "", err
	}
	return name, nil
}

func agentNameAt(index *big.Int) string {
	firstCount := big.NewInt(int64(len(agentFirstNames)))
	if index.Cmp(firstCount) < 0 {
		return agentFirstNames[index.Int64()]
	}

	remaining := new(big.Int).Sub(index, firstCount)
	familyIndex, firstSlot := new(big.Int), new(big.Int)
	familyIndex.QuoRem(remaining, firstCount, firstSlot)

	surnameCount := big.NewInt(int64(len(agentSurnames)))
	tierSize := new(big.Int).Set(surnameCount)
	wordCount := 1
	for familyIndex.Cmp(tierSize) >= 0 {
		familyIndex.Sub(familyIndex, tierSize)
		tierSize.Mul(tierSize, surnameCount)
		wordCount++
	}

	words := make([]string, wordCount+1)
	words[0] = agentFirstNames[firstSlot.Int64()]
	for wordSlot := wordCount; wordSlot > 0; wordSlot-- {
		digit := new(big.Int)
		familyIndex.QuoRem(familyIndex, surnameCount, digit)
		words[wordSlot] = agentSurnames[digit.Int64()]
	}
	return strings.Join(words, " ")
}

func (s *Session) AgentName() string { return s.agentName }
