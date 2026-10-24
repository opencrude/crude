package core

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/terminalvt"
	"crude/workspace"
)

func TestTerminalStreamGridAndOrderedInputIgnoreSlowViewer(t *testing.T) {
	r, root := resourceRuntime(t)
	w := newResourceWire(t, r, "stream-input-order")
	var attached coreprotocol.AttachResult
	w.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached)
	var info workspace.TerminalInfo
	w.call(t, attached.WorkspaceID, coreprotocol.TerminalCreate, workspace.TerminalCreateOpts{Command: "/bin/sh", Args: []string{"-c", "stty raw -echo; printf '\033[?1003h\033[?1006hREADY'; cat"}, Cols: 120, Rows: 4}, &info)
	var lease workspace.TerminalLease
	w.call(t, attached.WorkspaceID, coreprotocol.TerminalControl, coreprotocol.TerminalIDRequest{ID: info.ID}, &lease)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	frames := make(chan workspace.TerminalProjection, 8)
	errors := make(chan error, 8)
	hold := make(chan struct{})
	blocked := make(chan struct{}, 1)
	sub := w.c.SubscribeTerminal(ctx, attached.WorkspaceID, info.ID, func(p workspace.TerminalProjection, err error) {
		if err != nil {
			errors <- err
			return
		}
		select {
		case frames <- p:
		case <-ctx.Done():
		}
	})
	defer func() { cancel(); sub.Close() }()
	wait := func(want string) workspace.TerminalProjection {
		t.Helper()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for {
			select {
			case p := <-frames:
				if strings.Contains(projectionText(p), want) {
					return p
				}
			case err := <-errors:
				t.Fatal(err)
			case <-timer.C:
				t.Fatalf("no pushed grid containing %q", want)
			}
		}
	}
	wait("READY")
	input := w.c.OpenTerminalInput(ctx, attached.WorkspaceID, info.ID, func(err error) { errors <- err })
	defer input.Close()
	const want = "AbCdEfGhIjKl"
	for range 5 {
		for i, ch := range want {
			item := workspace.TerminalInput{Lease: lease, Kind: "key", Key: terminalvt.Key{Code: ch, Text: string(ch)}}
			if i%2 != 0 {
				item.Kind, item.Data = "paste", []byte(string(ch))
			}
			if err := input.Submit(item); err != nil {
				t.Fatal(err)
			}
		}
	}
	wait("READY" + strings.Repeat(want, 5))
	// A different connection's viewer may stop applying frames entirely; the
	// controller's independent input stream must still reach the real PTY.
	peer := newResourceWire(t, r, "slow-terminal-viewer")
	var peerAttached coreprotocol.AttachResult
	peer.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &peerAttached)
	peerSub := peer.c.SubscribeTerminal(ctx, attached.WorkspaceID, info.ID, func(_ workspace.TerminalProjection, err error) {
		if err != nil {
			return
		}
		select {
		case blocked <- struct{}{}:
		default:
		}
		select {
		case <-hold:
		case <-ctx.Done():
		}
	})
	defer func() { cancel(); peerSub.Close() }()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("slow viewer never got initial grid")
	}
	started := time.Now()
	for i := range 300 {
		if err := input.Submit(workspace.TerminalInput{Lease: lease, Kind: "mouse", Mouse: terminalvt.Mouse{Action: terminalvt.MouseMotion, X: i % 100}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := input.Submit(workspace.TerminalInput{Lease: lease, Kind: "key", Key: terminalvt.Key{Code: 'q', Text: "q"}}); err != nil {
		t.Fatal(err)
	}
	wait("q")
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("q behind pointer flood took %v", elapsed)
	}
	close(hold)
}

func TestTerminalStreamCarrierResumeRetainsLeaseAndInput(t *testing.T) {
	r, root := resourceRuntime(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	var latest corewire.Conn
	dial := func(ctx context.Context) (corewire.Conn, error) {
		a, b := net.Pipe()
		go func() { _ = ServeConnection(t.Context(), r, b) }()
		conn := corewire.NewMux(a, corewire.CarrierLocal, true)
		mu.Lock()
		latest = conn
		mu.Unlock()
		return conn, nil
	}
	c, err := coreclient.Dial(ctx, dial, "terminal-resume")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var attached coreprotocol.AttachResult
	if err := c.Call(ctx, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
		t.Fatal(err)
	}
	var info workspace.TerminalInfo
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.TerminalCreate, &workspace.TerminalCreateOpts{Command: "/bin/sh", Args: []string{"-c", "stty raw -echo; printf READY; cat"}, Cols: 120, Rows: 4}, &info); err != nil {
		t.Fatal(err)
	}
	var lease workspace.TerminalLease
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.TerminalControl, &coreprotocol.TerminalIDRequest{ID: info.ID}, &lease); err != nil {
		t.Fatal(err)
	}
	frames := make(chan workspace.TerminalProjection, 16)
	failures := make(chan error, 8)
	sub := c.SubscribeTerminal(ctx, attached.WorkspaceID, info.ID, func(p workspace.TerminalProjection, err error) {
		if err != nil {
			failures <- err
			return
		}
		select {
		case frames <- p:
		case <-ctx.Done():
		}
	})
	defer func() { cancel(); sub.Close() }()
	wait := func(want string) workspace.TerminalProjection {
		t.Helper()
		for {
			select {
			case p := <-frames:
				if strings.Contains(projectionText(p), want) {
					return p
				}
			case err := <-failures:
				t.Fatal(err)
			case <-ctx.Done():
				t.Fatal("no resumed grid", ctx.Err())
			}
		}
	}
	wait("READY")
	input := c.OpenTerminalInput(ctx, attached.WorkspaceID, info.ID, func(err error) { failures <- err })
	defer input.Close()
	if err := input.Submit(workspace.TerminalInput{Lease: lease, Kind: "paste", Data: []byte("BEFORE-")}); err != nil {
		t.Fatal(err)
	}
	wait("READYBEFORE-")
	r.mu.Lock()
	retained := r.clients["terminal-resume"]
	r.mu.Unlock()
	var oldSequence uint64
	key := attached.WorkspaceID + "/" + info.ID
	// Read retained state only after its producer joins on carrier cut below.
	mu.Lock()
	conn := latest
	mu.Unlock()
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	// Admission while the carrier is absent is retained; the same exact lease
	// and stream sequence resumes without an Acquire or command replay.
	if err := input.Submit(workspace.TerminalInput{Lease: lease, Kind: "paste", Data: []byte("AFTER")}); err != nil {
		t.Fatal(err)
	}
	p := wait("READYBEFORE-AFTER")
	sub.Close()
	retained.topicMu.Lock()
	running := retained.topicRunning[attached.WorkspaceID+"/terminal/"+info.ID]
	retained.topicMu.Unlock()
	if running != nil {
		select {
		case <-running:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	// The producer retains sequence history instead of restarting the grid topic.
	retained.topicMu.Lock()
	if view := retained.terminalViews[key]; view != nil {
		oldSequence = view.current.seq
		if view.current.reset {
			t.Error("resumed grid sent full reset despite retained acknowledged base")
		}
	}
	retained.topicMu.Unlock()
	if oldSequence < 3 {
		t.Fatal("grid sequence reset on resume", oldSequence)
	}
	if strings.Count(projectionText(p), "BEFORE-") != 1 || strings.Count(projectionText(p), "AFTER") != 1 {
		t.Fatal("lost or duplicated input", projectionText(p))
	}
}

func TestTerminalStreamDictionaryScrollDECAndUnicodeReplica(t *testing.T) {
	e, err := terminalvt.New(terminalvt.Options{Cols: 30, Rows: 6})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.Feed([]byte("HEADER\r\nline-A\r\nline-B\r\nline-C\r\nline-D\r\nFOOTER"))
	var view terminalViewState
	var replica coreclient.TerminalGrid
	var base terminalViewSnapshot
	apply := func(reset bool) coreprotocol.TerminalFrame {
		t.Helper()
		grid, err := e.StreamRows(terminalvt.RowRequest{Start: -1})
		if err != nil {
			t.Fatal(err)
		}
		frame, next := view.build(base, workspace.TerminalProjection{Grid: grid}, reset)
		view.current = next
		p, err := replica.Apply(coreprotocol.TopicHeader{Seq: next.seq, Reset: reset}, frame)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(p.Grid.Cells, grid.Cells) || !reflect.DeepEqual(p.Grid.Wrapped, grid.Wrapped) {
			t.Fatal("wire replica differs from sole VT owner")
		}
		base = next
		return frame
	}
	first := apply(true)
	if len(first.Styles) == 0 {
		t.Fatal("no style dictionary")
	}
	e.Feed([]byte("\x1b[2;5r\x1b[5;1H\n\x1b[5;1H\x1b[38;2;12;34;56m界🙂é\x1b[0m"))
	next := apply(false)
	if len(next.Scrolls) == 0 {
		t.Fatal("partial region scroll not represented")
	}
	if len(next.Rows) >= 6 {
		t.Fatal("scroll republished full grid")
	}
	e.Feed([]byte("\x1b[?2026h\x1b[2Jpartial"))
	frozen := apply(false)
	if !frozen.Projection.Grid.Synchronized || len(frozen.Rows) != 0 {
		t.Fatal("DEC2026 published partial rows")
	}
	e.Feed([]byte("\x1b[?2026l"))
	apply(false)
}

func TestTerminalStreamHistoricalViewportStaysAnchoredDuringOutput(t *testing.T) {
	r, root := resourceRuntime(t)
	w := newResourceWire(t, r, "history-stream")
	var attached coreprotocol.AttachResult
	w.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached)
	var info workspace.TerminalInfo
	w.call(t, attached.WorkspaceID, coreprotocol.TerminalCreate, workspace.TerminalCreateOpts{Command: "/bin/sleep", Args: []string{"300"}, Cols: 30, Rows: 4}, &info)
	r.mu.Lock()
	service := r.workspaces[attached.WorkspaceID].ws.Terminals
	r.mu.Unlock()
	var source strings.Builder
	for i := range 150 {
		fmt.Fprintf(&source, "history-%04d\r\n", i)
	}
	service.Send(workspace.TerminalOutputMsg{ID: info.ID, Data: []byte(source.String())})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	frames := make(chan workspace.TerminalProjection, 8)
	failures := make(chan error, 1)
	sub := w.c.SubscribeTerminal(ctx, attached.WorkspaceID, info.ID, func(p workspace.TerminalProjection, err error) {
		if err != nil {
			failures <- err
			return
		}
		select {
		case frames <- p:
		case <-ctx.Done():
		}
	})
	defer func() { cancel(); sub.Close() }()
	wait := func(want string) workspace.TerminalProjection {
		t.Helper()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for {
			select {
			case p := <-frames:
				if strings.Contains(projectionText(p), want) {
					return p
				}
			case err := <-failures:
				t.Fatal(err)
			case <-timer.C:
				t.Fatalf("missing historical frame %q", want)
			}
		}
	}
	live := wait("history-0149")
	sub.SetViewport(terminalvt.RowRequest{Start: 16, HistoryEpoch: live.Grid.HistoryEpoch, HistoryBase: live.Grid.HistoryBase})
	p := wait("history-0016")
	want := projectionText(p)
	for i := range 8 {
		service.Send(workspace.TerminalOutputMsg{ID: info.ID, Data: []byte(fmt.Sprintf("output-%04d\r\n", i))})
		q := wait("history-0016")
		if q.Grid.Start != p.Grid.Start || q.Grid.HistoryEpoch != p.Grid.HistoryEpoch || projectionText(q) != want {
			t.Fatal("pushed output displaced historical viewport", projectionText(q))
		}
	}
	sub.SetViewport(terminalvt.RowRequest{Start: -1})
	wait("output-0007")
}
