//go:build unix

package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/buildinfo"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/terminalvt"
	"crude/workspace"

	"github.com/fsnotify/fsnotify"
)

// Manual peers deliberately expose Hello, input retransmission, final-result ACK
// and applied topic cursors. Only the driver is test code: ServeCarrier, session
// admission, command receipts, terminal service, VT and PTY are real owners.
type sessionTerminalFuzzCarrier struct {
	conn        corewire.Conn
	done        <-chan struct{}
	controlDone chan struct{}
}

func sessionTerminalFuzzDial(t *testing.T, ctx context.Context, r *Runtime, hello coreprotocol.Hello) (*sessionTerminalFuzzCarrier, coreprotocol.Welcome) {
	t.Helper()
	a, b := net.Pipe()
	c := &sessionTerminalFuzzCarrier{conn: corewire.NewMux(a, corewire.CarrierLocal, true), controlDone: make(chan struct{})}
	done := make(chan struct{})
	c.done = done
	go func() { defer close(done); _ = ServeConnection(ctx, r, b) }()
	stop := context.AfterFunc(ctx, func() { _ = c.conn.Close() })
	t.Cleanup(func() {
		stop()
		_ = c.conn.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("session/terminal carrier server did not join")
		}
		select {
		case <-c.controlDone:
		case <-time.After(3 * time.Second):
			t.Error("session/terminal control reader did not join")
		}
	})
	s, err := c.conn.OpenStream(ctx, corewire.ClassControl)
	if err != nil {
		t.Fatal(err)
	}
	hello.Schema, hello.Build = coreprotocol.Schema, buildinfo.ProtocolBuild
	if err := send(s, &hello); err != nil {
		t.Fatal(err)
	}
	reader := corewire.NewReader(s, 64<<10)
	body, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	var welcome coreprotocol.Welcome
	if err := coreprotocol.Decode(body, &welcome); err != nil {
		t.Fatal(err)
	}
	if welcome.Fault != nil {
		close(c.controlDone)
		if err := s.CloseWrite(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	} else {
		// A replacement sends SessionEnd. Consume it and close the old carrier
		// immediately, so replacing a live session needs no shutdown-timeout sleep.
		go func() { defer close(c.controlDone); _, _ = reader.Next(); _ = c.conn.Close() }()
	}
	return c, welcome
}

