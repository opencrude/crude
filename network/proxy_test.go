package network

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPForwardProxyWithCredentials(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
		http.Error(writer, "request should not reach target", http.StatusInternalServerError)
	}))
	defer target.Close()

	authorization := make(chan string, 1)
	requestURI := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization <- request.Header.Get("Proxy-Authorization")
		requestURI <- request.RequestURI
		writer.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(writer, "proxied")
	}))
	defer proxy.Close()

	proxyURL := strings.Replace(proxy.URL, "http://", "http://proxy-user:proxy-pass@", 1)
	settings := DefaultSettings()
	settings.ProxyMode = ProxyModeCustom
	settings.ProxyURL = proxyURL
	client := clientForSettings(t, settings)

	response, err := client.Get(target.URL + "/resource?q=1")
	if err != nil {
		t.Fatalf("GET through HTTP proxy: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "proxied" {
		t.Fatalf("body = %q", body)
	}
	if got := <-authorization; got != basicProxyHeader("proxy-user", "proxy-pass") {
		t.Fatalf("Proxy-Authorization = %q", got)
	}
	if got := <-requestURI; !strings.HasPrefix(got, target.URL+"/resource") {
		t.Fatalf("proxy RequestURI = %q, want absolute target URL", got)
	}
	if targetCalls.Load() != 0 {
		t.Fatalf("target received %d requests; proxy should have answered directly", targetCalls.Load())
	}
}

func TestHTTPSForwardProxyUsesCustomRoots(t *testing.T) {
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !request.URL.IsAbs() {
			http.Error(writer, "proxy request URL was not absolute", http.StatusBadRequest)
			return
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(writer, "secure proxy")
	}))
	defer proxy.Close()

	settings := DefaultSettings()
	settings.ProxyMode = ProxyModeCustom
	settings.ProxyURL = proxy.URL
	settings.CAFile = writeCertificateFile(t, proxy.Certificate())
	client := clientForSettings(t, settings)

	response, err := client.Get("http://unreachable.invalid/resource")
	if err != nil {
		t.Fatalf("GET through HTTPS proxy: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "secure proxy" {
		t.Fatalf("body = %q", body)
	}
}

func TestHTTPConnectProxyTunnelsHTTPSAndAuthenticates(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "through tunnel")
	}))
	defer target.Close()

	authorization := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodConnect {
			http.Error(writer, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		authorization <- request.Header.Get("Proxy-Authorization")
		tunnelHTTPConnect(t, writer, request, request.Host)
	}))
	defer proxy.Close()

	settings := DefaultSettings()
	settings.ProxyMode = ProxyModeCustom
	settings.ProxyURL = strings.Replace(proxy.URL, "http://", "http://tunnel-user:tunnel-pass@", 1)
	settings.CAFile = writeCertificateFile(t, target.Certificate())
	client := clientForSettings(t, settings)

	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("GET through CONNECT proxy: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "through tunnel" {
		t.Fatalf("body = %q", body)
	}
	if got := <-authorization; got != basicProxyHeader("tunnel-user", "tunnel-pass") {
		t.Fatalf("Proxy-Authorization = %q", got)
	}
}

func TestProxyFailureNeverFallsBackDirectAndRedactsCredentials(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyAddress := listener.Addr().String()
	listener.Close()

	settings := DefaultSettings()
	settings.ProxyMode = ProxyModeCustom
	settings.ProxyURL = "http://sensitive-user:sensitive-password@" + proxyAddress
	client := clientForSettings(t, settings)

	_, err = client.Get(target.URL)
	if err == nil {
		t.Fatal("GET unexpectedly succeeded without its configured proxy")
	}
	if targetCalls.Load() != 0 {
		t.Fatalf("target received %d direct requests", targetCalls.Load())
	}
	if strings.Contains(err.Error(), "sensitive-user") || strings.Contains(err.Error(), "sensitive-password") {
		t.Fatalf("proxy error leaked credentials: %v", err)
	}
}

func TestCustomNoProxyExplicitlyBypassesProxy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "direct")
	}))
	defer target.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unreachableProxy := listener.Addr().String()
	listener.Close()

	targetURL, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	settings := DefaultSettings()
	settings.ProxyMode = ProxyModeCustom
	settings.ProxyURL = "http://" + unreachableProxy
	settings.NoProxy = targetURL.Hostname()
	client := clientForSettings(t, settings)

	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("GET with no_proxy bypass: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "direct" {
		t.Fatalf("body = %q", body)
	}
}

