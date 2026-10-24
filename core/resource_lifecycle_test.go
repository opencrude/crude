package core

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/terminalvt"
	"crude/types"
	"crude/workspace"
)

// resourceWire issues commands over a real session; send does not wait, so
// tests can hold several commands in flight at once.
type resourceWire struct {
	c       *coreclient.Client
	owner   coreprotocol.Owner
	next    int
	results chan resourceResult
	close   func()
}

type resourceResult struct {
	id       string
	response coreprotocol.Response
}

func newResourceWire(t *testing.T, r *Runtime, id string) *resourceWire {
	t.Helper()
	a, b := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	done := make(chan error, 1)
	go func() { done <- ServeConnection(ctx, r, b) }()
	c, err := coreclient.Dial(ctx, coreclient.SingleCarrier(a), id)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	closeWire := func() {
		once.Do(func() {
			_ = c.Close()
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("resource wire server did not join")
			}
		})
	}
	t.Cleanup(closeWire)
	return &resourceWire{c: c, owner: c.Owner(), results: make(chan resourceResult, 64), close: closeWire}
}

func (w *resourceWire) send(t *testing.T, ws, method string, value any) string {
	t.Helper()
	w.next++
	id := fmt.Sprintf("resource-%d-%d", w.owner.Epoch, w.next)
	raw, err := testPayload(value)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		response, err := w.c.CallRequest(context.Background(), coreprotocol.Request{ID: id, WorkspaceID: ws, Method: method, Payload: raw})
		if err != nil {
			response = coreprotocol.Response{ID: id, Error: &coreprotocol.Fault{Code: "transport", Message: err.Error()}}
		}
		w.results <- resourceResult{id, response}
	}()
	return id
}

func (w *resourceWire) receive(t *testing.T, count int) map[string]coreprotocol.Response {
	t.Helper()
	results := make(map[string]coreprotocol.Response)
	timeout := time.After(10 * time.Second)
	for len(results) < count {
		select {
		case result := <-w.results:
			results[result.id] = result.response
		case <-timeout:
			t.Fatalf("only %d of %d responses arrived", len(results), count)
		}
	}
	return results
}

func (w *resourceWire) call(t *testing.T, ws, method string, input, out any) {
	t.Helper()
	id := w.send(t, ws, method, input)
	result := w.receive(t, 1)[id]
	if result.Error != nil {
		t.Fatalf("%s: %+v", method, result.Error)
	}
	if out != nil {
		if err := testDecode(result.Payload, out); err != nil {
			t.Fatal(err)
		}
	}
}

func resourceRuntime(t *testing.T) (*Runtime, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY workload")
	}
	e, err := terminalvt.New(terminalvt.Options{})
	if err != nil {
		t.Fatal(err)
	}
	e.Close()
	setCoreTestHome(t, t.TempDir())
	root := t.TempDir()
	r, err := New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{}), nil
	}, t.TempDir()+"/receipts.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r, root
}

