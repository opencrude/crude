package workspace

import "sync"

type registryCostWriter struct {
	db     *WorkspaceDB
	logger Logger
	mu     sync.Mutex
	latest map[string]float64
	wake   chan struct{}
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
}

func newRegistryCostWriter(db *WorkspaceDB, logger Logger) *registryCostWriter {
	if db == nil {
		return nil
	}
	w := &registryCostWriter{db: db, logger: logger, latest: make(map[string]float64), wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	go w.loop()
	return w
}

func (w *registryCostWriter) Enqueue(sessionID string, cost float64) {
	if w == nil || sessionID == "" {
		return
	}
	w.mu.Lock()
	w.latest[sessionID] = cost
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *registryCostWriter) Close() {
	if w == nil {
		return
	}
	w.once.Do(func() { close(w.stop); <-w.done })
}

func (w *registryCostWriter) loop() {
	defer close(w.done)
	for {
		select {
		case <-w.wake:
			w.drain()
		case <-w.stop:
			w.drain()
			return
		}
	}
}

func (w *registryCostWriter) drain() {
	for {
		w.mu.Lock()
		if len(w.latest) == 0 {
			w.mu.Unlock()
			return
		}
		pending := w.latest
		w.latest = make(map[string]float64)
		w.mu.Unlock()
		for sessionID, cost := range pending {
			if err := w.db.UpdateSessionCost(sessionID, cost); err != nil && w.logger != nil {
				w.logger.Warn("WORKSPACE", "session_cost_sync_failed", "id", sessionID, "error", err.Error())
			}
		}
	}
}
