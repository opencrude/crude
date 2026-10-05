package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"crude/provider"
	"crude/sessiondb"
)

// reconcileInterruptedTools closes only missing outcomes, never rerunning a
// historical call or claiming that an unknown side effect was cancelled.
func reconcileInterruptedTools(messages []provider.ChatMessage) []provider.ChatMessage {
	result := make([]provider.ChatMessage, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		message := messages[i]
		result = append(result, message)
		if message.Role != "assistant" || len(message.ToolCalls) == 0 {
			continue
		}
		present := make(map[string]bool)
		for i+1 < len(messages) && messages[i+1].Role == "tool" {
			i++
			result = append(result, messages[i])
			present[messages[i].ToolCallID] = true
		}
		for _, call := range message.ToolCalls {
			if !present[call.ID] {
				result = append(result, provider.ChatMessage{
					Role: "tool", ToolCallID: call.ID, ToolError: true,
					Content: provider.TextContent("Outcome unknown: execution was interrupted before a durable result was recorded. This call has not been replayed."),
				})
			}
		}
	}
	return result
}

// Restore the durable accepted tail to the ordinary transcript before exposing
// a resumed session. Existing snapshots retain their identity and timestamps.
// The recovery envelope is current-format state, not a legacy fallback reader.
func (s *Session) restoreRecoveryTranscript(checkpoint *sessiondb.RecoveryCheckpoint) error {
	archive := recoveryArchiveMessages(checkpoint.Archive)
	for i := 0; i < min(len(archive), len(s.msgs)); i++ {
		old := s.msgs[i]
		current := archive[i]
		old.Origin, old.Visibility = old.EffectiveOrigin(), old.EffectiveVisibility()
		current.Origin, current.Visibility = current.EffectiveOrigin(), current.EffectiveVisibility()
		left, err := json.Marshal(old)
		if err != nil {
			return err
		}
		right, err := json.Marshal(current)
		if err != nil {
			return err
		}
		if !bytes.Equal(left, right) {
			return fmt.Errorf("recovery transcript differs from accepted prefix at message %d", i)
		}
	}
	if len(archive) < len(s.msgs) {
		// Finalization can commit its mirror before settlement. Keep the
		// proven newer canonical tail and its matching active context.
		checkpoint.Archive = append([]provider.ChatMessage(nil), s.msgs...)
		checkpoint.ActiveContext = append([]provider.ChatMessage(nil), s.activeContext...)
		checkpoint.ContextGeneration = s.contextGeneration
		checkpoint.ContextReason = s.contextReason
		return nil
	}
	args := toolCallArgsIndex(archive)
	names := make(map[string]string)
	for _, message := range archive {
		for _, call := range message.ToolCalls {
			names[call.ID] = call.Function.Name
		}
	}
	when := checkpoint.Status.UpdatedAt
	if when.IsZero() {
		when = checkpoint.Status.StartedAt
	}
	if when.IsZero() {
		when = time.Now()
	}
	for i := len(s.msgs); i < len(archive); i++ {
		message := archive[i]
		meta := sessiondb.DisplayMeta{DisplayRole: message.Role}
		if isProviderControlMessage(message) || message.Role == "system" {
			meta.ContextOnly, meta.DisplayRole = true, "hidden_runtime_context"
		}
		if message.Role == "tool" {
			meta.ToolName, meta.ToolArgs = names[message.ToolCallID], args[message.ToolCallID]
			if message.ToolError {
				meta.ToolStatus = "error"
			}
		}
		if message.Role == "assistant" || message.Role == "tool" {
			meta.TurnGeneration = checkpoint.Status.Generation
		}
		meta = canonicalizeDisplayMeta(message, meta, args[message.ToolCallID], i, when)
		s.msgs = append(s.msgs, message)
		s.msgTurns = append(s.msgTurns, s.turnNumber)
		s.displayMetas = append(s.displayMetas, meta)
		s.createdAts = append(s.createdAts, when.UnixNano())
	}
	s.appendToWriteBuf(s.msgs, s.msgTurns, s.displayMetas)
	return s.flushWriteBufSync()
}
