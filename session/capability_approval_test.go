package session

import (
	"path/filepath"
	"testing"

	"crude/agent"
	"crude/safety"
)

func TestCapabilityChangeDoesNotReuseGenericAlwaysReadApproval(t *testing.T) {
	owner, err := NewWithSessionDir("approval-test", testConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	target := filepath.Join(owner.WorkDir(), "exact-capability")
	owner.permStore.GrantAlways(safety.OpRead, "capability_activate", target)
	if !owner.permStore.IsAlwaysAllowed(safety.OpRead, "capability_activate", target) {
		t.Fatal("fixture failed to establish generic read permission")
	}
	orchestrator := NewOrchestrator("root", owner)
	sender := newMockEventSender()
	data := map[string]string{
		"block_id": "exact-grant-change", "operation": string(safety.OpRead),
		"tool": "capability_activate", "target": target, "question": "Approve exact schemas?",
	}
	orchestrator.handleBlockingEvent(data, agent.AgentEvent{Type: "blocking", Data: data}, sender)
	event := <-sender.ch
	if event.typ != "blocking" {
		t.Fatalf("generic Always permission approved another capability change: %s", event.typ)
	}
	select {
	case <-orchestrator.BlockResolveCh:
		t.Fatal("exact capability change was automatically answered")
	default:
	}
}
