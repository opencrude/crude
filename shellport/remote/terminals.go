package remote

import (
	"context"
	"slices"
	"sync"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/shellport"
	"crude/terminalvt"
	"crude/workspace"
)

// terminalReplica mirrors the core's PTY inventory for synchronous shell reads.
// The core owns every PTY and its one parser; this shell holds only a lease.
type terminalReplica struct {
	w           *Workspace
	infos       []workspace.TerminalInfo
	leaseMu     sync.Mutex
	leases      map[string]workspace.TerminalLease
	streamMu    sync.Mutex
	grids       map[string]*terminalGrid
	inputs      map[string]*coreclient.TerminalInputStream
	inputLeases map[string]workspace.TerminalLease
	modes       map[string]terminalvt.Modes
	projections map[string]workspace.TerminalProjection
	predicted   map[string]workspace.TerminalProjection
	prediction  terminalPredictionPolicy
	display     time.Duration
}

// terminalGrid is one exact mounted subscription generation. Its context also
// cancels application waits; cancelling only the transport would leave a queued
// frame waiting for an Update that may never run after detach.
type terminalGrid struct {
	*coreclient.TerminalSubscription
	ctx    context.Context
	cancel context.CancelFunc
	client *coreclient.Client
}

type terminalInputFailure struct {
	grid  *terminalGrid
	lease workspace.TerminalLease
}

var (
	_ shellport.TerminalService    = (*terminalReplica)(nil)
	_ shellport.TerminalManager    = (*terminalReplica)(nil)
	_ shellport.StreamingTerminals = (*terminalReplica)(nil)
)

func (w *Workspace) Terminals() shellport.TerminalService { return w.terminals }
func (w *Workspace) TermMgr() shellport.TerminalManager   { return w.terminals }

// EnsureTerminals returns the core-owned service; creation happens in the core.
func (w *Workspace) EnsureTerminals() (shellport.TerminalService, error) {
	return w.terminals, nil
}

func (t *terminalReplica) Get(id string) (workspace.TerminalInfo, bool) {
	for _, info := range t.infos {
		if info.ID == id {
			return info, true
		}
	}
	return workspace.TerminalInfo{}, false
}

func (t *terminalReplica) List() []workspace.TerminalInfo { return slices.Clone(t.infos) }

func (t *terminalReplica) ownsClipboard(offer coreprotocol.TerminalClipboard, owner coreprotocol.Owner) bool {
	t.leaseMu.Lock()
	defer t.leaseMu.Unlock()
	lease, ok := t.leases[offer.TerminalID]
	return ok && offer.Generation != 0 && lease.Generation == offer.Generation &&
		lease.Owner.ClientID == owner.ClientID && lease.Owner.Epoch == owner.Epoch
}

// install adopts a fresh inventory and derives the exit/title messages the
// shell would have received from an in-process terminal manager.
func (t *terminalReplica) install(infos []workspace.TerminalInfo) []any {
	var follow []any
	for _, next := range infos {
		previous, known := t.Get(next.ID)
		if known && previous.Alive && !next.Alive {
			follow = append(follow, workspace.TerminalExitedMsg{ID: next.ID, ExitCode: next.ExitCode, Err: next.ExitErr})
		}
		if known && previous.Title != next.Title {
			follow = append(follow, workspace.TerminalTitleMsg{ID: next.ID, Title: next.Title})
		}
	}
	// A terminal closed by another owner (a peer shell or Crude control) leaves
	// the inventory entirely; this shell must unmount it like an exit.
	for _, previous := range t.infos {
		if !slices.ContainsFunc(infos, func(next workspace.TerminalInfo) bool { return next.ID == previous.ID }) {
			t.stopStreams(previous.ID)
			t.leaseMu.Lock()
			delete(t.leases, previous.ID)
			t.leaseMu.Unlock()
			follow = append(follow, workspace.TerminalExitedMsg{ID: previous.ID, ExitCode: previous.ExitCode, Err: previous.ExitErr})
		}
	}
	t.infos = slices.Clone(infos)
	return follow
}

