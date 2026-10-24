package remote

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"crude/core"
	"crude/coreclient"
	"crude/shellport"
	"crude/terminalvt"
	"crude/types"
	"crude/workspace"
)

func TestTerminalFrameWaitsForUIAndFencesReopenedGrid(t *testing.T) {
	var retained *workspace.Workspace
	server, err := core.New(func(root string) (*workspace.Workspace, error) {
		retained = workspace.NewWorkspace(root, "", "test", types.Settings{}, nil)
		retained.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}, Memory: types.MemorySettings{AutoRecall: "off", AutoExtract: "off", Consolidation: "off"}})
		return retained, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	dial := func(ctx context.Context, id string) (*coreclient.Client, error) {
		a, b := net.Pipe()
		go func() { _ = core.ServeConnection(t.Context(), server, a) }()
		return coreclient.Dial(ctx, coreclient.SingleCarrier(b), id)
	}
	port, err := Attach(t.Context(), Options{Root: t.TempDir(), Dial: dial})
	if err != nil {
		t.Fatal(err)
	}
	defer port.Close()
	messages := make(chan any, 64)
	bus := workspace.NewEventBus()
	bus.SetSink(pushSink{messages})
	port.SetEventBus(bus)
	port.StartReplica()
	opts := workspace.TerminalCreateOpts{Command: "/bin/sleep", Args: []string{"300"}, Cols: 8, Rows: 2}
	if runtime.GOOS == "windows" {
		opts.Command, opts.Args = "powershell.exe", []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 300"}
	}
	info, err := port.terminals.Create(opts)
	if errors.Is(err, terminalvt.ErrNoCGO) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	top := port.terminals
	next := func() shellport.TerminalFrameMsg {
		t.Helper()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for {
			select {
			case event := <-messages:
				msg, ok := event.(shellport.TerminalFrameMsg)
				if !ok {
					continue
				}
				if msg.Err != nil {
					t.Fatalf("unexpected frame: %#v", event)
				}
				return msg
			case <-deadline.C:
				t.Fatal("missing terminal frame")
			}
		}
	}
	quiet := func(interval time.Duration) {
		t.Helper()
		deadline := time.NewTimer(interval)
		defer deadline.Stop()
		for {
			select {
			case event := <-messages:
				if _, ok := event.(shellport.TerminalFrameMsg); ok {
					t.Fatal("producer advanced before queued frame applied")
				}
			case <-deadline.C:
				return
			}
		}
	}
	top.StartGrid(info.ID)
	held := next()
	if len(top.modes) != 0 || len(top.projections) != 0 {
		t.Fatal("FIFO admission advanced UI-owned caches")
	}
	retained.Terminals.Send(workspace.TerminalOutputMsg{ID: info.ID, Data: []byte("\x1b[?1003hNEXT")})
	quiet(70 * time.Millisecond)
	if err := top.ApplyTerminalFrame(&held); err != nil {
		t.Fatal(err)
	}
	// Installing service caches is not the shell commit receipt either.
	quiet(40 * time.Millisecond)
	held.Applied <- nil
	queued := next()
	if !queued.Projection.Grid.Modes.MouseTracking {
		t.Fatal("new modes missing from publication")
	}
	if top.modes[info.ID].MouseTracking {
		t.Fatal("transport worker installed modes before UI")
	}
	old := top.grids[info.ID]
	top.stopStreams(info.ID)
	top.StartGrid(info.ID)
	reopened := next()
	if err := top.ApplyTerminalFrame(&queued); !errors.Is(err, context.Canceled) {
		t.Fatal("reopened grid accepted stale frame", err)
	}
	if len(top.projections) != 0 || len(top.modes) != 0 {
		t.Fatal("stale frame changed replacement caches")
	}
	joined := make(chan struct{})
	go func() { old.Close(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled subscription blocked waiting on Update")
	}
	queued.Applied <- context.Canceled
	if err := top.ApplyTerminalFrame(&reopened); err != nil {
		t.Fatal(err)
	}
	if !top.modes[info.ID].MouseTracking {
		t.Fatal("owning UI did not install modes")
	}
	// An input worker's late failure cannot roll back a replacement lease. A
	// current failure resolves the latest authority on Update, never an earlier
	// projection captured when the asynchronous failure was emitted.
	lease := workspace.TerminalLease{TerminalID: info.ID, Generation: 2}
	top.inputLeases = map[string]workspace.TerminalLease{info.ID: lease}
	top.predicted[info.ID] = held.Projection
	failure := shellport.TerminalFrameMsg{ID: info.ID, Err: workspace.ErrTerminalControl, Frame: terminalInputFailure{grid: top.grids[info.ID], lease: workspace.TerminalLease{TerminalID: info.ID, Generation: 1}}}
	if err := top.ApplyTerminalFrame(&failure); !errors.Is(err, context.Canceled) {
		t.Fatal("replacement lease accepted old input failure", err)
	}
	if _, pending := top.predicted[info.ID]; !pending {
		t.Fatal("old failure cleared current prediction")
	}
	failure.Frame = terminalInputFailure{grid: top.grids[info.ID], lease: lease}
	if err := top.ApplyTerminalFrame(&failure); err != nil {
		t.Fatal(err)
	}
	if failure.Projection.Generation != reopened.Projection.Generation || !failure.Projection.Grid.Modes.MouseTracking {
		t.Fatal("rollback did not resolve latest UI authority")
	}
	if _, pending := top.predicted[info.ID]; pending {
		t.Fatal("current failure retained provisional ink")
	}
	// Leave the frame unapplied: client/workspace shutdown must interrupt its
	// off-UI callback, not require Update to finish the queued receipt.
	closed := make(chan error, 1)
	go func() { closed <- port.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("workspace close waited for unapplied frame")
	}
	if err := top.ApplyTerminalFrame(&reopened); !errors.Is(err, context.Canceled) {
		t.Fatal("closed workspace accepted frame", err)
	}
}
