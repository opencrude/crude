package network

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

type socksObservation struct {
	version     int
	addressType byte
	host        string
	username    string
	password    string
}

type socksTestProxy struct {
	listener net.Listener
	target   string
	seen     chan socksObservation

	mu          sync.Mutex
	connections map[net.Conn]struct{}
	wg          sync.WaitGroup
}

func TestSOCKSProtocolsDNSAndAuthentication(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Host == "" {
			http.Error(writer, "missing host", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(writer, "via socks")
	}))
	defer target.Close()

	targetURL, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, targetPort, err := net.SplitHostPort(targetURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	requestURL := "http://localhost:" + targetPort + "/resource"

	tests := []struct {
		name         string
		scheme       string
		user         *url.Userinfo
		wantVersion  int
		wantRemote   bool
		wantUsername string
		wantPassword string
	}{
		{
			name:         "SOCKS4 local DNS and user ID",
			scheme:       "socks4",
			user:         url.User("alice"),
			wantVersion:  4,
			wantRemote:   false,
			wantUsername: "alice",
		},
		{
			name:         "SOCKS4a remote DNS and user ID",
			scheme:       "socks4a",
			user:         url.User("alice"),
			wantVersion:  4,
			wantRemote:   true,
			wantUsername: "alice",
		},
		{
			name:         "SOCKS5 local DNS and username password",
			scheme:       "socks5",
			user:         url.UserPassword("alice", "secret"),
			wantVersion:  5,
			wantRemote:   false,
			wantUsername: "alice",
			wantPassword: "secret",
		},
		{
			name:         "SOCKS5h remote DNS and username password",
			scheme:       "socks5h",
			user:         url.UserPassword("alice", "secret"),
			wantVersion:  5,
			wantRemote:   true,
			wantUsername: "alice",
			wantPassword: "secret",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proxy := startSOCKSTestProxy(t, targetURL.Host)
			defer proxy.Close()

			settings := DefaultSettings()
			settings.ProxyMode = ProxyModeCustom
			settings.ProxyURL = formatProxyURL(test.scheme, test.user, proxy.listener.Addr().String())
			client := clientForSettings(t, settings)

			response, err := client.Get(requestURL)
			if err != nil {
				t.Fatalf("GET through %s: %v", test.scheme, err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			client.CloseIdleConnections()
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != "via socks" {
				t.Fatalf("body = %q", body)
			}

			var observation socksObservation
			select {
			case observation = <-proxy.seen:
			case <-time.After(time.Second):
				t.Fatal("SOCKS proxy did not report a handshake")
			}
			if observation.version != test.wantVersion {
				t.Fatalf("SOCKS version = %d, want %d", observation.version, test.wantVersion)
			}
			if observation.username != test.wantUsername || observation.password != test.wantPassword {
				t.Fatalf("credentials = %q/%q, want %q/%q",
					observation.username, observation.password, test.wantUsername, test.wantPassword)
			}
			remote := observation.addressType == 0x03
			if remote != test.wantRemote {
				t.Fatalf("remote DNS = %v (ATYP %#x, host %q), want %v",
					remote, observation.addressType, observation.host, test.wantRemote)
			}
			if test.wantRemote && observation.host != "localhost" {
				t.Fatalf("remote target host = %q, want localhost", observation.host)
			}
			if !test.wantRemote && net.ParseIP(observation.host) == nil {
				t.Fatalf("local DNS target = %q, want an IP literal", observation.host)
			}
		})
	}
}

func TestSOCKSHandshakeCancellationIsPrompt(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connection := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		connection <- conn
		buffer := make([]byte, 3)
		_, _ = io.ReadFull(conn, buffer)
		_, _ = io.Copy(io.Discard, conn)
		conn.Close()
	}()

	settings := DefaultSettings()
	settings.ProxyMode = ProxyModeCustom
	settings.ProxyURL = "socks5h://" + listener.Addr().String()
	roundTripper, err := NewTransport(settings, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripper}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://stalled-socks.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err = client.Do(request)
	if err == nil {
		t.Fatal("request through stalled SOCKS proxy unexpectedly succeeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("SOCKS cancellation took %v", elapsed)
	}
	select {
	case conn := <-connection:
		conn.Close()
	default:
	}
}

func startSOCKSTestProxy(t *testing.T, target string) *socksTestProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &socksTestProxy{
		listener:    listener,
		target:      target,
		seen:        make(chan socksObservation, 4),
		connections: make(map[net.Conn]struct{}),
	}
	proxy.wg.Add(1)
	go proxy.accept()
	return proxy
}

