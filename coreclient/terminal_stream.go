package coreclient

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
	"crude/terminalvt"
	"crude/workspace"
)

// TerminalGrid is a detached grid replica; only the subscription goroutine
// mutates it. Each published projection remains immutable after publication.
type TerminalGrid struct {
	sequence   uint64
	projection workspace.TerminalProjection
	decoder    terminalvt.RowDecoder
}

func (g *TerminalGrid) Apply(header coreprotocol.TopicHeader, frame coreprotocol.TerminalFrame) (workspace.TerminalProjection, error) {
	if frame.Fault != nil {
		return workspace.TerminalProjection{}, frame.Fault
	}
	if header.Seq <= g.sequence {
		return workspace.TerminalProjection{}, errors.New("terminal grid sequence regressed")
	}
	p := frame.Projection
	r := &p.Grid
	if r.Cols < 8 || r.Cols > terminalvt.MaxCols || r.GridRows < 2 || r.GridRows > terminalvt.MaxRows || r.Total < r.GridRows || r.Start < 0 || r.Start > r.Total-r.GridRows || len(frame.Rows) > r.GridRows || len(frame.Scrolls) > r.GridRows {
		return p, terminalvt.ErrPacked
	}
	if !header.Reset && (frame.Base != g.sequence || r.Cols != g.projection.Grid.Cols || r.GridRows != g.projection.Grid.GridRows) {
		return p, errors.New("terminal grid sequence gap")
	}
	r.Cells = make([][]terminalvt.Cell, r.GridRows)
	r.Wrapped = make([]bool, r.GridRows)
	if !header.Reset {
		copy(r.Cells, g.projection.Grid.Cells)
		copy(r.Wrapped, g.projection.Grid.Wrapped)
	}
	seen := make([]bool, r.GridRows)
	if frame.StyleBase < 0 || frame.StyleBase > len(g.decoder.Styles) || frame.StyleBase+len(frame.Styles) > 4096 {
		return p, terminalvt.ErrPacked
	}
	styles := append(g.decoder.Styles[:frame.StyleBase:frame.StyleBase], frame.Styles...)
	for _, style := range frame.Styles {
		if style.Text != "" || style.Width != 0 {
			return p, terminalvt.ErrPacked
		}
	}
	decoder := g.decoder
	decoder.Styles = styles
	for _, op := range frame.Scrolls {
		if header.Reset || op.Top < 0 || op.Bottom > r.GridRows || op.Top >= op.Bottom || op.Rows == 0 || op.Rows <= op.Top-op.Bottom || op.Rows >= op.Bottom-op.Top {
			return p, terminalvt.ErrPacked
		}
		if op.Rows > 0 {
			copy(r.Cells[op.Top:op.Bottom-op.Rows], r.Cells[op.Top+op.Rows:op.Bottom])
			copy(r.Wrapped[op.Top:op.Bottom-op.Rows], r.Wrapped[op.Top+op.Rows:op.Bottom])
			clear(r.Cells[op.Bottom-op.Rows : op.Bottom])
		} else {
			copy(r.Cells[op.Top-op.Rows:op.Bottom], r.Cells[op.Top:op.Bottom+op.Rows])
			copy(r.Wrapped[op.Top-op.Rows:op.Bottom], r.Wrapped[op.Top:op.Bottom+op.Rows])
			clear(r.Cells[op.Top : op.Top-op.Rows])
		}
	}
	for _, row := range frame.Rows {
		if row.Index < 0 || row.Index >= r.GridRows || seen[row.Index] {
			return p, terminalvt.ErrPacked
		}
		// Width was bounded before allocation. Each changed row owns one exact
		// grid-width buffer; published rows are never recycled into later frames.
		cells, wrapped, rest, err := decoder.Decode(make([]terminalvt.Cell, 0, r.Cols), row.Data)
		if err != nil || len(rest) != 0 || len(cells) != r.Cols {
			return p, terminalvt.ErrPacked
		}
		r.Cells[row.Index], r.Wrapped[row.Index], seen[row.Index] = cells, wrapped, true
	}
	for _, row := range r.Cells {
		if len(row) != r.Cols {
			return p, terminalvt.ErrPacked
		}
	}
	g.sequence, g.projection = header.Seq, p
	g.decoder = decoder
	return p, nil
}

type TerminalSubscription struct {
	mu       sync.Mutex
	viewport terminalvt.RowRequest
	wake     chan struct{}
	cancel   context.CancelFunc
	done     chan struct{}
	display  time.Duration
}

func (s *TerminalSubscription) Close()  { s.cancel(); <-s.done }
func (s *TerminalSubscription) Cancel() { s.cancel() }

