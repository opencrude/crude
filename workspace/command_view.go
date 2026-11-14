package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"crude/fileio"
	"crude/terminalvt"
)

// CommandOutputSource observes captured output without consuming the agent's
// cursor. changed belongs to the same snapshot, not a later subscription.
type CommandOutputSource interface {
	ObserveAsyncOutput(fileio.AsyncCmdRef, int, int) ([]byte, int, fileio.AsyncCmdInfo, <-chan struct{}, error)
}

// commandView is a private, read-only terminal resource. It reuses the PTY's
// grid stream, row paging and input sequencing; its lease permits only resize.
// No command output can write to the command, clipboard or shell title.
type commandView struct {
	owner  TerminalController
	info   TerminalInfo // protected by TerminalService.mu
	cancel context.CancelFunc
	done   chan struct{}
}

var commandViewSequence atomic.Uint64

// PrepareOpenCommandView captures the original session on the workspace owner.
// The returned body creates the engine off that owner and never starts a process.
func (w *Workspace) PrepareOpenCommandView(agentID string, ref fileio.AsyncCmdRef, owner TerminalController, cols, rows int) (func(context.Context) (TerminalInfo, error), error) {
	if w == nil || w.AgentMgr == nil || w.Terminals == nil {
		return nil, errors.New("command output is unavailable; open the session again")
	}
	root := w.AgentMgr.OrchestratorFor(agentID)
	session := w.SessionsByAgent[agentID]
	if root == nil || root.ID != agentID || session == nil || session.backend == nil {
		return nil, errors.New("the command session is closed; open it again")
	}
	source, service := session.backend, w.Terminals
	return func(ctx context.Context) (TerminalInfo, error) {
		return service.openCommandView(ctx, owner, source, ref, cols, rows)
	}, nil
}

func (s *TerminalService) openCommandView(ctx context.Context, owner TerminalController, source CommandOutputSource, ref fileio.AsyncCmdRef, cols, rows int) (TerminalInfo, error) {
	_, _, info, _, err := source.ObserveAsyncOutput(ref, 0, 1)
	if err != nil {
		return TerminalInfo{}, err
	}
	cols, rows = NormalizeTerminalSize(cols, rows)
	engine, err := terminalvt.New(terminalvt.Options{Cols: cols, Rows: rows, ScrollbackLines: s.manager.Settings().ScrollbackLines})
	if err != nil {
		return TerminalInfo{}, err
	}
	id := fmt.Sprintf("command-output-%d", commandViewSequence.Add(1))
	lifetime, cancel := context.WithCancel(context.Background())
	view := &commandView{owner: owner, cancel: cancel, done: make(chan struct{}), info: TerminalInfo{
		ID: id, Name: "Output · " + ref.Handle, Title: info.Command, CWD: info.CWD,
		PID: ref.PID, Cols: cols, Rows: rows, Alive: true,
	}}
	// Echo the complete command as literal text; never interpret its escapes.
	echo := sanitizeCommandEcho(strings.ReplaceAll(info.Command, "\r\n", "\n"))
	engine.Feed([]byte("$ " + strings.ReplaceAll(echo, "\n", "\r\n") + "\r\n"))
	resource := &terminalResource{engine: engine, command: view, generation: 1}
	s.mu.Lock()
	count := 0
	for _, item := range s.items {
		if item.command != nil && item.command.owner == owner {
			count++
		}
	}
	if ctx.Err() != nil || s.closed || s.idleFenced || s.detached[owner] || owner.ClientID == "" || owner.Epoch == 0 || count >= 8 {
		s.mu.Unlock()
		cancel()
		engine.Close()
		if ctx.Err() != nil {
			return TerminalInfo{}, ctx.Err()
		}
		return TerminalInfo{}, errors.New("cannot open another output window; close an output window and try again")
	}
	s.items[id] = resource
	out := view.info
	// Register the producer before releasing the owner lock. CloseAll joins
	// it, and Detach cancels it without waiting on the workspace actor.
	s.commandWorkers.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.commandWorkers.Done()
		defer close(view.done)
		s.followCommand(lifetime, id, resource, source, ref)
	}()
	return out, nil
}

func (s *TerminalService) followCommand(ctx context.Context, id string, r *terminalResource, source CommandOutputSource, ref fileio.AsyncCmdRef) {
	cursor, previousCR := 0, false
	for ctx.Err() == nil {
		raw, total, info, changed, err := source.ObserveAsyncOutput(ref, cursor, fileio.MaxOutputChunkBytes)
		// A pipe lacks the PTY's ONLCR line discipline. Preserve CRLF and
		// carriage-return progress, including CR/LF split across chunks.
		data := make([]byte, 0, len(raw)+len(raw)/16)
		for _, b := range raw {
			if b == '\n' && !previousCR {
				data = append(data, '\r')
			}
			data = append(data, b)
			previousCR = b == '\r'
		}
		cursor += len(raw)
		ended := err != nil || info.Status != fileio.AsyncRunning && cursor >= total
		s.mu.Lock()
		if s.items[id] != r || ctx.Err() != nil {
			s.mu.Unlock()
			return
		}
		if len(data) > 0 {
			r.engine.Feed(data) // all OSC/query effects are deliberately inert
		}
		if ended {
			r.command.info.Alive = false
			r.command.info.ExitCode, r.command.info.ExitErr = info.ExitCode, info.Error
			status := "Finished"
			if info.Status == fileio.AsyncKilled {
				status = "Stopped"
			}
			if info.Status == fileio.AsyncFailed {
				status = fmt.Sprintf("Failed · exit %d", info.ExitCode)
			}
			if info.Uncertain {
				status = "Outcome unknown"
			}
			if info.TimedOut {
				status = "Time limit reached"
			}
			if err != nil {
				status = "Output unavailable"
			}
			r.command.info.Name = "Output · " + ref.Handle + " · " + status
			if err != nil {
				// Losing the source stops updates, not access to the output
				// already captured. Do not replace that evidence with an
				// error-only terminal surface.
				r.command.info.ExitErr = "Command output is no longer available. Close this window."
			}
		}
		if len(data) > 0 || ended {
			r.generation++
			s.notify(r)
		}
		s.mu.Unlock()
		if ended {
			return
		}
		if cursor < total {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
	}
}

// CheckViewer fences private mirrors to the attachment that opened them. PTYs
// remain readable by every attached shell, as before.
func (s *TerminalService) CheckViewer(id string, owner TerminalController) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.items[id]
	if r != nil && r.command != nil && r.command.owner != owner {
		return ErrTerminalControl
	}
	return nil
}

func sanitizeCommandEcho(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0 {
			return '�'
		}
		return r
	}, text)
}
