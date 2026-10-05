package network

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const maxProxyConnectHeaderBytes = 64 << 10

type noProxyMatcher struct {
	all     bool
	entries []noProxyEntry
}

type noProxyEntry struct {
	host           string
	port           string
	ip             netip.Addr
	prefix         netip.Prefix
	isIP           bool
	isPrefix       bool
	subdomainsOnly bool
}

func compileNoProxy(value string) (noProxyMatcher, error) {
	var matcher noProxyMatcher
	if strings.TrimSpace(value) == "" {
		return matcher, nil
	}
	items := strings.Split(value, ",")
	if len(items) > maxNoProxyItems {
		return noProxyMatcher{}, fmt.Errorf("contains more than %d entries", maxNoProxyItems)
	}
	for _, rawItem := range items {
		item := strings.TrimSpace(rawItem)
		if item == "" {
			continue
		}
		if item == "*" {
			matcher.all = true
			matcher.entries = nil
			return matcher, nil
		}
		if prefix, err := netip.ParsePrefix(item); err == nil {
			matcher.entries = append(matcher.entries, noProxyEntry{
				prefix:   prefix.Masked(),
				isPrefix: true,
			})
			continue
		}
		if strings.ContainsAny(item, "\x00\r\n/@") {
			return noProxyMatcher{}, errors.New("entry contains invalid characters")
		}

		host, port, err := splitNoProxyHostPort(item)
		if err != nil {
			return noProxyMatcher{}, err
		}
		if port != "" {
			port, err = parsePort(port)
			if err != nil {
				return noProxyMatcher{}, err
			}
		}
		if ip, err := netip.ParseAddr(host); err == nil {
			matcher.entries = append(matcher.entries, noProxyEntry{
				ip:   ip.Unmap(),
				port: port,
				isIP: true,
			})
			continue
		}

		subdomainsOnly := false
		if strings.HasPrefix(host, "*.") {
			host = host[2:]
			subdomainsOnly = true
		} else if strings.HasPrefix(host, ".") {
			host = host[1:]
			subdomainsOnly = true
		}
		host, err = canonicalHost(host)
		if err != nil {
			return noProxyMatcher{}, fmt.Errorf("entry hostname is invalid: %w", err)
		}
		matcher.entries = append(matcher.entries, noProxyEntry{
			host:           host,
			port:           port,
			subdomainsOnly: subdomainsOnly,
		})
	}
	return matcher, nil
}

func splitNoProxyHostPort(value string) (string, string, error) {
	if ip, err := netip.ParseAddr(value); err == nil {
		return ip.String(), "", nil
	}
	if strings.HasPrefix(value, "[") {
		host, port, err := net.SplitHostPort(value)
		if err != nil {
			return "", "", errors.New("bracketed no_proxy entry must include a valid port")
		}
		if port == "" {
			return "", "", errors.New("no_proxy entry port is empty")
		}
		return strings.Trim(host, "[]"), port, nil
	}
	if strings.Count(value, ":") == 1 {
		host, port, err := net.SplitHostPort(value)
		if err == nil {
			if host == "" {
				return "", "", errors.New("no_proxy entry hostname is empty")
			}
			if port == "" {
				return "", "", errors.New("no_proxy entry port is empty")
			}
			return host, port, nil
		}
	}
	if strings.Contains(value, ":") {
		return "", "", errors.New("IPv6 no_proxy entries with a port must use brackets")
	}
	return value, "", nil
}

