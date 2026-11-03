package toolstore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	// MaxHistoryPage is the largest page that History returns.
	MaxHistoryPage = 1000

	maxHistoryLine = 1 << 20
)

// Event is one history record. Kind is "install", "install-failed", "remove"
// or "use".
type Event struct {
	Time     time.Time `json:"time"`
	Kind     string    `json:"kind"`
	ToolID   string    `json:"tool_id,omitempty"`
	Package  string    `json:"package,omitempty"`
	Version  string    `json:"version,omitempty"`
	Detail   string    `json:"detail,omitempty"`
	By       string    `json:"by,omitempty"`
	Approval string    `json:"approval,omitempty"`
}

// History returns a page of events, newest first, and the total number of
// events. offset must be in the range of existing events; offset 0 is valid
// for an empty history.
func (s *Store) History(ctx context.Context, offset, limit int) ([]Event, int, error) {
	if limit < 1 || limit > MaxHistoryPage {
		return nil, 0, fmt.Errorf("history page size %d is not permitted; use 1 to %d", limit, MaxHistoryPage)
	}
	if err := s.mu.LockContext(ctx); err != nil {
		return nil, 0, err
	}
	defer s.mu.Unlock()
	name := filepath.Join(s.root, historyName)
	total, err := scanHistory(ctx, name, nil)
	if err != nil {
		return nil, 0, err
	}
	if offset < 0 || offset > 0 && offset >= total {
		if total == 0 {
			return nil, 0, fmt.Errorf("history offset %d is out of range; the history is empty, so use offset 0", offset)
		}
		return nil, total, fmt.Errorf("history offset %d is out of range; use an offset from 0 to %d", offset, total-1)
	}
	end := total - offset
	start := max(0, end-limit)
	page := make([]Event, 0, end-start)
	_, err = scanHistory(ctx, name, func(i int, event Event) {
		if i >= start && i < end {
			page = append(page, event)
		}
	})
	if err != nil {
		return nil, 0, err
	}
	for i, j := 0, len(page)-1; i < j; i, j = i+1, j-1 {
		page[i], page[j] = page[j], page[i]
	}
	return page, total, nil
}

// scanHistory skips invalid and oversized lines, keeping only the caller's page.
func scanHistory(ctx context.Context, name string, visit func(int, Event)) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	file, err := os.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read tool history: %w", err)
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(file)
	total := 0
	for {
		line, err := readLine(ctx, reader)
		if len(line) > 0 {
			var event Event
			if json.Unmarshal(line, &event) == nil && event.Kind != "" {
				if visit != nil {
					visit(total, event)
				}
				total++
			}
		}
		if errors.Is(err, io.EOF) {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			return total, nil
		}
		if err != nil {
			return 0, fmt.Errorf("read tool history: %w", err)
		}
	}
}

// readLine returns one line without its newline. A line longer than
// maxHistoryLine is returned empty so that the caller skips it.
func readLine(ctx context.Context, reader *bufio.Reader) ([]byte, error) {
	var line []byte
	tooLong := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		chunk, err := reader.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > maxHistoryLine {
				tooLong = true
				line = nil
			} else {
				line = append(line, chunk...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if len(line) > 0 && line[len(line)-1] == '\n' {
			line = line[:len(line)-1]
		}
		return line, err
	}
}

// appendLocked appends one event, syncs the file and its parent directory.
// A torn final line from an interrupted append is sealed with a newline first,
// so the new event starts on its own line.
func (s *Store) appendLocked(event Event) error {
	if event.Time.IsZero() {
		event.Time = s.now().UTC()
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	name := filepath.Join(s.root, historyName)
	if err := sealHistoryTail(name); err != nil {
		return err
	}
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("record tool history: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("record tool history: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("record tool history: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("record tool history: %w", err)
	}
	if err := s.syncHistoryDir(s.root); err != nil {
		return fmt.Errorf("record tool history directory: %w", err)
	}
	return nil
}

// sealHistoryTail appends a newline when the history does not end with one.
func sealHistoryTail(name string) error {
	file, err := os.OpenFile(name, os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open tool history: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("open tool history: %w", err)
	}
	if info.Size() == 0 {
		return nil
	}
	last := make([]byte, 1)
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		return fmt.Errorf("read tool history: %w", err)
	}
	if last[0] == '\n' {
		return nil
	}
	if _, err := file.WriteAt([]byte{'\n'}, info.Size()); err != nil {
		return fmt.Errorf("repair tool history: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("repair tool history: %w", err)
	}
	return nil
}
