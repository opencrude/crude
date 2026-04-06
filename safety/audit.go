package safety

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"crude/dag"
)

// AuditLogger records operation execution to a JSONL file and optionally to a logger.
type AuditLogger struct {
	mu      sync.Mutex
	file    *os.File
	logger  *log.Logger
	entries []dag.AuditEntry
}

// NewAuditLogger creates an audit logger that writes to the given file path.
// If logDir is empty, entries are only kept in memory.
func NewAuditLogger(logDir string) (*AuditLogger, error) {
	al := &AuditLogger{
		logger: log.Default(),
	}

	if logDir == "" {
		return al, nil
	}

	if err := os.MkdirAll(logDir, 0700); err != nil {
		return nil, fmt.Errorf("safety: create audit dir: %w", err)
	}

	ts := time.Now().UTC().Format("20060102-150405")
	logPath := filepath.Join(logDir, fmt.Sprintf("audit-%s.jsonl", ts))
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("safety: open audit log: %w", err)
	}
	al.file = f
	al.logger.Printf("[audit] logging to %s", logPath)
	return al, nil
}

// Record writes an audit entry.
func (al *AuditLogger) Record(entry dag.AuditEntry) {
	al.mu.Lock()
	defer al.mu.Unlock()

	al.entries = append(al.entries, entry)

	if al.file == nil {
		return
	}

	data, err := json.Marshal(entry)
	if err != nil {
		al.logger.Printf("[audit] marshal error: %v", err)
		return
	}
	if _, err := al.file.Write(append(data, '\n')); err != nil {
		al.logger.Printf("[audit] write error: %v", err)
	}
}

// Entries returns a copy of all recorded entries.
func (al *AuditLogger) Entries() []dag.AuditEntry {
	al.mu.Lock()
	defer al.mu.Unlock()
	out := make([]dag.AuditEntry, len(al.entries))
	copy(out, al.entries)
	return out
}

// Close closes the audit log file.
func (al *AuditLogger) Close() error {
	al.mu.Lock()
	defer al.mu.Unlock()
	if al.file != nil {
		return al.file.Close()
	}
	return nil
}