func (m noProxyMatcher) match(requestURL *url.URL) bool {
	if m.all {
		return true
	}
	host, err := canonicalHost(requestURL.Hostname())
	if err != nil {
		return false
	}
	port := requestURL.Port()
	if port == "" {
		switch strings.ToLower(requestURL.Scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	ip, ipErr := netip.ParseAddr(strings.TrimSuffix(host, "."))
	if ipErr == nil {
		ip = ip.Unmap()
	}
	for _, entry := range m.entries {
		if entry.port != "" && entry.port != port {
			continue
		}
		switch {
		case entry.isPrefix && ipErr == nil && entry.prefix.Contains(ip):
			return true
		case entry.isIP && ipErr == nil && entry.ip == ip:
			return true
		case entry.host != "" && ipErr != nil:
			if host == entry.host && !entry.subdomainsOnly {
				return true
			}
			if strings.HasSuffix(host, "."+entry.host) {
				return true
			}
		}
	}
	return false
}

type proxyPolicy struct {
	mode               string
	httpProxy          *url.URL
	httpsProxy         *url.URL
	noProxy            noProxyMatcher
	bypassLocal        bool
	refuseCGIHTTPProxy bool
}

func newProxyPolicy(settings Settings) (*proxyPolicy, error) {
	policy := &proxyPolicy{mode: settings.ProxyMode}
	switch settings.ProxyMode {
	case ProxyModeDirect:
		return policy, nil
	case ProxyModeCustom:
		proxyURL, err := parseProxyURL(settings.ProxyURL, true)
		if err != nil {
			return nil, fmt.Errorf("custom proxy URL is invalid: %w", err)
		}
		noProxy, err := compileNoProxy(settings.NoProxy)
		if err != nil {
			return nil, fmt.Errorf("custom no_proxy is invalid: %w", err)
		}
		policy.httpProxy = proxyURL
		policy.httpsProxy = proxyURL
		policy.noProxy = noProxy
		return policy, nil
	case ProxyModeEnvironment:
		httpValue := firstEnvironment("HTTP_PROXY", "http_proxy")
		httpsValue := firstEnvironment("HTTPS_PROXY", "https_proxy")
		noProxyValue := settings.NoProxy
		if noProxyValue == "" {
			noProxyValue = firstEnvironment("NO_PROXY", "no_proxy")
		}
		var err error
		if httpValue != "" {
			policy.httpProxy, err = parseProxyURL(httpValue, true)
			if err != nil {
				return nil, errors.New("environment HTTP proxy is invalid")
			}
		}
		if httpsValue != "" {
			policy.httpsProxy, err = parseProxyURL(httpsValue, true)
			if err != nil {
				return nil, errors.New("environment HTTPS proxy is invalid")
			}
		}
		policy.noProxy, err = compileNoProxy(noProxyValue)
		if err != nil {
			return nil, fmt.Errorf("environment no_proxy is invalid: %w", err)
		}
		policy.bypassLocal = true
		policy.refuseCGIHTTPProxy = os.Getenv("REQUEST_METHOD") != "" && policy.httpProxy != nil
		return policy, nil
	default:
		return nil, errors.New("unsupported proxy mode")
	}
}

func firstEnvironment(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

func (p *proxyPolicy) proxyForURL(requestURL *url.URL) (*url.URL, error) {
	if p.mode == ProxyModeDirect {
		return nil, nil
	}
	if p.bypassLocal {
		host := strings.TrimSuffix(strings.ToLower(requestURL.Hostname()), ".")
		if host == "localhost" {
			return nil, nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil, nil
		}
	}
	if p.noProxy.match(requestURL) {
		return nil, nil
	}
	switch strings.ToLower(requestURL.Scheme) {
	case "http":
		if p.refuseCGIHTTPProxy {
			return nil, errors.New("refusing environment HTTP proxy in a CGI process")
		}
		return p.httpProxy, nil
	case "https":
		return p.httpsProxy, nil
	default:
		return nil, errors.New("network transport supports only HTTP and HTTPS requests")
	}
}

func parseProxyURL(value string, allowBare bool) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("proxy URL is empty")
	}
	if len(value) > maxProxyURLBytes {
		return nil, errors.New("proxy URL is too long")
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return nil, errors.New("proxy URL contains invalid characters")
	}
	candidate := value
	if allowBare && !strings.Contains(candidate, "://") {
		candidate = "http://" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil {
		return nil, errors.New("proxy URL cannot be parsed")
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "http", "https", "socks4", "socks4a", "socks5", "socks5h":
	default:
		return nil, errors.New("proxy URL scheme is not supported")
	}
	if parsed.Opaque != "" || parsed.Host == "" {
		return nil, errors.New("proxy URL must contain a hostname")
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return nil, errors.New("proxy URL port is empty")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("proxy URL must not contain a path, query, or fragment")
	}
	host, err := canonicalHost(parsed.Hostname())
	if err != nil {
		return nil, errors.New("proxy URL hostname is invalid")
	}
	port := parsed.Port()
	if port == "" {
		switch scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			port = "1080"
		}
	} else {
		port, err = parsePort(port)
		if err != nil {
			return nil, errors.New("proxy URL port is invalid")
		}
	}

	if parsed.User != nil {
		username := parsed.User.Username()
		password, hasPassword := parsed.User.Password()
		if strings.ContainsAny(username, "\x00\r\n") || strings.ContainsAny(password, "\x00\r\n") {
			return nil, errors.New("proxy credentials contain invalid characters")
		}
		switch scheme {
		case "socks4", "socks4a":
			if hasPassword {
				return nil, errors.New("SOCKS4 proxies support a username but not a password")
			}
			if len(username) > 255 {
				return nil, errors.New("SOCKS4 proxy username is too long")
			}
		case "socks5", "socks5h":
			if username == "" {
				return nil, errors.New("SOCKS5 proxy username is empty")
			}
			if len(username) > 255 || len(password) > 255 {
				return nil, errors.New("SOCKS5 proxy credentials are too long")
			}
		}
	}

	parsed.Scheme = scheme
	parsed.Host = hostPort(host, port)
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func (t *Transport) dialProxyTunnel(ctx context.Context, network, address string, proxyURL *url.URL) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("proxy does not support network %q", network)
	}
	ctx, cancel := context.WithTimeout(ctx, t.connectTimeout)
	defer cancel()

	switch proxyURL.Scheme {
	case "http", "https":
		return t.dialHTTPConnect(ctx, address, proxyURL)
	case "socks4":
		return t.dialSOCKS4(ctx, address, proxyURL, false)
	case "socks4a":
		return t.dialSOCKS4(ctx, address, proxyURL, true)
	case "socks5":
		return t.dialSOCKS5(ctx, address, proxyURL, false)
	case "socks5h":
		return t.dialSOCKS5(ctx, address, proxyURL, true)
	default:
		return nil, errors.New("unsupported proxy protocol")
	}
}

