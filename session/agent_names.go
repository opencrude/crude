package session

import (
	"database/sql"
	"fmt"
	"math/big"
	"os"
	"path/filepath"

	"crude/buildinfo"
)

var agentFirstNames = []string{
	"Jeff", "Tom", "Jake", "Ava", "Maya", "Leo", "Nora", "Sam", "Ella", "Noah", "Mia", "Owen", "Ivy", "Finn", "Zoe", "Liam",
	"Ada", "Max", "Ruby", "Eli", "Luna", "Theo", "Rose", "Alex", "Iris", "Jack", "Lily", "Ben", "Esme", "Arlo", "June", "Luke",
	"Clara", "Jude", "Nina", "Hugo", "Sara", "Miles", "Alice", "Oscar", "Grace", "Felix", "Emma", "Levi", "Lucy", "Ezra", "Cora", "Adam",
	"Wren", "Dean", "Jade", "Cole", "Tess", "Joel", "Hope", "Paul", "Vera", "Seth", "Ruth", "Neil", "Faye", "Eric", "Dawn", "Ryan",
}

// A durable app-wide arbitrary-precision counter gives names a never-exhausted
// namespace across workspaces, restarts, and concurrent processes. First pass
// uses plain first names, then Jeff 2, Tom 2, ... without reusing an identity.
func allocateAgentName(home string) (string, error) {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
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
	cycle, slot := new(big.Int), new(big.Int)
	cycle.QuoRem(index, big.NewInt(int64(len(agentFirstNames))), slot)
	name := agentFirstNames[slot.Int64()]
	if cycle.Sign() > 0 {
		name += " " + cycle.Add(cycle, big.NewInt(1)).String()
	}
	return name
}

func (s *Session) AgentName() string { return s.agentName }
