package desktopnotify

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// The freedesktop.org Desktop Notifications Specification, version 1.2:
// https://specifications.freedesktop.org/notification-spec/latest/
// GNOME, KDE Plasma, Xfce, Cinnamon, dunst, mako and swaync implement it.
// Other Unix systems speak it too, but godbus needs cgo to authenticate
// there, so they use the unsupported backend until a user needs them.
const (
	fdoName      = "org.freedesktop.Notifications"
	fdoPath      = dbus.ObjectPath("/org/freedesktop/Notifications")
	fdoInterface = "org.freedesktop.Notifications"
	fdoTimeout   = 5 * time.Second
	// fdoDefaultAction is the spec's key for a click on the message itself.
	fdoDefaultAction = "default"
)

type fdoBackend struct {
	conn      *dbus.Conn
	object    dbus.BusObject
	name      string
	icon      string
	markup    bool
	actions   bool
	activated func(string)

	signals chan *dbus.Signal
	stop    chan struct{}
	done    chan struct{}

	mu   sync.Mutex
	ids  map[string]uint32
	keys map[uint32]string
}

func openBackend(id Identity, activated func(string)) (backend, error) {
	// No auto start: launching a private bus (dbus-launch) where there is no
	// desktop session, for example over SSH, would show nothing to anyone.
	conn, err := dbus.SessionBusPrivateNoAutoStartup()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoService, err)
	}
	if err := conn.Auth(nil); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: %v", ErrNoService, err)
	}
	if err := conn.Hello(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: %v", ErrNoService, err)
	}
	b, err := openFreedesktop(conn, id, activated)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return b, nil
}

// openFreedesktop takes ownership of an authenticated session connection.
func openFreedesktop(conn *dbus.Conn, id Identity, activated func(string)) (*fdoBackend, error) {
	ctx, cancel := context.WithTimeout(context.Background(), fdoTimeout)
	defer cancel()
	object := conn.Object(fdoName, fdoPath)
	var caps []string
	if err := object.CallWithContext(ctx, fdoInterface+".GetCapabilities", 0).Store(&caps); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoService, err)
	}
	// Only the service's own signals: any program on the bus may emit a
	// signal with this interface name.
	match := []dbus.MatchOption{dbus.WithMatchSender(fdoName), dbus.WithMatchObjectPath(fdoPath), dbus.WithMatchInterface(fdoInterface)}
	if err := conn.AddMatchSignalContext(ctx, match...); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoService, err)
	}
	icon, _ := iconFile(id)
	if icon != "" {
		// The specification accepts an icon-theme name or a file URI, not a
		// bare absolute path. Some desktops treat the latter as a theme name.
		icon = (&url.URL{Scheme: "file", Path: icon}).String()
	}
	b := &fdoBackend{
		conn: conn, object: object, name: id.Name, icon: icon, activated: activated,
		signals: make(chan *dbus.Signal, 16), stop: make(chan struct{}), done: make(chan struct{}),
		ids: make(map[string]uint32), keys: make(map[uint32]string),
	}
	for _, capability := range caps {
		switch capability {
		case "body-markup":
			b.markup = true
		case "actions":
			b.actions = true
		}
	}
	conn.Signal(b.signals)
	go b.listen()
	return b, nil
}

func (b *fdoBackend) available() error { return nil }

func (b *fdoBackend) show(n Notification) error {
	body := n.Body
	if b.markup {
		// A server with body-markup parses the body as markup; text from an
		// agent must not become markup.
		body = escapeMarkup(body)
	}
	var actions []string
	if b.actions {
		actions = []string{fdoDefaultAction, "Open"}
	}
	urgency := byte(1) // normal
	hints := map[string]dbus.Variant{}
	if n.Quiet {
		urgency = 0 // low: no banner on most servers
	}
	if n.Quiet || n.Silent {
		hints["suppress-sound"] = dbus.MakeVariant(true)
	}
	hints["urgency"] = dbus.MakeVariant(urgency)
	b.mu.Lock()
	replaces := b.ids[n.Key]
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), fdoTimeout)
	defer cancel()
	var id uint32
	err := b.object.CallWithContext(ctx, fdoInterface+".Notify", 0,
		b.name, replaces, b.icon, n.Title, body, actions, hints, int32(-1)).Store(&id)
	if err != nil {
		return notifyError(err)
	}
	b.mu.Lock()
	if replaces != 0 && replaces != id {
		delete(b.keys, replaces)
	}
	b.ids[n.Key], b.keys[id] = id, n.Key
	b.mu.Unlock()
	return nil
}

func (b *fdoBackend) withdraw(key string) error {
	b.mu.Lock()
	id, ok := b.ids[key]
	delete(b.ids, key)
	delete(b.keys, id)
	b.mu.Unlock()
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), fdoTimeout)
	defer cancel()
	// A message the user already closed answers with an error; it is gone
	// either way.
	_ = b.object.CallWithContext(ctx, fdoInterface+".CloseNotification", 0, id).Err
	return nil
}

func (b *fdoBackend) close() {
	close(b.stop)
	<-b.done
	b.conn.RemoveSignal(b.signals)
	_ = b.conn.Close()
}

// listen turns the service's signals into clicks and forgets closed
// messages. Signal bodies are checked: a malformed signal is ignored.
func (b *fdoBackend) listen() {
	defer close(b.done)
	for {
		var signal *dbus.Signal
		select {
		case <-b.stop:
			return
		case signal = <-b.signals:
		}
		if signal == nil {
			return
		}
		switch signal.Name {
		case fdoInterface + ".ActionInvoked":
			if len(signal.Body) != 2 {
				continue
			}
			id, okID := signal.Body[0].(uint32)
			action, okAction := signal.Body[1].(string)
			if !okID || !okAction || action != fdoDefaultAction {
				continue
			}
			b.mu.Lock()
			key, ours := b.keys[id]
			b.mu.Unlock()
			if ours && b.activated != nil {
				b.activated(key)
			}
		case fdoInterface + ".NotificationClosed":
			if len(signal.Body) < 1 {
				continue
			}
			id, ok := signal.Body[0].(uint32)
			if !ok {
				continue
			}
			b.mu.Lock()
			if key, ours := b.keys[id]; ours {
				delete(b.keys, id)
				if b.ids[key] == id {
					delete(b.ids, key)
				}
			}
			b.mu.Unlock()
		}
	}
}

func notifyError(err error) error {
	var dbusErr dbus.Error
	if errors.As(err, &dbusErr) && (dbusErr.Name == "org.freedesktop.DBus.Error.ServiceUnknown" || dbusErr.Name == "org.freedesktop.DBus.Error.NameHasNoOwner") {
		return fmt.Errorf("%w: %v", ErrNoService, err)
	}
	return err
}

var markupEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func escapeMarkup(text string) string { return markupEscaper.Replace(text) }
