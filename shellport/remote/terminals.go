package remote

import (
	"context"
	"slices"
	"sync"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/terminalvt"
	"crude/workspace"
)

// terminalReplica mirrors the core's PTY inventory for synchronous shell reads.
// The core owns every PTY and its one parser; this shell holds only a lease.
type terminalReplica struct {
	w       *Workspace
	infos   []workspace.TerminalInfo
	leaseMu sync.Mutex
	leases  map[string]workspace.TerminalLease
}

var (
	_ shellport.TerminalService = (*terminalReplica)(nil)
	_ shellport.TerminalManager = (*terminalReplica)(nil)
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
			follow = append(follow, workspace.TerminalExitedMsg{ID: previous.ID, ExitCode: previous.ExitCode, Err: previous.ExitErr})
		}
	}
	t.infos = slices.Clone(infos)
	return follow
}

func (t *terminalReplica) fetch(ctx context.Context) ([]workspace.TerminalInfo, error) {
	var infos []workspace.TerminalInfo
	err := t.w.call(ctx, coreprotocol.TerminalList, struct{}{}, &infos)
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
	t.leaseMu.Lock()
	leases := t.leases
	t.leases = nil
	t.leaseMu.Unlock()
	for _, lease := range leases {
		t.w.commands.push(func(ctx context.Context) {
			_ = t.w.call(ctx, coreprotocol.TerminalRelease, lease, &struct{}{})
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
	t.w.commands.push(func(ctx context.Context) {
		_ = t.w.call(ctx, coreprotocol.TerminalClose, coreprotocol.TerminalIDRequest{ID: id}, &struct{}{})
	})
}

func (t *terminalReplica) Rows(id string, start, count int) (workspace.TerminalProjection, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return t.w.rpc().TerminalRows(ctx, id, start, count)
}

// input runs on the shell's existing ordered terminal worker, so its return
// is the core acknowledgement rather than an optimistic second queue.
func (t *terminalReplica) input(request coreprotocol.TerminalInputRequest) error {
	ctx, cancel := context.WithTimeout(t.w.ctx, callTimeout)
	defer cancel()
	return t.w.call(ctx, coreprotocol.TerminalInput, request, &struct{}{})
}

func (t *terminalReplica) Focus(lease workspace.TerminalLease, focused bool) error {
	return t.input(coreprotocol.TerminalInputRequest{Lease: lease, Kind: "focus", Focused: focused})
}

func (t *terminalReplica) Key(lease workspace.TerminalLease, key terminalvt.Key) error {
	return t.input(coreprotocol.TerminalInputRequest{Lease: lease, Kind: "key", Key: key})
}

func (t *terminalReplica) Mouse(lease workspace.TerminalLease, mouse terminalvt.Mouse) error {
	return t.input(coreprotocol.TerminalInputRequest{Lease: lease, Kind: "mouse", Mouse: mouse})
}

func (t *terminalReplica) Paste(lease workspace.TerminalLease, data []byte) error {
	return t.input(coreprotocol.TerminalInputRequest{Lease: lease, Kind: "paste", Data: append([]byte(nil), data...)})
}

func (t *terminalReplica) Resize(lease workspace.TerminalLease, cols, rows int) error {
	ctx, cancel := context.WithTimeout(t.w.ctx, callTimeout)
	defer cancel()
	return t.w.call(ctx, coreprotocol.TerminalResize, coreprotocol.TerminalResizeRequest{Lease: lease, Cols: cols, Rows: rows}, &struct{}{})
}

func (w *Workspace) requestTerminalRefresh() {
	w.pending.add(coreprotocol.Change{Terminals: []string{""}})
}

// terminalMessages converts one core terminal notification into the shell's
// existing invalidation and effects messages.
func terminalMessages(change coreprotocol.Change) []any {
	var out []any
	for _, id := range change.Terminals {
		if id != "" {
			out = append(out, workspace.TerminalChangedMsg{ID: id})
		}
	}
	if change.TerminalEffects != nil {
		out = append(out, *change.TerminalEffects)
	}
	return out
}
