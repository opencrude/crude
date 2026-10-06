package network

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestInvalidStartupPolicyFailsClosedWithoutContactingServer(t *testing.T) {
	previous := Current()
	defer func() {
		if err := ConfigureDefault(previous); err != nil {
			t.Fatal(err)
		}
	}()

	invalid := DefaultSettings()
	invalid.ProxyMode = ProxyModeCustom
	invalid.ProxyURL = "http://user:private@[invalid"
	if err := ConfigureStartupDefault(invalid); err == nil {
		t.Fatal("invalid startup policy was accepted")
	}

	var dials atomic.Int32
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			dials.Add(1)
			_ = conn.Close()
		}
	}()

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+listener.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewClient(time.Second).Do(request)
	if err == nil {
		t.Fatal("request used a previous/default transport after invalid startup policy")
	}
	time.Sleep(20 * time.Millisecond)
	if dials.Load() != 0 {
		t.Fatal("fail-closed startup transport contacted the destination")
	}
}

func TestValidConfigureRepairsFailedStartupPolicy(t *testing.T) {
	previous := Current()
	defer func() { _ = ConfigureDefault(previous) }()
	invalid := DefaultSettings()
	invalid.ConnectTimeoutSeconds = -1
	if err := ConfigureStartupDefault(invalid); err == nil {
		t.Fatal("invalid startup policy was accepted")
	}
	valid := DefaultSettings()
	valid.ProxyMode = ProxyModeDirect
	if err := ConfigureDefault(valid); err != nil {
		t.Fatal(err)
	}
	if _, err := NewTransport(Current(), 0); err != nil {
		t.Fatalf("valid save did not repair startup policy: %v", err)
	}
}
