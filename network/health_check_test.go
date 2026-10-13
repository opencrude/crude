package network

import (
	"testing"
	"time"
)

// Dead pooled connections are detected by HTTP/2 pings and, for every
// protocol including HTTP/1.1, by TCP keep-alive probes. Both come from the
// configured settings, never transport constants.
func TestTransportHealthChecksUseConfiguredSettings(t *testing.T) {
	settings := DefaultSettings()
	settings.HTTP2PingIntervalSeconds, settings.HTTP2PingTimeoutSeconds = 21, 8
	settings.KeepAliveSeconds, settings.KeepAliveIntervalSeconds, settings.KeepAliveCount = 33, 7, 5
	roundTripper, err := NewTransport(settings, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	transport := roundTripper.(*Transport)
	built, err := transport.transportFor("https", "api.example", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if built.HTTP2 == nil || built.HTTP2.SendPingTimeout != 21*time.Second || built.HTTP2.PingTimeout != 8*time.Second {
		t.Fatalf("HTTP/2 health check = %+v", built.HTTP2)
	}
	keepAlive := transport.dialer.KeepAliveConfig
	if !keepAlive.Enable || keepAlive.Idle != 33*time.Second || keepAlive.Interval != 7*time.Second || keepAlive.Count != 5 {
		t.Fatalf("TCP keep-alive = %+v", keepAlive)
	}
	for _, invalid := range []Settings{{HTTP2PingTimeoutSeconds: 601}, {KeepAliveCount: 101}, {KeepAliveIntervalSeconds: -1}} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("out-of-range health check accepted: %+v", invalid)
		}
	}
}
