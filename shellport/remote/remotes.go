package remote

import "crude/shellport"

func (w *Workspace) BackendStatus() shellport.RemoteStatus {
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
	if c.Err() != nil {
		state = "offline"
	}
	return shellport.RemoteStatus{State: state, Carrier: string(carrier), RTT: rtt}
}
