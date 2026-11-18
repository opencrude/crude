package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"crude/terminalvt"
	"crude/workspace/fileio"
)

// Terminal revival keeps the buffer of every interactive terminal when its
// core stops (an explicit stop, an upgrade restart or a workspace close). A
// shell process cannot outlive its core, but its output can: the core saves
// each screen and scrollback as VT bytes from the one engine that parsed them,
// and the next core shows them again above a new shell in the same folder.
// Terminals that ran their own command, and terminals that exited, are not
// revived. A terminal that the user closed is gone before the core stops.

// TerminalRevivedMsg reports a terminal that the core created again with its
// earlier buffer. Inventory owners publish it like a new terminal.
type TerminalRevivedMsg struct{ ID string }

const (
	terminalRevivalFormat = 1
	// maxRevivalScreen bounds one terminal's saved buffer. The newest rows
	// are kept when a scrollback is larger.
	maxRevivalScreen = 8 << 20
)

type terminalRevival struct {
	Name      string            `json:"name,omitempty"`
	CWD       string            `json:"cwd,omitempty"`
	Cols      int               `json:"cols"`
	Rows      int               `json:"rows"`
	Placement TerminalPlacement `json:"placement"`
	SavedAt   int64             `json:"saved_at"`
	Screen    []byte            `json:"screen"`
}

type terminalRevivalFile struct {
	Version   int               `json:"version"`
	Terminals []terminalRevival `json:"terminals"`
}

// SetRevivalPath names the file that keeps terminal buffers across a core
// stop. An empty path turns revival off.
func (s *TerminalService) SetRevivalPath(path string) {
	s.reviveMu.Lock()
	s.revivePath = path
	s.reviveMu.Unlock()
}

// Revive creates the saved terminals again, once per service, with their
// buffers and a new shell in their folder. Call it off any UI thread after
// the shell environment for new terminals is known. It returns the new IDs.
func (s *TerminalService) Revive() ([]string, error) {
	s.reviveMu.Lock()
	defer s.reviveMu.Unlock()
	if s.revived || s.revivePath == "" {
		return nil, nil
	}
	s.mu.Lock()
	closed := s.closed || s.idleFenced
	s.mu.Unlock()
	if closed {
		return nil, nil
	}
	s.revived = true
	saved, err := readTerminalRevival(s.revivePath)
	if err != nil || len(saved) == 0 {
		return nil, err
	}
	var ids []string
	var failed []terminalRevival
	for _, entry := range saved {
		id, err := s.reviveOne(entry)
		if err != nil {
			failed = append(failed, entry)
			continue
		}
		ids = append(ids, id)
		s.manager.emit(TerminalRevivedMsg{ID: id})
	}
	// Keep what could not start, so a later core can try again; never drop
	// a saved buffer because one folder or shell was unavailable.
	if err := writeTerminalRevival(s.revivePath, failed); err != nil {
		return ids, err
	}
	if len(failed) > 0 {
		return ids, fmt.Errorf("%d saved terminals could not start", len(failed))
	}
	return ids, nil
}

// StartRevive runs Revive once in the background. CloseAll joins it, and
// saving waits for it, so a stopping core never loses a buffer being revived.
func (s *TerminalService) StartRevive(report func(error)) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.commandWorkers.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.commandWorkers.Done()
		if _, err := s.Revive(); err != nil && report != nil {
			report(err)
		}
	}()
}

