// Package desktopnotify shows native desktop notifications from a shell
// process: toast notifications on Windows and freedesktop.org notifications
// (D-Bus) on Linux. macOS is not implemented yet; darwin.go
// holds its place and the plan for it.
//
// One Center per process owns one platform backend on one locked OS thread,
// because the Windows Runtime binds its objects to the thread that created
// them. Callers never block: Show and Withdraw only record the latest wish
// for a key, and the worker applies it. Clicks, readiness and failures come
// back on Events. Policy (what to say and when) belongs to the caller; this
// package only delivers, replaces and withdraws messages.
package desktopnotify

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"crude/internal/hostenv"
)

// Identity names the application to the desktop.
type Identity struct {
	// AppID is the stable machine identity. Windows uses it as the
	// AppUserModelID that groups the notifications and owns their on/off
	// switch in Windows Settings.
	AppID string
	// Name is what the desktop shows as the sender.
	Name string
	// IconPNG is the application icon. It is written once under Dir, because
	// the desktop reads it from a file.
	IconPNG []byte
	// Dir holds the files that the desktop reads.
	Dir string
}

// Notification is one message. A later Show with the same Key replaces it in
// place; Withdraw removes it from the screen and from the notification list.
type Notification struct {
	Key   string
	Title string
	Body  string
	// Quiet updates the message without a new banner or sound where the
	// desktop supports that, for news that is not urgent.
	Quiet bool
	// Silent shows the banner without the system sound.
	Silent bool
}

// EventKind classifies an Event.
type EventKind uint8

const (
	// Ready reports whether the desktop can show notifications now. Err is
	// nil when it can, and says why not when it cannot.
	Ready EventKind = iota + 1
	// Shown reports that the notification with Key reached the desktop.
	Shown
	// Failed reports that the notification with Key was not shown, and why.
	Failed
	// Activated reports that the user clicked the notification with Key.
	Activated
)

// Event is one report from the worker to the caller.
type Event struct {
	Kind EventKind
	Key  string
	Err  error
}

var (
	// ErrUnsupported means this operating system has no backend yet.
	ErrUnsupported = errors.New("desktop notifications are not available on this operating system")
	// ErrNoService means no notification service answers, for example in an
	// SSH session or a desktop without a notification daemon.
	ErrNoService = errors.New("no desktop notification service is running")
	// ErrBlocked means the user or a policy turned notifications off for
	// this application.
	ErrBlocked = errors.New("desktop notifications are turned off for this application")
	// ErrRemoteSession means this process runs in an SSH session. Its
	// desktop, if any, belongs to the remote computer, not to the user.
	ErrRemoteSession = errors.New("desktop notifications are not shown from an SSH session")
)

// backend is one platform's delivery. The Center calls it only from its
// worker goroutine, which holds one OS thread; activated may be called from
// any goroutine.
type backend interface {
	// available reports whether a notification shown now would appear.
	available() error
	show(Notification) error
	withdraw(key string) error
	close()
}

type opener func(id Identity, activated func(key string)) (backend, error)

const (
	// eventBuffer bounds unread events. Events are rare; a full buffer drops
	// the newest instead of blocking a platform callback thread.
	eventBuffer = 64
	// queueLimit bounds pending requests if a caller produces many keys.
	queueLimit = 256
	// closeWait bounds how long Close waits for a stalled desktop service.
	closeWait = 3 * time.Second
	maxTitle  = 120
	maxBody   = 400
)

type requestKind uint8

const (
	requestShow requestKind = iota + 1
	requestWithdraw
	requestWithdrawAll
)

type request struct {
	kind requestKind
	note Notification
}

// Center delivers notifications for one process.
type Center struct {
	id   Identity
	open opener

	mu       sync.Mutex
	queue    []request
	closed   bool
	finished bool
	events   chan Event

	wake chan struct{}
	stop chan struct{}
	done chan struct{}
}

// Start begins delivery for id. The backend opens on the worker at once, so
// the first Ready event tells the caller whether this desktop can notify.
func Start(id Identity) *Center {
	return start(id, func(id Identity, activated func(string)) (backend, error) {
		if hostenv.SSHSession(os.Getenv) {
			return nil, ErrRemoteSession
		}
		return openBackend(id, activated)
	})
}

