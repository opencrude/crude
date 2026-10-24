package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
)

// The real PTY child deliberately produces no output: lifecycle publication must
// not depend on a prompt, title, first read or natural exit waking subscribers.
func TestResourceEventsSilentTerminalChild(t *testing.T) {
	if os.Getenv("CRUDE_TEST_SILENT_TERMINAL_CHILD") != "1" {
		t.Skip("native silent-PTY subprocess fixture")
	}
	time.Sleep(5 * time.Minute)
	os.Exit(0)
}

func TestResourceEventsSilentTerminalCreateAndCloseReachAllSubscribers(t *testing.T) {
	for _, route := range []string{"RPC", "semantic control"} {
		t.Run(route, func(t *testing.T) { testSilentTerminalLifecycle(t, route == "semantic control") })
	}
}

func testSilentTerminalLifecycle(t *testing.T, semantic bool) {
	t.Helper()
	setCoreTestHome(t, t.TempDir())
	t.Setenv("CRUDE_TEST_SILENT_TERMINAL_CHILD", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{}), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	root := t.TempDir()
	creator, ws := topicResourceConnection(t, r, root, "silent-creator")
	peer, _ := topicResourceConnection(t, r, root, "silent-peer")
	inventories := []<-chan resourcePublication[coreprotocol.WorkspaceState]{
		resourceTopic[coreprotocol.WorkspaceState](t, creator, ws, coreprotocol.TopicTerminalInventory),
		resourceTopic[coreprotocol.WorkspaceState](t, peer, ws, coreprotocol.TopicTerminalInventory),
	}
	for _, inventory := range inventories {
		initial := nextResourcePublication(t, inventory, func(s coreprotocol.WorkspaceState) bool { return s.Terminals != nil })
		if len(*initial.Terminals) != 0 {
			t.Fatalf("initial inventory not empty: %+v", initial)
		}
	}
	if semantic {
		resourceTopicCall(t, creator, ws, coreprotocol.ControlClientRegister, &coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Execute, control.Delete}}, nil)
	}
	lifecycle := func(id, method string, input coreprotocol.Value, out coreprotocol.Target) {
		t.Helper()
		if !semantic {
			resourceTopicCall(t, creator, ws, method, input, out)
			return
		}
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		var result control.Result
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		if _, err := creator.CallWith(ctx, ws, coreprotocol.ControlExecute, &control.Request{ID: id, CommandID: method, Input: raw}, &result, coreclient.CallOptions{ID: id}); err != nil {
			t.Fatal(err)
		}
		if result.Fault != nil || result.State != control.Committed {
			t.Fatalf("%s did not commit: %+v", method, result)
		}
		if out != nil {
			if err := json.Unmarshal(result.Output, out); err != nil {
				t.Fatal(err)
			}
		}
	}
	var info workspace.TerminalInfo
	lifecycle("silent-create", coreprotocol.TerminalCreate, &workspace.TerminalCreateOpts{
		Command: executable, Args: []string{"-test.run=^TestResourceEventsSilentTerminalChild$"}, CWD: root, Cols: 40, Rows: 4,
	}, &info)
	if info.ID == "" || info.PID <= 0 || !info.Alive {
		t.Fatalf("silent child was not created alive: %+v", info)
	}
	for _, inventory := range inventories {
		state := nextResourcePublication(t, inventory, func(s coreprotocol.WorkspaceState) bool { return s.Terminals != nil && len(*s.Terminals) > 0 })
		infos := *state.Terminals
		if len(infos) != 1 || infos[0].ID != info.ID || infos[0].PID != info.PID || !infos[0].Alive {
			t.Fatalf("subscriber did not discover the same silent PTY: %+v", infos)
		}
	}
	grid := resourceGrid(t, peer, ws, info.ID)
	projection := nextResourcePublication(t, grid, func(workspace.TerminalProjection) bool { return true })
	if !projection.Info.Alive || projection.Info.PID != info.PID || strings.TrimSpace(projectionText(projection)) != "" {
		t.Fatalf("fixture must remain alive and silent: %+v text=%q", projection.Info, projectionText(projection))
	}
	lifecycle("silent-close", coreprotocol.TerminalClose, &coreprotocol.TerminalIDRequest{ID: info.ID}, nil)
	for _, inventory := range inventories {
		nextResourcePublication(t, inventory, func(s coreprotocol.WorkspaceState) bool { return s.Terminals != nil && len(*s.Terminals) == 0 })
	}
}

