package remote

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/types"
	"crude/workspace"
)

type startupTestOwner struct {
	mu        sync.Mutex
	settings  types.OnboardingSettings
	saves     atomic.Int32
	afterSave func()
}

func (o *startupTestOwner) StartupSnapshot(context.Context) (coreprotocol.StartupSnapshot, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return coreprotocol.StartupSnapshot{Onboarding: o.settings, ShowIntroduction: !o.settings.IntroductionCompleted}, nil
}

func (o *startupTestOwner) ReadStartupSettings(context.Context) (types.AppSettings, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return types.AppSettings{Onboarding: o.settings}, nil
}

func (o *startupTestOwner) SaveStartup(_ context.Context, edits []types.SettingEdit) (types.OnboardingSettings, error) {
	o.saves.Add(1)
	o.mu.Lock()
	settings := types.AppSettings{Onboarding: o.settings}
	err := types.ApplySettingsEdits(&settings, edits)
	if err == nil {
		o.settings = settings.Onboarding
	}
	out := o.settings
	o.mu.Unlock()
	if o.afterSave != nil {
		o.afterSave()
	}
	return out, err
}

func newStartupTestCore(t *testing.T, owner *startupTestOwner) *core.Runtime {
	t.Helper()
	t.Setenv("CRUDE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	r, err := core.New(func(string) (*workspace.Workspace, error) {
		t.Error("startup operation constructed a workspace")
		return nil, errors.New("startup must not attach")
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetStartupOwner(owner); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}

func startupTestEdit(field string) []types.SettingEdit {
	return []types.SettingEdit{{Path: []string{"onboarding", field}, After: json.RawMessage(`true`)}}
}

func startupTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestStartupClientExplicitInspectReconnectsAfterDialAndConnectionFailure(t *testing.T) {
	owner := &startupTestOwner{}
	r := newStartupTestCore(t, owner)
	var dials atomic.Int32
	dialFailure := errors.New("selected core is unavailable")
	var serverConn net.Conn
	s := &StartupClient{ClientID: "startup-reconnect"}
	s.Dial = func(ctx context.Context, id string) (*coreclient.Client, error) {
		if dials.Add(1) == 1 {
			return nil, dialFailure
		}
		a, b := net.Pipe()
		serverConn = a
		go func() { _ = core.ServeConnection(t.Context(), r, a) }()
		return coreclient.Dial(ctx, b, id)
	}
	t.Cleanup(func() { _ = s.Close() })
	root := t.TempDir()
	if _, err := s.Inspect(startupTestContext(t), root); !errors.Is(err, dialFailure) || dials.Load() != 1 {
		t.Fatalf("connection error hidden or automatically retried: dials=%d err=%v", dials.Load(), err)
	}
	out, err := s.Inspect(startupTestContext(t), root)
	if err != nil || !out.ShowIntroduction || out.Root != root || dials.Load() != 2 {
		t.Fatalf("explicit retry did not reconnect: %+v dials=%d err=%v", out, dials.Load(), err)
	}
	old := s.Client
	if err := serverConn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-old.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("client did not observe transport close")
	}
	if dials.Load() != 2 {
		t.Fatal("transport loss reconnected without an explicit action")
	}
	if _, err := s.Inspect(startupTestContext(t), root); err != nil || dials.Load() != 3 || s.Client == old {
		t.Fatalf("next Inspect did not replace disconnected client: dials=%d err=%v", dials.Load(), err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil || s.Client != nil {
		t.Fatalf("Close is not idempotent: %v", err)
	}
}

func TestStartupClientUncertainSaveIsNotReplayed(t *testing.T) {
	owner := &startupTestOwner{}
	r := newStartupTestCore(t, owner)
	var dials atomic.Int32
	var mu sync.Mutex
	var serverConn net.Conn
	owner.afterSave = func() {
		mu.Lock()
		defer mu.Unlock()
		// Commit succeeds, but its acknowledgement is lost on the actual wire.
		_ = serverConn.Close()
	}
	s := &StartupClient{ClientID: "startup-uncertain"}
	s.Dial = func(ctx context.Context, id string) (*coreclient.Client, error) {
		dials.Add(1)
		a, b := net.Pipe()
		mu.Lock()
		serverConn = a
		mu.Unlock()
		go func() { _ = core.ServeConnection(t.Context(), r, a) }()
		return coreclient.Dial(ctx, b, id)
	}
	t.Cleanup(func() { _ = s.Close() })
	_, err := s.Save(startupTestContext(t), startupTestEdit("introduction_completed"))
	var uncertain *coreclient.UncertainError
	if !errors.As(err, &uncertain) || uncertain.RequestID == "" {
		t.Fatalf("lost acknowledgement was not surfaced as uncertain: %v", err)
	}
	if dials.Load() != 1 || owner.saves.Load() != 1 {
		t.Fatalf("uncertain mutation replayed: dials=%d saves=%d", dials.Load(), owner.saves.Load())
	}
	out, err := s.Inspect(startupTestContext(t), t.TempDir())
	if err != nil || !out.Onboarding.IntroductionCompleted || out.ShowIntroduction || dials.Load() != 2 || owner.saves.Load() != 1 {
		t.Fatalf("explicit Inspect did not recover committed state without replay: %+v dials=%d saves=%d err=%v", out, dials.Load(), owner.saves.Load(), err)
	}
}

// startupBrowseResponseDropper consumes just the Browse response. All requests
// still go through the real core; other response frames pass immediately. This
// deterministically leaves Browse awaiting network delivery without a slow disk.
type startupBrowseResponseDropper struct {
	net.Conn
	buffer  []byte
	dropped chan struct{}
	once    sync.Once
}

func (c *startupBrowseResponseDropper) Write(p []byte) (int, error) {
	c.buffer = append(c.buffer, p...)
	for len(c.buffer) >= 4 {
		size := int(binary.BigEndian.Uint32(c.buffer[:4]))
		if len(c.buffer) < 4+size {
			break
		}
		packet := c.buffer[:4+size]
		var frame corewire.Frame
		if err := json.Unmarshal(packet[4:], &frame); err != nil {
			return 0, err
		}
		if frame.Kind == corewire.KindResponse && frame.Method == coreprotocol.StartupBrowse {
			c.once.Do(func() { close(c.dropped) })
		} else {
			for len(packet) > 0 {
				n, err := c.Conn.Write(packet)
				if err != nil {
					return 0, err
				}
				packet = packet[n:]
			}
		}
		c.buffer = c.buffer[4+size:]
	}
	return len(p), nil
}

func TestStartupClientPendingBrowseDoesNotSerializeInspectOrSaveAndCancels(t *testing.T) {
	owner := &startupTestOwner{}
	r := newStartupTestCore(t, owner)
	dropped := make(chan struct{})
	var dials atomic.Int32
	s := &StartupClient{ClientID: "startup-cancel-browse"}
	s.Dial = func(ctx context.Context, id string) (*coreclient.Client, error) {
		dials.Add(1)
		a, b := net.Pipe()
		go func() {
			_ = core.ServeConnection(t.Context(), r, &startupBrowseResponseDropper{Conn: a, dropped: dropped})
		}()
		return coreclient.Dial(ctx, b, id)
	}
	t.Cleanup(func() { _ = s.Close() })
	browseCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	root := t.TempDir()
	go func() {
		_, err := s.Browse(browseCtx, coreprotocol.StartupBrowseRequest{Path: root})
		result <- err
	}()
	select {
	case <-dropped:
	case <-time.After(3 * time.Second):
		t.Fatal("Browse never reached framed core")
	}
	if _, err := s.Inspect(startupTestContext(t), root); err != nil {
		t.Fatalf("pending Browse blocked independent Inspect: %v", err)
	}
	if saved, err := s.Save(startupTestContext(t), startupTestEdit("ai_setup_reminder_hidden")); err != nil || !saved.AISetupReminderHidden {
		t.Fatalf("pending Browse blocked independent Save: %+v, %v", saved, err)
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Browse cancellation not visible: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Browse ignored context cancellation")
	}
	out, err := s.Inspect(startupTestContext(t), root)
	if err != nil || !out.Onboarding.AISetupReminderHidden || dials.Load() != 1 {
		t.Fatalf("cancelled Browse broke independent connection/state: %+v dials=%d err=%v", out, dials.Load(), err)
	}
}
