package core

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
	"crude/types"
	"crude/workspace"
)

// Only the carrier is fake: ownership, replacement, grace timers and command
// dispatch/cache all run through their production owners. No sockets or wall
// clock sleeps are needed for each generated sequence.
type ownershipFuzzCarrier struct {
	done chan struct{}
	once sync.Once
}

func (c *ownershipFuzzCarrier) OpenStream(context.Context, corewire.Class) (corewire.Stream, error) {
	return nil, corewire.ErrClosed
}
func (c *ownershipFuzzCarrier) AcceptStream(context.Context) (corewire.Stream, error) {
	return nil, corewire.ErrClosed
}
func (c *ownershipFuzzCarrier) Close() error              { c.once.Do(func() { close(c.done) }); return nil }
func (c *ownershipFuzzCarrier) Done() <-chan struct{}     { return c.done }
func (c *ownershipFuzzCarrier) Err() error                { return corewire.ErrClosed }
func (c *ownershipFuzzCarrier) RTT() time.Duration        { return 0 }
func (c *ownershipFuzzCarrier) Carrier() corewire.Carrier { return corewire.CarrierLocal }

// The application-owned effect counter is independent of the command cache.
// An accidentally repeated Dispatch therefore fails even if responses match.
type ownershipFuzzStartup struct {
	effects atomic.Int64
	release <-chan struct{}
}

func (*ownershipFuzzStartup) StartupSnapshot(context.Context) (coreprotocol.StartupSnapshot, error) {
	return coreprotocol.StartupSnapshot{}, nil
}
func (s *ownershipFuzzStartup) SaveStartup(context.Context, []types.SettingEdit) (types.OnboardingSettings, error) {
	<-s.release
	s.effects.Add(1)
	return types.OnboardingSettings{}, nil
}
func (*ownershipFuzzStartup) ReadStartupSettings(context.Context) (types.AppSettings, error) {
	return types.AppSettings{}, nil
}

