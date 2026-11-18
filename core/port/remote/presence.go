package remote

import (
	"context"
	"sync"

	"crude/core/port"
	"crude/core/protocol"
)

var _ shellport.Presence = (*Workspace)(nil)

// presenceReport holds the shell's latest presence. The core keeps it per
// connection, so a reconnect sends it again.
type presenceReport struct {
	mu     sync.Mutex
	value  coreprotocol.ShellPresenceReport
	known  bool
	queued bool
}

func (w *Workspace) ReportPresence(present, notifies bool) {
	report := coreprotocol.ShellPresenceReport{Present: present, Notifies: notifies}
	w.presence.mu.Lock()
	if w.presence.known && w.presence.value == report {
		w.presence.mu.Unlock()
		return
	}
	w.presence.value, w.presence.known = report, true
	w.presence.mu.Unlock()
	w.sendPresence()
}

// sendPresence queues one delivery of the latest report on the ordered
// service lane. Reports made while it waits merge into it.
func (w *Workspace) sendPresence() {
	w.presence.mu.Lock()
	if !w.presence.known || w.presence.queued {
		w.presence.mu.Unlock()
		return
	}
	w.presence.queued = true
	w.presence.mu.Unlock()
	w.services.push(func(ctx context.Context) {
		w.presence.mu.Lock()
		report := w.presence.value
		w.presence.queued = false
		w.presence.mu.Unlock()
		if err := w.call(ctx, coreprotocol.ShellPresenceMethod, &report, &coreprotocol.Empty{}); err != nil {
			w.logger.Warn("REMOTE", "presence_report_failed", "error", err.Error())
		}
	})
}