func start(id Identity, open opener) *Center {
	c := &Center{
		id: id, open: open,
		events: make(chan Event, eventBuffer),
		wake:   make(chan struct{}, 1),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go c.run()
	return c
}

// Show queues n, replacing any pending wish for the same key.
func (c *Center) Show(n Notification) {
	if c == nil || n.Key == "" {
		return
	}
	n.Title, n.Body = clean(n.Title, maxTitle, false), clean(n.Body, maxBody, true)
	if n.Title == "" {
		n.Title, n.Body = n.Body, ""
	}
	if n.Title == "" {
		return
	}
	c.enqueue(request{kind: requestShow, note: n})
}

// Withdraw removes the notification with key, if this Center showed it.
func (c *Center) Withdraw(key string) {
	if c == nil || key == "" {
		return
	}
	c.enqueue(request{kind: requestWithdraw, note: Notification{Key: key}})
}

// WithdrawAll removes every notification this Center showed. Pending wishes
// queued before it are superseded.
func (c *Center) WithdrawAll() {
	if c == nil {
		return
	}
	c.enqueue(request{kind: requestWithdrawAll})
}

// Events returns the report channel. It is closed after Close.
func (c *Center) Events() <-chan Event {
	if c == nil {
		return nil
	}
	return c.events
}

// Close withdraws everything this Center showed and stops the worker. A
// desktop service that does not answer cannot hold the caller past closeWait.
func (c *Center) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()
	close(c.stop)
	select {
	case <-c.done:
	case <-time.After(closeWait):
	}
}

func (c *Center) enqueue(r request) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	if r.kind == requestWithdrawAll {
		c.queue = append(c.queue[:0], r)
	} else {
		kept := c.queue[:0]
		for _, pending := range c.queue {
			if pending.kind == requestWithdrawAll || pending.note.Key != r.note.Key {
				kept = append(kept, pending)
			}
		}
		c.queue = append(kept, r)
		for len(c.queue) > queueLimit {
			c.queue = c.queue[1:]
		}
	}
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Center) take() []request {
	c.mu.Lock()
	defer c.mu.Unlock()
	queue := c.queue
	c.queue = nil
	return queue
}

func (c *Center) emit(e Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return
	}
	select {
	case c.events <- e:
	default:
	}
}

func (c *Center) activated(key string) { c.emit(Event{Kind: Activated, Key: key}) }

func (c *Center) run() {
	// The Windows Runtime apartment belongs to this thread for the life of
	// the backend; every backend call happens here.
	runtime.LockOSThread()
	defer func() {
		c.mu.Lock()
		c.finished = true
		close(c.events)
		c.mu.Unlock()
		close(c.done)
	}()
	b, err := c.open(c.id, c.activated)
	if err == nil {
		err = b.available()
	}
	c.emit(Event{Kind: Ready, Err: err})
	shown := make(map[string]bool)
	for {
		select {
		case <-c.wake:
		case <-c.stop:
			if b != nil {
				for key := range shown {
					_ = b.withdraw(key)
				}
				b.close()
			}
			return
		}
		for _, r := range c.take() {
			if b == nil && r.kind == requestShow {
				// The service may have started since; try again on demand.
				if b, err = c.open(c.id, c.activated); err != nil {
					b = nil
					c.emit(Event{Kind: Failed, Key: r.note.Key, Err: err})
					continue
				}
				c.emit(Event{Kind: Ready, Err: b.available()})
			}
			if b == nil {
				continue
			}
			switch r.kind {
			case requestShow:
				if err := b.show(r.note); err != nil {
					c.emit(Event{Kind: Failed, Key: r.note.Key, Err: err})
					continue
				}
				shown[r.note.Key] = true
				c.emit(Event{Kind: Shown, Key: r.note.Key})
			case requestWithdraw:
				if shown[r.note.Key] {
					delete(shown, r.note.Key)
					_ = b.withdraw(r.note.Key)
				}
			case requestWithdrawAll:
				for key := range shown {
					delete(shown, key)
					_ = b.withdraw(key)
				}
			}
		}
	}
}

// clean makes text safe for every desktop: valid UTF-8, no control
// characters (a newline survives in a body), no edge spaces, and at most
// limit characters with an ellipsis.
func clean(text string, limit int, multiline bool) string {
	text = strings.ToValidUTF8(text, "\uFFFD")
	var b strings.Builder
	for _, r := range text {
		switch {
		case r == '\n' && multiline:
			b.WriteRune(r)
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(' ')
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	text = strings.TrimSpace(b.String())
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit-1])) + "\u2026"
}
