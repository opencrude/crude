package chatprojection

import (
	"crude/types"
	"crude/types/command"
)

type commandOrigin struct {
	index   int
	message *types.ChatMessage
}

type commandSettlement struct {
	index   int
	message *types.ChatMessage
}

func commandStart(message *types.ChatMessage) bool {
	switch message.ToolName {
	case "execute_command", "async_execute_command", "exec_command":
		return message.ToolOutcome.ID != ""
	}
	return false
}

func settlementID(message *types.ChatMessage) string {
	if !isCommandOutcome(message) || message.Collaboration.Command == nil {
		return ""
	}
	info := message.Collaboration.Command
	return command.ExecutionID(info.PID, info.StartedAt)
}

// indexCommandEvidence runs only on a source change. It widens the incremental
// rebuild to an originating row when a later exact settlement changes that row.
// The canonical audit and journal messages are never rewritten or acknowledged.
func (r *Renderer) indexCommandEvidence(canonical []*types.ChatMessage, from int, changed map[string]bool) int {
	if r.commandOrigins == nil || from == 0 {
		r.commandOrigins = make(map[string]commandOrigin)
		r.commandSettlements = make(map[string]commandSettlement)
		r.commandRefs = make(map[string][]string)
	}
	affected := make(map[string]bool)
	for _, prior := range r.activityCanon[min(from, len(r.activityCanon)):] {
		for _, id := range r.commandRefs[prior.InternalID] {
			if origin, ok := r.commandOrigins[id]; ok && origin.index >= from {
				delete(r.commandOrigins, id)
			}
			if settlement, ok := r.commandSettlements[id]; ok && settlement.index >= from {
				delete(r.commandSettlements, id)
			}
			affected[id] = true
		}
		delete(r.commandRefs, prior.InternalID)
	}
	for index := from; index < len(canonical); index++ {
		message := canonical[index]
		leaves := []*types.ChatMessage{message}
		if message.ToolName == "multi_tool_use.parallel" {
			batch, ok := r.activityBatches[message.InternalID]
			if !ok || changed[message.InternalID] {
				batch = parallelMessages(message)
				r.activityBatches[message.InternalID] = batch
			}
			leaves = batch
		}
		for _, leaf := range leaves {
			if commandStart(leaf) {
				id := leaf.ToolOutcome.ID
				// An execution has one originating call, not a later output poll.
				if _, exists := r.commandOrigins[id]; !exists {
					r.commandOrigins[id] = commandOrigin{index: index, message: leaf}
				}
				affected[id] = true
				r.commandRefs[message.InternalID] = append(r.commandRefs[message.InternalID], id)
			}
		}
		if id := settlementID(message); id != "" {
			r.commandSettlements[id] = commandSettlement{index: index, message: message}
			affected[id] = true
			r.commandRefs[message.InternalID] = append(r.commandRefs[message.InternalID], id)
		}
	}
	for id := range affected {
		if origin, exists := r.commandOrigins[id]; exists {
			from = min(from, origin.index)
			changed[origin.message.InternalID] = true
		}
	}
	return from
}

func (r *Renderer) projectCommandEvidence(message *types.ChatMessage) *types.ChatMessage {
	if commandStart(message) {
		if settlement, ok := r.commandSettlements[message.ToolOutcome.ID]; ok {
			// A presentation-only copy keeps the originating identity, operands,
			// permission decision and initial result available on expansion.
			projected := *message
			projected.Role = types.RoleUser
			projected.Collaboration = settlement.message.Collaboration
			return &projected
		}
	}
	if id := settlementID(message); id != "" {
		if _, exists := r.commandOrigins[id]; exists {
			return nil
		}
	}
	return message
}
