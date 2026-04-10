package agent

import (
	"encoding/json"
	"fmt"

	"crude/dag"
	"crude/statemachine"
)

// execute runs the DAG with safety integration.
func (a *Agent) execute(
	execDAG *dag.DAG,
	emit func(AgentEvent),
	blockResolveCh <-chan string,
	sm *statemachine.ConversationStateMachine,
	sessionID string,
) {
	// Register goal in state machine.
	if sm != nil {
		sm.CreateGoal(sessionID, "execute_dag", "DAG execution", statemachine.PriorityNormal, false)
	}

	// Pre-check: capture rollback data for mutating operations.
	if a.RollbackHandler != nil {
		for _, n := range execDAG.Nodes {
			if n.Operation.IsDestructive() {
				if err := a.RollbackHandler.CapturePreState(n); err != nil {
					logAgent("rollback capture failed for %s: %v", n.ID, err)
				}
			}
		}
	}

	// Pre-check: mark nodes needing confirmation.
	for _, n := range execDAG.Nodes {
		if a.Policy.NeedsConfirmation(n.Operation) {
			n.RequiresConfirmation = true
			n.ConfirmationReason = fmt.Sprintf("%s operation: %s", n.Operation, n.Description)
		}
		if !a.Policy.IsAllowed(n.Operation) {
			n.Status = dag.Err
			n.RunErr = fmt.Errorf("operation %s not allowed by policy", n.Operation)
		}
	}

	eventFn := func(e dag.Event) {
		switch e.Type {
		case "node_start":
			argsJSON, _ := json.Marshal(e.Node.Parameters)
			emit(AgentEvent{Type: "tool_call", Data: map[string]string{
				"id":   e.Node.ID,
				"name": e.Node.Tool,
				"args": string(argsJSON),
			}})
		case "node_done":
			result := ""
			if s, ok := e.Data.(string); ok {
				result = s
			}
			emit(AgentEvent{Type: "tool_result", Data: map[string]string{
				"id":     e.Node.ID,
				"name":   e.Node.Tool,
				"result": result,
			}})
		case "node_error":
			errMsg := fmt.Sprintf("%v", e.Data)
			emit(AgentEvent{Type: "thinking", Data: "[node error: " + errMsg + "]"})
		case "node_skipped":
			reason := fmt.Sprintf("%v", e.Data)
			if reason == "user rejected confirmation" {
				emit(AgentEvent{Type: "thinking", Data: fmt.Sprintf(
					"[user denied %s on %s — do not retry this operation, inform the user and suggest alternatives]",
					e.Node.Operation, e.Node.Description)})
			} else {
				emit(AgentEvent{Type: "thinking", Data: "[node skipped: " + reason + "]"})
			}
		case "blocking":
			if data, ok := e.Data.(map[string]string); ok {
				emit(AgentEvent{Type: "blocking", Data: data})
			} else {
				question := fmt.Sprintf("%v", e.Data)
				emit(AgentEvent{Type: "blocking", Data: map[string]string{
					"question": question,
				}})
			}
		case "confirmation_always":
			target := ""
			if p, ok := e.Node.Parameters["path"]; ok {
				target = fmt.Sprintf("%v", p)
			} else if p, ok := e.Node.Parameters["destination"]; ok {
				target = fmt.Sprintf("%v", p)
			} else if p, ok := e.Node.Parameters["source"]; ok {
				target = fmt.Sprintf("%v", p)
			}
			emit(AgentEvent{Type: "confirmation_always", Data: map[string]string{
				"operation": string(e.Node.Operation),
				"tool":      e.Node.Tool,
				"target":    target,
			}})
		case "blocking_complete":
			emit(AgentEvent{Type: "blocking_complete", Data: e.Data})
		case "complete":
		}
	}

	executor := dag.New(execDAG, a.ToolFn, eventFn, blockResolveCh)

	// Wire interaction logger for detailed node logging.
	if a.Logger != nil {
		executor.SetInteractionLogger(a.Logger)
	}

	// Wire safety components.
	if a.RollbackHandler != nil {
		executor.SetRollbackFunc(a.RollbackHandler.Rollback)
	}
	executor.SetConfirmFunc(func(node *dag.Node) (bool, bool, error) {
		target := ""
		if p, ok := node.Parameters["path"]; ok {
			target = fmt.Sprintf("%v", p)
		} else if p, ok := node.Parameters["destination"]; ok {
			target = fmt.Sprintf("%v", p)
		} else if p, ok := node.Parameters["source"]; ok {
			target = fmt.Sprintf("%v", p)
		}
		question := fmt.Sprintf("Allow %s: %s?", node.Operation, node.Description)
		data := map[string]string{
			"question":  question,
			"operation": string(node.Operation),
			"tool":      node.Tool,
			"target":    target,
		}
		emit(AgentEvent{Type: "blocking", Data: data})
		answer, ok := <-blockResolveCh
		if !ok {
			return false, false, fmt.Errorf("confirmation channel closed")
		}
		switch answer {
		case "always":
			return true, true, nil
		case "allow":
			return true, false, nil
		default:
			return false, false, nil
		}
	})
	if a.AuditLogger != nil {
		executor.SetAuditFunc(a.AuditLogger.Record)
	}

	if runErr := executor.Run(); runErr != nil {
		logAgent("DAG execution error: %v", runErr)
	}

	if sm != nil {
		hasFailure := false
		for _, n := range execDAG.Nodes {
			if n.Status == dag.Err {
				hasFailure = true
				break
			}
		}
		if hasFailure {
			sm.FailCurrentGoal(sessionID, "DAG execution had failures")
		} else {
			sm.CompleteCurrentGoal(sessionID, nil)
		}
	}
}