func (c *sessionTerminalFuzzCarrier) cut(t *testing.T, ctx context.Context) {
	t.Helper()
	if err := c.conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.done: // includes unbind and all stream producers joining
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func (c *sessionTerminalFuzzCarrier) command(t *testing.T, ctx context.Context, ws, id, method string, value coreprotocol.Value, acknowledge bool) coreprotocol.Response {
	t.Helper()
	s, err := c.conn.OpenStream(ctx, corewire.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := send(s, &coreprotocol.Open{Kind: coreprotocol.StreamCommand, WorkspaceID: ws, ID: id, Method: method}, value); err != nil {
		t.Fatal(err)
	}
	reader := corewire.NewReader(s, corewire.MaxMessage)
	for {
		body, err := reader.Next()
		if err != nil {
			t.Fatalf("%s/%s final reply: %v", method, id, err)
		}
		var reply coreprotocol.Reply
		n, err := coreprotocol.DecodePrefix(body, &reply)
		if err != nil {
			t.Fatal(err)
		}
		if !reply.Final {
			continue
		}
		result := coreprotocol.Response{ID: id, Revision: reply.Revision, Error: reply.Fault, Payload: bytes.Clone(n)}
		if acknowledge {
			if err := send(s, &coreprotocol.TopicAck{Seq: 1}); err != nil {
				t.Fatal(err)
			}
			// Match the SDK half-close/drain. The server abandons its stream
			// after its FIN; the mux may report reset after the known result.
			_ = s.CloseWrite()
			if _, err := reader.Next(); !errors.Is(err, io.EOF) && !errors.Is(err, corewire.ErrReset) {
				t.Fatalf("%s/%s final ACK drain: %v", method, id, err)
			}
		}
		return result
	}
}

type sessionTerminalFuzzPeer struct {
	carrier *sessionTerminalFuzzCarrier
	welcome coreprotocol.Welcome
	latest  workspace.TerminalLease
	cursor  uint64
	grid    coreclient.TerminalGrid
}

func (p *sessionTerminalFuzzPeer) frame(t *testing.T, ctx context.Context, ws, terminal, want string) {
	t.Helper()
	s, err := p.carrier.conn.OpenStream(ctx, corewire.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := send(s, &coreprotocol.Open{Kind: coreprotocol.StreamTopic, WorkspaceID: ws, Topic: coreprotocol.TopicTerminal, Key: terminal, Since: p.cursor}); err != nil {
		t.Fatal(err)
	}
	if err := send(s, &coreprotocol.TerminalViewport{Request: terminalvt.RowRequest{Start: -1}, DisplayIntervalNS: int64(time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	reader := corewire.NewReader(s, corewire.MaxMessage)
	for {
		body, err := reader.Next()
		if err != nil {
			t.Fatal(err)
		}
		var header coreprotocol.TopicHeader
		n, err := coreprotocol.DecodePrefix(body, &header)
		if err != nil {
			t.Fatal(err)
		}
		var frame coreprotocol.TerminalFrame
		if err := coreprotocol.Decode(n, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Fault != nil || header.Seq <= p.cursor || header.Reset != (p.cursor == 0) || frame.Base != p.cursor {
			t.Fatalf("covered terminal base reset/regressed: cursor=%d header=%+v base=%d fault=%v", p.cursor, header, frame.Base, frame.Fault)
		}
		projection, err := p.grid.Apply(header, frame)
		if err != nil {
			t.Fatal(err)
		}
		p.cursor = header.Seq
		if err := send(s, &coreprotocol.TopicAck{Seq: p.cursor}); err != nil {
			t.Fatal(err)
		}
		var row strings.Builder
		for _, cell := range projection.Grid.Cells[0] {
			row.WriteString(cell.Text)
		}
		if strings.TrimRight(row.String(), " ") == want {
			return
		}
	}
}

// FuzzCoreSessionTerminalLifecycle composes random retained-session and terminal
// transitions in one real-carrier/raw-PTY journey. The expected controller,
// sequence/failure log and literal child bytes are maintained independently of
// production receipt/encoding helpers. Eight actions bound each native case.
//
// Grace expiry is deliberately NOT simulated here: synctest cannot safely own
// this real PTY/socket workload. FuzzCoreSessionOwnership separately exercises
// the production grace callback on a virtual clock; integrated grace expiry,
// physical network roaming and concurrent controller races remain unverified.
func FuzzCoreSessionTerminalLifecycle(f *testing.F) {
	for _, seed := range [][]byte{
		{}, {0, 1, 6, 3, 10, 8, 9, 5},
		{1, 1, 4, 5, 6, 2, 0, 7},
		{80, 16, 1, 16, 32, 48, 64, 0},
		{3, 10, 8, 11, 2, 0, 6, 7},
		{4, 4, 5, 6, 12, 13, 14, 9},
		{0, 33, 6, 4, 6, 5, 2, 0},
		{80, 16, 0, 1, 128, 3, 10, 136},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, actions []byte) {
		if len(actions) > 8 {
			actions = actions[:8]
		}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		r, root := resourceRuntime(t)
		var peers [2]sessionTerminalFuzzPeer
		ids := [2]string{"integrated-author", "integrated-peer"}
		var known []workspace.TerminalController
		for i := range peers {
			p := &peers[i]
			p.carrier, p.welcome = sessionTerminalFuzzDial(t, ctx, r, coreprotocol.Hello{ClientID: ids[i]})
			if p.welcome.Fault != nil || p.welcome.Resumed || p.welcome.Owner != (coreprotocol.Owner{ClientID: ids[i], Epoch: 1}) {
				t.Fatalf("fresh owner: %+v", p.welcome)
			}
			known = append(known, workspace.TerminalController{ClientID: ids[i], Epoch: 1})
		}
		nextID := 0
		call := func(i int, ws, method string, value coreprotocol.Value, out coreprotocol.Target) coreprotocol.Response {
			nextID++
			response := peers[i].carrier.command(t, ctx, ws, fmt.Sprintf("command-%d", nextID), method, value, true)
			if out != nil && response.Error == nil {
				if err := coreprotocol.Decode(response.Payload, out); err != nil {
					t.Fatal(err)
				}
			}
			return response
		}
		must := func(response coreprotocol.Response) {
			t.Helper()
			if response.Error != nil {
				t.Fatal(response.Error)
			}
		}
		var attached coreprotocol.AttachResult
		must(call(0, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached))
		ws := attached.WorkspaceID
		must(call(1, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, nil))
		capture := filepath.Join(t.TempDir(), "child-input")
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			t.Fatal(err)
		}
		defer watcher.Close()
		if err := watcher.Add(filepath.Dir(capture)); err != nil {
			t.Fatal(err)
		}
		var info workspace.TerminalInfo
		must(call(0, ws, coreprotocol.TerminalCreate, &workspace.TerminalCreateOpts{
			Command: "/bin/sh", Args: []string{"-c", `stty raw -echo; printf READY; exec tee "$1"`, "integrated-fuzz", capture}, Cols: 120, Rows: 4,
		}, &info))
		r.mu.Lock()
		actor := r.workspaces[ws]
		service, manager := actor.ws.Terminals, actor.ws.TermMgr
		r.mu.Unlock()
		peers[0].frame(t, ctx, ws, info.ID, "READY")

		// Model state is intent-owned, never filled from ControllerLease/rows.
		var active workspace.TerminalLease
		var generation, sequence uint64
		modelStream := ""
		failures := make(map[uint64]string)
		var wantBytes []byte
		observe := func() {
			t.Helper()
			controllers := 0
			for _, owner := range known {
				got, ok := service.ControllerLease(info.ID, owner)
				want := active.Generation != 0 && active.Owner == owner
				if ok != want || ok && got != active || !ok && got != (workspace.TerminalLease{}) {
					t.Fatalf("actions=%x controller=%v got=%v/%v active=%v", actions, owner, got, ok, active)
				}
				if ok {
					controllers++
				}
			}
			if controllers > 1 || active.Generation != 0 && controllers != 1 {
				t.Fatalf("controller count=%d active=%v", controllers, active)
			}
			for i := range peers {
				r.mu.Lock()
				c := r.clients[ids[i]]
				r.mu.Unlock()
				if c == nil || c.Owner != peers[i].welcome.Owner || c.id != peers[i].welcome.SessionID {
					t.Fatal("retained session owner changed")
				}
			}
		}
		acquire := func(i int) {
			t.Helper()
			owner := workspace.TerminalController{ClientID: ids[i], Epoch: peers[i].welcome.Owner.Epoch}
			allowed := active.Generation == 0 || active.Owner == owner
			var got workspace.TerminalLease
			response := call(i, ws, coreprotocol.TerminalControl, &coreprotocol.TerminalIDRequest{ID: info.ID}, &got)
			if allowed != (response.Error == nil) || !allowed && response.Error.Code != "resource_failed" {
				t.Fatalf("acquire allowed=%v fault=%v", allowed, response.Error)
			}
			if allowed {
				if active.Generation == 0 {
					generation++
					active = workspace.TerminalLease{TerminalID: info.ID, Owner: owner, Generation: generation}
					sequence, failures = 0, make(map[uint64]string)
					modelStream = ""
				}
				if got != active {
					t.Fatalf("lease=%v want=%v", got, active)
				}
				peers[i].latest = active
			}
		}
		acquire(0)
		observe()
		// A known effect is intentionally retained without its final ACK first.
		// The cached retry ACKs it; all later retries must use the durable receipt.
		original := coreprotocol.TerminalInputRequest{Lease: active, Kind: "paste", Data: []byte("r")}
		knownResult := peers[0].carrier.command(t, ctx, ws, "known-effect", coreprotocol.TerminalInput, &original, false)
		must(knownResult)
		wantBytes = append(wantBytes, 'r')
		changedPending := original
		changedPending.Data = []byte("X")
		badPending := peers[0].carrier.command(t, ctx, ws, "known-effect", coreprotocol.TerminalInput, &changedPending, true)
		if badPending.Error == nil || badPending.Error.Code != "invalid_request" {
			t.Fatalf("changed unacknowledged preimage admitted: %+v", badPending)
		}
		retryReceipt := func() {
			t.Helper()
			got := peers[0].carrier.command(t, ctx, ws, "known-effect", coreprotocol.TerminalInput, &original, true)
			if !reflect.DeepEqual(got, knownResult) {
				t.Fatalf("known receipt changed: got=%+v want=%+v", got, knownResult)
			}
			r.mu.Lock()
			retained := r.clients[ids[0]]
			r.mu.Unlock()
			retained.commandMu.Lock()
			cached := retained.commands[coreprotocol.TerminalInput+"\x00known-effect"] != nil
			retained.commandMu.Unlock()
			if cached {
				t.Fatal("final ACK did not release cached command; durable retry not established")
			}
			var receipt coreprotocol.Receipt
			must(call(0, "", coreprotocol.GetReceipt, &coreprotocol.ReceiptRequest{RequestID: "known-effect"}, &receipt))
			if receipt.State != "completed" || receipt.ClientID != ids[0] || receipt.RequestID != "known-effect" || receipt.Response.Error != nil || !bytes.Equal(receipt.Response.Payload, knownResult.Payload) {
				t.Fatalf("exact receipt not retained: %+v", receipt)
			}
			changed := original
			changed.Data = []byte("X")
			bad := peers[0].carrier.command(t, ctx, ws, "known-effect", coreprotocol.TerminalInput, &changed, true)
			if bad.Error == nil || bad.Error.Code != "receipt_failed" {
				t.Fatalf("changed durable preimage admitted: %+v", bad)
			}
		}
		retryReceipt()

		streamID := func() string { return fmt.Sprintf("lease-%d", generation) }
		input := func(i int, name string, seq, ack uint64, item workspace.TerminalInput, retryAfterCut bool) {
			t.Helper()
			wantFault, wantFailure := "", ""
			owner := workspace.TerminalController{ClientID: ids[i], Epoch: peers[i].welcome.Owner.Epoch}
			switch {
			case item.Lease.Generation == 0 || item.Lease != active || item.Lease.Owner != owner:
				wantFault = "terminal_control"
			case (modelStream != "" && name != modelStream) || seq == 0 || seq > sequence+1 || ack > sequence:
				wantFault = "terminal_input"
			case seq <= sequence:
				wantFailure = failures[seq]
			default:
				sequence, modelStream = seq, name
				if item.Kind == "resize" {
					wantFailure = terminalvt.ErrBounds.Error()
					failures[seq] = wantFailure
				} else {
					// All admitted printable keys/pastes in this fixture have the
					// literal one-byte meaning; no production encoder supplies it.
					wantBytes = append(wantBytes, item.Data...)
				}
			}
			batch := coreprotocol.TerminalInputBatch{First: seq, Ack: ack, Inputs: []workspace.TerminalInput{item}}
			submit := func(read bool) {
				s, err := peers[i].carrier.conn.OpenStream(ctx, corewire.ClassInput)
				if err != nil {
					t.Fatal(err)
				}
				if read {
					defer s.Close()
				} // Otherwise carrier cut, not stream reset, owns the lost response.
				if err := send(s, &coreprotocol.Open{Kind: coreprotocol.StreamInput, ID: name, WorkspaceID: ws, Topic: coreprotocol.TopicTerminalInput, Key: info.ID}); err != nil {
					t.Fatal(err)
				}
				if err := send(s, &batch); err != nil {
					t.Fatal(err)
				}
				if !read {
					return
				}
				body, err := corewire.NewReader(s, 64<<10).Next()
				if err != nil {
					t.Fatal(err)
				}
				var got coreprotocol.TerminalInputAck
				if err := coreprotocol.Decode(body, &got); err != nil {
					t.Fatal(err)
				}
				if wantFault != "" {
					if got.Fault == nil || got.Fault.Code != wantFault || got.Seq != 0 || len(got.Failures) != 0 {
						t.Fatalf("input rejection=%+v want=%s", got, wantFault)
					}
				} else if got.Fault != nil || got.Seq != seq || wantFailure == "" && len(got.Failures) != 0 || wantFailure != "" && (len(got.Failures) != 1 || got.Failures[0].Seq != seq || got.Failures[0].Message != wantFailure) {
					t.Fatalf("input receipt=%+v want seq=%d failure=%q", got, seq, wantFailure)
				}
			}
			if retryAfterCut {
				submit(false) // lose the response; admission may already have happened
				p := &peers[i]
				p.carrier.cut(t, ctx)
				observe()      // carrier loss must not transfer or revoke a lease
				acquire(1 - i) // a peer cannot steal an absent controller's lease
				old := p.welcome
				p.carrier, p.welcome = sessionTerminalFuzzDial(t, ctx, r, coreprotocol.Hello{ClientID: ids[i], Resume: &coreprotocol.SessionResume{SessionID: old.SessionID, Secret: old.Secret}})
				if p.welcome.Fault != nil || !p.welcome.Resumed || p.welcome.Owner != old.Owner || p.welcome.SessionID != old.SessionID || p.welcome.Secret != old.Secret {
					t.Fatalf("resume changed owner: %+v", p.welcome)
				}
			}
			submit(true)
		}
		for step, action := range actions {
			i := int(action>>3) & 1
			p := &peers[i]
			token := byte('A' + step)
			switch action & 7 {
			case 0, 1, 7:
				item := workspace.TerminalInput{Lease: p.latest, Kind: "paste", Data: []byte{token}}
				if action&128 != 0 {
					item.Kind, item.Key = "key", terminalvt.Key{Code: rune(token), Text: string(token)}
				}
				name, seq, ack := streamID(), sequence+1, uint64(0)
				if action&7 == 0 {
					switch (action >> 4) % 6 {
					case 1:
						seq = sequence // duplicate must not emit the changed byte
						if seq == 0 {
							seq = 2
						} // zero First is malformed, not a domain ACK
					case 2:
						seq += 1
					case 3:
						ack = sequence + 1
					case 4:
						name = "wrong-stream"
					case 5:
						item.Kind, item.Cols, item.Rows = "resize", terminalvt.MaxCols+1, 4
					}
				}
				if action&7 == 7 {
					item.Lease.Generation += 100 // stale/foreign authority never admits bytes
				}
				input(i, name, seq, ack, item, action&7 == 1)
			case 2:
				acquire(i)
			case 3:
				allowed := p.latest.Generation != 0 && p.latest == active
				response := call(i, ws, coreprotocol.TerminalRelease, &p.latest, nil)
				if allowed != (response.Error == nil) {
					t.Fatalf("release allowed=%v fault=%v", allowed, response.Error)
				}
				if allowed {
					active = workspace.TerminalLease{}
				}
			case 4:
				old, oldCarrier := p.welcome, p.carrier
				p.carrier, p.welcome = sessionTerminalFuzzDial(t, ctx, r, coreprotocol.Hello{ClientID: ids[i]})
				if p.welcome.Fault != nil || p.welcome.Resumed || p.welcome.Owner != (coreprotocol.Owner{ClientID: ids[i], Epoch: old.Owner.Epoch + 1}) || p.welcome.SessionID == old.SessionID {
					t.Fatalf("replacement: %+v", p.welcome)
				}
				oldCarrier.cut(t, ctx)
				if active.Owner.ClientID == ids[i] {
					active = workspace.TerminalLease{}
				}
				known = append(known, workspace.TerminalController{ClientID: ids[i], Epoch: p.welcome.Owner.Epoch})
				p.cursor, p.grid = 0, coreclient.TerminalGrid{}
				must(call(i, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, nil))
				_, refused := sessionTerminalFuzzDial(t, ctx, r, coreprotocol.Hello{ClientID: ids[i], Resume: &coreprotocol.SessionResume{SessionID: old.SessionID, Secret: old.Secret}})
				if refused.Fault == nil || refused.Fault.Code != "session_gone" {
					t.Fatal("out-of-order resume revived replaced owner", refused)
				}
			case 5:
				for _, hello := range []coreprotocol.Hello{
					{ClientID: ids[i], Resume: &coreprotocol.SessionResume{SessionID: p.welcome.SessionID, Secret: p.welcome.Secret + "wrong"}},
					{ClientID: ids[1-i], Resume: &coreprotocol.SessionResume{SessionID: p.welcome.SessionID, Secret: p.welcome.Secret}},
				} {
					_, refused := sessionTerminalFuzzDial(t, ctx, r, hello)
					if refused.Fault == nil || refused.Fault.Code != "session_gone" {
						t.Fatal("foreign/invalid resume admitted", refused)
					}
				}
				// A valid duplicate/live resume replaces only the carrier. The
				// older server's delayed unbind cannot revoke the resumed lease.
				old, oldCarrier := p.welcome, p.carrier
				p.carrier, p.welcome = sessionTerminalFuzzDial(t, ctx, r, coreprotocol.Hello{ClientID: ids[i], Resume: &coreprotocol.SessionResume{SessionID: old.SessionID, Secret: old.Secret}})
				oldCarrier.cut(t, ctx)
				if p.welcome.Fault != nil || !p.welcome.Resumed || p.welcome.Owner != old.Owner || p.welcome.SessionID != old.SessionID {
					t.Fatal("duplicate resume changed owner", p.welcome)
				}
			case 6:
				retryReceipt()
			}
			observe()
			p.frame(t, ctx, ws, info.ID, "READY"+string(wantBytes))
		}
		// A FIFO barrier bypasses controller authority only as fixture plumbing.
		// tee records the actual raw child bytes, including any rejected input or
		// replay the implementation accidentally sent. File notifications avoid
		// polling/sleeps and the real-I/O deadline bounds a missing barrier.
		const barrier = "~DONE~"
		if err := manager.Write(info.ID, []byte(barrier)); err != nil {
			t.Fatal(err)
		}
		wantBytes = append(wantBytes, barrier...)
		for {
			got, err := os.ReadFile(capture)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if bytes.Contains(got, []byte(barrier)) {
				if !bytes.Equal(got, wantBytes) {
					t.Fatalf("actions=%x raw child bytes=%q want=%q", actions, got, wantBytes)
				}
				break
			}
			select {
			case <-watcher.Events:
			case err := <-watcher.Errors:
				t.Fatal(err)
			case <-ctx.Done():
				t.Fatalf("raw child barrier missing: got=%q want=%q: %v", got, wantBytes, ctx.Err())
			}
		}
		peers[0].frame(t, ctx, ws, info.ID, "READY"+string(wantBytes))
	})
}
