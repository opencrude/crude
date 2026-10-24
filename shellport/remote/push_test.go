package remote

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
	"github.com/go-git/go-git/v5"
)

type pushSink struct{ messages chan any }

func (s pushSink) Send(msg any) { s.messages <- msg }
func pushNext(t *testing.T, ch <-chan any) shellport.ReplicaChangesMsg {
	t.Helper()
	for {
		select {
		case msg := <-ch:
			if batch, ok := msg.(shellport.ReplicaChangesMsg); ok {
				return batch
			}
		case <-time.After(5 * time.Second):
			t.Fatal("pushed publication did not arrive")
			return shellport.ReplicaChangesMsg{}
		}
	}
}
func pushUntil(t *testing.T, w *Workspace, ch <-chan any, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for !condition() {
		select {
		case msg := <-ch:
			if batch, ok := msg.(shellport.ReplicaChangesMsg); ok {
				applied := w.ApplyReplicaChanges(batch)
				for _, follow := range applied.Follow {
					if failure, ok := follow.(shellport.ReplicaFailureMsg); ok {
						t.Fatalf("replica failure: %+v", failure)
					}
				}
			}
		case <-deadline.C:
			t.Fatal("replica did not converge")
		}
	}
}

func TestPushConnectedTwoShellsSlowUIAndReset(t *testing.T) {
	t.Setenv("CRUDE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	server, err := core.New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "tester", types.Settings{}, nil)
		ws.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}, Memory: types.MemorySettings{AutoRecall: "off", AutoExtract: "off", Consolidation: "off"}})
		ws.RegisterAgentRoot("root")
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	dial := func(ctx context.Context, id string) (*coreclient.Client, error) {
		a, b := net.Pipe()
		go func() { _ = core.ServeConnection(t.Context(), server, a) }()
		return coreclient.Dial(ctx, coreclient.SingleCarrier(b), id)
	}
	root := t.TempDir()
	if _, err := git.PlainInit(root, false); err != nil {
		t.Fatal(err)
	}
	makeShell := func() (*Workspace, chan any) {
		w, err := Attach(t.Context(), Options{Root: root, Dial: dial})
		if err != nil {
			t.Fatal(err)
		}
		ch := make(chan any, 32)
		bus := workspace.NewEventBus()
		bus.SetSink(pushSink{ch})
		w.SetEventBus(bus)
		w.StartReplica()
		return w, ch
	}
	fast, fastMessages := makeShell()
	defer fast.Close()
	slow, slowMessages := makeShell()
	defer slow.Close()
	pushUntil(t, fast, fastMessages, func() bool { return fast.initialized })
	var held shellport.ReplicaChangesMsg
	for {
		msg := pushNext(t, slowMessages)
		if batch := msg.Batch.(publicationBatch); batch.topic == coreprotocol.TopicWorkspace {
			held = msg
			break
		}
		slow.ApplyReplicaChanges(msg)
	}
	if slow.initialized {
		t.Fatal("queued snapshot installed before UI commit")
	}
	// Hold the slow shell's initial snapshot uncommitted while the peer and core
	// continue beyond the retained journal. This must recover by a pushed reset.
	for i := 0; i < 280; i++ {
		text := fmt.Sprintf("entry-%03d", i)
		var out coreprotocol.BoolResult
		if err := fast.call(t.Context(), coreprotocol.AgentAppendMessages, &coreprotocol.AppendMessagesRequest{AgentID: "root", Messages: []workspace.Message{{InternalID: text, Role: workspace.MessageRoleAssistant, Content: strings.Repeat("x", 30)}}}, &out); err != nil || !out.OK {
			t.Fatalf("append %d: %+v %v", i, out, err)
		}
		pushUntil(t, fast, fastMessages, func() bool { return len(fast.view.AgentMgr.Get("root").State.Messages.Messages()) == i+1 })
	}
	// No second log batch may be admitted before the UI acknowledges the held
	// snapshot. Independent state topics remain free to publish in the meantime.
	draining := true
	for draining {
		select {
		case msg := <-slowMessages:
			if batch, ok := msg.(shellport.ReplicaChangesMsg); ok {
				if publication, ok := batch.Batch.(publicationBatch); ok && publication.topic == coreprotocol.TopicWorkspace {
					t.Fatal("log advanced before UI-applied ACK")
				}
				slow.ApplyReplicaChanges(batch)
			}
		default:
			draining = false
		}
	}
	slow.ApplyReplicaChanges(held)
	pushUntil(t, slow, slowMessages, func() bool {
		return slow.initialized && len(slow.view.AgentMgr.Get("root").State.Messages.Messages()) == 280
	})
	if fast.view.AgentMgr.Get("root") == slow.view.AgentMgr.Get("root") {
		t.Fatal("connected shells share mutable nodes")
	}
	var notice coreprotocol.StatusMutationResult
	if err := fast.call(t.Context(), coreprotocol.StatusMutateMethod, &coreprotocol.StatusMutation{Op: coreprotocol.StatusNotify, Notification: &types.NotificationRecord{Summary: "peer notice"}}, &notice); err != nil {
		t.Fatal(err)
	}
	pushUntil(t, slow, slowMessages, func() bool {
		for _, n := range slow.status.Notifications(false) {
			if n.Summary == "peer notice" {
				return true
			}
		}
		return false
	})
}

