package desktopnotify

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/internal/hostenv"
)

type fakeBackend struct {
	mu        sync.Mutex
	shown     []Notification
	withdrawn []string
	closed    bool
	showErr   error
	gate      chan struct{}
}

func (f *fakeBackend) available() error { return nil }
func (f *fakeBackend) show(n Notification) error {
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.showErr != nil {
		return f.showErr
	}
	f.shown = append(f.shown, n)
	return nil
}

func (f *fakeBackend) withdraw(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.withdrawn = append(f.withdrawn, key)
	return nil
}

func (f *fakeBackend) close() { f.mu.Lock(); f.closed = true; f.mu.Unlock() }

func (f *fakeBackend) snapshot() ([]Notification, []string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Notification(nil), f.shown...), append([]string(nil), f.withdrawn...), f.closed
}

func next(t *testing.T, c *Center, kind EventKind) Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-c.Events():
			if !ok {
				t.Fatalf("events closed while waiting for kind %d", kind)
			}
			if e.Kind == kind {
				return e
			}
		case <-timeout:
			t.Fatalf("no event of kind %d", kind)
		}
	}
}

func TestCenterReplacesPendingWishesByKey(t *testing.T) {
	b := &fakeBackend{gate: make(chan struct{})}
	c := start(Identity{}, func(Identity, func(string)) (backend, error) { return b, nil })
	next(t, c, Ready)
	c.Show(Notification{Key: "w", Title: "first"})
	// The worker is now blocked inside show; the next wishes queue.
	time.Sleep(20 * time.Millisecond)
	c.Show(Notification{Key: "w", Title: "second"})
	c.Show(Notification{Key: "w", Title: "third"})
	close(b.gate)
	next(t, c, Shown)
	next(t, c, Shown)
	shown, _, _ := b.snapshot()
	if len(shown) != 2 || shown[0].Title != "first" || shown[1].Title != "third" {
		t.Fatalf("shown = %+v, want first and only the latest pending wish", shown)
	}
	c.Close()
	_, withdrawn, closed := b.snapshot()
	if !closed || len(withdrawn) != 1 || withdrawn[0] != "w" {
		t.Fatalf("close: withdrawn = %v closed = %v; Close must withdraw what it showed", withdrawn, closed)
	}
	if _, ok := <-c.Events(); ok {
		t.Fatal("events stay open after Close")
	}
}

func TestCenterWithdrawsOnlyWhatItShowed(t *testing.T) {
	b := &fakeBackend{}
	c := start(Identity{}, func(Identity, func(string)) (backend, error) { return b, nil })
	defer c.Close()
	next(t, c, Ready)
	c.Withdraw("never-shown")
	c.Show(Notification{Key: "a", Title: "A"})
	next(t, c, Shown)
	c.Show(Notification{Key: "b", Title: "B"})
	next(t, c, Shown)
	c.WithdrawAll()
	c.Show(Notification{Key: "c", Title: "C"})
	next(t, c, Shown)
	_, withdrawn, _ := b.snapshot()
	if len(withdrawn) != 2 || strings.Join(sorted(withdrawn), ",") != "a,b" {
		t.Fatalf("withdrawn = %v, want a and b only", withdrawn)
	}
}

func sorted(values []string) []string {
	out := append([]string(nil), values...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestCenterReportsFailuresAndRetriesAMissingService(t *testing.T) {
	b := &fakeBackend{}
	opens := 0
	c := start(Identity{}, func(Identity, func(string)) (backend, error) {
		opens++
		if opens == 1 {
			return nil, ErrNoService
		}
		return b, nil
	})
	defer c.Close()
	if e := next(t, c, Ready); !errors.Is(e.Err, ErrNoService) {
		t.Fatalf("ready = %+v, want no service", e)
	}
	c.Show(Notification{Key: "a", Title: "A"})
	if e := next(t, c, Ready); e.Err != nil {
		t.Fatalf("second ready = %+v", e)
	}
	next(t, c, Shown)
	b.mu.Lock()
	b.showErr = ErrBlocked
	b.mu.Unlock()
	c.Show(Notification{Key: "b", Title: "B"})
	if e := next(t, c, Failed); e.Key != "b" || !errors.Is(e.Err, ErrBlocked) {
		t.Fatalf("failed = %+v", e)
	}
}

func TestCenterDeliversClicksByKey(t *testing.T) {
	var click func(string)
	c := start(Identity{}, func(_ Identity, activated func(string)) (backend, error) {
		click = activated
		return &fakeBackend{}, nil
	})
	defer c.Close()
	next(t, c, Ready)
	click("workspace:x")
	if e := next(t, c, Activated); e.Key != "workspace:x" {
		t.Fatalf("activated = %+v", e)
	}
}

func TestCenterDropsEmptyMessages(t *testing.T) {
	b := &fakeBackend{}
	c := start(Identity{}, func(Identity, func(string)) (backend, error) { return b, nil })
	next(t, c, Ready)
	c.Show(Notification{Key: "a", Title: " \t ", Body: "\x00\x07"})
	c.Show(Notification{Key: "", Title: "no key"})
	c.Show(Notification{Key: "b", Body: "body only"})
	next(t, c, Shown)
	c.Close()
	shown, _, _ := b.snapshot()
	if len(shown) != 1 || shown[0].Title != "body only" || shown[0].Body != "" {
		t.Fatalf("shown = %+v", shown)
	}
}

func TestCleanText(t *testing.T) {
	if got := clean("a\tb\r\nc\x1b[31m", 100, false); got != "a b  c[31m" {
		t.Fatalf("title = %q", got)
	}
	if got := clean("line 1\nline 2", 100, true); got != "line 1\nline 2" {
		t.Fatalf("body = %q", got)
	}
	if got := clean(strings.Repeat("é", 10), 5, false); got != "éééé…" {
		t.Fatalf("truncated = %q", got)
	}
	if got := clean("bad \xff utf8", 100, false); got != "bad \uFFFD utf8" {
		t.Fatalf("invalid = %q", got)
	}
}

func TestRemoteSession(t *testing.T) {
	env := map[string]string{}
	getenv := func(key string) string { return env[key] }
	if hostenv.SSHSession(getenv) {
		t.Fatal("a local session counts as remote")
	}
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		env = map[string]string{key: "x"}
		if !hostenv.SSHSession(getenv) {
			t.Fatalf("%s does not mark an SSH session", key)
		}
	}
}