func (p *socksTestProxy) accept() {
	defer p.wg.Done()
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		p.connections[conn] = struct{}{}
		p.mu.Unlock()
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			defer func() {
				p.mu.Lock()
				delete(p.connections, conn)
				p.mu.Unlock()
				conn.Close()
			}()
			p.handle(conn)
		}()
	}
}

func (p *socksTestProxy) handle(conn net.Conn) {
	reader := bufio.NewReader(conn)
	version, err := reader.ReadByte()
	if err != nil {
		return
	}
	switch version {
	case 0x04:
		p.handleSOCKS4(conn, reader)
	case 0x05:
		p.handleSOCKS5(conn, reader)
	}
}

func (p *socksTestProxy) handleSOCKS4(conn net.Conn, reader *bufio.Reader) {
	fixed := make([]byte, 7)
	if _, err := io.ReadFull(reader, fixed); err != nil {
		return
	}
	if fixed[0] != 0x01 {
		return
	}
	username, err := readCString(reader, 256)
	if err != nil {
		return
	}
	addressType := byte(0x01)
	host := net.IP(fixed[3:7]).String()
	if bytes.Equal(fixed[3:7], []byte{0, 0, 0, 1}) {
		addressType = 0x03
		host, err = readCString(reader, 256)
		if err != nil {
			return
		}
	}
	p.seen <- socksObservation{
		version:     4,
		addressType: addressType,
		host:        host,
		username:    username,
	}

	target, err := net.DialTimeout("tcp", p.target, time.Second)
	if err != nil {
		_, _ = conn.Write([]byte{0x00, 0x5b, 0, 0, 0, 0, 0, 0})
		return
	}
	if _, err := conn.Write([]byte{0x00, 0x5a, 0, 0, 0, 0, 0, 0}); err != nil {
		target.Close()
		return
	}
	proxyBidirectional(conn, target)
}

func (p *socksTestProxy) handleSOCKS5(conn net.Conn, reader *bufio.Reader) {
	methodCount, err := reader.ReadByte()
	if err != nil {
		return
	}
	methods := make([]byte, int(methodCount))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return
	}
	method := byte(0x00)
	if bytes.Contains(methods, []byte{0x02}) {
		method = 0x02
	}
	if _, err := conn.Write([]byte{0x05, method}); err != nil {
		return
	}

	observation := socksObservation{version: 5}
	if method == 0x02 {
		authHeader := make([]byte, 2)
		if _, err := io.ReadFull(reader, authHeader); err != nil || authHeader[0] != 0x01 {
			return
		}
		username := make([]byte, int(authHeader[1]))
		if _, err := io.ReadFull(reader, username); err != nil {
			return
		}
		passwordLength, err := reader.ReadByte()
		if err != nil {
			return
		}
		password := make([]byte, int(passwordLength))
		if _, err := io.ReadFull(reader, password); err != nil {
			return
		}
		observation.username = string(username)
		observation.password = string(password)
		if _, err := conn.Write([]byte{0x01, 0x00}); err != nil {
			return
		}
	}

	requestHeader := make([]byte, 4)
	if _, err := io.ReadFull(reader, requestHeader); err != nil {
		return
	}
	if requestHeader[0] != 0x05 || requestHeader[1] != 0x01 {
		return
	}
	observation.addressType = requestHeader[3]
	switch requestHeader[3] {
	case 0x01:
		address := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(reader, address); err != nil {
			return
		}
		observation.host = net.IP(address).String()
	case 0x04:
		address := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(reader, address); err != nil {
			return
		}
		observation.host = net.IP(address).String()
	case 0x03:
		length, err := reader.ReadByte()
		if err != nil {
			return
		}
		address := make([]byte, int(length))
		if _, err := io.ReadFull(reader, address); err != nil {
			return
		}
		observation.host = string(address)
	default:
		return
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		return
	}
	p.seen <- observation

	target, err := net.DialTimeout("tcp", p.target, time.Second)
	if err != nil {
		_, _ = conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		target.Close()
		return
	}
	proxyBidirectional(conn, target)
}

func (p *socksTestProxy) Close() {
	p.listener.Close()
	p.mu.Lock()
	for conn := range p.connections {
		conn.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
}

func readCString(reader *bufio.Reader, maximum int) (string, error) {
	var value []byte
	for len(value) <= maximum {
		char, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		if char == 0 {
			return string(value), nil
		}
		value = append(value, char)
	}
	return "", errors.New("SOCKS string is too long")
}
