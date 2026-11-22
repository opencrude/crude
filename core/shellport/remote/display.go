package remote

import "time"

func (w *Workspace) SetDisplayInterval(interval time.Duration) {
	w.connMu.Lock()
	client := w.client
	w.connMu.Unlock()
	if client != nil {
		client.SetDisplayInterval(interval)
	}
	w.terminals.streamMu.Lock()
	defer w.terminals.streamMu.Unlock()
	w.terminals.display = interval
	for _, grid := range w.terminals.grids {
		grid.SetDisplayInterval(interval)
	}
	for _, input := range w.terminals.inputs {
		input.SetDisplayInterval(interval)
	}
}
