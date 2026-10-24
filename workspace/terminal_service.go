package workspace

import (
	"errors"
	"fmt"
	"sync"

	"crude/terminalvt"
)

var ErrTerminalControl = errors.New("terminal control lease is missing or stale")
var ErrTerminalInputSequence = errors.New("terminal input sequence is invalid")

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
	engine        *terminalvt.Engine
	lease         TerminalLease
	generation    uint64
	err           string
	watchers      map[chan struct{}]struct{}
	inputStream   string
	inputSequence uint64
	inputErrors   map[uint64]error
}

// TerminalInput is one ordered intent, encoded only by the authoritative engine.
type TerminalInput struct {
	Lease      TerminalLease
	Kind       string
	Key        terminalvt.Key
	Mouse      terminalvt.Mouse
	Data       []byte
	Focused    bool
	Cols, Rows int
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
	scrollback := s.manager.Settings().ScrollbackLines
	return s.manager.CreatePrepared(opts, func(info TerminalInfo) error {
		e, err := terminalvt.New(terminalvt.Options{Cols: info.Cols, Rows: info.Rows, ScrollbackLines: scrollback})
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
	s.notify(r)
	s.mu.Unlock()
	effects.Replies = nil // replies have one owner; clients cannot replay them
	if effects.TitleChanged || effects.CWDChanged || effects.Bell || len(effects.Clipboard) > 0 {
		s.manager.emit(TerminalEffectsMsg{ID: out.ID, Effects: effects})
	}
}

// Subscribe observes latest-state invalidations directly, without involving the
// workspace actor. A slow viewer occupies one notification slot, never the PTY.
func (s *TerminalService) Subscribe(id string) (<-chan struct{}, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.items[id]
	if r == nil {
		return nil, nil, fmt.Errorf("terminal %s not found", id)
	}
	ch := make(chan struct{}, 1)
	if r.watchers == nil {
		r.watchers = make(map[chan struct{}]struct{})
	}
	r.watchers[ch] = struct{}{}
	ch <- struct{}{}
	var once sync.Once
	return ch, func() { once.Do(func() { s.mu.Lock(); delete(r.watchers, ch); s.mu.Unlock() }) }, nil
}

func (s *TerminalService) notify(r *terminalResource) {
	for ch := range r.watchers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// ApplyInput acknowledges every processed sequence, including rejected intents.
// Retransmission after a carrier failure never writes an accepted intent twice.
// The exact lease fences a replacement controller and bounds retained log state.
func (s *TerminalService) ApplyInput(stream string, sequence, acknowledged uint64, input TerminalInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.controlled(input.Lease)
	if err != nil {
		return err
	}
	if (r.inputStream != "" && r.inputStream != stream) || acknowledged > r.inputSequence ||
		sequence == 0 || sequence > r.inputSequence+1 {
		return ErrTerminalInputSequence
	}
	if sequence > r.inputSequence && sequence-acknowledged > 256 {
		return ErrTerminalBackpressure
	}
	// Rejected ordering is not admission: it cannot claim a fresh stream or
	// discard retained failure receipts using an otherwise plausible ACK.
	if r.inputStream == "" {
		r.inputStream = stream
	}
	for seq := range r.inputErrors {
		if seq <= acknowledged {
			delete(r.inputErrors, seq)
		}
	}
	if sequence <= r.inputSequence && sequence != 0 {
		err := r.inputErrors[sequence]
		return err
	}
	// Admission, mode-aware encoding, nonblocking writer enqueue and receipt
	// commit share one engine/control boundary. Releasing/reacquiring the same
	// lock between these stages let a fullscreen export overtake one key three
	// times and made lease replacement race the receipt commit.
	err = s.inputLocked(r, input)
	r.inputSequence = sequence
	if err != nil {
		if r.inputErrors == nil {
			r.inputErrors = make(map[uint64]error)
		}
		r.inputErrors[sequence] = err
	}
	return err
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
	r.inputStream, r.inputSequence, r.inputErrors = "", 0, nil
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
	_, ok := s.ControllerLease(id, owner)
	return ok
}

// ControllerLease observes the exact current controller generation without
// acquiring, renewing or otherwise changing terminal authority.
func (s *TerminalService) ControllerLease(id string, owner TerminalController) (TerminalLease, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.items[id]
	if r == nil || r.lease.Generation == 0 || r.lease.Owner != owner || s.detached[owner] {
		return TerminalLease{}, false
	}
	return r.lease, true
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
	return s.input(TerminalInput{Lease: lease, Kind: "key", Key: key})
}
func (s *TerminalService) Mouse(lease TerminalLease, mouse terminalvt.Mouse) error {
	return s.input(TerminalInput{Lease: lease, Kind: "mouse", Mouse: mouse})
}
func (s *TerminalService) Paste(lease TerminalLease, data []byte) error {
	return s.input(TerminalInput{Lease: lease, Kind: "paste", Data: data})
}
func (s *TerminalService) Focus(lease TerminalLease, focused bool) error {
	return s.input(TerminalInput{Lease: lease, Kind: "focus", Focused: focused})
}
func (s *TerminalService) Resize(lease TerminalLease, cols, rows int) error {
	return s.input(TerminalInput{Lease: lease, Kind: "resize", Cols: cols, Rows: rows})
}

func (s *TerminalService) input(input TerminalInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.controlled(input.Lease)
	if err != nil {
		return err
	}
	return s.inputLocked(r, input)
}

func (s *TerminalService) inputLocked(r *terminalResource, input TerminalInput) error {
	id := input.Lease.TerminalID
	switch input.Kind {
	case "key":
		return s.manager.Write(id, r.engine.EncodeKey(input.Key))
	case "mouse":
		return s.manager.Write(id, r.engine.EncodeMouse(input.Mouse))
	case "paste":
		return s.manager.Write(id, r.engine.EncodePaste(input.Data))
	case "focus":
		return s.manager.Write(id, r.engine.EncodeFocus(input.Focused))
	case "resize":
		return s.resizeLocked(r, id, input.Cols, input.Rows)
	default:
		return errors.New("invalid terminal input kind")
	}
}

func (s *TerminalService) resizeLocked(r *terminalResource, id string, cols, rows int) error {
	cols, rows = NormalizeTerminalSize(cols, rows)
	if cols > terminalvt.MaxCols || rows > terminalvt.MaxRows {
		return terminalvt.ErrBounds
	}
	if err := s.manager.Resize(id, cols, rows); err != nil {
		return err
	}
	if err := r.engine.Resize(cols, rows); err != nil {
		r.err = err.Error()
		return err
	}
	r.generation++
	s.notify(r)
	return nil
}

// Rows returns a detached bounded page; start=-1 requests the active grid.
func (s *TerminalService) Rows(id string, request terminalvt.RowRequest) (TerminalProjection, error) {
	return s.rows(id, request, false)
}

// StreamRows exports only render-dirty active rows. Historical pages remain
// explicit bounded reads and never consume a viewer's acknowledgement state.
func (s *TerminalService) StreamRows(id string, request terminalvt.RowRequest) (TerminalProjection, error) {
	return s.rows(id, request, true)
}

func (s *TerminalService) rows(id string, request terminalvt.RowRequest, stream bool) (TerminalProjection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.items[id]
	if r == nil {
		return TerminalProjection{}, fmt.Errorf("terminal %s not found", id)
	}
	var grid terminalvt.Projection
	var err error
	if stream {
		grid, err = r.engine.StreamRows(request)
	} else {
		grid, err = r.engine.Rows(request)
	}
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
		s.notify(r)
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