func (t *terminalReplica) fetch(ctx context.Context) ([]workspace.TerminalInfo, error) {
	var infos coreprotocol.TerminalInfos
	err := t.w.call(ctx, coreprotocol.TerminalList, &coreprotocol.Empty{}, &infos)
	return infos, err
}

// Acquire takes control for this connection. The core binds the lease to the
// connection's own identity; the shell's local controller value is not trusted.
func (t *terminalReplica) Acquire(id string, _ workspace.TerminalController) (workspace.TerminalLease, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	lease, err := t.w.rpc().ControlTerminal(ctx, id)
	if err == nil {
		t.leaseMu.Lock()
		if t.leases == nil {
			t.leases = make(map[string]workspace.TerminalLease)
		}
		t.leases[id] = lease
		t.leaseMu.Unlock()
	}
	return lease, err
}

func (t *terminalReplica) Detach(workspace.TerminalController) {
	t.streamMu.Lock()
	for id := range t.grids {
		t.stopStreamsLocked(id)
	}
	for id := range t.inputs {
		t.stopStreamsLocked(id)
	}
	t.streamMu.Unlock()
	t.leaseMu.Lock()
	leases := t.leases
	t.leases = nil
	t.leaseMu.Unlock()
	for _, lease := range leases {
		t.w.commands.push(func(ctx context.Context) {
			_ = t.w.call(ctx, coreprotocol.TerminalRelease, &lease, &coreprotocol.Empty{})
		})
	}
}

func (t *terminalReplica) Create(opts workspace.TerminalCreateOpts) (workspace.TerminalInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	info, err := t.w.rpc().CreateTerminal(ctx, opts)
	if err == nil {
		t.w.requestTerminalRefresh()
	}
	return info, err
}

func (t *terminalReplica) Close(id string) {
	t.stopStreams(id)
	t.leaseMu.Lock()
	delete(t.leases, id)
	t.leaseMu.Unlock()
	t.w.commands.push(func(ctx context.Context) {
		_ = t.w.call(ctx, coreprotocol.TerminalClose, coreprotocol.TerminalIDRequest{ID: id}, &coreprotocol.Empty{})
	})
}

func (t *terminalReplica) Rows(id string, request terminalvt.RowRequest) (workspace.TerminalProjection, error) {
	t.streamMu.Lock()
	if grid := t.grids[id]; grid != nil {
		viewport := request
		if request.Count > 0 {
			viewport.Start = request.ViewportStart
		}
		grid.SetViewport(viewport)
	}
	t.streamMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return t.w.rpc().TerminalRows(ctx, id, request)
}

func (t *terminalReplica) Focus(lease workspace.TerminalLease, focused bool) error {
	return t.SendInput(workspace.TerminalInput{Lease: lease, Kind: "focus", Focused: focused})
}

func (t *terminalReplica) Key(lease workspace.TerminalLease, key terminalvt.Key) error {
	return t.SendInput(workspace.TerminalInput{Lease: lease, Kind: "key", Key: key})
}

func (t *terminalReplica) Mouse(lease workspace.TerminalLease, mouse terminalvt.Mouse) error {
	return t.SendInput(workspace.TerminalInput{Lease: lease, Kind: "mouse", Mouse: mouse})
}

func (t *terminalReplica) Paste(lease workspace.TerminalLease, data []byte) error {
	return t.SendInput(workspace.TerminalInput{Lease: lease, Kind: "paste", Data: data})
}

func (t *terminalReplica) Resize(lease workspace.TerminalLease, cols, rows int) error {
	return t.SendInput(workspace.TerminalInput{Lease: lease, Kind: "resize", Cols: cols, Rows: rows})
}

