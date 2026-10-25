package remote

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/core"
	"crude/coreclient"
	"crude/workspace"
)

func TestBackendStatusFollowsRealCarrierLossAndShutdown(t *testing.T) {
	server, err := core.New(func(root string) (*workspace.Workspace, error) { return nil, nil }, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	a, b := net.Pipe()
	served := make(chan struct{})
	go func() { defer close(served); _ = core.ServeConnection(t.Context(), server, a) }()
	c, err := coreclient.Dial(t.Context(), coreclient.SingleCarrier(b), "connection-health-test")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	w := &Workspace{client: c, stop: make(chan struct{})}
	if s := w.BackendStatus(); s.State != "connected" || s.Carrier != "local" {
		t.Fatalf("healthy link=%+v", s)
	}
	_ = a.Close()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("carrier failure not detected")
	}
	// The Workspace collector retries fresh attachment after SDK resume ends.
	// Do not tell the wordmark to stop its tide while that owner still retries.
	if s := w.BackendStatus(); s.State != "reconnecting" || !strings.Contains(s.Notice, "fresh attachment") {
		t.Fatalf("lost link=%+v", s)
	}
	close(w.stop)
	if s := w.BackendStatus(); s.State != "offline" || s.Carrier != "" || s.RTT != 0 {
		t.Fatalf("closed link retained live telemetry: %+v", s)
	}
	select {
	case <-served:
	case <-time.After(time.Second):
		t.Fatal("server connection did not settle")
	}
}
