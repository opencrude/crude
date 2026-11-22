package desktopnotify

import (
	"bufio"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"crude/assets"
)

// fakeServer is an org.freedesktop.Notifications service on a private
// session bus. Each test owns its daemon; the desktop bus is never used.
type fakeServer struct {
	mu     sync.Mutex
	caps   []string
	next   uint32
	calls  []notifyCall
	closed []uint32
}

type notifyCall struct {
	app      string
	replaces uint32
	icon     string
	summary  string
	body     string
	actions  []string
	hints    map[string]dbus.Variant
	timeout  int32
}

func (s *fakeServer) GetCapabilities() ([]string, *dbus.Error) { return s.caps, nil }

func (s *fakeServer) Notify(app string, replaces uint32, icon, summary, body string, actions []string, hints map[string]dbus.Variant, timeout int32) (uint32, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, notifyCall{app, replaces, icon, summary, body, actions, hints, timeout})
	if replaces != 0 {
		return replaces, nil
	}
	s.next++
	return s.next, nil
}

func (s *fakeServer) CloseNotification(id uint32) *dbus.Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = append(s.closed, id)
	return nil
}

func (s *fakeServer) GetServerInformation() (string, string, string, string, *dbus.Error) {
	return "fake", "crude", "1", "1.2", nil
}

func (s *fakeServer) lastCall(t *testing.T) notifyCall {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		t.Fatal("no Notify call")
	}
	return s.calls[len(s.calls)-1]
}

func startFakeServer(t *testing.T, caps ...string) (*fakeServer, *dbus.Conn) {
	t.Helper()
	startPrivateBus(t)
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	server := &fakeServer{caps: caps}
	if err := conn.Export(server, fdoPath, fdoInterface); err != nil {
		t.Fatal(err)
	}
	reply, err := conn.RequestName(fdoName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own %s: %v %v", fdoName, reply, err)
	}
	return server, conn
}

func startPrivateBus(t *testing.T) {
	t.Helper()
	binary, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon is required for the private notification bus")
	}
	cmd := exec.Command(binary, "--session", "--nofork", "--nopidfile",
		"--print-address=1", "--address=unix:tmpdir="+t.TempDir())
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	readDone := make(chan struct{})
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = output.Close()
		<-readDone
	})
	address := make(chan string, 1)
	go func() {
		defer close(readDone)
		line, _ := bufio.NewReader(output).ReadString('\n')
		address <- strings.TrimSpace(line)
	}()
	select {
	case value := <-address:
		if value == "" {
			t.Fatal("private bus did not publish its address")
		}
		t.Setenv("DBUS_SESSION_BUS_ADDRESS", value)
	case <-time.After(5 * time.Second):
		t.Fatal("private bus did not become ready")
	}
}