func TestEnvironmentProxyIsSnapshotted(t *testing.T) {
	proxyCalls := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		proxyCalls <- request.Host
		_, _ = io.WriteString(writer, "environment proxy")
	}))
	defer proxy.Close()

	t.Setenv("http_proxy", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	t.Setenv("REQUEST_METHOD", "")
	// Windows environment names are case-insensitive: set the desired value
	// after clearing both spellings, rather than erasing it via http_proxy.
	t.Setenv("HTTP_PROXY", proxy.URL)

	settings := DefaultSettings()
	settings.ProxyMode = ProxyModeEnvironment
	client := clientForSettings(t, settings)
	// Mutating the process environment after construction must not affect this
	// transport's immutable routing snapshot.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")

	response, err := client.Get("http://environment-proxy.invalid/resource")
	if err != nil {
		t.Fatalf("GET through environment proxy: %v", err)
	}
	response.Body.Close()
	if got := <-proxyCalls; got != "environment-proxy.invalid" {
		t.Fatalf("proxy Host = %q", got)
	}
}

func TestProxyConnectCancellationIsPrompt(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
			_, _ = io.Copy(io.Discard, conn)
			conn.Close()
		}
	}()

	settings := DefaultSettings()
	settings.ProxyMode = ProxyModeCustom
	settings.ProxyURL = "http://cancel-user:cancel-password@" + listener.Addr().String()
	roundTripper, err := NewTransport(settings, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripper}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://stalled-proxy.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err = client.Do(request)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("request through stalled proxy unexpectedly succeeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request error = %v, want context deadline exceeded", err)
	}
	if strings.Contains(err.Error(), "cancel-user") || strings.Contains(err.Error(), "cancel-password") {
		t.Fatalf("cancellation error leaked proxy credentials: %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("proxy cancellation took %v", elapsed)
	}
	select {
	case conn := <-accepted:
		conn.Close()
	default:
	}
}

func TestTransportSupportsCloseIdleConnections(t *testing.T) {
	settings := DefaultSettings()
	settings.ProxyMode = ProxyModeDirect
	roundTripper, err := NewTransport(settings, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	closer, ok := roundTripper.(interface{ CloseIdleConnections() })
	if !ok {
		t.Fatalf("%T does not implement CloseIdleConnections", roundTripper)
	}
	closer.CloseIdleConnections()
}

func clientForSettings(t *testing.T, settings Settings) *http.Client {
	t.Helper()
	roundTripper, err := NewTransport(settings, 2*time.Second)
	if err != nil {
		t.Fatalf("NewTransport() error = %v", err)
	}
	client := &http.Client{
		Transport: roundTripper,
		Timeout:   5 * time.Second,
	}
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func basicProxyHeader(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

func writeCertificateFile(t *testing.T, certificate *x509.Certificate) string {
	t.Helper()
	path := t.TempDir() + "/ca.pem"
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func tunnelHTTPConnect(t *testing.T, writer http.ResponseWriter, request *http.Request, targetAddress string) {
	t.Helper()
	target, err := net.DialTimeout("tcp", targetAddress, 2*time.Second)
	if err != nil {
		http.Error(writer, "dial target", http.StatusBadGateway)
		return
	}
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		target.Close()
		http.Error(writer, "hijacking unavailable", http.StatusInternalServerError)
		return
	}
	client, buffer, err := hijacker.Hijack()
	if err != nil {
		target.Close()
		return
	}
	if _, err := buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		client.Close()
		target.Close()
		return
	}
	if err := buffer.Flush(); err != nil {
		client.Close()
		target.Close()
		return
	}
	go proxyBidirectional(client, target)
}

func proxyBidirectional(left, right net.Conn) {
	var closed atomic.Bool
	closeBoth := func() {
		if closed.CompareAndSwap(false, true) {
			left.Close()
			right.Close()
		}
	}
	go func() {
		_, _ = io.Copy(left, right)
		closeBoth()
	}()
	_, _ = io.Copy(right, left)
	closeBoth()
}

func formatProxyURL(scheme string, user *url.Userinfo, address string) string {
	proxyURL := &url.URL{Scheme: scheme, User: user, Host: address}
	return proxyURL.String()
}
