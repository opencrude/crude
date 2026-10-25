package core

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/tinylib/msgp/msgp"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/workspace"
)

func TestLifecycleChannelFrozenBytesAndNoSession(t *testing.T) {
	for _, attached := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "attached"}[attached], func(t *testing.T) {
			r := transportRuntime(t)
			r.SetOwnerGeneration("generation")
			r.SetLifetimePolicy(true, time.Minute, func() {})
			if attached {
				c, err := r.Connect("actual-shell")
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
			}
			a, b := net.Pipe()
			go func() { _ = ServeConnection(t.Context(), r, b) }()
			conn := corewire.NewMux(a, corewire.CarrierLocal, true)
			defer conn.Close()
			s, err := conn.OpenStream(t.Context(), corewire.ClassControl)
			if err != nil {
				t.Fatal(err)
			}
			// Literal bytes are independent of the implementation's encoder and
			// generated Hello shape, schema and source-cohort constants.
			if err := corewire.WriteMessage(s, []byte("CRUDE-STOP\x00\x00generation")); err != nil {
				t.Fatal(err)
			}
			body, err := corewire.NewReader(s, 8192).Next()
			if err != nil {
				t.Fatal(err)
			}
			prefix := []byte("CRUDE-STOP-RESULT\x00")
			if !bytes.HasPrefix(body, prefix) || body[len(prefix)] != map[bool]byte{false: 1, true: 0}[attached] {
				t.Fatalf("result %q", body)
			}
			if attached && !bytes.Contains(body, []byte("1 shell(s)")) {
				t.Fatalf("not actionable: %q", body)
			}
			if r.ClientCount() != map[bool]int{false: 0, true: 1}[attached] {
				t.Fatal("lifecycle request created a session")
			}
			if err := s.CloseWrite(); err != nil {
				t.Fatal(err)
			}
			if _, err := corewire.NewReader(s, 8192).Next(); err != io.EOF {
				t.Fatal(err)
			}
		})
	}
}

func TestLifecycleChannelRejectsNetworkAndMalformedRequests(t *testing.T) {
	for _, mode := range []string{"network", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			r := transportRuntime(t)
			r.SetLifetimePolicy(true, time.Minute, func() { t.Error("invalid lifecycle stop admitted") })
			a, b := net.Pipe()
			var auth Authenticator
			if mode == "network" {
				auth = func(context.Context, coreprotocol.Hello) (RemotePrincipal, error) {
					t.Error("lifecycle request reached app authentication")
					return RemotePrincipal{}, nil
				}
			}
			go func() { _ = ServeCarrier(t.Context(), r, corewire.NewMux(b, corewire.CarrierLocal, false), auth) }()
			conn := corewire.NewMux(a, corewire.CarrierLocal, true)
			defer conn.Close()
			s, err := conn.OpenStream(t.Context(), corewire.ClassControl)
			if err != nil {
				t.Fatal(err)
			}
			body := []byte("CRUDE-STOP\x00\x00")
			if mode == "malformed" {
				body = []byte("CRUDE-STOP\x00\x02")
			}
			if err := corewire.WriteMessage(s, body); err != nil {
				t.Fatal(err)
			}
			if _, err := corewire.NewReader(s, 8192).Next(); err == nil {
				t.Fatal("invalid lifecycle request received a result")
			}
			if r.draining || r.stopping {
				t.Fatal("invalid stop mutated core")
			}
		})
	}
}

func TestLifecycleResultBoundsAndStrictDecoding(t *testing.T) {
	b := coreprotocol.EncodeLifecycleResult(false, string(bytes.Repeat([]byte("x"), 16384)))
	if len(b) != 8192 {
		t.Fatal("unbounded result", len(b))
	}
	if out, err := coreprotocol.DecodeLifecycleResult(b); err != nil || out.Accepted || len(out.Reason) == 0 {
		t.Fatal(out, err)
	}
	for _, bad := range [][]byte{nil, []byte("CRUDE-STOP-RESULT\x00"), []byte("CRUDE-STOP-RESULT\x00\x02"), append(b, 0)} {
		if _, err := coreprotocol.DecodeLifecycleResult(bad); err == nil {
			t.Fatalf("malformed result accepted: %q", bad)
		}
	}
	if _, err := coreprotocol.EncodeLifecycleStop(false, string(bytes.Repeat([]byte("x"), 257))); err == nil {
		t.Fatal("unbounded owner accepted")
	}
}

