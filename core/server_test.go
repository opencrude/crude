package core

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/tinylib/msgp/msgp"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/workspace"
)

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
