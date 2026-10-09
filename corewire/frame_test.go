package corewire

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func pipeFramers(t *testing.T) (*Framer, *Framer, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	a, b := net.Pipe()
	x, y := NewFramer(ctx, a), NewFramer(ctx, b)
	t.Cleanup(func() { cancel(); _ = x.Close(); _ = y.Close() })
	return x, y, cancel
}

func TestNegotiationAndConcurrentWrites(t *testing.T) {
	a, b, _ := pipeFramers(t)
	server := make(chan error, 1)
	go func() {
		id, n, err := b.NegotiateServer(4096)
		if err == nil && (id != "client" || n != 4096) {
			t.Errorf("negotiated %q %d", id, n)
		}
		server <- err
	}()
	if n, err := a.NegotiateClient("client", MaxFrameSize); err != nil || n != 4096 {
		t.Fatalf("%d %v", n, err)
	}
	if err := <-server; err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.Write(Frame{Version: 1, Kind: KindRequest, Method: "snapshot", Payload: json.RawMessage(`{"value":1}`)}); err != nil {
				t.Error(err)
			}
		}()
	}
	for i := 0; i < 20; i++ {
		f, err := b.Read()
		if err != nil || f.Method != "snapshot" {
			t.Fatalf("%+v %v", f, err)
		}
	}
	wg.Wait()
	if err := a.Write(Frame{Version: 1, Kind: KindEvent, Payload: json.RawMessage(`"` + strings.Repeat("x", 4096) + `"`)}); err == nil {
		t.Fatal("accepted oversize negotiated frame")
	}
}

func TestRejectMalformedFrames(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"version":2,"kind":"event"}`, `{"version":1,"kind":"unknown"}`, `{"version":1,"kind":"event","extra":1}`, `{"version":1,"kind":"event","version":1}`, `{"version":1,"kind":"event","payload":{"x":1,"x":2}}`, `{"version":1,"kind":"event"} {}`, `{"version":1,`, strings.Repeat("[", 129) + strings.Repeat("]", 129)} {
		t.Run(body[:min(len(body), 40)], func(t *testing.T) {
			a, b, _ := pipeFramers(t)
			go func() {
				var h [4]byte
				binary.BigEndian.PutUint32(h[:], uint32(len(body)))
				_, _ = a.conn.Write(h[:])
				_, _ = io.WriteString(a.conn, body)
			}()
			if _, err := b.Read(); err == nil {
				t.Fatal("accepted malformed input")
			}
		})
	}
}

func TestOversizeHeaderClosesWithoutBody(t *testing.T) {
	a, b, _ := pipeFramers(t)
	go func() { var h [4]byte; binary.BigEndian.PutUint32(h[:], MaxFrameSize+1); _, _ = a.conn.Write(h[:]) }()
	if _, err := b.Read(); err == nil {
		t.Fatal("accepted oversized header")
	}
	if _, err := a.conn.Write([]byte{0}); err == nil {
		t.Fatal("connection was not closed")
	}
}

func TestCancellationUnblocksSlowPeers(t *testing.T) {
	for _, write := range []bool{false, true} {
		a, _, cancel := pipeFramers(t)
		done := make(chan error, 1)
		go func() {
			if write {
				done <- a.Write(Frame{Version: 1, Kind: KindEvent})
			} else {
				_, err := a.Read()
				done <- err
			}
		}()
		cancel()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("no cancellation error")
			}
		case <-time.After(time.Second):
			t.Fatal("blocked after cancel")
		}
	}
}

func TestRejectWrongHandshake(t *testing.T) {
	a, b, _ := pipeFramers(t)
	go func() {
		_ = a.Write(Frame{Version: 1, Kind: KindRequest, ClientID: "client", Payload: json.RawMessage(`{"max_frame":1024}`)})
	}()
	if _, _, err := b.NegotiateServer(MaxFrameSize); err == nil {
		t.Fatal("accepted request before hello")
	}
}