func transportRuntime(t *testing.T) *Runtime {
	t.Helper()
	r, err := New(func(string) (*workspace.Workspace, error) { t.Error("unexpected workspace creation"); return nil, nil }, t.TempDir()+"/receipts.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}

func TestServeConnectionReadyAndDisconnect(t *testing.T) {
	r := transportRuntime(t)
	a, b := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeConnection(context.Background(), r, b) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := coreclient.Dial(ctx, coreclient.SingleCarrier(a), "transport-client")
	if err != nil {
		t.Fatal(err)
	}
	if c.Owner().Epoch != 1 {
		t.Fatal(c.Owner())
	}
	err = c.Call(ctx, "foreign-workspace", coreprotocol.Snapshot, &coreprotocol.Empty{}, nil)
	if err == nil {
		t.Fatal("foreign workspace accepted")
	}
	_ = c.Close()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("server did not join")
	}
	if r.ClientCount() != 0 {
		t.Fatal("connection not released")
	}
	other, err := r.Connect("still-live")
	if err != nil {
		t.Fatal("transport closed runtime:", err)
	}
	other.Close()
}

// A session belongs to the carrier's handshake, which refuses any other
// protocol before application traffic.
func TestServeRefusesForeignSchema(t *testing.T) {
	same, _ := coreprotocol.Marshal(&coreprotocol.Hello{Schema: "another-protocol", ClientID: "foreign"})
	// Another build's Hello shares only the frozen [Schema, Build] prefix.
	foreign := msgp.AppendArrayHeader(nil, 3)
	foreign = msgp.AppendString(foreign, "another-protocol")
	foreign = msgp.AppendString(foreign, "another-cohort")
	foreign = msgp.AppendString(foreign, "foreign")
	for name, body := range map[string][]byte{"same layout": same, "foreign layout": foreign} {
		t.Run(name, func(t *testing.T) { serveRefusesForeignSchema(t, body) })
	}
}

func serveRefusesForeignSchema(t *testing.T, body []byte) {
	r := transportRuntime(t)
	a, b := net.Pipe()
	go func() { _ = ServeConnection(context.Background(), r, b) }()
	conn := corewire.NewMux(a, corewire.CarrierLocal, true)
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	control, err := conn.OpenStream(ctx, corewire.ClassControl)
	if err != nil {
		t.Fatal(err)
	}
	if err := corewire.WriteMessage(control, body); err != nil {
		t.Fatal(err)
	}
	reply, err := corewire.NewReader(control, 1<<16).Next()
	if err != nil {
		t.Fatal(err)
	}
	var welcome coreprotocol.Welcome
	if err := coreprotocol.Decode(reply, &welcome); err != nil || welcome.Fault == nil || welcome.Fault.Code != "schema_mismatch" {
		t.Fatalf("foreign schema admitted: %+v %v", welcome, err)
	}
	if r.ClientCount() != 0 {
		t.Fatal("refused handshake created a session")
	}

}

func TestServeCancellationJoinsAndRetainsRuntime(t *testing.T) {
	r := transportRuntime(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, r, l) }()
	a, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c, err := coreclient.Dial(context.Background(), coreclient.SingleCarrier(a), "listener-client")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join")
	}
	// Stopping a listener is carrier loss: the session waits for a carrier
	// within its grace period, and the runtime itself stays usable.
	if r.ClientCount() != 1 {
		t.Fatal("listener shutdown ended the session instead of keeping it resumable")
	}
	other, err := r.Connect("after-shutdown")
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
}