func (t *terminalReplica) StartGrid(id string) {
	t.streamMu.Lock()
	defer t.streamMu.Unlock()
	if t.grids == nil {
		t.grids = make(map[string]*terminalGrid)
		t.modes = make(map[string]terminalvt.Modes)
		t.projections = make(map[string]workspace.TerminalProjection)
		t.predicted = make(map[string]workspace.TerminalProjection)
	}
	rpc := t.w.rpc()
	if old := t.grids[id]; old != nil {
		if old.client == rpc.Client && old.ctx.Err() == nil {
			return
		}
		t.stopStreamsLocked(id)
	}
	ctx, cancel := context.WithCancel(t.w.ctx)
	grid := &terminalGrid{ctx: ctx, cancel: cancel, client: rpc.Client}
	t.grids[id] = grid
	grid.TerminalSubscription = rpc.Client.SubscribeTerminalApplied(ctx, rpc.ID, id, func(p workspace.TerminalProjection, err error) error {
		return t.publishTerminalFrame(grid, shellport.TerminalFrameMsg{Service: t, ID: id, Projection: p, Err: err, Frame: grid})
	})
	if t.display > 0 {
		grid.SetDisplayInterval(t.display)
	}
}

// publishTerminalFrame runs only off Update. FIFO admission is not application:
// the subscription holds its ACK until the owning Update completes this receipt.
func (t *terminalReplica) publishTerminalFrame(grid *terminalGrid, msg shellport.TerminalFrameMsg) error {
	select {
	case <-t.w.busSet:
	case <-grid.ctx.Done():
		return grid.ctx.Err()
	case <-grid.client.Done():
		return coreclient.ErrClosed
	}
	done := make(chan error, 1)
	msg.Applied = done
	t.w.send(msg)
	select {
	case err := <-done:
		return err
	case <-grid.ctx.Done():
		return grid.ctx.Err()
	case <-grid.client.Done():
		return coreclient.ErrClosed
	}
}

// ApplyTerminalFrame is called by the owning Update before painting the frame.
// No transport callback advances modes, prediction or authoritative caches.
func (t *terminalReplica) ApplyTerminalFrame(msg *shellport.TerminalFrameMsg) error {
	t.streamMu.Lock()
	defer t.streamMu.Unlock()
	grid, ok := msg.Frame.(*terminalGrid)
	failure, inputFailure := msg.Frame.(terminalInputFailure)
	if inputFailure {
		grid = failure.grid
		ok = t.inputLeases[msg.ID] == failure.lease
	}
	client, _ := t.w.conn()
	if !ok || grid == nil || t.grids[msg.ID] != grid || grid.ctx.Err() != nil || grid.client != client {
		return context.Canceled
	}
	select {
	case <-client.Done():
		return context.Canceled
	default:
	}
	if inputFailure {
		if _, pending := t.predicted[msg.ID]; pending {
			msg.Projection = t.projections[msg.ID]
			delete(t.predicted, msg.ID)
		}
	} else if msg.Err == nil {
		t.modes[msg.ID] = msg.Projection.Grid.Modes
		t.projections[msg.ID] = msg.Projection
		delete(t.predicted, msg.ID)
	}
	return nil
}

func (t *terminalReplica) SendInput(input workspace.TerminalInput) error {
	id := input.Lease.TerminalID
	t.streamMu.Lock()
	defer t.streamMu.Unlock()
	if input.Kind == "mouse" && input.Mouse.Action == terminalvt.MouseMotion && !t.modes[id].MouseTracking {
		return nil
	}
	if t.inputs == nil {
		t.inputs = make(map[string]*coreclient.TerminalInputStream)
		t.inputLeases = make(map[string]workspace.TerminalLease)
	}
	stream := t.inputs[id]
	if stream != nil && t.inputLeases[id] != input.Lease {
		stream.Cancel()
		delete(t.predicted, id)
		stream = nil
	}
	if stream == nil {
		grid := t.grids[id]
		stream = t.w.rpc().Client.OpenTerminalInput(t.w.ctx, t.w.id, id, func(err error) {
			if grid != nil {
				_ = t.publishTerminalFrame(grid, shellport.TerminalFrameMsg{Service: t, ID: id, Err: err, Frame: terminalInputFailure{grid: grid, lease: input.Lease}})
			}
		})
		t.inputs[id] = stream
		t.inputLeases[id] = input.Lease
		if t.display > 0 {
			stream.SetDisplayInterval(t.display)
		}
	}
	return stream.Submit(input)
}