func (s *TerminalService) reviveOne(entry terminalRevival) (string, error) {
	cwd := entry.CWD
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		cwd = "" // the folder is gone: the project root keeps the buffer usable
	}
	s.mu.Lock()
	closed := s.closed || s.idleFenced
	if !closed {
		s.creating++
	}
	s.mu.Unlock()
	if closed {
		return "", errors.New("terminal service is closed")
	}
	defer func() { s.mu.Lock(); s.creating--; s.mu.Unlock() }()
	scrollback := s.manager.Settings().ScrollbackLines
	info, err := s.manager.CreatePrepared(TerminalCreateOpts{CWD: cwd, Cols: entry.Cols, Rows: entry.Rows}, func(info TerminalInfo) error {
		e, err := terminalvt.New(terminalvt.Options{Cols: info.Cols, Rows: info.Rows, ScrollbackLines: scrollback})
		if err != nil {
			return err
		}
		e.Feed(entry.Screen)
		e.Feed(revivalNotice(entry.SavedAt))
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			e.Close()
			return errors.New("terminal service is closed")
		}
		s.items[info.ID] = &terminalResource{engine: e}
		return nil
	})
	if err != nil {
		return "", err
	}
	if entry.Name != "" {
		_ = s.manager.rename(info.ID, entry.Name)
	}
	s.manager.SetPlacement(info.ID, entry.Placement)
	return info.ID, nil
}

// revivalNotice separates the kept buffer from the new shell's output.
func revivalNotice(savedAt int64) []byte {
	when := ""
	if savedAt > 0 {
		when = " from " + time.Unix(savedAt, 0).Format("Jan 2 15:04")
	}
	return []byte("\x1b[0m\r\n\x1b[2m── Crude kept this output" + when + ". The earlier process stopped; a new shell started. ──\x1b[0m\r\n")
}

// saveRevival records every live interactive terminal before the service
// closes them. Saved terminals that were never revived in this core stay.
func (s *TerminalService) saveRevival() {
	s.reviveMu.Lock()
	defer s.reviveMu.Unlock()
	if s.revivePath == "" {
		return
	}
	var entries []terminalRevival
	if !s.revived {
		// This core never showed them; keep them for the next one.
		entries, _ = readTerminalRevival(s.revivePath)
	}
	now := time.Now().Unix()
	for _, info := range s.manager.List() {
		if !info.Alive || !s.manager.isInteractive(info.ID) {
			continue
		}
		s.mu.Lock()
		r := s.items[info.ID]
		var screen []byte
		var err error
		if r != nil && r.command == nil {
			screen, err = r.engine.Snapshot()
		}
		s.mu.Unlock()
		if r == nil || r.command != nil || err != nil {
			continue
		}
		entries = append(entries, terminalRevival{
			Name: info.Name, CWD: info.CWD, Cols: info.Cols, Rows: info.Rows,
			Placement: info.Placement, SavedAt: now, Screen: boundRevivalScreen(screen),
		})
	}
	if err := writeTerminalRevival(s.revivePath, entries); err != nil && s.manager != nil {
		s.manager.emit(TerminalRevivalFailedMsg{Err: err.Error()})
	}
}

// TerminalRevivalFailedMsg reports that terminal buffers could not be saved.
type TerminalRevivalFailedMsg struct{ Err string }

// boundRevivalScreen keeps the newest whole lines of an oversized buffer.
func boundRevivalScreen(screen []byte) []byte {
	if len(screen) <= maxRevivalScreen {
		return screen
	}
	tail := screen[len(screen)-maxRevivalScreen:]
	if i := bytes.IndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	// A cut line can leave an open style; start from the default style.
	return append([]byte("\x1b[0m"), tail...)
}

func readTerminalRevival(path string) ([]terminalRevival, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var file terminalRevivalFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("read saved terminals: %w", err)
	}
	if file.Version != terminalRevivalFormat {
		return nil, fmt.Errorf("saved terminals use format %d; this Crude reads format %d", file.Version, terminalRevivalFormat)
	}
	return file.Terminals, nil
}

func writeTerminalRevival(path string, entries []terminalRevival) error {
	if len(entries) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.Marshal(terminalRevivalFile{Version: terminalRevivalFormat, Terminals: entries})
	if err != nil {
		return err
	}
	return fileio.WriteAtomicChecked(path, data, 0o600, nil)
}
