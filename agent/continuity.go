package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"crude/fileio"
	rootprompt "crude/prompt"
	"crude/provider"
)

func (a *Agent) continuityMessage(ctx context.Context, compacted bool) (provider.ChatMessage, error) {
	if err := ctx.Err(); err != nil {
		return provider.ChatMessage{}, err
	}
	state := struct {
		AsOfMS              int64                        `json:"as_of_unix_ms"`
		AgentID             AgentID                      `json:"agent_id"`
		ParentID            AgentID                      `json:"parent_id,omitempty"`
		TurnGeneration      uint64                       `json:"turn_generation"`
		WorkDir             string                       `json:"working_directory"`
		AsyncCommands       []fileio.AsyncCommandContext `json:"async_commands"`
		Children            []SubAgentStatusEntry        `json:"children"`
		FinalReportRequired bool                         `json:"final_report_required"`
	}{
		AsOfMS: time.Now().UnixMilli(), AgentID: a.AgentID, ParentID: a.ParentID,
		TurnGeneration: a.TurnGeneration, WorkDir: a.WorkDir,
		AsyncCommands: []fileio.AsyncCommandContext{}, Children: []SubAgentStatusEntry{},
		FinalReportRequired: a.Depth > 0 && !a.reportPublished,
	}
	if a.AsyncMgr != nil {
		state.AsyncCommands = a.AsyncMgr.ContextSnapshot()
		slices.SortFunc(state.AsyncCommands, func(left, right fileio.AsyncCommandContext) int { return strings.Compare(left.Handle, right.Handle) })
	}
	if a.SubAgentMgr != nil {
		state.Children = a.SubAgentMgr.ListStatus(true)
		slices.SortFunc(state.Children, func(left, right SubAgentStatusEntry) int {
			return strings.Compare(string(left.AgentID), string(right.AgentID))
		})
	}
	data, err := json.Marshal(state)
	if err != nil {
		return provider.ChatMessage{}, fmt.Errorf("encode authoritative runtime state: %w", err)
	}
	var session rootprompt.SessionContinuityData
	if a.ContinuationContext != nil {
		session, err = a.ContinuationContext(ctx)
		if err != nil {
			return provider.ChatMessage{}, fmt.Errorf("read authoritative session continuity: %w", err)
		}
	}
	message := provider.NewTextMessage("user", rootprompt.RuntimeContinuity(string(data), compacted, session))
	message.InternalID = provider.NewMessageID()
	message.Origin, message.Visibility = provider.OriginRuntimeContext, provider.VisibilityContextOnly
	return message, ctx.Err()
}
