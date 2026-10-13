package remote

import (
	"context"
	"sync"

	"crude/coreclient"
	"crude/shellport"
	"crude/types/control"
)

var _ shellport.ShellControl = (*Workspace)(nil)

type controlReader struct{ cancel context.CancelFunc }

var controlReaders sync.Map // *Workspace -> *controlReader

func (w *Workspace) StopShellControl() {
	w.connMu.Lock()
	defer w.connMu.Unlock()
	w.controlEnabled = false
	w.controlClient = nil
	if cancel, ok := controlReaders.LoadAndDelete(w); ok {
		cancel.(*controlReader).cancel()
	}
}

func (w *Workspace) ControlIdentity() control.Target {
	client, id := w.conn()
	if client == nil || w.ctx.Err() != nil {
		return control.Target{}
	}
	owner := client.Owner()
	return control.Target{WorkspaceID: id, Shell: &control.ShellOwner{ClientID: owner.ClientID, Epoch: owner.Epoch}}
}

func (w *Workspace) StartShellControl() {
	w.connMu.Lock()
	defer w.connMu.Unlock()
	w.controlEnabled = true
	w.startControlLocked(w.client, w.id)
}

// connMu serializes worker admission against teardown and reconnect. A new
// connection gets its own registration; a settled connection is never replayed.
func (w *Workspace) startControlLocked(client *coreclient.Client, id string) {
	if !w.controlEnabled || client == nil || w.controlClient == client || w.ctx.Err() != nil {
		return
	}
	w.controlClient = client
	if previous, ok := controlReaders.LoadAndDelete(w); ok {
		previous.(*controlReader).cancel()
	}
	readerCtx, stop := context.WithCancel(w.ctx)
	reader := &controlReader{cancel: stop}
	controlReaders.Store(w, reader)
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer stop()
		defer controlReaders.CompareAndDelete(w, reader)
		select {
		case <-w.busSet:
		case <-readerCtx.Done():
			return
		}
		ctx, cancel := context.WithTimeout(readerCtx, callTimeout)
		err := client.RegisterControlShell(ctx, id)
		cancel()
		if err != nil {
			w.logger.Warn("REMOTE", "control_register_failed", "error", err.Error())
			return
		}
		for {
			select {
			case <-readerCtx.Done():
				return
			case envelope, ok := <-client.ControlRequests():
				if !ok {
					return
				}
				w.send(shellport.ControlRequestMsg{Owner: w, Envelope: envelope, Reply: func(ctx context.Context, result control.Result) error {
					ctx, cancel := context.WithCancel(ctx)
					defer cancel()
					stop := context.AfterFunc(readerCtx, cancel)
					defer stop()
					return client.ReplyControl(ctx, id, result)
				}})
			}
		}
	}()
}

func (w *Workspace) ReplyShellControl(ctx context.Context, result control.Result) error {
	client, id := w.conn()
	if client == nil || w.ctx.Err() != nil {
		return errDisconnected
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(w.ctx, cancel)
	defer stop()
	return client.ReplyControl(ctx, id, result)
}
