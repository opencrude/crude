package workspace

import (
	"errors"
	"fmt"
	"sync"

	"crude/terminalvt"
)

var ErrTerminalControl = errors.New("terminal control lease is missing or stale")

// TerminalController identifies one exact client attachment, not a session or
// selected tab. Reattachment must use a new Epoch.
type TerminalController struct {
	ClientID string
	Epoch    uint64
}
type TerminalLease struct {
	TerminalID string
	Owner      TerminalController
	Generation uint64
}
type TerminalProjection struct {
	Info       TerminalInfo
	Grid       terminalvt.Projection
	Generation uint64
	Error      string
}

// TerminalEffectsMsg offers nonvisual child effects. Clipboard bytes are never
// applied by the service; downstream policy decides whether to offer them.
type TerminalEffectsMsg struct {
	ID      string
	Effects terminalvt.Effects
}

// TerminalChangedMsg invalidates a retained reader without exposing raw PTY
// bytes or granting it query-reply authority.
type TerminalChangedMsg struct {
	ID         string
	Generation uint64
}
type terminalResource struct {
	engine     *terminalvt.Engine
	lease      TerminalLease
	generation uint64
	err        string
}

// TerminalService owns exactly one parser and reply source per managed PTY,
// including when no clients are attached. It must be constructed on an empty
// manager before creation; callers must create terminals through this service.
// Selection, scroll offsets, theme and placement belong to clients, not here.
type TerminalService struct {
	mu            sync.Mutex
	manager       *TerminalManager
	items         map[string]*terminalResource
	leaseSequence uint64
	detached      map[TerminalController]bool
	closed        bool
}

func NewTerminalService(manager *TerminalManager) (*TerminalService, error) {
	if manager == nil {
		return nil, errors.New("terminal manager is nil")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.items) != 0 || manager.outputSink != nil {
		return nil, errors.New("terminal service requires an unclaimed empty manager")
	}
	s := &TerminalService{manager: manager, items: make(map[string]*terminalResource), detached: make(map[TerminalController]bool)}
	manager.outputSink = s
	return s, nil
}
func (s *TerminalService) Create(opts TerminalCreateOpts) (TerminalInfo, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return TerminalInfo{}, errors.New("terminal service is closed")
	}
	return s.manager.CreatePrepared(opts, func(info TerminalInfo) error {
		e, err := terminalvt.New(terminalvt.Options{Cols: info.Cols, Rows: info.Rows})
		if err != nil {
			return err
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			e.Close()
			return errors.New("terminal service is closed")
		}
		s.items[info.ID] = &terminalResource{engine: e}
		s.mu.Unlock()
		return nil
	})
}

// Send is the sole synchronous PTY byte ingress. Protocol replies are enqueued
// before downstream effects, and no PTY bytes are broadcast to clients.
func (s *TerminalService) Send(msg any) {
	out, ok := msg.(TerminalOutputMsg)
	if !ok {
		return
	}
	s.mu.Lock()
	r := s.items[out.ID]
	if r == nil {
		s.mu.Unlock()
		return
	}
	effects := r.engine.Feed(out.Data)
	r.generation++
	generation := r.generation
	for _, reply := range effects.Replies {
		if err := s.manager.Write(out.ID, reply); err != nil {
			r.err = err.Error()
		}
	}
	// Metadata updates must not synchronously enter the UI while holding the
	// engine lock. Title publication is part of downstream effect delivery.
	s.manager.mu.Lock()
	if item := s.manager.items[out.ID]; item != nil {
		if effects.TitleChanged {
			if item.title == effects.Title {
				effects.TitleChanged = false
			} else {
				item.title = effects.Title
			}
		}
		if effects.CWDChanged {
			if item.cwd == effects.CWD {
				effects.CWDChanged = false
			} else {
				item.cwd = effects.CWD
			}
		}
	}
	s.manager.mu.Unlock()
	s.mu.Unlock()
	effects.Replies = nil // replies have one owner; clients cannot replay them
	if effects.TitleChanged || effects.CWDChanged || effects.Bell || len(effects.Clipboard) > 0 {
		s.manager.emit(TerminalEffectsMsg{ID: out.ID, Effects: effects})
	}
	s.manager.emit(TerminalChangedMsg{ID: out.ID, Generation: generation})
}
func (s *TerminalService) Acquire(id string, owner TerminalController) (TerminalLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.items[id]
	if r == nil {
		return TerminalLease{}, fmt.Errorf("terminal %s not found", id)
	}
	if info, ok := s.manager.Get(id); !ok || !info.Alive {
		return TerminalLease{}, ErrTerminalControl
	}
	if owner.ClientID == "" || owner.Epoch == 0 || s.detached[owner] {
		return TerminalLease{}, ErrTerminalControl
	}
	if r.lease.Owner == owner {
		return r.lease, nil
	}
	if r.lease.Owner.ClientID != "" {
		return TerminalLease{}, ErrTerminalControl
	}
	s.leaseSequence++
	r.lease = TerminalLease{id, owner, s.leaseSequence}
	return r.lease, nil
}
func (s *TerminalService) Detach(owner TerminalController) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.detached[owner] = true
	for _, r := range s.items {
		if r.lease.Owner == owner {
			r.lease = TerminalLease{}
		}
	}
}

