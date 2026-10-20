package session

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"crude/collaboration"
	"crude/fileio"
	"crude/types/command"
)

// observeCommandOutcomes routes a background command manager's settled commands
// to the agent that owns it, so agents are told work finished instead of
// polling check_async_command or blocking on wait.
func (s *Session) observeCommandOutcomes(manager *fileio.AsyncCommandManager, agentID string) {
	if manager == nil || agentID == "" {
		return
	}
	manager.SetSettleObserver(func(info fileio.AsyncCmdInfo) { s.publishCommandOutcome(agentID, info) })
}

// publishCommandOutcome delivers one settled background command through the
// durable collaboration journal: a running turn admits it at its next safe
// boundary and an idle agent is woken by it. An explicit kill is not reported;
// whoever stopped the command already knows, and a human Terminate must not
// restart autonomous work. Completion, failure and timeout are reported.
func (s *Session) publishCommandOutcome(agentID string, info fileio.AsyncCmdInfo) {
	if info.Status == fileio.AsyncRunning || info.Status == fileio.AsyncKilled && info.Error == "killed" {
		return
	}
	if atomic.LoadInt32(&s.closing) != 0 {
		return
	}
	store := s.CollaborationStore()
	if store == nil {
		return
	}
	commandText := []rune(info.Command)
	if len(commandText) > 300 {
		commandText = append(commandText[:300], '…')
	}
	body := fmt.Sprintf("Background command %q settled after %s: %s\nCommand: %s",
		info.Handle, info.Duration.Round(time.Second), FormatCommandStatus(info), string(commandText))
	_, err := store.Publish(context.Background(), collaboration.PublishRequest{
		SenderID: agentID, SenderName: "Background command " + info.Handle,
		IdempotencyKey: fmt.Sprintf("command-outcome:%s:%s:%d:%d", agentID, info.Handle, info.PID, info.StartedAt.UnixNano()),
		RecipientIDs:   []string{agentID},
		Origin:         collaboration.OriginRuntime, Kind: collaboration.EventCommandOutcome,
		Body: body,
		Command: &command.Outcome{
			Handle: info.Handle, Command: info.Command, PID: info.PID, Status: string(info.Status),
			StartedAt: info.StartedAt, EndedAt: info.EndedAt, Duration: info.Duration,
			ExitCode: info.ExitCode, ExitKnown: info.Status == fileio.AsyncCompleted || info.Status == fileio.AsyncFailed,
			OutputBytes: info.OutputBytes, Error: info.Error,
			Output: info.SettledOutput,
		},
	})
	if err != nil && s.logger != nil && !errors.Is(err, collaboration.ErrNotFound) {
		s.logger.Warn("ASYNC", "command_outcome_publish_failed", "agent_id", agentID, "handle", info.Handle, "error", err.Error())
	}
}
