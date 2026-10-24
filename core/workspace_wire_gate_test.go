//go:build unix

package core

import (
	"context"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/types"
	"crude/workspace"
)

func TestWorkspaceTwentyKBReplyCompleteCarrierByteGate(t *testing.T) {
	r := failureRuntime(t)
	local, server := net.Pipe()
	wire := &terminalWireCounter{Conn: local}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); _ = ServeConnection(ctx, r, server) }()
	t.Cleanup(func() { cancel(); <-done })
	client, err := coreclient.Dial(ctx, coreclient.SingleCarrier(wire), "reply-wire-gate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	var attached coreprotocol.AttachResult
	resourceTopicCall(t, client, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: t.TempDir()}, &attached)
	r.mu.Lock()
	a := r.workspaces[attached.WorkspaceID]
	r.mu.Unlock()
	if err := a.inspect(ctx, func() {
		a.ws.RegisterAgentRoot("reply-owner")
		a.changed([]string{"reply-owner"})
	}); err != nil {
		t.Fatal(err)
	}
	var replica *coreclient.WorkspaceView // exclusively owned by the subscriber
	installed := make(chan string, 2)
	failures := make(chan error, 1)
	sub := client.Subscribe(ctx, attached.WorkspaceID, coreprotocol.TopicWorkspace, "", func(header coreprotocol.TopicHeader, body []byte) error {
		var update coreprotocol.WorkspaceUpdate
		err := coreprotocol.Decode(body, &update)
		if err == nil {
			if header.Reset {
				if update.Snapshot == nil {
					err = coreclient.ErrResync
				} else {
					replica, err = coreclient.NewWorkspaceView(client, *update.Snapshot)
				}
			} else if replica == nil {
				err = coreclient.ErrResync
			} else {
				_, err = replica.ApplyPublication(update)
			}
		}
		if err != nil {
			select {
			case failures <- err:
			default:
			}
			return err
		}
		var text strings.Builder
		for _, message := range replica.AgentMgr.Get("reply-owner").State.Messages.Messages() {
			text.WriteString(message.Content)
		}
		select {
		case installed <- text.String():
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	t.Cleanup(func() { cancel(); sub.Close() })
	await := func(want string) {
		t.Helper()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for {
			select {
			case got := <-installed:
				if got != want {
					t.Fatalf("streamed reply lost or duplicated text: have %d bytes, want %d", len(got), len(want))
				}
				return
			case err := <-failures:
				t.Fatal(err)
			case <-timer.C:
				t.Fatal("paced reply was not installed by the real subscriber")
			}
		}
	}
	await("")
	start := wire.received.Load()
	chunk := strings.Repeat("abcdefghijklmnop", 32)
	want := ""
	for range 40 {
		// Actual semantic actor ingress, not a re-encoded size proxy. Wait for
		// every 512-byte paced publication so conflation cannot hide overhead.
		a.SendAgentEventWithID("stream_token", chunk, "reply-owner")
		want += chunk
		await(want)
	}
	bytes := wire.received.Load() - start
	t.Logf("20 KiB reply in forty 512-byte publications: %d complete local carrier bytes, ratio %.4f", bytes, float64(bytes)/float64(len(want)))
	if bytes <= 0 || bytes*2 >= int64(len(want))*3 {
		t.Fatalf("reply used %d carrier bytes for %d text bytes, gate <1.5x", bytes, len(want))
	}
}

func TestWorkspaceMetadataComparisonDoesNotSuppressStatusTransitions(t *testing.T) {
	clock := time.Unix(100, 0)
	original := workspace.AgentSnapshot{ID: "owner", Activity: types.AgentActivityView{Status: types.AgentStatusSnapshot{Phase: types.AgentResponding, Detail: "Receiving response", UpdatedAt: clock}}}
	observed := original
	observed.LastTokenTime = clock.Add(time.Second)
	observed.ConversationRevision = 7
	observed.Activity.Status.UpdatedAt = clock.Add(time.Second)
	if !reflect.DeepEqual(stateMetadata(original), stateMetadata(observed)) {
		t.Fatal("observation clocks changed semantic metadata")
	}
	for _, mutate := range []func(*workspace.AgentSnapshot){
		func(s *workspace.AgentSnapshot) { s.Activity.Status.Phase = types.AgentCompleted },
		func(s *workspace.AgentSnapshot) { s.Activity.Status.Detail = "A different status" },
		func(s *workspace.AgentSnapshot) { s.Activity.Status.PhaseStartedAt = clock.Add(time.Second) },
		func(s *workspace.AgentSnapshot) { s.Activity.Status.NeedsAction = true },
	} {
		changed := observed
		mutate(&changed)
		if reflect.DeepEqual(stateMetadata(original), stateMetadata(changed)) {
			t.Fatal("semantic status change disappeared from workspace publication")
		}
	}
	if original.Activity.Status.UpdatedAt != clock || observed.Activity.Status.UpdatedAt != clock.Add(time.Second) {
		t.Fatal("comparison mutated the authority's actual observation clocks")
	}
}