func FuzzCoreSessionOwnership(f *testing.F) {
	for _, seed := range [][]byte{
		{}, {18, 2, 8, 10, 12, 18, 20},
		{18, 18, 10, 2, 18, 20, 14, 6, 0, 6},
		{10, 16, 2, 0, 6, 38, 4},
		{8, 8, 10, 12, 2, 18, 18, 20, 22},
		{18, 0, 6, 12, 18, 10, 16, 6, 0, 6},
		{1, 19, 9, 11, 17, 3, 5, 7, 15, 3},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, actions []byte) {
		if len(actions) > 32 {
			actions = actions[:32]
		}
		r, err := New(func(string) (*workspace.Workspace, error) {
			t.Error("ownership fuzz must not construct a workspace")
			return nil, errors.New("unexpected workspace")
		}, filepath.Join(t.TempDir(), "receipts.db"))
		if err != nil {
			t.Fatal(err)
		}
		owner := &ownershipFuzzStartup{}
		if err := r.SetStartupOwner(owner); err != nil {
			t.Fatal(err)
		}
		r.SetSessionGrace(time.Millisecond)
		synctest.Test(t, func(t *testing.T) {
			// Dispatch associates the runtime's worker WaitGroup with this
			// bubble; join it here as well, before synctest destroys the clock.
			defer func() {
				if err := r.Close(); err != nil {
					t.Error(err)
				}
			}()
			type record struct {
				conn     *Connection
				owner    coreprotocol.Owner
				alive    bool
				carrier  *ownershipFuzzCarrier
				carriers []*ownershipFuzzCarrier
				commands map[string]*command
			}
			var current [2]*record
			var epochs [2]uint64
			var history [2][]*record
			var effects int64
			ids := [2]string{"fuzz-local-a", "fuzz-local-b"}
			bind := func(m *record) {
				old := m.carrier
				next := &ownershipFuzzCarrier{done: make(chan struct{})}
				m.conn.bind(next, nil)
				if old != nil {
					select {
					case <-old.done:
					default:
						t.Fatal("replacement left old carrier live")
					}
				}
				m.carrier = next
				m.carriers = append(m.carriers, next)
			}
			fresh := func(i int) {
				c, resumed, err := r.openSession(coreprotocol.Hello{ClientID: ids[i]})
				if err != nil || resumed {
					t.Fatalf("fresh: %v %v", resumed, err)
				}
				if current[i] != nil {
					current[i].alive = false
					current[i].carrier = nil
				}
				epochs[i]++
				m := &record{conn: c, owner: coreprotocol.Owner{ClientID: ids[i], Epoch: epochs[i]}, alive: true, commands: make(map[string]*command)}
				current[i] = m
				history[i] = append(history[i], m)
				bind(m)
			}
			check := func() {
				r.mu.Lock()
				defer r.mu.Unlock()
				count := 0
				for i, records := range history {
					for _, m := range records {
						if m.conn.Owner != m.owner {
							t.Fatal("owner identity changed")
						}
						select {
						case <-m.conn.Done():
							if m.alive {
								t.Fatal("live owner retired")
							}
						default:
							if !m.alive {
								t.Fatal("retired owner still live")
							}
						}
					}
					m := current[i]
					if m != nil && m.alive {
						count++
						m.conn.carrierMu.Lock()
						carrierMatches := m.conn.carrier == nil
						if m.carrier != nil {
							carrierMatches = m.conn.carrier == m.carrier
						}
						m.conn.carrierMu.Unlock()
						if r.clients[ids[i]] != m.conn || r.sessions[m.conn.id] != m.conn || !carrierMatches {
							t.Fatal("current owner/carrier index diverged")
						}
					} else if r.clients[ids[i]] != nil {
						t.Fatal("retired owner remained indexed")
					}
					if r.epochs[ids[i]] != epochs[i] {
						t.Fatal("attachment epoch regressed or advanced on resume")
					}
				}
				if len(r.clients) != count || len(r.sessions) != count {
					t.Fatal("more than one retained owner per client")
				}
				if owner.effects.Load() != effects {
					t.Fatalf("effects executed %d, want %d", owner.effects.Load(), effects)
				}
			}
			fresh(0)
			check()
			defer func() {
				for _, records := range history {
					for _, m := range records {
						m.conn.Close()
					}
				}
			}()
			for step, b := range actions {
				i, op := int(b&1), int(b>>1)%12
				m := current[i]
				if m == nil {
					fresh(i)
					m = current[i]
				}
				switch op {
				case 0:
					fresh(i)
				case 1, 2, 3:
					target := m
					clientID := ids[i]
					if op == 3 {
						target = history[i][0]
						if b&32 != 0 {
							clientID = ids[1-i]
						}
					}
					secret := target.conn.secret
					if op == 2 {
						secret += "wrong"
					}
					got, resumed, err := r.openSession(coreprotocol.Hello{ClientID: clientID, Resume: &coreprotocol.SessionResume{SessionID: target.conn.id, Secret: secret}})
					if target.alive && op != 2 && clientID == ids[i] {
						if err != nil || !resumed || got != target.conn {
							t.Fatalf("resume lost owner: %v", err)
						}
						bind(target)
					} else if !errors.Is(err, errSessionGone) || got != nil {
						t.Fatalf("bad/expired secret resumed: %v", err)
					}
				case 4:
					if m.alive {
						bind(m)
					}
				case 5:
					c := m.carriers[step%len(m.carriers)]
					m.conn.unbind(c)
					if m.alive && c == m.carrier {
						m.carrier = nil
					}
				case 6:
					c := m.carriers[0]
					m.conn.closeCarrier(c)
					if m.alive && c == m.carrier {
						m.alive = false
						m.carrier = nil
					}
					// A late close/unbind from an older session cannot erase a replacement.
					if len(history[i]) > 1 {
						old := history[i][0]
						old.conn.Close()
						old.conn.unbind(old.carriers[0])
					}
				case 7:
					m.conn.Close()
					m.alive = false
					m.carrier = nil
				case 8:
					// This Sleep advances only synctest's virtual clock, running the
					// production AfterFunc callback (not an explicit stand-in Close).
					time.Sleep(2 * time.Millisecond)
					for _, records := range history {
						for _, r := range records {
							if r.carrier == nil {
								r.alive = false
							}
						}
					}
				case 9, 10, 11:
					if !m.alive {
						break
					}
					id := string(rune('a' + (b/24)%4))
					payload, err := coreprotocol.Marshal(&coreprotocol.StartupSaveRequest{})
					if err != nil {
						t.Fatal(err)
					}
					request := coreprotocol.Request{ID: id, Owner: m.owner, Method: coreprotocol.StartupSave, Payload: payload}
					existing := m.commands[id]
					if op == 10 && existing != nil {
						if b&32 == 0 {
							request.WorkspaceID = "wrong-workspace"
						} else {
							request.Payload = append(request.Payload, 0)
						}
					}
					if op == 11 {
						request.ID = "wrong-owner"
						request.Owner.Epoch++
					}
					var release chan struct{}
					if existing == nil && op != 11 {
						release = make(chan struct{})
						owner.release = release
					}
					waiter, cancelWaiter := context.WithCancel(t.Context())
					cmd, err := m.conn.admit(waiter, request)
					cancelWaiter() // Carrier/waiter loss cannot cancel an admitted effect.
					if op == 10 && existing != nil {
						if err == nil {
							t.Fatal("changed preimage admitted")
						}
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if release != nil {
						// Lose the carrier while the actual application owner is
						// blocked, then reattach the same admitted command on resume.
						m.conn.unbind(m.carrier)
						m.carrier = nil
						resumed, ok, err := r.openSession(coreprotocol.Hello{ClientID: ids[i], Resume: &coreprotocol.SessionResume{SessionID: m.conn.id, Secret: m.conn.secret}})
						if err != nil || !ok || resumed != m.conn {
							t.Fatalf("pending effect lost its owner on resume: %v", err)
						}
						bind(m)
						duplicate, err := m.conn.admit(t.Context(), request)
						if err != nil || duplicate != cmd {
							t.Fatalf("in-flight identity was not retained: %v", err)
						}
						close(release)
					}
					<-cmd.done
					if op == 11 {
						if cmd.response.Error == nil || cmd.response.Error.Code != "invalid_owner" {
							t.Fatal("wrong owner executed")
						}
						break
					}
					if cmd.response.Error != nil {
						t.Fatal(cmd.response.Error)
					}
					if existing != nil {
						if cmd != existing {
							t.Fatal("same identity executed again")
						}
					} else {
						m.commands[id] = cmd
						effects++
					}
				}
				synctest.Wait()
				check()
			}
		})
	})
}