// Keep the direct actor helper for tests of owner/resource admission. Topic
// acceptance uses a real net.Pipe carrier, exact-build handshake and decoded ACKs.
func resourceConnection(t *testing.T, r *Runtime, root, id string) (*Connection, string) {
	t.Helper()
	c, err := r.Connect(id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	var attached coreprotocol.AttachResult
	decodeResource(t, callCore(t, c, "attach", "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}), &attached)
	return c, attached.WorkspaceID
}

func decodeResource(t *testing.T, response coreprotocol.Response, out any) {
	t.Helper()
	if err := testDecode(response.Payload, out); err != nil {
		t.Fatal(err)
	}
}

// topicResourceConnection intentionally does not change resourceConnection's
// direct actor semantics, which unrelated resource and deletion tests rely on.
func topicResourceConnection(t *testing.T, r *Runtime, root, id string) (*coreclient.Client, string) {
	t.Helper()
	c := controlWireClient(t, r, id)
	var attached coreprotocol.AttachResult
	resourceTopicCall(t, c, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached)
	return c, attached.WorkspaceID
}

func resourceTopicCall(t *testing.T, c *coreclient.Client, ws, method string, input coreprotocol.Value, out coreprotocol.Target) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := c.Call(ctx, ws, method, input, out); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

type resourcePublication[T any] struct {
	value T
	err   error
}

// The callback decodes and admits each immutable value before Subscribe releases
// its application ACK. No invalidation triggers a terminal.list/rows request.
func resourceTopic[T any](t *testing.T, c *coreclient.Client, ws, topic string) <-chan resourcePublication[T] {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	values := make(chan resourcePublication[T], 32)
	var sequence uint64
	sub := c.Subscribe(ctx, ws, topic, "", func(h coreprotocol.TopicHeader, body []byte) error {
		var value T
		err := testDecode(body, &value)
		if err == nil && (h.Seq == 0 || h.Seq <= sequence || (sequence == 0 && !h.Reset)) {
			err = fmt.Errorf("invalid %s sequence: %d -> %+v", topic, sequence, h)
		}
		if state, ok := any(value).(coreprotocol.WorkspaceState); ok && state.Fault != nil {
			err = state.Fault
		}
		if update, ok := any(value).(coreprotocol.WorkspaceUpdate); ok {
			for _, effect := range update.TerminalEffects {
				if len(effect.Effects.Clipboard) != 0 || len(effect.Effects.Replies) != 0 {
					err = fmt.Errorf("private clipboard/query bytes leaked to workspace topic: %+v", effect)
				}
			}
		}
		select {
		case values <- resourcePublication[T]{value: value, err: err}:
		case <-ctx.Done():
			return ctx.Err()
		}
		sequence = h.Seq
		return err
	})
	t.Cleanup(func() { cancel(); sub.Close() })
	return values
}

func resourceGrid(t *testing.T, c *coreclient.Client, ws, id string) <-chan resourcePublication[workspace.TerminalProjection] {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	values := make(chan resourcePublication[workspace.TerminalProjection], 32)
	sub := c.SubscribeTerminal(ctx, ws, id, func(p workspace.TerminalProjection, err error) {
		select {
		case values <- resourcePublication[workspace.TerminalProjection]{value: p, err: err}:
		case <-ctx.Done():
		}
	})
	t.Cleanup(func() { cancel(); sub.Close() })
	return values
}

func nextResourcePublication[T any](t *testing.T, values <-chan resourcePublication[T], accept func(T) bool) T {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case publication := <-values:
			if publication.err != nil {
				t.Fatal(publication.err)
			}
			if accept(publication.value) {
				return publication.value
			}
		case <-timer.C:
			t.Fatal("no unsolicited resource publication arrived")
		}
	}
}

func resourceTitle(t *testing.T, updates <-chan resourcePublication[coreprotocol.WorkspaceUpdate], id, title string) workspace.TerminalEffectsMsg {
	t.Helper()
	var matched workspace.TerminalEffectsMsg
	nextResourcePublication(t, updates, func(u coreprotocol.WorkspaceUpdate) bool {
		for _, effect := range u.TerminalEffects {
			if effect.ID == id && effect.Effects.Title == title {
				matched = effect
				return true
			}
		}
		return false
	})
	return matched
}

func TestResourceEventsUnsolicitedOutputAndNaturalExit(t *testing.T) {
	r, root := resourceRuntime(t)
	c, ws := topicResourceConnection(t, r, root, "terminal-events")
	updates := resourceTopic[coreprotocol.WorkspaceUpdate](t, c, ws, coreprotocol.TopicWorkspace)
	inventory := resourceTopic[coreprotocol.WorkspaceState](t, c, ws, coreprotocol.TopicTerminalInventory)
	nextResourcePublication(t, updates, func(u coreprotocol.WorkspaceUpdate) bool { return u.Snapshot != nil })
	nextResourcePublication(t, inventory, func(s coreprotocol.WorkspaceState) bool { return s.Terminals != nil })
	var info workspace.TerminalInfo
	resourceTopicCall(t, c, ws, coreprotocol.TerminalCreate, &workspace.TerminalCreateOpts{
		Command: "/bin/sh", Args: []string{"-c", "stty -echo; printf '\033]2;ready\007'; read line; printf 'FINAL-GRID'; exit 7"}, Cols: 40, Rows: 4,
	}, &info)
	// Readiness is a pushed semantic fact, with no Rows or inventory polling.
	resourceTitle(t, updates, info.ID, "ready")
	grid := resourceGrid(t, c, ws, info.ID)
	nextResourcePublication(t, grid, func(p workspace.TerminalProjection) bool { return p.Info.ID == info.ID && p.Info.Alive })
	var lease workspace.TerminalLease
	resourceTopicCall(t, c, ws, coreprotocol.TerminalControl, &coreprotocol.TerminalIDRequest{ID: info.ID}, &lease)
	resourceTopicCall(t, c, ws, coreprotocol.TerminalInput, &coreprotocol.TerminalInputRequest{Lease: lease, Kind: "paste", Data: []byte("\n")}, nil)
	// Latest-state conflation may join output and exit. Wait for the actual final
	// grid and independent lifecycle metadata, not a count of invalidations.
	final := nextResourcePublication(t, grid, func(p workspace.TerminalProjection) bool { return strings.Contains(projectionText(p), "FINAL-GRID") })
	if final.Info.ID != info.ID {
		t.Fatal("final grid changed identity", final.Info)
	}
	exited := nextResourcePublication(t, inventory, func(s coreprotocol.WorkspaceState) bool {
		if s.Terminals == nil {
			return false
		}
		for _, terminal := range *s.Terminals {
			if terminal.ID == info.ID && !terminal.Alive {
				return true
			}
		}
		return false
	})
	if len(*exited.Terminals) != 1 || (*exited.Terminals)[0].ExitCode != 7 {
		t.Fatalf("incorrect exit metadata: %+v", *exited.Terminals)
	}
	if final.Info.Alive {
		final = nextResourcePublication(t, grid, func(p workspace.TerminalProjection) bool { return !p.Info.Alive })
	}
	if final.Info.ExitCode != 7 || !strings.Contains(projectionText(final), "FINAL-GRID") {
		t.Fatalf("exit grid lost final output: %+v text=%q", final.Info, projectionText(final))
	}
}

// A control request on the same stop-and-wait session journal is a deterministic
// fence: preceding private offers have reached their inbox before this returns.
func resourcePrivateFence(t *testing.T, r *Runtime, c *coreclient.Client, ws, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	envelope := shellEnvelope(id, ws, c.Owner())
	done := make(chan error, 1)
	go func() {
		_, err := r.RequestShellControl(ctx, ws, *envelope.Request.Target.Shell, envelope)
		done <- err
	}()
	select {
	case got := <-c.ControlRequests():
		if got.Request.ID != id {
			t.Fatalf("wrong private fence: %+v", got)
		}
	case <-ctx.Done():
		t.Fatal("private fence was not delivered", ctx.Err())
	}
	if err := c.ReplyControl(ctx, ws, control.Result{RequestID: id, OperationID: id, State: control.Applied, Output: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestResourceEventsClipboardOnlyExactControllerAndObserverMetadata(t *testing.T) {
	r, root := resourceRuntime(t)
	a, ws := topicResourceConnection(t, r, root, "clipboard-controller")
	b, _ := topicResourceConnection(t, r, root, "clipboard-observer")
	updates := make(map[*coreclient.Client]<-chan resourcePublication[coreprotocol.WorkspaceUpdate])
	for _, c := range []*coreclient.Client{a, b} {
		resourceTopicCall(t, c, ws, coreprotocol.ControlShellRegister, &coreprotocol.Empty{}, nil)
		updates[c] = resourceTopic[coreprotocol.WorkspaceUpdate](t, c, ws, coreprotocol.TopicWorkspace)
		nextResourcePublication(t, updates[c], func(u coreprotocol.WorkspaceUpdate) bool { return u.Snapshot != nil })
	}
	var info workspace.TerminalInfo
	resourceTopicCall(t, a, ws, coreprotocol.TerminalCreate, &workspace.TerminalCreateOpts{
		Command: "/bin/sh", Args: []string{"-c", "stty -echo; printf '\033]2;ready\007'; while read line; do printf '\033]2;offered\007\033]52;c;c2VjcmV0\007\033[5n'; done"}, Cols: 40, Rows: 4,
	}, &info)
	for _, c := range []*coreclient.Client{a, b} {
		resourceTitle(t, updates[c], info.ID, "ready")
	}
	var lease workspace.TerminalLease
	resourceTopicCall(t, a, ws, coreprotocol.TerminalControl, &coreprotocol.TerminalIDRequest{ID: info.ID}, &lease)
	check := func(controller, observer *coreclient.Client, inputID string, titleChanged bool) {
		t.Helper()
		resourceTopicCall(t, controller, ws, coreprotocol.TerminalInput, &coreprotocol.TerminalInputRequest{Lease: lease, Kind: "paste", Data: []byte("offer\n")}, nil)
		for _, c := range []*coreclient.Client{controller, observer} {
			effect := resourceTitle(t, updates[c], info.ID, "offered")
			if effect.Effects.TitleChanged != titleChanged {
				t.Fatalf("observer lost repeated title metadata: %+v", effect)
			}
		}
		select {
		case effect := <-controller.PrivateEffects():
			offer := effect.TerminalClipboard
			if effect.Error != "" || effect.WorkspaceID != ws || offer == nil || offer.TerminalID != info.ID || offer.Generation != lease.Generation || len(offer.Clipboard) != 1 || string(offer.Clipboard[0]) != "secret" {
				t.Fatalf("controller lost exact private offer: %+v", effect)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("controller did not receive clipboard offer")
		}
		for _, c := range []*coreclient.Client{controller, observer} {
			resourcePrivateFence(t, r, c, ws, inputID+"-"+c.Owner().ClientID)
			select {
			case effect := <-c.PrivateEffects():
				t.Fatalf("unexpected private effect (observer leak or duplicate): %+v", effect)
			default:
			}
		}
	}
	check(a, b, "offer-first", true)
	resourceTopicCall(t, a, ws, coreprotocol.TerminalRelease, &lease, nil)
	resourceTopicCall(t, b, ws, coreprotocol.TerminalControl, &coreprotocol.TerminalIDRequest{ID: info.ID}, &lease)
	check(b, a, "offer-second", false)
}
