package capability

import (
	"context"
	captypes "crude/types/capability"
	"encoding/json"
	"errors"
)

// Settlement uses the same passive projection as the next model request. It
// never admits a new run or starts a connection from a background completion.
func (s *Service) observedActivation(ctx context.Context, actor captypes.RuntimeActor, id string, clients actorClient) (captypes.Result, error) {
	if err := ctx.Err(); err != nil {
		return captypes.Result{}, err
	}
	s.mu.RLock()
	settings := s.settings
	s.mu.RUnlock()
	states := clients.Catalog()
	var prepared captypes.RuntimeSnapshot
	s.selectTools(actor, settings, states, &prepared)
	result := captypes.ActivationResult{ConnectionID: id, Notices: prepared.Notices}
	for _, state := range states {
		if state.ID == id {
			result.State = state.State
			break
		}
	}
	if result.State != "connected" {
		return captypes.Result{}, errors.New("MCP connection changed before discovery completed")
	}
	for _, tool := range prepared.Tools {
		if tool.ConnectionID == id {
			result.Usable = append(result.Usable, captypes.UsableTool{Name: tool.Name, Tool: tool.OriginalName, Effect: tool.Effect, Scope: tool.Scope})
		}
	}
	if len(result.Usable) > 0 {
		result.Next = "Call the tools by name in the next request."
	} else {
		result.Next = "No tools are available to this agent. Check the connection and tool settings."
	}
	encoded, err := json.Marshal(result)
	return textResult(string(encoded)), err
}