func TestPushRejectsMalformedAtomicallyAndCancelsStaleGeneration(t *testing.T) {
	snapshot := coreprotocol.WorkspaceSnapshot{ID: "w", Root: "/project", Revision: 1, Agents: []workspace.AgentSnapshot{{ID: "root", ConversationRevision: 1, Messages: []workspace.Message{{InternalID: "m", Role: workspace.MessageRoleAssistant, Content: "a"}}}}}
	view, err := coreclient.NewWorkspaceView(nil, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w := &Workspace{ctx: ctx, view: view, initialized: true, logger: nopLogger{}, topicClients: make(map[string]*coreclient.Client), topicCursors: map[string]uint64{coreprotocol.TopicWorkspace: 1}}
	malformed := publicationBatch{ctx: ctx, topic: coreprotocol.TopicWorkspace, header: coreprotocol.TopicHeader{Seq: 2}, done: make(chan error, 1), update: coreprotocol.WorkspaceUpdate{Changes: coreprotocol.AgentChangesResult{Revision: 2}, Conversations: []coreprotocol.ConversationUpdate{{AgentID: "root", Delta: workspace.ConversationDelta{BaseRevision: 1, Revision: 2, Appends: []workspace.ConversationTextAppend{{InternalID: "m", Offset: 9, Text: "bad"}}}}}, Notices: []workspace.AgentNotice{{}}}}
	applied := w.ApplyReplicaChanges(shellport.ReplicaChangesMsg{Owner: w, Batch: malformed})
	if err := <-malformed.done; !errors.Is(err, coreclient.ErrTopicReset) {
		t.Fatalf("bad update acknowledged: %v", err)
	}
	if view.Revision() != 1 || w.topicCursors[coreprotocol.TopicWorkspace] != 1 || len(applied.Notices) != 0 || len(applied.Reductions) != 0 || view.AgentMgr.Get("root").State.Messages.Messages()[0].Content != "a" {
		t.Fatal("malformed publication advanced cursor/state/effects")
	}
	staleCtx, staleCancel := context.WithCancel(ctx)
	staleCancel()
	malformed.ctx = staleCtx
	malformed.done = make(chan error, 1)
	applied = w.ApplyReplicaChanges(shellport.ReplicaChangesMsg{Owner: w, Batch: malformed})
	if !errors.Is(<-malformed.done, context.Canceled) || len(applied.Follow) != 0 {
		t.Fatal("cancelled subscription installed stale batch")
	}
}

func TestPushStateTopicsIndependentAndWrongProjectionRejected(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w := &Workspace{ctx: ctx, logger: nopLogger{}, topicClients: make(map[string]*coreclient.Client), topicCursors: make(map[string]uint64)}
	w.status = newStatusReplica(w)
	w.settings = &settingsReplica{w: w}
	w.lsp = newLSPReplica(w)
	w.terminals = &terminalReplica{w: w}
	apply := func(name string, seq uint64, state coreprotocol.WorkspaceState) error {
		b := publicationBatch{ctx: ctx, topic: name, header: coreprotocol.TopicHeader{Seq: seq, Reset: true}, state: state, done: make(chan error, 1)}
		w.ApplyReplicaChanges(shellport.ReplicaChangesMsg{Owner: w, Batch: b})
		return <-b.done
	}
	status := coreprotocol.StatusSnapshot{Version: 100, Notifications: []types.NotificationRecord{{ID: "notice", Summary: "retained"}}}
	if err := apply(coreprotocol.TopicStatus, 100, coreprotocol.WorkspaceState{Status: &status}); err != nil {
		t.Fatal(err)
	}
	settings := coreprotocol.SettingsFetchResult{}
	defaults := types.AgentsSettings{}
	if err := apply(coreprotocol.TopicSettings, 2, coreprotocol.WorkspaceState{Settings: &settings, AgentDefaults: &defaults}); err != nil {
		t.Fatal(err)
	}
	if w.topicCursors[coreprotocol.TopicStatus] != 100 || w.topicCursors[coreprotocol.TopicSettings] != 2 {
		t.Fatal("independent topics shared a sequence domain")
	}
	malformed := coreprotocol.StatusSnapshot{Version: 101, Notifications: []types.NotificationRecord{{ID: "bad", Summary: "must not install"}}}
	if err := apply(coreprotocol.TopicSettings, 3, coreprotocol.WorkspaceState{Status: &malformed}); !errors.Is(err, coreclient.ErrTopicReset) {
		t.Fatalf("wrong projection acknowledged: %v", err)
	}
	if w.status.core.Version != 100 || w.topicCursors[coreprotocol.TopicSettings] != 2 {
		t.Fatal("wrong projection partially installed")
	}
	w.ApplyPreferencesSave(workspace.PreferencesSaveResult{AppSettings: types.AppSettings{Onboarding: types.OnboardingSettings{IntroductionCompleted: true}}})
	if w.settings.current.fetch.Application.Onboarding.IntroductionCompleted {
		t.Fatal("unsequenced save result overwrote pushed settings")
	}
}
