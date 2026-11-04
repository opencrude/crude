package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"time"
)

// ManualLSPRequestTimeout includes startup, document synchronization and the
// answer. A cold references search on a real large project measured about a
// minute; a ten-second inner timeout cancelled every attempt before it finished.
// Automatic completion/hover work retains its shorter, separate deadlines.
const ManualLSPRequestTimeout = 2 * time.Minute

// awaitSynchronized waits on the lifecycle owner's broadcast, not a polling
// clock. Waiting never changes the requested source or revives a stopped server.
// Missing tools, failed startup, revoked policy and replaced documents fail
// promptly instead of leaving an unbounded request waiting for no possible start.
func (m *LSPManager) awaitSynchronized(ctx context.Context, path string) error {
	path = filepath.Clean(path)
	m.mu.Lock()
	original, generation := m.documents[path], m.settingsGeneration
	var revision uint64
	if original != nil {
		revision = original.revision
	}
	m.mu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.mu.Lock()
		doc := m.documents[path]
		var err error
		switch {
		case m.closed:
			err = context.Canceled
		case m.paused:
			err = ErrLSPStopped
		case original == nil || doc != original || doc.revision != revision || m.settingsGeneration != generation:
			err = errors.New("the file or language server changed; request again")
		case !m.wantedLocked(doc):
			err = errors.New("language intelligence is off or blocked for this file")
		case len(doc.text) > m.settings.MaxDocumentBytes:
			err = errors.New("the file exceeds the language server document size limit")
		}
		if err != nil {
			m.mu.Unlock()
			return err
		}
		state := m.states[doc.key]
		if state == nil {
			if _, ok := m.resolveServerLocked(path); !ok {
				m.mu.Unlock()
				return errors.New("no enabled language server handles this file")
			}
		} else if !state.closing {
			switch {
			case state.stopped:
				err = ErrLSPStopped
			case state.status == LSPFailed || state.teardownErr != nil || state.waiting:
				err = errors.New(state.err)
			case state.status == LSPConnected && state.client != nil && doc.opened && !doc.syncPending &&
				doc.syncedRevision == revision && doc.syncedContext == m.contextGeneration:
				m.mu.Unlock()
				return nil
			}
		}
		if err != nil {
			m.mu.Unlock()
			return err
		}
		// Explicit requests flush an outstanding edit debounce. Only the existing
		// worker performs the send; a request never owns a second startup path.
		if timer := m.updateTimers[path]; timer != nil {
			timer.Stop()
			delete(m.updateTimers, path)
		}
		changed := m.stateChanged
		m.signalLocked()
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}
