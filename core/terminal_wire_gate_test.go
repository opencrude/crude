//go:build unix

package core

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/workspace"
)

type terminalWireCounter struct {
	net.Conn
	received atomic.Int64
}

func (c *terminalWireCounter) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.received.Add(int64(n))
	return n, err
}

// Count the real local carrier, including mux framing, not a re-encoded size or
// the application payload alone. No other topic is subscribed during the sample.
func TestTerminalPlainFrameCompleteCarrierByteGate(t *testing.T) {
	r, root := resourceRuntime(t)
	a, b := net.Pipe()
	wire := &terminalWireCounter{Conn: a}
	serverContext, stop := context.WithCancel(context.Background())
	serverDone := make(chan struct{})
	go func() { defer close(serverDone); _ = ServeConnection(serverContext, r, b) }()
	t.Cleanup(func() { stop(); <-serverDone })
	c, err := coreclient.Dial(t.Context(), coreclient.SingleCarrier(wire), "full-frame-byte-gate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	var attached coreprotocol.AttachResult
	resourceTopicCall(t, c, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached)
	var terminal workspace.TerminalInfo
	resourceTopicCall(t, c, attached.WorkspaceID, coreprotocol.TerminalCreate,
		&workspace.TerminalCreateOpts{Command: "/bin/sleep", Args: []string{"30"}, Cols: 200, Rows: 50}, &terminal)
	r.mu.Lock()
	service := r.workspaces[attached.WorkspaceID].ws.Terminals
	r.mu.Unlock()
	var screen strings.Builder
	// Explicit row addresses avoid auto-wrap/scroll changing the last row.
	screen.WriteString("\x1b[?7l")
	for row := range 50 {
		fmt.Fprintf(&screen, "\x1b[%d;1H", row+1)
		for col := range 200 {
			screen.WriteByte(byte('a' + (row*7+col)%26))
		}
	}
	service.Send(workspace.TerminalOutputMsg{ID: terminal.ID, Data: []byte(screen.String())})
	start := wire.received.Load()
	frames := make(chan workspace.TerminalProjection, 1)
	ctx, cancel := context.WithCancel(t.Context())
	sub := c.SubscribeTerminal(ctx, attached.WorkspaceID, terminal.ID, func(p workspace.TerminalProjection, err error) {
		if err != nil {
			t.Error(err)
			return
		}
		select {
		case frames <- p:
		case <-ctx.Done():
		}
	})
	t.Cleanup(func() { cancel(); sub.Close() })
	select {
	case projection := <-frames:
		if projection.Grid.Cols != 200 || projection.Grid.GridRows != 50 || len(projection.Grid.Cells) != 50 {
			t.Fatal("wrong text-frame geometry")
		}
		for row, cells := range projection.Grid.Cells {
			if len(cells) != 200 {
				t.Fatal("incomplete row", row)
			}
			for col, cell := range cells {
				if cell.Text != string(byte('a'+(row*7+col)%26)) || cell.Width != 1 {
					t.Fatalf("wrong visible cell at %d,%d: %+v", col, row, cell)
				}
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("complete text frame not applied")
	}
	bytes := wire.received.Load() - start
	t.Logf("200x50 complete first terminal frame: %d local carrier bytes, including mux open/data/control and application framing", bytes)
	if bytes <= 0 || bytes >= 16000 {
		t.Fatalf("complete text frame used %d bytes; gate <16000", bytes)
	}
}
