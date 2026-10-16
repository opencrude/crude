package session

import (
	"context"
	"strings"
	"testing"

	"crude/agent"
	"crude/provider"
	captypes "crude/types/capability"
)

type sharingRuntime struct{}

func (sharingRuntime) Prepare(context.Context, captypes.RuntimeActor) (captypes.RuntimeSnapshot, error) {
	return captypes.RuntimeSnapshot{}, nil
}

func (sharingRuntime) Execute(_ context.Context, _ captypes.RuntimeActor, _ captypes.RuntimeSnapshot, tool string, params map[string]any) (captypes.Result, error) {
	return captypes.Result{Content: []captypes.Content{{Type: "text", Text: "ok " + tool}}}, nil
}

func sharingAgentFor(t *testing.T, s *Session, actor captypes.RuntimeActor, name string) *agent.Agent {
	t.Helper()
	p := provider.FromPreset("openai-chat", "key", "http://127.0.0.1:0")
	live := agent.NewAgent(p, "model", nil)
	s.bindAgentRuntime(t.Context(), live, actor, name)
	return live
}

func sharedNotices(live *agent.Agent) string {
	_, notices := live.PrepareStep()
	var texts []string
	for _, notice := range notices {
		texts = append(texts, notice.ContentString())
	}
	return strings.Join(texts, "\n")
}

// A capability one agent of a session makes usable is announced once to its
// parent and children at their next request, so they reuse it instead of
// searching for and installing another.
func TestActivatedCapabilityIsSharedWithParentAndChildren(t *testing.T) {
	s := newTestSessionForPlan(t)
	s.SetCapabilities(sharingRuntime{}, agent.NewFileOwnership())
	root := sharingAgentFor(t, s, captypes.RuntimeActor{ID: "root", SessionID: "s"}, "Ada")
	child := sharingAgentFor(t, s, captypes.RuntimeActor{ID: "child", ParentID: "root", SessionID: "s"}, "Naoki")
	sibling := sharingAgentFor(t, s, captypes.RuntimeActor{ID: "sibling", ParentID: "root", SessionID: "s"}, "Lily")

	if _, err := child.ToolFn(t.Context(), "capability_activate", map[string]any{"id": "registry:screenshots"}); err != nil {
		t.Fatal(err)
	}
	got := sharedNotices(root)
	if !strings.Contains(got, `agent="Naoki" relation="child" operation="activated" id="registry:screenshots"`) {
		t.Fatalf("parent notice = %q", got)
	}
	if got := sharedNotices(sibling); !strings.Contains(got, `relation="peer"`) {
		t.Fatalf("sibling notice = %q", got)
	}
	if got := sharedNotices(child); strings.Contains(got, "runtime_capability_shared") {
		t.Fatalf("the activating agent was told about its own activation: %q", got)
	}
	if got := sharedNotices(root); strings.Contains(got, "runtime_capability_shared") {
		t.Fatalf("a delivered notice was repeated: %q", got)
	}

	// The parent granting access reaches the child as its parent's change;
	// reactivating an announced capability announces nothing.
	if _, err := root.ToolFn(t.Context(), "mcp_request_access", map[string]any{"connection_id": "screenshots", "grants": []any{}}); err != nil {
		t.Fatal(err)
	}
	if got := sharedNotices(child); !strings.Contains(got, `agent="Ada" relation="parent" operation="granted access to" id="screenshots"`) {
		t.Fatalf("child notice = %q", got)
	}
	if _, err := root.ToolFn(t.Context(), "capability_activate", map[string]any{"id": "registry:screenshots"}); err != nil {
		t.Fatal(err)
	}
	if got := sharedNotices(child); strings.Contains(got, "registry:screenshots") {
		t.Fatalf("an already announced capability was announced again: %q", got)
	}
}
