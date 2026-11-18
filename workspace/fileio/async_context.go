package fileio

import (
	"strconv"
	"strings"
	"time"
)

type AsyncCommandContext struct {
	Handle          string      `json:"handle"`
	Command         string      `json:"command"`
	CWD             string      `json:"cwd"`
	PID             int         `json:"pid"`
	StartedAt       time.Time   `json:"started_at"`
	Status          AsyncStatus `json:"status"`
	ExitCode        *int        `json:"exit_code,omitempty"`
	Error           string      `json:"error,omitempty"`
	NativeSessionID int         `json:"native_session_id,omitempty"`
	ReadCursor      *int        `json:"read_cursor_byte,omitempty"`
	OutputBytes     int         `json:"output_bytes"`
}

// ContextSnapshot observes the existing command owner without reading output,
// advancing a cursor, signaling processes, or treating a running exit code as 0.
func (m *AsyncCommandManager) ContextSnapshot() []AsyncCommandContext {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]AsyncCommandContext, 0, len(m.commands))
	for _, state := range m.commands {
		state.mu.Lock()
		info := state.info
		entry := AsyncCommandContext{Handle: info.Handle, Command: info.Command, PID: info.PID,
			StartedAt: info.StartedAt, Status: info.Status, Error: info.Error}
		if state.cmd != nil {
			entry.CWD = state.cmd.Dir
		}
		switch info.Status {
		case AsyncCompleted, AsyncFailed:
			code := info.ExitCode
			entry.ExitCode = &code
		case AsyncKilled:
			code := -1
			entry.ExitCode = &code
		}
		interactive := state.input != nil
		state.mu.Unlock()
		state.outputMu.Lock()
		entry.OutputBytes = state.output.Len()
		if interactive {
			entry.NativeSessionID, _ = strconv.Atoi(strings.TrimPrefix(info.Handle, "auto-"))
			cursor := state.outputCursor
			entry.ReadCursor = &cursor
		} else {
			cursor := state.readCursor
			entry.ReadCursor = &cursor
		}
		state.outputMu.Unlock()
		result = append(result, entry)
	}
	return result
}