// PredictiveKey runs on Update after successful admission, never sends to the
// synchronous event bus. Any authoritative frame (including mismatch) replaces
// this provisional underline; errors explicitly roll it back to the authority.
func (t *terminalReplica) PredictiveKey(id string, key terminalvt.Key) (workspace.TerminalProjection, bool) {
	carrier, rtt := t.w.rpc().Client.Carrier()
	t.streamMu.Lock()
	defer t.streamMu.Unlock()
	p, ok := t.projections[id]
	predicted, active := t.predicted[id]
	if active {
		p = predicted
	}
	if !t.prediction.update(carrier, rtt, active) || !ok || t.inputLeases[id].Generation == 0 {
		return p, false
	}
	p, ok = terminalPrediction(p, key, t.prediction.flag)
	if ok {
		t.predicted[id] = p
	}
	return p, ok
}

// terminalPredictionPolicy is mosh's adaptive display (terminaloverlay.cc) on
// its send interval, ceil(SRTT/2) clamped to 20–250 ms: show predictions above
// 30 ms until 20 ms with none outstanding; underline above 80 ms until 50 ms.
type terminalPredictionPolicy struct{ show, flag bool }

func (p *terminalPredictionPolicy) update(carrier corewire.Carrier, rtt time.Duration, active bool) bool {
	if carrier == "" || carrier == corewire.CarrierLocal {
		*p = terminalPredictionPolicy{}
		return false
	}
	interval := min(max(rtt/2, 20*time.Millisecond), 250*time.Millisecond)
	if interval > 30*time.Millisecond {
		p.show = true
	} else if interval <= 20*time.Millisecond && !active {
		p.show = false
	}
	if interval > 80*time.Millisecond {
		p.flag = true
	} else if interval <= 50*time.Millisecond {
		p.flag = false
	}
	return p.show
}

func terminalPrediction(p workspace.TerminalProjection, key terminalvt.Key, flag bool) (workspace.TerminalProjection, bool) {
	g := p.Grid
	if key.Mod != 0 || len(key.Text) != 1 || key.Text[0] < ' ' || key.Text[0] > '~' || g.Synchronized || g.Modes.AltScreen || g.Modes.MouseTracking || !g.Cursor.Visible || g.Start != g.Total-g.GridRows {
		return p, false
	}
	x, y := g.Cursor.X, g.Cursor.Y
	if y < 0 || y >= len(g.Cells) || x < 0 || x >= g.Cols-1 || len(g.Cells[y]) != g.Cols || g.Cells[y][x].Width != 1 {
		return p, false
	}
	g.Cells = append([][]terminalvt.Cell(nil), g.Cells...)
	g.Cells[y] = append([]terminalvt.Cell(nil), g.Cells[y]...)
	g.Cells[y][x].Text = key.Text
	g.Cells[y][x].Style.Underline = flag
	g.Cursor.X++
	p.Grid = g
	return p, true
}

func (t *terminalReplica) SetViewport(id string, request terminalvt.RowRequest) {
	t.streamMu.Lock()
	defer t.streamMu.Unlock()
	if grid := t.grids[id]; grid != nil {
		grid.SetViewport(request)
	}
}

func (t *terminalReplica) stopStreams(id string) {
	t.streamMu.Lock()
	defer t.streamMu.Unlock()
	t.stopStreamsLocked(id)
}
func (t *terminalReplica) stopStreamsLocked(id string) {
	grid, input := t.grids[id], t.inputs[id]
	delete(t.grids, id)
	delete(t.inputs, id)
	delete(t.inputLeases, id)
	delete(t.modes, id)
	delete(t.projections, id)
	delete(t.predicted, id)
	// Cancel never waits on Update. The core client owns and joins workers at
	// workspace shutdown, including cancelled inventory subscriptions.
	if grid != nil {
		grid.cancel()
		grid.Cancel()
	}
	if input != nil {
		input.Cancel()
	}
}

func (w *Workspace) requestTerminalRefresh() {
	w.resetTopic(coreprotocol.TopicTerminalInventory)
}