// Release relinquishes exactly one current controller lease. A delayed release
// cannot revoke a replacement controller, even from the same client profile.
func (s *TerminalService) Release(lease TerminalLease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.controlled(lease)
	if err != nil {
		return err
	}
	r.lease = TerminalLease{}
	return nil
}

func (s *TerminalService) ControlledBy(id string, owner TerminalController) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.items[id]
	return r != nil && r.lease.Generation != 0 && r.lease.Owner == owner && !s.detached[owner]
}
func (s *TerminalService) controlled(lease TerminalLease) (*terminalResource, error) {
	r := s.items[lease.TerminalID]
	if r == nil || lease.Generation == 0 || r.lease != lease {
		return nil, ErrTerminalControl
	}
	if info, ok := s.manager.Get(lease.TerminalID); !ok || !info.Alive {
		return nil, ErrTerminalControl
	}
	return r, nil
}
func (s *TerminalService) Input(lease TerminalLease, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.controlled(lease); err != nil {
		return err
	}
	return s.manager.Write(lease.TerminalID, data)
}
func (s *TerminalService) Key(lease TerminalLease, key terminalvt.Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.controlled(lease)
	if err != nil {
		return err
	}
	return s.manager.Write(lease.TerminalID, r.engine.EncodeKey(key))
}
func (s *TerminalService) Mouse(lease TerminalLease, mouse terminalvt.Mouse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.controlled(lease)
	if err != nil {
		return err
	}
	return s.manager.Write(lease.TerminalID, r.engine.EncodeMouse(mouse))
}
func (s *TerminalService) Paste(lease TerminalLease, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.controlled(lease)
	if err != nil {
		return err
	}
	return s.manager.Write(lease.TerminalID, r.engine.EncodePaste(data))
}
func (s *TerminalService) Focus(lease TerminalLease, focused bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.controlled(lease)
	if err != nil {
		return err
	}
	return s.manager.Write(lease.TerminalID, r.engine.EncodeFocus(focused))
}
func (s *TerminalService) Resize(lease TerminalLease, cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.controlled(lease)
	if err != nil {
		return err
	}
	cols, rows = NormalizeTerminalSize(cols, rows)
	if cols > terminalvt.MaxCols || rows > terminalvt.MaxRows {
		return terminalvt.ErrBounds
	}
	if err = s.manager.Resize(lease.TerminalID, cols, rows); err != nil {
		return err
	}
	if err = r.engine.Resize(cols, rows); err != nil {
		r.err = err.Error()
		return err
	}
	r.generation++
	return nil
}

// Rows returns a detached bounded page; start=-1 requests the active grid.
func (s *TerminalService) Rows(id string, start, count int) (TerminalProjection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.items[id]
	if r == nil {
		return TerminalProjection{}, fmt.Errorf("terminal %s not found", id)
	}
	grid, err := r.engine.Rows(start, count)
	if err != nil {
		return TerminalProjection{}, err
	}
	info, ok := s.manager.Get(id)
	if !ok {
		return TerminalProjection{}, fmt.Errorf("terminal %s not found", id)
	}
	return TerminalProjection{Info: info, Grid: grid, Generation: r.generation, Error: r.err}, nil
}
func (s *TerminalService) Close(id string) {
	s.mu.Lock()
	r := s.items[id]
	delete(s.items, id)
	if r != nil {
		r.engine.Close()
	}
	s.mu.Unlock()
	// Never join a reader waiting to enter Send while holding the engine lock.
	s.manager.Close(id)
}
func (s *TerminalService) CloseAll() {
	s.mu.Lock()
	s.closed = true
	ids := make([]string, 0, len(s.items))
	for id := range s.items {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.Close(id)
	}
}
