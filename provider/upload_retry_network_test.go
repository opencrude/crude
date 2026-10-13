package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/network"
)

// A blackholed HTTP/2 connection remains locally established. Cancelling its
// stream alone does not remove it from the transport's multiplexed pool.
func TestAutomaticRetryEscapesBlackholedHTTP2Connection(t *testing.T) {
	for _, phase := range []string{"upload", "response headers", "download"} {
		t.Run(phase, func(t *testing.T) {
			var blackhole atomic.Bool
			var requests atomic.Int32
			payloads := make(chan []byte, 2)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor != 2 {
					t.Errorf("expected HTTP/2, got %s", r.Proto)
				}
				if requests.Add(1) == 1 {
					if phase != "upload" {
						_, _ = io.Copy(io.Discard, r.Body)
					}
					if phase == "download" {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
						w.(http.Flusher).Flush()
					}
					if phase != "download" {
						blackhole.Store(true)
					}
					<-r.Context().Done()
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("retry upload: %v", err)
					return
				}
				payloads <- body
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			server.EnableHTTP2 = true
			server.StartTLS()
			t.Cleanup(server.Close)
			proxy, dials := blackholeTCPProxy(t, server.Listener.Addr().String(), &blackhole)
			settings := network.DefaultSettings()
			settings.ProxyMode = network.ProxyModeDirect
			settings.TrustedCertificateHost = "127.0.0.1"
			settings.TrustedCertificateSHA256 = fmt.Sprintf("%x", sha256.Sum256(server.Certificate().Raw))
			transport, err := network.NewTransport(settings, 0)
			if err != nil {
				t.Fatal(err)
			}
			httpClient := &http.Client{Transport: transport}
			defer httpClient.CloseIdleConnections()
			p := FromPreset("openai-chat", "key", "https://"+proxy)
			p.SetDialSettings(DialSettings{MaxAttempts: 2})
			var retries atomic.Int32
			client, err := p.Client(WithHTTPClient(httpClient),
				WithProgressTimeouts(200*time.Millisecond, 200*time.Millisecond, 200*time.Millisecond),
				OnRetry(func(_ int, _ time.Duration, err error) {
					var stall *ProgressStallError
					if !errors.As(err, &stall) || stall.Phase != phase {
						t.Errorf("expected %s stall, got %v", phase, err)
					}
					retries.Add(1)
				}))
			if err != nil {
				t.Fatal(err)
			}
			client.dial.BaseBackoff, client.dial.MaxBackoff = 20*time.Millisecond, 20*time.Millisecond
			text := "header stall original payload"
			if phase == "upload" {
				text = string(bytes.Repeat([]byte("original payload "), 1024*1024))
			}
			req := &ChatRequest{Model: "m", Messages: []ChatMessage{NewTextMessage("user", text)}}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			stream, err := client.Stream(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			update, err := stream.Next()
			if phase == "download" {
				if err != nil || update.ContentDelta != "partial" {
					t.Fatalf("accepted prefix missing: update=%+v err=%v", update, err)
				}
				blackhole.Store(true)
				_, err = stream.Next()
				var stall *ProgressStallError
				if !errors.As(err, &stall) || stall.Phase != "download" || requests.Load() != 1 || dials.Load() != 1 || retries.Load() != 0 {
					t.Fatalf("accepted stochastic output replayed: err=%v requests=%d dials=%d retries=%d", err, requests.Load(), dials.Load(), retries.Load())
				}
				return
			}
			if err != nil || update.ContentDelta != "ok" {
				t.Fatalf("automatic cooldown did not produce usable retry: update=%+v err=%v dials=%d requests=%d", update, err, dials.Load(), requests.Load())
			}
			if dials.Load() != 2 || requests.Load() != 2 || retries.Load() != 1 {
				t.Fatalf("dials=%d requests=%d retries=%d", dials.Load(), requests.Load(), retries.Load())
			}
			want, err := client.encodeChatRequest(withStreamFlag(req, true))
			if err != nil {
				t.Fatal(err)
			}
			if got := <-payloads; !bytes.Equal(got, want) {
				t.Fatalf("retry did not upload complete original payload: got=%d want=%d", len(got), len(want))
			}
		})
	}
}

func TestAutomaticUploadRetryCooldownHonorsCancellationAndDeadline(t *testing.T) {
	for _, stop := range []string{"cancel", "deadline"} {
		t.Run(stop, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				<-r.Context().Done()
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
			defer cancel()
			p := FromPreset("openai-chat", "key", server.URL)
			p.SetDialSettings(DialSettings{MaxAttempts: 2})
			client, err := p.Client(
				WithProgressTimeouts(50*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond),
				OnRetry(func(_ int, _ time.Duration, _ error) {
					if stop == "cancel" {
						cancel()
					}
				}))
			if err != nil {
				t.Fatal(err)
			}
			client.dial.BaseBackoff, client.dial.MaxBackoff = time.Second, time.Second
			stream, err := client.Stream(ctx, &ChatRequest{Model: "m", Messages: []ChatMessage{NewTextMessage("user", "original")}})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			_, err = stream.Next()
			want := context.Canceled
			if stop == "deadline" {
				want = context.DeadlineExceeded
			}
			if !errors.Is(err, want) || requests.Load() != 1 {
				t.Fatalf("cooldown ignored stop: err=%v requests=%d", err, requests.Load())
			}
		})
	}
}

// The first TCP connection silently drops traffic once armed; subsequent dials
// reach the same healthy endpoint. Neither side is reset during the cooldown.
func blackholeTCPProxy(t *testing.T, upstream string, blackhole *atomic.Bool) (string, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dials := new(atomic.Int32)
	var mu sync.Mutex
	var connections []net.Conn
	var workers sync.WaitGroup
	acceptDone := make(chan struct{})
	workers.Add(1)
	go func() {
		defer workers.Done()
		defer close(acceptDone)
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			first := dials.Add(1) == 1
			server, err := net.Dial("tcp", upstream)
			if err != nil {
				_ = client.Close()
				return
			}
			mu.Lock()
			connections = append(connections, client, server)
			mu.Unlock()
			for _, pair := range [][2]net.Conn{{client, server}, {server, client}} {
				workers.Add(1)
				go func(source, destination net.Conn) {
					defer workers.Done()
					buffer := make([]byte, 32*1024)
					for {
						n, err := source.Read(buffer)
						if n > 0 && (!first || !blackhole.Load()) {
							if _, writeErr := destination.Write(buffer[:n]); writeErr != nil {
								return
							}
						}
						if err != nil {
							return
						}
					}
				}(pair[0], pair[1])
			}
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-acceptDone
		mu.Lock()
		for _, conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	return listener.Addr().String(), dials
}