func (t *Transport) dialHTTPConnect(ctx context.Context, address string, proxyURL *url.URL) (net.Conn, error) {
	conn, err := t.dialer.DialContext(ctx, "tcp", proxyURL.Host)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			conn.Close()
		}
	}()

	if proxyURL.Scheme == "https" {
		tlsConn := tlsClientForProxy(conn, t.proxyTLSConfig(proxyURL.Hostname()))
		if err := runConnHandshake(ctx, tlsConn, func() error {
			return tlsConn.HandshakeContext(ctx)
		}); err != nil {
			return nil, fmt.Errorf("HTTPS proxy TLS handshake failed: %w", err)
		}
		conn = tlsConn
	}

	var reader *bufio.Reader
	err = runConnHandshake(ctx, conn, func() error {
		var request bytes.Buffer
		fmt.Fprintf(&request, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", address, address)
		if authorization := proxyAuthorization(proxyURL); authorization != "" {
			fmt.Fprintf(&request, "Proxy-Authorization: %s\r\n", authorization)
		}
		request.WriteString("Proxy-Connection: Keep-Alive\r\n\r\n")
		if err := writeAll(conn, request.Bytes()); err != nil {
			return err
		}

		reader = bufio.NewReaderSize(conn, 4096)
		header, err := readProxyConnectHeader(reader)
		if err != nil {
			return err
		}
		statusCode, err := proxyConnectStatus(header)
		if err != nil {
			return err
		}
		if statusCode < 200 || statusCode > 299 {
			return fmt.Errorf("proxy CONNECT returned HTTP %d", statusCode)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	keep = true
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

func (t *Transport) dialSOCKS4(ctx context.Context, address string, proxyURL *url.URL, remoteDNS bool) (net.Conn, error) {
	targetHost, targetPort, err := splitTargetAddress(address)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(targetHost); ip != nil {
		targetIP := ip.To4()
		if targetIP == nil {
			return nil, errors.New("SOCKS4 does not support IPv6 targets")
		}
		return t.dialSOCKS4Target(ctx, targetIP, "", targetPort, proxyURL)
	}
	if remoteDNS {
		targetDomain, err := canonicalHost(targetHost)
		if err != nil {
			return nil, errors.New("SOCKS4a target hostname is invalid")
		}
		return t.dialSOCKS4Target(ctx, net.IPv4(0, 0, 0, 1), targetDomain, targetPort, proxyURL)
	}

	targetIPs, err := resolveIPv4s(ctx, targetHost)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, targetIP := range targetIPs {
		conn, dialErr := t.dialSOCKS4Target(ctx, targetIP, "", targetPort, proxyURL)
		if dialErr == nil {
			return conn, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		lastErr = dialErr
	}
	return nil, fmt.Errorf("SOCKS4 proxy could not connect to any locally resolved address: %w", lastErr)
}

func (t *Transport) dialSOCKS4Target(
	ctx context.Context,
	targetIP net.IP,
	targetDomain string,
	targetPort int,
	proxyURL *url.URL,
) (net.Conn, error) {
	conn, err := t.dialer.DialContext(ctx, "tcp", proxyURL.Host)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			conn.Close()
		}
	}()

	err = runConnHandshake(ctx, conn, func() error {
		request := []byte{0x04, 0x01, byte(targetPort >> 8), byte(targetPort)}
		request = append(request, targetIP.To4()...)
		if proxyURL.User != nil {
			request = append(request, proxyURL.User.Username()...)
		}
		request = append(request, 0x00)
		if targetDomain != "" {
			request = append(request, targetDomain...)
			request = append(request, 0x00)
		}
		if err := writeAll(conn, request); err != nil {
			return err
		}
		response := make([]byte, 8)
		if _, err := io.ReadFull(conn, response); err != nil {
			return err
		}
		if response[1] != 0x5a {
			return fmt.Errorf("SOCKS4 proxy rejected connection with code %d", response[1])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	keep = true
	return conn, nil
}

func (t *Transport) dialSOCKS5(ctx context.Context, address string, proxyURL *url.URL, remoteDNS bool) (net.Conn, error) {
	targetHost, targetPort, err := splitTargetAddress(address)
	if err != nil {
		return nil, err
	}
	targets := []string{targetHost}
	resolvedLocally := !remoteDNS && net.ParseIP(targetHost) == nil
	if resolvedLocally {
		targets, err = resolveIPs(ctx, targetHost)
		if err != nil {
			return nil, err
		}
	}

	var lastErr error
	for _, target := range targets {
		conn, dialErr := t.dialSOCKS5Target(ctx, target, targetPort, proxyURL)
		if dialErr == nil {
			return conn, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		lastErr = dialErr
	}
	if !resolvedLocally {
		return nil, lastErr
	}
	return nil, fmt.Errorf("SOCKS5 proxy could not connect to any locally resolved address: %w", lastErr)
}

func (t *Transport) dialSOCKS5Target(
	ctx context.Context,
	targetHost string,
	targetPort int,
	proxyURL *url.URL,
) (net.Conn, error) {
	conn, err := t.dialer.DialContext(ctx, "tcp", proxyURL.Host)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			conn.Close()
		}
	}()

	err = runConnHandshake(ctx, conn, func() error {
		hasCredentials := proxyURL.User != nil
		method := byte(0x00)
		if hasCredentials {
			method = 0x02
		}
		if err := writeAll(conn, []byte{0x05, 0x01, method}); err != nil {
			return err
		}
		methodReply := make([]byte, 2)
		if _, err := io.ReadFull(conn, methodReply); err != nil {
			return err
		}
		if methodReply[0] != 0x05 || methodReply[1] != method {
			return errors.New("SOCKS5 proxy did not accept the configured authentication method")
		}
		if hasCredentials {
			username := proxyURL.User.Username()
			password, _ := proxyURL.User.Password()
			authRequest := []byte{0x01, byte(len(username))}
			authRequest = append(authRequest, username...)
			authRequest = append(authRequest, byte(len(password)))
			authRequest = append(authRequest, password...)
			if err := writeAll(conn, authRequest); err != nil {
				return err
			}
			authReply := make([]byte, 2)
			if _, err := io.ReadFull(conn, authReply); err != nil {
				return err
			}
			if authReply[0] != 0x01 || authReply[1] != 0x00 {
				return errors.New("SOCKS5 proxy authentication failed")
			}
		}

		connectRequest := []byte{0x05, 0x01, 0x00}
		ip := net.ParseIP(targetHost)
		switch {
		case ip != nil && ip.To4() != nil:
			connectRequest = append(connectRequest, 0x01)
			connectRequest = append(connectRequest, ip.To4()...)
		case ip != nil && ip.To16() != nil:
			connectRequest = append(connectRequest, 0x04)
			connectRequest = append(connectRequest, ip.To16()...)
		default:
			host, err := canonicalHost(targetHost)
			if err != nil || len(host) > 255 {
				return errors.New("SOCKS5 target hostname is invalid")
			}
			connectRequest = append(connectRequest, 0x03, byte(len(host)))
			connectRequest = append(connectRequest, host...)
		}
		connectRequest = append(connectRequest, byte(targetPort>>8), byte(targetPort))
		if err := writeAll(conn, connectRequest); err != nil {
			return err
		}
		if err := readSOCKS5Reply(conn); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	keep = true
	return conn, nil
}

func splitTargetAddress(address string) (string, int, error) {
	host, portValue, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, errors.New("proxy target address is invalid")
	}
	port, err := strconv.Atoi(portValue)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("proxy target port is invalid")
	}
	if host == "" {
		return "", 0, errors.New("proxy target hostname is empty")
	}
	return host, port, nil
}

func resolveIPv4s(ctx context.Context, host string) ([]net.IP, error) {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve SOCKS4 target: %w", err)
	}
	resolved := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		if ip := address.IP.To4(); ip != nil {
			resolved = append(resolved, ip)
		}
	}
	if len(resolved) == 0 {
		return nil, errors.New("SOCKS4 target has no IPv4 address")
	}
	return resolved, nil
}

func resolveIPs(ctx context.Context, host string) ([]string, error) {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve SOCKS5 target: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("SOCKS5 target has no IP address")
	}
	resolved := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if ip := address.IP.To4(); ip != nil {
			resolved = append(resolved, ip.String())
		}
	}
	for _, address := range addresses {
		if address.IP.To4() == nil {
			resolved = append(resolved, address.IP.String())
		}
	}
	return resolved, nil
}

func readSOCKS5Reply(reader io.Reader) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	if header[0] != 0x05 {
		return errors.New("SOCKS5 proxy returned an invalid protocol version")
	}
	if header[1] != 0x00 {
		return fmt.Errorf("SOCKS5 proxy rejected connection with code %d", header[1])
	}
	var addressBytes int
	switch header[3] {
	case 0x01:
		addressBytes = net.IPv4len
	case 0x04:
		addressBytes = net.IPv6len
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(reader, length); err != nil {
			return err
		}
		addressBytes = int(length[0])
	default:
		return errors.New("SOCKS5 proxy returned an invalid address type")
	}
	discard := make([]byte, addressBytes+2)
	_, err := io.ReadFull(reader, discard)
	return err
}

