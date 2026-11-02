package toolstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"crude/fileio"
)

const maxUsageBytes = 64 << 20

// usageFile is the mutable accounting that is kept outside the immutable tool
// directories.
type usageFile struct {
	Tools map[string]usageEntry `json:"tools"`
}

type usageEntry struct {
	Workspaces []string  `json:"workspaces"`
	LastUsed   time.Time `json:"last_used"`
}

func readUsage(name string) (usageFile, error) {
	usage := usageFile{Tools: make(map[string]usageEntry)}
	data, err := readBounded(name, maxUsageBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return usage, nil
	}
	if err != nil {
		return usage, fmt.Errorf("read tool usage file: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&usage); err != nil {
		return usage, fmt.Errorf("tool usage file %s is damaged: %w. Repair or move the file, then open the tool store again", name, err)
	}
	if usage.Tools == nil {
		usage.Tools = make(map[string]usageEntry)
	}
	return usage, nil
}

// writeUsageLocked replaces usage.json atomically: write a temporary file,
// sync it, then rename it into place.
func (s *Store) writeUsageLocked() error {
	data, err := json.MarshalIndent(s.usage, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(s.root, usageTemp+"*.tmp")
	if err != nil {
		return fmt.Errorf("save tool usage: %w", err)
	}
	tempName := temp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempName)
		}
	}()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("save tool usage: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("save tool usage: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("save tool usage: %w", err)
	}
	err = fileio.CommitAtomicFile(tempName, filepath.Join(s.root, usageName))
	if err == nil || fileio.WriteCommitted(err) {
		committed = true
	}
	if err != nil && !fileio.WriteCommitted(err) {
		return fmt.Errorf("save tool usage: %w", err)
	}
	return nil
}