func projectionText(p workspace.TerminalProjection) string {
	var b strings.Builder
	for _, row := range p.Grid.Cells {
		for _, c := range row {
			b.WriteString(c.Text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestResourceWireTerminalInputPreservesFrameOrder(t *testing.T) {
	r, root := resourceRuntime(t)
	w := newResourceWire(t, r, "input-order")
	var attached coreprotocol.AttachResult
	w.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached)
	var info workspace.TerminalInfo
	w.call(t, attached.WorkspaceID, coreprotocol.TerminalCreate, workspace.TerminalCreateOpts{Command: "/bin/sh", Args: []string{"-c", "stty raw -echo; printf READY; cat"}, Cols: 120, Rows: 4}, &info)
	var lease workspace.TerminalLease
	w.call(t, attached.WorkspaceID, coreprotocol.TerminalControl, coreprotocol.TerminalIDRequest{ID: info.ID}, &lease)
	inputContext, cancelInput := context.WithCancel(t.Context())
	defer cancelInput()
	inputErrors := make(chan error, 1)
	inputStream := w.c.OpenTerminalInput(inputContext, attached.WorkspaceID, info.ID, func(err error) {
		select {
		case inputErrors <- err:
		default:
		}
	})
	defer inputStream.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var p workspace.TerminalProjection
		w.call(t, attached.WorkspaceID, coreprotocol.TerminalRows, coreprotocol.TerminalRowsRequest{ID: info.ID, Start: -1, Count: 4}, &p)
		if strings.Contains(projectionText(p), "READY") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("PTY did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
	const want = "AbCdEfGhIjKl"
	for round := 0; round < 5; round++ {
		for i, ch := range want {
			input := workspace.TerminalInput{Lease: lease, Kind: "key", Key: terminalvt.Key{Code: ch, Text: string(ch)}}
			if i%2 == 1 {
				input.Kind = "paste"
				input.Data = []byte(string(ch))
			}
			if err := inputStream.Submit(input); err != nil {
				t.Fatal(err)
			}
		}
	}
	deadline = time.Now().Add(3 * time.Second)
	for {
		select {
		case err := <-inputErrors:
			t.Fatal("ordered input rejected", err)
		default:
		}
		var p workspace.TerminalProjection
		w.call(t, attached.WorkspaceID, coreprotocol.TerminalRows, coreprotocol.TerminalRowsRequest{ID: info.ID, Start: -1, Count: 4}, &p)
		text := projectionText(p)
		if strings.Contains(text, "READY"+strings.Repeat(want, 5)) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("PTY bytes reordered or lost: %q", text)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestResourceWireDetachCannotRetainQueuedAcquire(t *testing.T) {
	r, root := resourceRuntime(t)
	a := newResourceWire(t, r, "old-controller")
	b := newResourceWire(t, r, "new-controller")
	var attached coreprotocol.AttachResult
	a.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached)
	b.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, nil)
	var info workspace.TerminalInfo
	a.call(t, attached.WorkspaceID, coreprotocol.TerminalCreate, workspace.TerminalCreateOpts{Command: "/bin/cat", Cols: 30, Rows: 4}, &info)
	// Hold the real terminal lane, not a substitute queue. A snapshot response
	// proves the actor admitted Acquire while its service effect is still delayed.
	r.mu.Lock()
	actor := r.workspaces[attached.WorkspaceID]
	r.mu.Unlock()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	if !actor.terminalLane.submit(func(context.Context) { close(entered); <-release }) {
		t.Fatal("could not hold terminal lane")
	}
	<-entered
	acquire := a.send(t, attached.WorkspaceID, coreprotocol.TerminalControl, coreprotocol.TerminalIDRequest{ID: info.ID})
	barrier := a.send(t, attached.WorkspaceID, coreprotocol.Snapshot, coreprotocol.Empty{})
	if result, ok := a.receive(t, 1)[barrier]; !ok || result.Error != nil {
		t.Fatal("slow resource effect blocked unrelated snapshot response")
	}
	detach := a.send(t, attached.WorkspaceID, coreprotocol.Detach, coreprotocol.Empty{})
	results := a.receive(t, 1)
	if result := results[detach]; result.Error != nil {
		t.Fatalf("detach: %+v", result.Error)
	}
	unblock()
	if result := a.receive(t, 1)[acquire]; result.Error == nil {
		t.Fatal("delayed acquisition revived detached owner")
	}
	var lease workspace.TerminalLease
	b.call(t, attached.WorkspaceID, coreprotocol.TerminalControl, coreprotocol.TerminalIDRequest{ID: info.ID}, &lease)
	if lease.Owner.ClientID != b.owner.ClientID {
		t.Fatalf("old owner survived detach: %+v", lease)
	}
	var p workspace.TerminalProjection
	b.call(t, attached.WorkspaceID, coreprotocol.TerminalRows, coreprotocol.TerminalRowsRequest{ID: info.ID, Start: -1, Count: 4}, &p)
	if !p.Info.Alive {
		t.Fatal("detach killed shared PTY")
	}
	b.call(t, attached.WorkspaceID, coreprotocol.TerminalRelease, lease, nil)
	a.close()
	reconnected := newResourceWire(t, r, "old-controller")
	if reconnected.owner.Epoch <= a.owner.Epoch {
		t.Fatal("connection epoch was reused")
	}
	reconnected.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, nil)
	var replacement workspace.TerminalLease
	reconnected.call(t, attached.WorkspaceID, coreprotocol.TerminalControl, coreprotocol.TerminalIDRequest{ID: info.ID}, &replacement)
	if replacement.Owner.Epoch != reconnected.owner.Epoch || replacement.Generation == lease.Generation {
		t.Fatalf("reconnect reused stale lease: %+v", replacement)
	}
	// A forged old epoch is rejected on the real request path, not only by
	// directly calling the terminal service's lease checker.
	id := reconnected.send(t, attached.WorkspaceID, coreprotocol.TerminalInput, coreprotocol.TerminalInputRequest{Lease: lease, Kind: "paste", Data: []byte("stale")})
	if result := reconnected.receive(t, 1)[id]; result.Error == nil {
		t.Fatal("foreign old lease accepted after reconnect")
	}
}