func runConnHandshake(ctx context.Context, conn net.Conn, handshake func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return err
		}
	}
	done := make(chan struct{})
	cancelled := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-done:
		}
		close(cancelled)
	}()

	err := handshake()
	close(done)
	<-cancelled
	clearErr := conn.SetDeadline(time.Time{})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		return err
	}
	return clearErr
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func readProxyConnectHeader(reader *bufio.Reader) ([]byte, error) {
	var header bytes.Buffer
	for header.Len() <= maxProxyConnectHeaderBytes {
		fragment, err := reader.ReadSlice('\n')
		if header.Len()+len(fragment) > maxProxyConnectHeaderBytes {
			return nil, errors.New("proxy CONNECT response headers are too large")
		}
		header.Write(fragment)
		if bytes.HasSuffix(header.Bytes(), []byte("\r\n\r\n")) {
			return header.Bytes(), nil
		}
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
	return nil, errors.New("proxy CONNECT response headers are too large")
}

func proxyConnectStatus(header []byte) (int, error) {
	lineEnd := bytes.Index(header, []byte("\r\n"))
	if lineEnd < 0 {
		return 0, errors.New("proxy CONNECT response is malformed")
	}
	fields := strings.Fields(string(header[:lineEnd]))
	if len(fields) < 2 || (fields[0] != "HTTP/1.0" && fields[0] != "HTTP/1.1") {
		return 0, errors.New("proxy CONNECT response has an invalid status line")
	}
	statusCode, err := strconv.Atoi(fields[1])
	if err != nil || statusCode < 100 || statusCode > 999 {
		return 0, errors.New("proxy CONNECT response has an invalid status code")
	}
	return statusCode, nil
}

func proxyAuthorization(proxyURL *url.URL) string {
	if proxyURL.User == nil {
		return ""
	}
	username := proxyURL.User.Username()
	password, _ := proxyURL.User.Password()
	token := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	return "Basic " + token
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(data []byte) (int, error) {
	return c.reader.Read(data)
}
