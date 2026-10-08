package session

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
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
	s.msgsMu.Lock()
	defer s.msgsMu.Unlock()
	current, found, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
	if err != nil {
		return err
	}
	if current.Revision < checkpoint.ContextRevision {
		return &sessiondb.ContextRevisionConflictError{SessionID: s.sessionID, AgentID: s.sessionID, Expected: checkpoint.ContextRevision, Actual: current.Revision}
	}
	if !found && s.contextRevision != 0 {
		return &sessiondb.ContextRevisionConflictError{SessionID: s.sessionID, AgentID: s.sessionID, Expected: s.contextRevision, Actual: 0}
	}
	// Reuse exact stored row metadata. PublishContext separately validates the
	// full identity and the messages table's persisted projection in its transaction.
	rows, err := s.db.Query(`SELECT internal_id,turn_number,created_at,COALESCE(agent_id,''),display_meta FROM messages ORDER BY sequence_num`)
	if err != nil {
		return err
	}
	defer rows.Close()
	stored := make(map[string]sessiondb.ContextMessageDelta)
	positions := make(map[string]int)
	var transcriptIDs []string
	for rows.Next() {
		var id string
		var delta sessiondb.ContextMessageDelta
		var meta sql.NullString
		if err := rows.Scan(&id, &delta.Turn, &delta.CreatedAt, &delta.AgentID, &meta); err != nil {
			return err
		}
		if id == "" {
			return fmt.Errorf("recovery transcript has no immutable message identity")
		}
		if _, duplicate := stored[id]; duplicate {
			return &sessiondb.ContextMessageConflictError{InternalID: id, Location: "duplicate recovery transcript"}
		}
		if meta.Valid && meta.String != "" {
			if err := json.Unmarshal([]byte(meta.String), &delta.Meta); err != nil {
				return err
			}
		}
		stored[id] = delta
		positions[id] = len(transcriptIDs)
		transcriptIDs = append(transcriptIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	rows, err = s.db.Query(`SELECT internal_id,message_json FROM immutable_message_identities WHERE session_id=? AND agent_id=?`, s.sessionID, s.sessionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	identities := make(map[string]map[string]json.RawMessage)
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		var identity map[string]json.RawMessage
		if err := json.Unmarshal(raw, &identity); err != nil {
			return err
		}
		identities[id] = identity
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	archive := recoveryArchiveMessages(checkpoint.Archive)
	seen := make(map[string]bool, len(archive))
	lastMatched := -1
	for _, message := range archive {
		id := message.InternalID
		if id == "" {
			continue
		}
		if seen[id] {
			return &sessiondb.ContextMessageConflictError{InternalID: id, Location: "duplicate recovery archive"}
		}
		seen[id] = true
		if position, exists := positions[id]; exists {
			lastMatched = max(lastMatched, position)
		}
	}
	// A committed transcript tail can outrun settlement, but it must be a
	// proven append to this archive, never an arbitrary semantic merge.
	for position, id := range transcriptIDs {
		if seen[id] {
			continue
		}
		if position <= lastMatched || current.Revision <= checkpoint.ContextRevision {
			return fmt.Errorf("recovery archive omits committed transcript identity %q", id)
		}
		var message provider.ChatMessage
		if err := json.Unmarshal(identities[id]["message"], &message); err != nil {
			return fmt.Errorf("load immutable transcript identity %q: %w", id, err)
		}
		archive = append(archive, message)
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
	var deltas []sessiondb.ContextMessageDelta
	var metas []sessiondb.DisplayMeta
	var turns []int
	var times []int64
	for i := range archive {
		message := archive[i]
		anonymous := message.InternalID == ""
		if message.InternalID == "" {
			message.InternalID = newMessageID()
			archive[i].InternalID = message.InternalID
		}
		canonical := canonicalContextMessage(message)
		identity, exists := identities[message.InternalID]
		if !exists && found && !anonymous {
			return &sessiondb.ContextMessageConflictError{InternalID: message.InternalID, Location: "missing recovery immutable identity"}
		}
		if exists {
			encoded, err := json.Marshal(canonical)
			if err != nil {
				return err
			}
			// Compare the complete stored identity, including nil-shape facts
			// omitted by ChatMessage JSON, independently of transcript projection.
			flags := map[string]bool{
				"nil_reference_labels":  canonical.ReferenceLabels == nil,
				"nil_tool_calls":        canonical.ToolCalls == nil,
				"nil_reasoning_details": canonical.ReasoningDetails == nil,
				"nil_provider_items":    canonical.ProviderItems == nil,
			}
			if !bytes.Equal(identity["message"], encoded) {
				return &sessiondb.ContextMessageConflictError{InternalID: message.InternalID, Location: "recovery immutable identity"}
			}
			for key, flag := range flags {
				encoded, err := json.Marshal(flag)
				if err != nil {
					return err
				}
				if !bytes.Equal(identity[key], encoded) {
					return &sessiondb.ContextMessageConflictError{InternalID: message.InternalID, Location: "recovery immutable identity"}
				}
			}
		}
		if delta, exists := stored[message.InternalID]; exists {
			delta.Message = canonical
			deltas = append(deltas, delta)
			metas = append(metas, delta.Meta)
			turns = append(turns, delta.Turn)
			times = append(times, delta.CreatedAt)
			continue
		}
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
		metas = append(metas, meta)
		turns = append(turns, s.turnNumber)
		times = append(times, when.UnixNano())
		if !meta.ContextOnly {
			deltas = append(deltas, contextDelta(message, meta, s.turnNumber, when, checkpoint.Status.AgentID))
		}
	}
	active := checkpoint.ActiveContext
	generation, reason := max(uint64(1), checkpoint.ContextGeneration), "passive_recovery"
	if found {
		generation, reason = current.Generation, current.Reason
		if current.Revision == checkpoint.ContextRevision && !retainsContextSource(active, current.Messages) {
			return fmt.Errorf("recovery context does not retain committed revision %d", current.Revision)
		}
		active = reconcileInterruptedTools(current.Messages)
	}
	active = canonicalContextMessages(inheritContextMessageIDs(active, archive))
	// Compacted active contexts are not archive-shaped. Match anonymous accepted
	// outcomes by their exact canonical bytes, preserving repeated occurrences.
	if slices.ContainsFunc(active, func(message provider.ChatMessage) bool { return message.InternalID == "" }) {
		activeIDs := make(map[string]bool, len(active))
		for _, message := range active {
			activeIDs[message.InternalID] = true
		}
		anonymousIDs := make(map[string][]string)
		for _, message := range archive {
			id := message.InternalID
			if activeIDs[id] {
				continue
			}
			message = canonicalContextMessage(message)
			message.InternalID = ""
			encoded, err := json.Marshal(message)
			if err != nil {
				return err
			}
			anonymousIDs[string(encoded)] = append(anonymousIDs[string(encoded)], id)
		}
		for i, message := range active {
			if message.InternalID != "" {
				continue
			}
			encoded, err := json.Marshal(message)
			if err != nil {
				return err
			}
			ids := anonymousIDs[string(encoded)]
			if len(ids) == 0 {
				return fmt.Errorf("recovery active message has no exact archive identity")
			}
			active[i].InternalID = ids[0]
			anonymousIDs[string(encoded)] = ids[1:]
		}
	}
	durable := *checkpoint
	durable.Archive, durable.ActiveContext = archive, active
	state, err := s.publishRootContext(sessiondb.ContextPublication{
		ExpectedRevision: current.Revision,
		TranscriptAppend: deltas,
		RuntimeAgentID:   checkpoint.Status.AgentID, Recovery: &durable,
		Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{
			Generation: generation, Reason: reason, Messages: active,
		}},
	})
	if err != nil {
		return err
	}
	s.msgs = append([]provider.ChatMessage(nil), archive...)
	for i := range s.msgs {
		s.msgs[i].DisplayContent = metas[i].DisplayContent
		s.msgs[i].ReferenceLabels = append([]string(nil), metas[i].ReferenceLabels...)
	}
	s.msgTurns, s.displayMetas, s.createdAts = turns, metas, times
	checkpoint.Archive, checkpoint.ActiveContext = archive, append([]provider.ChatMessage(nil), state.Messages...)
	checkpoint.ContextRevision, checkpoint.ContextGeneration, checkpoint.ContextReason = state.Revision, state.Generation, state.Reason
	return nil
}
