package remote

import (
	"context"
	"crude/capability"
	"crude/coreclient"
	"crude/types/control"
	"sync"
)

type capabilityHostReader struct {
	client *coreclient.Client
	cancel context.CancelFunc
}

var capabilityHostEnabled sync.Map
var capabilityHostReaders sync.Map

func (w *Workspace) StopCapabilityHost() {
	w.connMu.Lock()
	defer w.connMu.Unlock()
	capabilityHostEnabled.Delete(w)
	if v, ok := capabilityHostReaders.LoadAndDelete(w); ok {
		v.(*capabilityHostReader).cancel()
	}
}
func (w *Workspace) StartCapabilityHost() {
	w.connMu.Lock()
	defer w.connMu.Unlock()
	capabilityHostEnabled.Store(w, true)
	w.startCapabilityHostLocked(w.client, w.id)
}

// Parent reconnect plumbing may call this after changing client/id. Registration
// is passive and never inherits the prior owner's lease, requests or transports.
func (w *Workspace) startCapabilityHostLocked(client *coreclient.Client, id string) {
	if _, enabled := capabilityHostEnabled.Load(w); !enabled {
		return
	}
	if client == nil || w.ctx.Err() != nil {
		return
	}
	if v, ok := capabilityHostReaders.Load(w); ok {
		old := v.(*capabilityHostReader)
		if old.client == client {
			return
		}
		old.cancel()
	}
	ctx, cancel := context.WithCancel(w.ctx)
	reader := &capabilityHostReader{client: client, cancel: cancel}
	capabilityHostReaders.Store(w, reader)
	owner := client.Owner()
	// Shell paths are shell-owned; do not treat remote workspace paths as local.
	worker := capability.NewHostWorker(ctx, control.ShellOwner{ClientID: owner.ClientID, Epoch: owner.Epoch}, id, "")
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer cancel()
		defer worker.Close()
		defer capabilityHostReaders.CompareAndDelete(w, reader)
		registerCtx, stop := context.WithTimeout(ctx, callTimeout)
		err := client.RegisterCapabilityHost(registerCtx, id)
		stop()
		if err != nil {
			return
		}
		var calls sync.WaitGroup
		defer func() { cancel(); worker.Close(); calls.Wait() }()
		for {
			select {
			case <-ctx.Done():
				return
			case <-client.Done():
				cancel()
				return
			case request := <-client.CapabilityHostRequests():
				calls.Add(1)
				go func() {
					defer calls.Done()
					reply := worker.Handle(ctx, request)
					replyCtx, stop := context.WithTimeout(ctx, callTimeout)
					defer stop()
					_ = client.ReplyCapabilityHost(replyCtx, id, reply)
				}()
			}
		}
	}()
}
