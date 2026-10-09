package core

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

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
	c, err := coreclient.Dial(ctx, a, "transport-client")
	if err != nil {
		t.Fatal(err)
	}
	if c.Owner().Epoch != 1 {
		t.Fatal(c.Owner())
	}
	err = c.Call(ctx, "foreign-workspace", coreprotocol.Snapshot, struct{}{}, nil)
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

func TestServeRejectsOwnerChange(t *testing.T) {
	r := transportRuntime(t)
	a, b := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeConnection(context.Background(), r, b) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	f := corewire.NewFramer(ctx, a)
	defer f.Close()
	if _, err := f.NegotiateClient("owner", corewire.MaxFrameSize); err != nil {
		t.Fatal(err)
	}
	ready, err := f.Read()
	if err != nil {
		t.Fatal(err)
	}
	var owner coreprotocol.Owner
	if err = json.Unmarshal(ready.Payload, &owner); err != nil {
		t.Fatal(err)
	}
	if err = f.Write(corewire.Frame{Version: 1, Kind: corewire.KindRequest, RequestID: "request", ClientID: owner.ClientID, Epoch: owner.Epoch + 1, Method: coreprotocol.Snapshot, Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("invalid owner accepted")
		}
	case <-ctx.Done():
		t.Fatal("invalid owner did not disconnect")
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
	c, err := coreclient.Dial(context.Background(), a, "listener-client")
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
	if r.ClientCount() != 0 {
		t.Fatal("shutdown retained clients")
	}
	other, err := r.Connect("after-shutdown")
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
}
