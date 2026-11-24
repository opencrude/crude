package session

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"crude/agent/collaboration"
	"crude/agent/tools"
	"crude/types/command"
	"crude/workspace/fileio"
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
	if info.Status == fileio.AsyncRunning || info.Status == fileio.AsyncKilled && !info.TimedOut && !info.Uncertain {
		return
	}
	if atomic.LoadInt32(&s.closing) != 0 {
		return
	}
	store := s.CollaborationStore()
	if store == nil {
		return
	}
	body := commandOutcomeBody(info)
	sender := "Background command " + info.Handle
	if info.TaskID != "" {
		sender = "Background job " + info.Handle
	}
	_, err := store.Publish(context.Background(), collaboration.PublishRequest{
		SenderID: agentID, SenderName: sender,
		IdempotencyKey: fmt.Sprintf("command-outcome:%s:%s:%d:%d", agentID, info.Handle, info.PID, info.StartedAt.UnixNano()),
		RecipientIDs:   []string{agentID},
		Origin:         collaboration.OriginRuntime, Kind: collaboration.EventCommandOutcome,
		Body: body,
		Command: &command.Outcome{
			TaskID: info.TaskID, Handle: info.Handle, Command: info.Command, PID: info.PID, Status: string(info.Status),
			StartedAt: info.StartedAt, EndedAt: info.EndedAt, Duration: info.Duration,
			ExitCode: info.ExitCode, ExitKnown: info.HasExitCode(),
			OutputBytes: info.OutputBytes, Error: info.Error,
			TimedOut: info.TimedOut, Uncertain: info.Uncertain,
			Output: info.SettledOutput,
		},
	})
	if err != nil && s.logger != nil && !errors.Is(err, collaboration.ErrNotFound) {
		s.logger.Warn("ASYNC", "command_outcome_publish_failed", "agent_id", agentID, "handle", info.Handle, "error", err.Error())
	}
}

// commandOutcomeInlineBytes bounds a delivery body that carries the complete
// output. The journal stores the typed excerpt beside the body, so a small
// body keeps the event within its page budget even for escape-heavy output.
const commandOutcomeInlineBytes = 16 << 10

// commandOutcomeBody is what the owning agent reads. Short complete output
// (a test summary, "DONE") is included, as a synchronous result would show
// it, so the agent need not spend a turn on read_async_output. Longer or
// partial output keeps the status and the exact read cursor.
func commandOutcomeBody(info fileio.AsyncCmdInfo) string {
	prefix := fmt.Sprintf("Background command %q settled.\n", info.Handle)
	if info.TaskID != "" {
		// An activation is in-process work, not a shell command: name it.
		prefix = fmt.Sprintf("Background job %q (%s) settled.\n", info.Handle, info.Command)
	}
	if settled := info.SettledOutput; settled != nil && settled.OutputDescriptor.OmittedBytes == 0 {
		result := *settled
		result.Retrieval = ""
		if body := prefix + tools.CommandResultContent(result); len(body) <= commandOutcomeInlineBytes {
			return body
		}
	}
	return prefix + FormatCommandStatus(info)
}
