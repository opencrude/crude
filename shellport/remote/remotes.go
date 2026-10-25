package remote

import (
	"errors"

	"crude/coreclient"
	"crude/corewire"
	"crude/shellport"
)

func (w *Workspace) BackendStatus() shellport.RemoteStatus {
	select {
	case <-w.stop:
		return shellport.RemoteStatus{State: "offline", Notice: "This shell's connection is closed."}
	default:
	}
	w.connMu.Lock()
	c := w.client
	w.connMu.Unlock()
	if c == nil {
		return shellport.RemoteStatus{State: "reconnecting"}
	}
	carrier, rtt := c.Carrier()
	state := "connected"
	if carrier == "" {
		state = "reconnecting"
	}
	err := c.Err()
	if err != nil {
		// The Workspace collector continues reconnecting even after the SDK's
		// retained-session grace expires. Do not report a terminal offline state
		// while that owner is still retrying a fresh attachment.
		state = "reconnecting"
	}
	status := shellport.RemoteStatus{State: state, Carrier: string(carrier), RTT: rtt}
	// Report known causes without exposing raw dial errors, credentials or
	// remote command payloads in a passive client-visible surface.
	switch {
	case errors.Is(err, corewire.ErrTimeout):
		status.Notice = "The core stopped answering transport heartbeats."
	case errors.Is(err, coreclient.ErrSessionLost):
		status.Notice = "The previous core session could not be resumed. Crude is preparing a fresh attachment."
	case errors.Is(err, corewire.ErrProtocol):
		status.Notice = "The core connection ended after a transport protocol error."
	case state == "reconnecting":
		status.Notice = "Restoring this shell's connection to the core."
	}
	return status
}