// SetDisplayInterval declares the viewer's measured presentation cadence.
func (s *TerminalSubscription) SetDisplayInterval(interval time.Duration) {
	s.mu.Lock()
	s.display = max(time.Millisecond, min(interval, 250*time.Millisecond))
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *TerminalSubscription) SetViewport(request terminalvt.RowRequest) {
	request.Count = 0
	s.mu.Lock()
	changed := request != s.viewport
	s.viewport = request
	s.mu.Unlock()
	if changed {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// SubscribeTerminal delivers immutable frames synchronously. Returning from handle
// means the application has installed the frame and permits its acknowledgement.
func (c *Client) SubscribeTerminal(ctx context.Context, workspaceID, id string, handle func(workspace.TerminalProjection, error)) *TerminalSubscription {
	return c.SubscribeTerminalApplied(ctx, workspaceID, id, func(p workspace.TerminalProjection, err error) error {
		handle(p, err)
		return nil
	})
}

// SubscribeTerminalApplied lets an asynchronous consumer wait for its owning
// application thread to install a frame before returning nil. A handler error
// ends the subscription without acknowledging or reopening the rejected frame.
// Handlers must observe ctx/client cancellation while waiting for application.
func (c *Client) SubscribeTerminalApplied(ctx context.Context, workspaceID, id string, handle func(workspace.TerminalProjection, error) error) *TerminalSubscription {
	ctx, cancel := context.WithCancel(ctx)
	sub := &TerminalSubscription{viewport: terminalvt.RowRequest{Start: -1}, display: 16 * time.Millisecond, wake: make(chan struct{}, 1), cancel: cancel, done: make(chan struct{})}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer close(sub.done)
		grid := &TerminalGrid{}
		for {
			conn, err := c.current(ctx)
			if err != nil {
				return
			}
			s, err := conn.OpenStream(ctx, corewire.ClassInteractive)
			if err != nil {
				if carrierLost(conn) {
					continue
				}
				_ = handle(workspace.TerminalProjection{}, err)
				return
			}
			rejected := false
			err = func() error {
				defer s.Close()
				streamCtx, stopStream := context.WithCancel(ctx)
				defer stopStream()
				stop := context.AfterFunc(streamCtx, func() { _ = s.Close() })
				defer stop()
				if err := sendValues(s, &coreprotocol.Open{Kind: coreprotocol.StreamTopic, WorkspaceID: workspaceID, Topic: coreprotocol.TopicTerminal, Key: id, Since: grid.sequence}); err != nil {
					return err
				}
				acks := make(chan uint64, 1)
				writerDone := make(chan struct{})
				go func() {
					defer close(writerDone)
					defer stopStream()
					publishViewport := func() error {
						sub.mu.Lock()
						request, display := sub.viewport, sub.display
						sub.mu.Unlock()
						return sendValues(s, &coreprotocol.TerminalViewport{Request: request, DisplayIntervalNS: int64(display)})
					}
					if publishViewport() != nil {
						return
					}
					for {
						select {
						case seq := <-acks:
							if sendValues(s, &coreprotocol.TopicAck{Seq: seq}) != nil {
								return
							}
						case <-sub.wake:
							if publishViewport() != nil {
								return
							}
						case <-streamCtx.Done():
							return
						case <-c.done:
							return
						}
					}
				}()
				defer func() { stopStream(); _ = s.Close(); <-writerDone }()
				reader := corewire.NewReader(s, corewire.MaxMessage)
				for {
					body, err := reader.Next()
					if err != nil {
						return err
					}
					var header coreprotocol.TopicHeader
					rest, err := coreprotocol.DecodePrefix(body, &header)
					if err != nil {
						return err
					}
					var frame coreprotocol.TerminalFrame
					if err := coreprotocol.Decode(rest, &frame); err != nil {
						return err
					}
					p, err := grid.Apply(header, frame)
					if err != nil {
						return err
					}
					if err := handle(p, nil); err != nil {
						rejected = true
						return err
					}
					select {
					case acks <- header.Seq:
					case <-streamCtx.Done():
						return streamCtx.Err()
					}
				}
			}()
			if ctx.Err() != nil || rejected {
				return
			}
			select {
			case <-c.done:
				return
			default:
			}
			if !carrierLost(conn) {
				_ = handle(workspace.TerminalProjection{}, err)
				return
			}
		}
	}()
	return sub
}

// TerminalInputStream admits ordered input without network I/O. A single worker
// sends bounded batches independently of grid delivery, retaining unacknowledged
// input across carrier replacement. Only adjacent pointer motions may conflate.
type TerminalInputStream struct {
	mu         sync.Mutex
	queue      []workspace.TerminalInput
	bytes      int
	wake       chan struct{}
	cancel     context.CancelFunc
	done       chan struct{}
	err        error
	motion     *workspace.TerminalInput
	nextMotion time.Time
	display    time.Duration
}

func (s *TerminalInputStream) Submit(input workspace.TerminalInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if input.Kind == "mouse" && input.Mouse.Action == terminalvt.MouseMotion {
		s.motion = &input
		select {
		case s.wake <- struct{}{}:
		default:
		}
		return nil
	}
	// A button/wheel carries an exact position and supersedes unsent motion.
	// Never deliver an old drag position after the corresponding release.
	if input.Kind == "mouse" {
		s.motion = nil
	}
	if len(s.queue) >= 256 || s.bytes+len(input.Data) > 1<<20 {
		return workspace.ErrTerminalBackpressure
	}
	input.Data = append([]byte(nil), input.Data...)
	s.queue = append(s.queue, input)
	s.bytes += len(input.Data)
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

func (s *TerminalInputStream) takeBatch(dst []workspace.TerminalInput, now time.Time) ([]workspace.TerminalInput, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dst = dst[:0]
	if s.motion != nil && !now.Before(s.nextMotion) {
		dst = append(dst, *s.motion)
		s.motion = nil
		interval := s.display
		if interval == 0 {
			interval = 16 * time.Millisecond
		}
		s.nextMotion = now.Add(interval)
	}
	count := min(64-len(dst), len(s.queue))
	dst = append(dst, s.queue[:count]...)
	copy(s.queue, s.queue[count:])
	clear(s.queue[len(s.queue)-count:])
	s.queue = s.queue[:len(s.queue)-count]
	for _, item := range dst {
		s.bytes -= len(item.Data)
	}
	if s.motion != nil {
		return dst, max(time.Nanosecond, s.nextMotion.Sub(now))
	}
	return dst, 0
}

func (s *TerminalInputStream) SetDisplayInterval(interval time.Duration) {
	s.mu.Lock()
	s.display = max(time.Millisecond, min(interval, 250*time.Millisecond))
	s.mu.Unlock()
}

func (s *TerminalInputStream) Cancel() { s.mu.Lock(); s.err = ErrClosed; s.mu.Unlock(); s.cancel() }
func (s *TerminalInputStream) Close()  { s.Cancel(); <-s.done }

func (c *Client) OpenTerminalInput(ctx context.Context, workspaceID, id string, failed func(error)) *TerminalInputStream {
	ctx, cancel := context.WithCancel(ctx)
	s := &TerminalInputStream{wake: make(chan struct{}, 1), cancel: cancel, done: make(chan struct{})}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer close(s.done)
		err := c.runTerminalInput(ctx, workspaceID, id, s, failed)
		if err == nil {
			err = ErrClosed
		}
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		if ctx.Err() == nil && failed != nil {
			failed(err)
		}
	}()
	return s
}

func (c *Client) runTerminalInput(ctx context.Context, workspaceID, id string, input *TerminalInputStream, failed func(error)) error {
	streamID := randomID()
	var ack uint64
	var batch []workspace.TerminalInput
	for {
		conn, err := c.current(ctx)
		if err != nil {
			return err
		}
		s, err := conn.OpenStream(ctx, corewire.ClassInput)
		if err != nil {
			if carrierLost(conn) {
				continue
			}
			return err
		}
		err = func() error {
			defer s.Close()
			stop := context.AfterFunc(ctx, func() { _ = s.Close() })
			defer stop()
			if err := sendValues(s, &coreprotocol.Open{Kind: coreprotocol.StreamInput, ID: streamID, WorkspaceID: workspaceID, Topic: coreprotocol.TopicTerminalInput, Key: id, Since: ack}); err != nil {
				return err
			}
			reader := corewire.NewReader(s, corewire.MaxMessage)
			motionTimer := time.NewTimer(time.Hour)
			defer motionTimer.Stop()
			for {
				if len(batch) == 0 {
					var delay time.Duration
					batch, delay = input.takeBatch(batch, time.Now())
					if len(batch) == 0 {
						var motionWake <-chan time.Time
						if delay > 0 {
							motionTimer.Reset(delay)
							motionWake = motionTimer.C
						}
						select {
						case <-motionWake:
							continue
						case <-input.wake:
							continue
						case <-ctx.Done():
							return ctx.Err()
						case <-c.done:
							return c.Err()
						case <-conn.Done():
							return conn.Err()
						}
					}
				}
				if err := sendValues(s, &coreprotocol.TerminalInputBatch{First: ack + 1, Ack: ack, Inputs: batch}); err != nil {
					return err
				}
				body, err := reader.Next()
				if err != nil {
					return err
				}
				var result coreprotocol.TerminalInputAck
				if err := coreprotocol.Decode(body, &result); err != nil {
					return err
				}
				if result.Fault != nil {
					if result.Fault.Code == "terminal_control" {
						return fmt.Errorf("%w: %s", workspace.ErrTerminalControl, result.Fault.Message)
					}
					return result.Fault
				}
				if result.Seq != ack+uint64(len(batch)) {
					return errors.New("invalid terminal input acknowledgement")
				}
				for _, failure := range result.Failures {
					if failed != nil {
						failed(fmt.Errorf("terminal input %d: %s", failure.Seq, failure.Message))
					}
				}
				ack = result.Seq
				clear(batch)
				batch = batch[:0]
			}
		}()
		if ctx.Err() != nil || !carrierLost(conn) {
			return err
		}
	}
}
