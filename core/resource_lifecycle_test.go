package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
	"crude/terminalvt"
	"crude/types"
	"crude/workspace"
)

type resourceWire struct {
	f     *corewire.Framer
	owner coreprotocol.Owner
	next  int
	close func()
}

func newResourceWire(t *testing.T, r *Runtime, id string) *resourceWire {
	t.Helper()
	a, b := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	done := make(chan error, 1)
	go func() { done <- ServeConnection(ctx, r, b) }()
	f := corewire.NewFramer(ctx, a)
	var once sync.Once
	closeWire := func() {
		once.Do(func() {
			cancel()
			_ = f.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("resource wire server did not join")
			}
		})
	}
	t.Cleanup(closeWire)
	if _, err := f.NegotiateClient(id, corewire.MaxFrameSize); err != nil {
		t.Fatal(err)
	}
	ready, err := f.Read()
	if err != nil {
		t.Fatal(err)
	}
	w := &resourceWire{f: f, close: closeWire}
	if err := json.Unmarshal(ready.Payload, &w.owner); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *resourceWire) send(t *testing.T, ws, method string, value any) string {
	t.Helper()
	w.next++
	id := fmt.Sprintf("resource-%d-%d", w.owner.Epoch, w.next)
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.f.Write(corewire.Frame{Version: 1, Kind: corewire.KindRequest, RequestID: id, ClientID: w.owner.ClientID, Epoch: w.owner.Epoch, WorkspaceID: ws, Method: method, Payload: raw}); err != nil {
		t.Fatal(err)
	}
	return id
}

func (w *resourceWire) receive(t *testing.T, count int) map[string]corewire.Frame {
	t.Helper()
	results := make(map[string]corewire.Frame)
	for len(results) < count {
		frame, err := w.f.Read()
		if err != nil {
			t.Fatal(err)
		}
		if frame.Kind == corewire.KindResponse {
			results[frame.RequestID] = frame
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
		if err := json.Unmarshal(result.Payload, out); err != nil {
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
			input := coreprotocol.TerminalInputRequest{Lease: lease, Kind: "key", Key: terminalvt.Key{Code: ch, Text: string(ch)}}
			if i%2 == 1 {
				input.Kind = "paste"
				input.Data = []byte(string(ch))
			}
			w.send(t, attached.WorkspaceID, coreprotocol.TerminalInput, input)
		}
		for _, result := range w.receive(t, len(want)) {
			if result.Error != nil {
				t.Fatalf("input rejected: %+v", result.Error)
			}
		}
	}
	deadline = time.Now().Add(3 * time.Second)
	for {
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
	// Hold the real effect lane, not a substitute queue. A snapshot response
	// proves the actor admitted Acquire while its service effect is still delayed.
	r.mu.Lock()
	actor := r.workspaces[attached.WorkspaceID]
	r.mu.Unlock()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	if !actor.resourceLane.submit(func(context.Context) { close(entered); <-release }) {
		t.Fatal("could not hold effect lane")
	}
	<-entered
	acquire := a.send(t, attached.WorkspaceID, coreprotocol.TerminalControl, coreprotocol.TerminalIDRequest{ID: info.ID})
	barrier := a.send(t, attached.WorkspaceID, coreprotocol.Snapshot, struct{}{})
	if result, ok := a.receive(t, 1)[barrier]; !ok || result.Error != nil {
		t.Fatal("slow resource effect blocked unrelated snapshot response")
	}
	detach := a.send(t, attached.WorkspaceID, coreprotocol.Detach, struct{}{})
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
