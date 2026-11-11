package workspace

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"crude/types"
)

// BeginIdleLease runs on the workspace actor, which fences new sessions and
// resource commands separately. All partial reservations roll back on refusal.
// IdlePurpose says why the core reserves an idle workspace. An automatic
// idle exit keeps the core alive for any live terminal, so a shell survives
// closing Crude. An explicit stop closes idle shells and refuses only for
// terminals that run a program.
type IdlePurpose uint8

const (
	IdleAutomatic IdlePurpose = iota
	IdleExplicitStop
)

func (w *Workspace) BeginIdleLease(ctx context.Context, purpose IdlePurpose) (func(), error) {
	var releases []func()
	release := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	fail := func(err error) (func(), error) { release(); return nil, err }
	for _, frontend := range w.Sessions {
		if frontend == nil || frontend.backend == nil {
			continue
		}
		unlock, err := frontend.backend.BeginIdleLease(ctx)
		if err != nil {
			return fail(fmt.Errorf("session %s: %w", frontend.SessionID, err))
		}
		releases = append(releases, unlock)
	}
	if w.LiveDocuments != nil {
		unlock, err := w.LiveDocuments.beginIdleLease()
		if err != nil {
			return fail(err)
		}
		releases = append(releases, unlock)
	}
	if w.ProjectContext != nil {
		unlock, err := w.ProjectContext.beginIdleLease()
		if err != nil {
			return fail(err)
		}
		releases = append(releases, unlock)
	}
	if w.Terminals != nil {
		unlock, err := w.Terminals.beginIdleLease(purpose)
		if err != nil {
			return fail(err)
		}
		releases = append(releases, unlock)
	}
	if w.ActiveWork().Any() {
		return fail(errors.New("agent work is still active"))
	}
	var once sync.Once
	return func() { once.Do(release) }, nil
}

func (s *DocumentService) beginIdleLease() (func(), error) {
	if !s.mu.TryLock() {
		return nil, types.IdleSettling("document operation is still running")
	}
	defer s.mu.Unlock()
	if s.closed || s.idleFenced {
		return nil, errors.New("documents already reserved or closing")
	}
	for _, d := range s.docs {
		if len(d.owners) != 0 {
			return nil, fmt.Errorf("document %s has open view leases; close its editors", d.snapshot.Path)
		}
	}
	s.idleFenced = true
	s.pruneCodeActionSetsLocked()
	return func() { s.mu.Lock(); s.idleFenced = false; s.mu.Unlock() }, nil
}

func (s *TerminalService) beginIdleLease(purpose IdlePurpose) (func(), error) {
	if !s.mu.TryLock() {
		return nil, types.IdleSettling("terminal operation is still running")
	}
	defer s.mu.Unlock()
	if s.closed || s.idleFenced || s.creating != 0 {
		return nil, types.IdleSettling("terminal creation or shutdown is in progress")
	}
	for id, r := range s.items {
		if r.lease.Owner.ClientID != "" {
			return nil, fmt.Errorf("terminal %s has a controller lease; release it", id)
		}
	}
	if purpose == IdleExplicitStop {
		if running := s.manager.RunningPrograms(); len(running) != 0 {
			return nil, running[0]
		}
	} else {
		for _, info := range s.manager.List() {
			if info.Alive {
				return nil, fmt.Errorf("%s is open", info.DisplayName())
			}
		}
	}
	s.idleFenced = true
	return func() { s.mu.Lock(); s.idleFenced = false; s.mu.Unlock() }, nil
}
