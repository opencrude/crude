package coreclient

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
	"crude/terminalvt"
	"crude/workspace"
)

func TestTerminalAppliedSubscriptionWithholdsAckAndEndsRejectedOwner(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "installed", true: "rejected"}[rejected], func(t *testing.T) {
			a, b := net.Pipe()
			clientMux := corewire.NewMux(a, corewire.CarrierLocal, true)
			serverMux := corewire.NewMux(b, corewire.CarrierLocal, false)
			defer clientMux.Close()
			defer serverMux.Close()
			c := &Client{carrier: clientMux, ready: make(chan struct{}), done: make(chan struct{})}
			close(c.ready)
			ctx, cancel := context.WithCancel(t.Context())
			entered, applied := make(chan struct{}), make(chan error, 1)
			sub := c.SubscribeTerminalApplied(ctx, "workspace", "terminal", func(_ workspace.TerminalProjection, err error) error {
				if err != nil {
					return err
				}
				close(entered)
				select {
				case err := <-applied:
					return err
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			defer func() { cancel(); sub.Close() }()
			s, err := serverMux.AcceptStream(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			r := corewire.NewReader(s, corewire.MaxMessage)
			// The request and initial viewport are independent of frame application.
			for range 2 {
				if _, err := r.Next(); err != nil {
					t.Fatal(err)
				}
			}
			row := make([]terminalvt.Cell, 8)
			for i := range row {
				row[i] = terminalvt.Cell{Text: "x", Width: 1}
			}
			packed := terminalvt.AppendRow(nil, row, false)
			frame := coreprotocol.TerminalFrame{Projection: workspace.TerminalProjection{Grid: terminalvt.Projection{Cols: 8, GridRows: 2, Total: 2}}, Rows: []coreprotocol.TerminalRow{{Index: 0, Data: packed}, {Index: 1, Data: packed}}}
			if err := sendValues(s, &coreprotocol.TopicHeader{Seq: 1, Reset: true}, &frame); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("frame not delivered")
			}
			type result struct {
				body []byte
				err  error
			}
			next := make(chan result, 1)
			go func() { body, err := r.Next(); next <- result{body, err} }()
			select {
			case got := <-next:
				t.Fatalf("received ACK before application: %v", got.err)
			case <-time.After(50 * time.Millisecond):
			}
			if rejected {
				applied <- ErrTopicReset
			} else {
				applied <- nil
			}
			select {
			case got := <-next:
				if rejected {
					if got.err == nil {
						t.Fatal("rejected frame acknowledged")
					}
				} else {
					var ack coreprotocol.TopicAck
					if got.err != nil || coreprotocol.Decode(got.body, &ack) != nil || ack.Seq != 1 {
						t.Fatalf("missing applied ACK: %+v %v", ack, got.err)
					}
				}
			case <-time.After(5 * time.Second):
				t.Fatal("application did not release stream")
			}
			if rejected {
				select {
				case <-sub.done:
				case <-time.After(5 * time.Second):
					t.Fatal("rejected owner reopened subscription")
				}
			}
		})
	}
}

func TestTerminalMotionFloodKeepsLatestAndOrderedQuit(t *testing.T) {
	s := &TerminalInputStream{wake: make(chan struct{}, 1)}
	for i := range 300 {
		if err := s.Submit(workspace.TerminalInput{Kind: "mouse", Mouse: terminalvt.Mouse{Action: terminalvt.MouseMotion, X: i}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Submit(workspace.TerminalInput{Kind: "key", Key: terminalvt.Key{Code: 'q', Text: "q"}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	batch, _ := s.takeBatch(nil, now)
	if len(batch) != 2 || batch[0].Mouse.X != 299 || batch[1].Key.Text != "q" {
		t.Fatalf("motion backlog %#v", batch)
	}
	for i := range 300 {
		if err := s.Submit(workspace.TerminalInput{Kind: "mouse", Mouse: terminalvt.Mouse{Action: terminalvt.MouseMotion, X: i}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Submit(workspace.TerminalInput{Kind: "key", Key: terminalvt.Key{Text: "z"}}); err != nil {
		t.Fatal(err)
	}
	batch, delay := s.takeBatch(batch[:0], now.Add(time.Millisecond))
	if len(batch) != 1 || batch[0].Key.Text != "z" || delay <= 0 {
		t.Fatal("key delayed behind motion frame", batch, delay)
	}
	batch, _ = s.takeBatch(batch[:0], now.Add(16*time.Millisecond))
	if len(batch) != 1 || batch[0].Mouse.X != 299 {
		t.Fatal("latest motion lost", batch)
	}

}

func TestTerminalInputNeverSilentlyOverflows(t *testing.T) {
	s := &TerminalInputStream{wake: make(chan struct{}, 1)}
	for range 256 {
		if err := s.Submit(workspace.TerminalInput{Kind: "key"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Submit(workspace.TerminalInput{Kind: "key"}); !errors.Is(err, workspace.ErrTerminalBackpressure) || len(s.queue) != 256 {
		t.Fatal("overflow changed accepted queue", err)
	}
}

func TestTerminalMotionCannotReplayDragAfterRelease(t *testing.T) {
	s := &TerminalInputStream{wake: make(chan struct{}, 1)}
	if err := s.Submit(workspace.TerminalInput{Kind: "mouse", Mouse: terminalvt.Mouse{Action: terminalvt.MouseMotion, X: 3}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Submit(workspace.TerminalInput{Kind: "mouse", Mouse: terminalvt.Mouse{Action: terminalvt.MouseRelease, X: 4}}); err != nil {
		t.Fatal(err)
	}
	batch, _ := s.takeBatch(nil, time.Now())
	if len(batch) != 1 || batch[0].Mouse.Action != terminalvt.MouseRelease || s.motion != nil {
		t.Fatal("old motion survived positional release", batch)
	}
}

func TestTerminalGridChangedRowsAreImmutableAndSequenceFenced(t *testing.T) {
	row := func(text string) []byte {
		cells := make([]terminalvt.Cell, 8)
		for i := range cells {
			cells[i] = terminalvt.Cell{Text: text, Width: 1}
		}
		return terminalvt.AppendRow(nil, cells, false)
	}
	frame := coreprotocol.TerminalFrame{Projection: workspace.TerminalProjection{Grid: terminalvt.Projection{Cols: 8, GridRows: 2, Total: 2}}, Rows: []coreprotocol.TerminalRow{{Index: 0, Data: row("a")}, {Index: 1, Data: row("b")}}}
	g := &TerminalGrid{}
	p, err := g.Apply(coreprotocol.TopicHeader{Seq: 1, Reset: true}, frame)
	if err != nil {
		t.Fatal(err)
	}
	frame.Base, frame.Rows = 1, []coreprotocol.TerminalRow{{Index: 1, Data: row("c")}}
	next, err := g.Apply(coreprotocol.TopicHeader{Seq: 2}, frame)
	if err != nil {
		t.Fatal(err)
	}
	if p.Grid.Cells[1][0].Text != "b" || next.Grid.Cells[0][0].Text != "a" || next.Grid.Cells[1][0].Text != "c" {
		t.Fatal("grid changed wrong rows or mutated published frame")
	}
	if _, err := g.Apply(coreprotocol.TopicHeader{Seq: 3}, frame); err == nil {
		t.Fatal("accepted stale base")
	}
	frame.Rows = []coreprotocol.TerminalRow{{Index: 0, Data: row("x")}}
	if _, err := g.Apply(coreprotocol.TopicHeader{Seq: 4, Reset: true}, frame); err == nil {
		t.Fatal("accepted incomplete reset")
	}
}
