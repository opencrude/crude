package remote

import (
	"context"

	"crude/coreprotocol"
	"crude/shellport"
)

var _ shellport.RemoteAccessAdmin = (*Workspace)(nil)

func (w *Workspace) LocalHost() bool {
	client, _ := w.conn()
	return client != nil && client.LocalHost()
}

func (w *Workspace) RemoteAccess(ctx context.Context) (coreprotocol.RemoteAccessView, error) {
	var out coreprotocol.RemoteAccessView
	client, _ := w.conn()
	if client == nil {
		return out, errDisconnected
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	stop := context.AfterFunc(w.ctx, cancel)
	defer stop()
	return client.RemoteAccess(ctx)
}

func (w *Workspace) CreateRemotePairing(ctx context.Context) (coreprotocol.RemotePairingCode, error) {
	var out coreprotocol.RemotePairingCode
	client, _ := w.conn()
	if client == nil {
		return out, errDisconnected
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	stop := context.AfterFunc(w.ctx, cancel)
	defer stop()
	return client.CreateRemotePairing(ctx)
}

func (w *Workspace) RevokeRemoteDevice(ctx context.Context, id string) error {
	client, _ := w.conn()
	if client == nil {
		return errDisconnected
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	stop := context.AfterFunc(w.ctx, cancel)
	defer stop()
	return client.RevokeRemoteDevice(ctx, id)
}
