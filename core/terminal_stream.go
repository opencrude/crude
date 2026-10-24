package core

import (
	"bytes"
	"context"
	"errors"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
	"crude/terminalvt"
	"crude/workspace"
)

func (c *Connection) terminalStreamService(open coreprotocol.Open) *workspace.TerminalService {
	c.mu.Lock()
	defer c.mu.Unlock()
	if a := c.attached[open.WorkspaceID]; a != nil {
		return a.ws.Terminals
	}
	return nil
}

func (c *Connection) serveTerminalTopic(ctx context.Context, s corewire.Stream, reader *corewire.Reader, open coreprotocol.Open) {
	ctx, release := c.claimTopic(ctx, open.WorkspaceID+"/terminal/"+open.Key, func() { _ = s.Close() })
	defer release()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	fail := func(err error) {
		_ = send(s, &coreprotocol.TopicHeader{Reset: true}, &coreprotocol.TerminalFrame{Fault: &coreprotocol.Fault{Code: "terminal_failed", Message: err.Error()}})
	}
	service := c.terminalStreamService(open)
	if service == nil {
		return
	}
	dirty, unsubscribe, err := service.Subscribe(open.Key)
	if err != nil {
		fail(err)
		return
	}
	defer unsubscribe()
	acks := make(chan uint64, 1)
	viewports := make(chan coreprotocol.TerminalViewport, 1)
	go func() {
		defer cancel()
		for {
			body, err := reader.Next()
			if err != nil {
				return
			}
			var ack coreprotocol.TopicAck
			if coreprotocol.Decode(body, &ack) == nil {
				select {
				case acks <- ack.Seq:
				case <-ctx.Done():
					return
				}
			} else {
				var viewport coreprotocol.TerminalViewport
				if coreprotocol.Decode(body, &viewport) != nil {
					return
				}
				select {
				case viewports <- viewport:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	// claimTopic joins the old producer before exposing retained viewer state.
	c.topicMu.Lock()
	if c.terminalViews == nil {
		c.terminalViews = make(map[string]*terminalViewState)
	}
	key := open.WorkspaceID + "/" + open.Key
	view := c.terminalViews[key]
	if view == nil {
		view = &terminalViewState{}
		c.terminalViews[key] = view
	}
	c.topicMu.Unlock()
	base := view.current
	if open.Since != base.seq {
		if open.Since == view.previous.seq {
			base = view.previous
		} else {
			base = terminalViewSnapshot{}
		}
	}
	// The subscriber sends its current viewport before any frame is emitted.
	viewport := terminalvt.RowRequest{Start: -1}
	display := 16 * time.Millisecond
	select {
	case request := <-viewports:
		viewport = request.Request
		display = clampDisplay(request.DisplayIntervalNS)
	case <-ctx.Done():
		return
	}
	// mosh SSP scheduling (transportsender-impl.h): a change waits only
	// SEND_MINDELAY for the application's burst, frames stay one interval apart,
	// and an idle view arms no timer. A DEC 2026 hold is re-read each interval.
	pending, waiting, armed := true, false, true
	var sent time.Time
	timer := time.NewTimer(0)
	defer timer.Stop()
	arm := func() {
		if armed || waiting || !pending && !base.projection.Grid.Synchronized {
			return
		}
		_, rtt := c.Carrier()
		interval := max(display, min(rtt/2, 250*time.Millisecond))
		now := time.Now()
		wait := max(terminalSendMinDelay, sent.Add(interval).Sub(now))
		if !pending {
			wait = interval
		}
		timer.Reset(wait)
		armed = true
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-dirty:
			pending = true
			arm()
		case request := <-viewports:
			viewport, pending = request.Request, true
			display = clampDisplay(request.DisplayIntervalNS)
			arm()
		case ack := <-acks:
			if !waiting || ack != view.current.seq {
				return
			}
			base = view.current
			waiting = false
			arm()
		case <-timer.C:
			armed = false
			if !waiting && (pending || base.projection.Grid.Synchronized) {
				p, err := service.StreamRows(open.Key, viewport)
				if err != nil {
					c.topicMu.Lock()
					if c.terminalViews[key] == view {
						delete(c.terminalViews, key)
					}
					c.topicMu.Unlock()
					fail(err)
					return
				}
				g := p.Grid
				if viewport.Start >= 0 {
					viewport.Start, viewport.HistoryEpoch, viewport.HistoryBase = g.Start, g.HistoryEpoch, g.HistoryBase
				}
				reset := base.seq == 0 || g.Cols != base.projection.Grid.Cols || g.GridRows != base.projection.Grid.GridRows || g.HistoryEpoch != base.projection.Grid.HistoryEpoch
				frame, next := view.build(base, p, reset)
				view.previous, view.current = base, next
				if send(s, &coreprotocol.TopicHeader{Seq: next.seq, Reset: reset}, &frame) != nil {
					return
				}
				pending, waiting, sent = false, true, time.Now()
			}
			arm()
		}
	}
}

// A session retains the last applied and outstanding snapshots, not a stream's
// buffers. Stop-and-wait means these two cover every possible resumed base.
type terminalViewState struct {
	encoder           terminalvt.RowEncoder
	current, previous terminalViewSnapshot
}
type terminalViewSnapshot struct {
	seq        uint64
	reset      bool
	styles     int
	projection workspace.TerminalProjection
	packed     [][]byte
}

func (v *terminalViewState) build(base terminalViewSnapshot, p workspace.TerminalProjection, reset bool) (coreprotocol.TerminalFrame, terminalViewSnapshot) {
	g := p.Grid
	next := terminalViewSnapshot{seq: max(v.current.seq, base.seq) + 1, reset: reset, projection: p, packed: make([][]byte, len(g.Cells))}
	frame := coreprotocol.TerminalFrame{Base: base.seq, Projection: p, StyleBase: base.styles}
	frame.Projection.Grid.Cells, frame.Projection.Grid.Wrapped = nil, nil
	for i, row := range g.Cells {
		if !reset && i < len(base.projection.Grid.Cells) && len(row) > 0 && &row[0] == &base.projection.Grid.Cells[i][0] && g.Wrapped[i] == base.projection.Grid.Wrapped[i] {
			next.packed[i] = base.packed[i]
		} else {
			next.packed[i] = v.encoder.Append(nil, row, g.Wrapped[i])
		}
	}
	// Find moved contiguous regions in packed dirty rows. No cell export or cell
	// comparison is needed, and fixed headers/footers survive partial-region scroll.
	changed := 0
	if !reset {
		for i, row := range next.packed {
			if !bytes.Equal(row, base.packed[i]) {
				changed++
			}
		}
	}
	if !reset && changed > 1 {
		frame.Scrolls = terminalScrolls(base.packed, next.packed)
	}
	shifted := append([][]byte(nil), base.packed...)
	for _, op := range frame.Scrolls {
		applyPackedScroll(shifted, op)
	}
	for i, row := range next.packed {
		if reset || i >= len(shifted) || !bytes.Equal(row, shifted[i]) {
			frame.Rows = append(frame.Rows, coreprotocol.TerminalRow{Index: i, Data: row})
		}
	}
	next.styles = len(v.encoder.Definitions)
	frame.Styles = v.encoder.Definitions[base.styles:next.styles]
	return frame, next
}

func terminalScrolls(old, rows [][]byte) []coreprotocol.TerminalScroll {
	if len(old) != len(rows) {
		return nil
	}
	var best coreprotocol.TerminalScroll
	bestCount := 1
	for delta := 1 - len(rows); delta < len(rows); delta++ {
		if delta == 0 {
			continue
		}
		start, count := 0, 0
		for i := max(0, -delta); i < min(len(rows), len(rows)-delta); i++ {
			if bytes.Equal(rows[i], old[i+delta]) {
				if count == 0 {
					start = i
				}
				count++
				if count > bestCount {
					bestCount = count
					best = coreprotocol.TerminalScroll{Top: min(start, start+delta), Bottom: max(i+1, i+delta+1), Rows: delta}
				}
			} else {
				count = 0
			}
		}
	}
	if bestCount > 1 {
		return []coreprotocol.TerminalScroll{best}
	}
	return nil
}
func applyPackedScroll(rows [][]byte, op coreprotocol.TerminalScroll) {
	for i := op.Top; i < op.Bottom; i++ {
		from := i + op.Rows
		if op.Rows > 0 {
			if from < op.Bottom {
				rows[i] = rows[from]
			} else {
				rows[i] = nil
			}
		}
	}
	if op.Rows < 0 {
		for i := op.Bottom - 1; i >= op.Top; i-- {
			from := i + op.Rows
			if from >= op.Top {
				rows[i] = rows[from]
			} else {
				rows[i] = nil
			}
		}
	}
}

func (c *Connection) serveTerminalInput(ctx context.Context, s corewire.Stream, reader *corewire.Reader, open coreprotocol.Open) {
	if !validID(open.ID) {
		return
	}
	ctx, release := c.claimTopic(ctx, open.WorkspaceID+"/input/"+open.Key, func() { _ = s.Close() })
	defer release()
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	service := c.terminalStreamService(open)
	if service == nil {
		return
	}
	owner := workspace.TerminalController{ClientID: c.Owner.ClientID, Epoch: c.Owner.Epoch}
	for {
		body, err := reader.Next()
		if err != nil {
			return
		}
		var batch coreprotocol.TerminalInputBatch
		if coreprotocol.Decode(body, &batch) != nil || batch.First == 0 || len(batch.Inputs) == 0 || len(batch.Inputs) > 256 {
			return
		}
		ack := coreprotocol.TerminalInputAck{}
		for i, input := range batch.Inputs {
			if input.Lease.Owner != owner || input.Lease.TerminalID != open.Key {
				ack.Fault = &coreprotocol.Fault{Code: "terminal_control", Message: workspace.ErrTerminalControl.Error()}
				break
			}
			seq := batch.First + uint64(i)
			if seq < batch.First {
				return
			}
			if err := service.ApplyInput(open.ID, seq, batch.Ack, input); err != nil {
				if errors.Is(err, workspace.ErrTerminalInputSequence) || errors.Is(err, workspace.ErrTerminalControl) {
					ack.Fault = &coreprotocol.Fault{Code: "terminal_input", Message: err.Error()}
					if errors.Is(err, workspace.ErrTerminalControl) {
						ack.Fault.Code = "terminal_control"
					}
					break
				}
				ack.Failures = append(ack.Failures, coreprotocol.TerminalInputFailure{Seq: seq, Message: err.Error()})
			}
			ack.Seq = seq
		}
		if send(s, &ack) != nil {
			return
		}
	}
}

// terminalSendMinDelay is mosh's SEND_MINDELAY: long enough to carry a key's
// whole echo burst in one frame, short against any display or network interval.
const terminalSendMinDelay = 8 * time.Millisecond

func clampDisplay(ns int64) time.Duration {
	if ns == 0 {
		return 16 * time.Millisecond
	}
	return max(time.Millisecond, min(time.Duration(ns), 250*time.Millisecond))
}