func TestFreedesktopShowReplaceWithdrawAndClick(t *testing.T) {
	server, serverConn := startFakeServer(t, "actions", "body-markup", "body")
	clicks := make(chan string, 4)
	dir := filepath.Join(t.TempDir(), "Crude logo #1")
	b, err := openBackend(Identity{Name: "Crude", IconPNG: []byte("png"), Dir: dir}, func(key string) { clicks <- key })
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	if err := b.show(Notification{Key: "w", Title: "Ivan needs your answer", Body: "<b>x</b> & y"}); err != nil {
		t.Fatal(err)
	}
	first := server.lastCall(t)
	if first.app != "Crude" || first.replaces != 0 || first.summary != "Ivan needs your answer" || first.timeout != -1 {
		t.Fatalf("notify = %+v", first)
	}
	if first.body != "&lt;b&gt;x&lt;/b&gt; &amp; y" {
		t.Fatalf("body %q: a markup server must receive escaped text", first.body)
	}
	icon, err := url.Parse(first.icon)
	if want := filepath.Join(dir, "icon.png"); err != nil || icon.Scheme != "file" || icon.Host != "" || icon.Path != want || icon.Fragment != "" {
		t.Fatalf("icon = %q, want an escaped file URI for %q: %v", first.icon, want, err)
	}
	if len(first.actions) != 2 || first.actions[0] != fdoDefaultAction {
		t.Fatalf("actions = %v, want the default action", first.actions)
	}
	if urgency := first.hints["urgency"].Value(); urgency != byte(1) {
		t.Fatalf("urgency = %v", urgency)
	}

	if err := b.show(Notification{Key: "w", Title: "2 agents need you", Quiet: true}); err != nil {
		t.Fatal(err)
	}
	second := server.lastCall(t)
	if second.replaces != 1 {
		t.Fatalf("replaces = %d, want the first message's id", second.replaces)
	}
	if second.hints["urgency"].Value() != byte(0) || second.hints["suppress-sound"].Value() != true {
		t.Fatalf("quiet hints = %v", second.hints)
	}
	if _, ok := first.hints["suppress-sound"]; ok {
		t.Fatalf("a normal message must keep the sound: %v", first.hints)
	}
	if err := b.show(Notification{Key: "w", Title: "silent", Silent: true}); err != nil {
		t.Fatal(err)
	}
	if silent := server.lastCall(t); silent.hints["urgency"].Value() != byte(1) || silent.hints["suppress-sound"].Value() != true {
		t.Fatalf("silent hints = %v; a silent banner keeps normal urgency", silent.hints)
	}

	// A signal from another program on the bus is not a click.
	rogue, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer rogue.Close()
	if err := rogue.Emit(fdoPath, fdoInterface+".ActionInvoked", uint32(1), fdoDefaultAction); err != nil {
		t.Fatal(err)
	}
	if err := serverConn.Emit(fdoPath, fdoInterface+".ActionInvoked", uint32(1), "other"); err != nil {
		t.Fatal(err)
	}
	if err := serverConn.Emit(fdoPath, fdoInterface+".ActionInvoked", uint32(1), fdoDefaultAction); err != nil {
		t.Fatal(err)
	}
	select {
	case key := <-clicks:
		if key != "w" {
			t.Fatalf("click key = %q", key)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no click")
	}
	select {
	case key := <-clicks:
		t.Fatalf("extra click %q from a rogue or non-default action", key)
	case <-time.After(200 * time.Millisecond):
	}

	if err := b.withdraw("w"); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	closed := append([]uint32(nil), server.closed...)
	server.mu.Unlock()
	if len(closed) != 1 || closed[0] != 1 {
		t.Fatalf("closed = %v", closed)
	}
}

func TestFreedesktopForgetsAMessageTheUserClosed(t *testing.T) {
	server, serverConn := startFakeServer(t, "body")
	b, err := openBackend(Identity{Name: "Crude"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	if err := b.show(Notification{Key: "w", Title: "A", Body: "<plain>"}); err != nil {
		t.Fatal(err)
	}
	if call := server.lastCall(t); call.body != "<plain>" || len(call.actions) != 0 || call.icon != "" {
		t.Fatalf("plain server call = %+v", call)
	}
	if err := serverConn.Emit(fdoPath, fdoInterface+".NotificationClosed", uint32(1), uint32(2)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		fb := b.(*fdoBackend)
		fb.mu.Lock()
		_, known := fb.ids["w"]
		fb.mu.Unlock()
		if !known {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a closed message is still tracked")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := b.show(Notification{Key: "w", Title: "B"}); err != nil {
		t.Fatal(err)
	}
	if call := server.lastCall(t); call.replaces != 0 {
		t.Fatalf("replaces = %d after the user closed the message", call.replaces)
	}
}

func TestFreedesktopNoServiceIsReported(t *testing.T) {
	startPrivateBus(t)
	if _, err := openBackend(Identity{Name: "Crude"}, nil); err == nil {
		t.Fatal("a private bus without a service must report ErrNoService")
	} else if !errors.Is(err, ErrNoService) {
		t.Fatalf("err = %v, want ErrNoService", err)
	}
}

// Opt-in native desktop acceptance. The ordinary suite always uses private
// buses; this test shows and withdraws only its own silent sample notification.
func TestFreedesktopNativeLogoAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_NATIVE_NOTIFICATION") != "1" {
		t.Skip("explicit native desktop notification acceptance")
	}
	b, err := openBackend(Identity{Name: "Crude", IconPNG: assets.IconPNG, Dir: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := b.withdraw("native-logo-check"); err != nil {
			t.Error(err)
		}
		b.close()
	}()
	if err := b.show(Notification{Key: "native-logo-check", Title: "Crude logo check", Body: "Crude's application icon.", Silent: true}); err != nil {
		t.Fatal(err)
	}
	if path := os.Getenv("CRUDE_NOTIFICATION_RESULT"); path != "" {
		if err := os.WriteFile(path, []byte("NATIVE_NOTIFICATION_READY\n"+b.(*fdoBackend).icon+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-time.After(8 * time.Second):
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
}
