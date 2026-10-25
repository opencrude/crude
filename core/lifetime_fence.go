package core

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"crude/workspace"
)

// reserveIdle is a reversible transaction. The runtime admission reservation
// precedes actor/session/resource leases. Never acquire actor owners with r.mu
// held. Refusal releases in reverse order without closing or removing an actor.
func (r *Runtime) reserveIdle(ctx context.Context) (func(), error) {
	var releases []func()
	release := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	storage, ok := workspace.TryIdleStorageLease()
	if !ok {
		return nil, errors.New("saved workspace catalog or storage operation is still running")
	}
	releases = append(releases, storage)
	for _, a := range r.actors() {
		name := a.id
		var unlock func()
		var refusal error
		err := a.inspect(ctx, func() {
			name = a.ws.RootPath
			if a.idleFenced || a.retiring || a.controlWorkers.Load() != 0 || len(a.subscribers) != 0 || a.inFlight != 0 || a.lifecycle || len(a.settingsQueue) != 0 || len(a.controlQueue) != 0 || len(a.commands) != 0 || len(a.ingress) != 0 || len(a.jobs) != 0 || len(a.documentReleases) != 0 {
				refusal = errors.New("views, queued commands or workspace operations are still finishing")
				return
			}
			var local []func()
			rollback := func() {
				for i := len(local) - 1; i >= 0; i-- {
					local[i]()
				}
			}
			for _, lane := range []*resourceLane{a.resourceLane, a.terminalLane} {
				u, ok := lane.beginIdleLease()
				if !ok {
					rollback()
					refusal = errors.New("resource operation is still running")
					return
				}
				local = append(local, u)
			}
			u, err := a.ws.BeginIdleLease(ctx)
			if err != nil {
				rollback()
				refusal = err
				return
			}
			local = append(local, u)
			a.idleFenced = true
			unlock = func() {
				// Owners stay reserved until this exact actor acknowledges rollback.
				// A closed actor no longer admits anything and needs no map mutation.
				if err := a.inspect(context.Background(), func() { rollback(); a.idleFenced = false }); err != nil {
					rollback()
				}
			}
		})
		if unlock != nil {
			releases = append(releases, unlock)
		}
		if err == nil {
			err = refusal
		}
		if err != nil {
			release()
			return nil, fmt.Errorf("workspace %s: %w", name, err)
		}
	}
	var once sync.Once
	return func() { once.Do(release) }, nil
}
