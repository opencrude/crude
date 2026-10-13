package network

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type tlsProfile uint8

const (
	tlsProfileClientCertificate tlsProfile = 1 << iota
	tlsProfilePinnedCertificate
)

type transportKey struct {
	requestScheme string
	proxyURL      string
	profile       tlsProfile
	specialHost   string
}

// Transport is a shared, concurrency-safe HTTP transport that chooses proxy
// and TLS policy for each request endpoint. Its internal connection pools are
// isolated so target client identities and certificate pins are never reused
// for a proxy or unrelated origin.
type Transport struct {
	settings       Settings
	connectTimeout time.Duration
	policy         *proxyPolicy
	material       tlsMaterial
	dialer         net.Dialer

	mu         sync.Mutex
	transports map[transportKey]*http.Transport
}

// NewTransport builds a transport from an immutable settings snapshot.
// connectTimeout overrides Settings.ConnectTimeoutSeconds when positive.
func NewTransport(settings Settings, connectTimeout time.Duration) (http.RoundTripper, error) {
	if err := currentStartupFailure(); err != nil {
		return nil, err
	}
	settings = Normalize(settings)
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	if connectTimeout < 0 {
		return nil, errors.New("connect timeout must not be negative")
	}
	if connectTimeout == 0 {
		connectTimeout = time.Duration(settings.ConnectTimeoutSeconds) * time.Second
	}
	if connectTimeout > 10*time.Minute {
		return nil, errors.New("connect timeout must not exceed 10 minutes")
	}

	material, err := loadTLSMaterial(settings)
	if err != nil {
		return nil, err
	}
	policy, err := newProxyPolicy(settings)
	if err != nil {
		return nil, err
	}
	transport := &Transport{
		settings:       settings,
		connectTimeout: connectTimeout,
		policy:         policy,
		material:       material,
		transports:     make(map[transportKey]*http.Transport),
	}
	transport.dialer = net.Dialer{
		Timeout: connectTimeout,
		KeepAliveConfig: net.KeepAliveConfig{
			Enable:   true,
			Idle:     time.Duration(settings.KeepAliveSeconds) * time.Second,
			Interval: time.Duration(settings.KeepAliveIntervalSeconds) * time.Second,
			Count:    settings.KeepAliveCount,
		},
	}
	return transport, nil
}

// NewClient captures Current at construction time. Invalid or unreadable
// certificate files produce a client whose requests fail closed.
func NewClient(timeout time.Duration) *http.Client {
	if timeout < 0 {
		return &http.Client{
			Transport: &errorRoundTripper{err: errors.New("HTTP client timeout must not be negative")},
		}
	}
	roundTripper, err := NewTransport(Current(), 0)
	if err != nil {
		roundTripper = &errorRoundTripper{err: err}
	}
	return &http.Client{
		Transport: roundTripper,
		Timeout:   timeout,
	}
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil {
		return nil, errors.New("network transport received a nil request")
	}
	scheme := strings.ToLower(request.URL.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, errors.New("network transport supports only HTTP and HTTPS requests")
	}
	host, err := canonicalHost(request.URL.Hostname())
	if err != nil {
		return nil, fmt.Errorf("request hostname is invalid: %w", err)
	}
	proxyURL, err := t.policy.proxyForURL(request.URL)
	if err != nil {
		return nil, err
	}
	profile := t.tlsProfileFor(scheme, host)
	underlying, err := t.transportFor(scheme, host, profile, proxyURL)
	if err != nil {
		return nil, err
	}
	response, err := underlying.RoundTrip(request)
	if err != nil && proxyURL != nil {
		return nil, redactProxyError(err, proxyURL)
	}
	return response, err
}

// CloseIdleConnections closes every currently pooled direct and proxied
// connection owned by this wrapper.
func (t *Transport) CloseIdleConnections() {
	t.mu.Lock()
	transports := make([]*http.Transport, 0, len(t.transports))
	for _, transport := range t.transports {
		transports = append(transports, transport)
	}
	t.mu.Unlock()
	for _, transport := range transports {
		transport.CloseIdleConnections()
	}
}

func (t *Transport) tlsProfileFor(scheme, host string) tlsProfile {
	if scheme != "https" {
		return 0
	}
	var profile tlsProfile
	if host == t.settings.ClientCertificateHost {
		profile |= tlsProfileClientCertificate
	}
	if host == t.settings.TrustedCertificateHost {
		profile |= tlsProfilePinnedCertificate
	}
	return profile
}

func (t *Transport) transportFor(scheme, host string, profile tlsProfile, proxyURL *url.URL) (*http.Transport, error) {
	key := transportKey{
		requestScheme: scheme,
		profile:       profile,
	}
	if profile != 0 {
		key.specialHost = host
	}
	if proxyURL != nil {
		key.proxyURL = proxyURL.String()
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if existing := t.transports[key]; existing != nil {
		return existing, nil
	}
	created, err := t.buildTransport(scheme, host, profile, proxyURL)
	if err != nil {
		return nil, err
	}
	t.transports[key] = created
	return created, nil
}

func (t *Transport) buildTransport(scheme, host string, profile tlsProfile, proxyURL *url.URL) (*http.Transport, error) {
	tlsConfig, err := t.originTLSConfig(host, profile)
	if err != nil {
		return nil, err
	}
	maxIdleConnections := t.settings.MaxIdleConnsPerHost * 4
	if maxIdleConnections < 100 {
		maxIdleConnections = 100
	}
	transport := &http.Transport{
		DialContext:           t.dialer.DialContext,
		ForceAttemptHTTP2:     !t.settings.DisableHTTP2,
		MaxIdleConns:          maxIdleConnections,
		MaxIdleConnsPerHost:   t.settings.MaxIdleConnsPerHost,
		IdleConnTimeout:       time.Duration(t.settings.IdleConnTimeoutSeconds) * time.Second,
		TLSHandshakeTimeout:   time.Duration(t.settings.TLSHandshakeTimeoutSeconds) * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       tlsConfig,
		// Health-check pooled HTTP/2 connections. A blackholed multiplexed
		// connection otherwise stays eligible through a retry cooldown, and the
		// next attempt waits out a full stall timeout before redialing.
		HTTP2: &http.HTTP2Config{
			SendPingTimeout: time.Duration(t.settings.HTTP2PingIntervalSeconds) * time.Second,
			PingTimeout:     time.Duration(t.settings.HTTP2PingTimeoutSeconds) * time.Second,
		},
	}
	if t.settings.DisableHTTP2 {
		transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	}
	if proxyURL == nil {
		return transport, nil
	}

	switch proxyURL.Scheme {
	case "socks4", "socks4a", "socks5", "socks5h":
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return t.dialProxyTunnel(ctx, network, address, proxyURL)
		}
	case "http", "https":
		if scheme == "https" {
			// Establish CONNECT ourselves. The outer http.Transport then applies
			// the target-only TLS config to the tunnel, while HTTPS proxy TLS
			// uses a separate config with no client identity or certificate pin.
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return t.dialProxyTunnel(ctx, network, address, proxyURL)
			}
		} else {
			// Plain HTTP needs standard forward-proxy request formatting. No
			// target TLS policy is present, so the base config is safe for an
			// HTTPS proxy handshake.
			transport.Proxy = func(*http.Request) (*url.URL, error) {
				clone := *proxyURL
				return &clone, nil
			}
		}
	default:
		return nil, errors.New("unsupported proxy protocol")
	}
	return transport, nil
}

func (t *Transport) originTLSConfig(host string, profile tlsProfile) (*tls.Config, error) {
	config := t.baseTLSConfig()
	if profile != 0 {
		config.ServerName = host
	}
	if profile&tlsProfileClientCertificate != 0 {
		if t.material.clientCertificate == nil {
			return nil, errors.New("client certificate material is unavailable")
		}
		config.Certificates = []tls.Certificate{*t.material.clientCertificate}
	}
	if profile&tlsProfilePinnedCertificate != 0 {
		if !t.material.hasTrustedLeaf {
			return nil, errors.New("trusted certificate pin is unavailable")
		}
		expected := t.material.trustedLeafSHA256
		// The exact leaf pin is the verification authority for this one
		// endpoint. Other endpoints and HTTPS proxies retain normal PKI checks.
		config.InsecureSkipVerify = true
		config.VerifyConnection = func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("trusted endpoint presented no certificate")
			}
			actual := sha256.Sum256(state.PeerCertificates[0].Raw)
			if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
				return errors.New("trusted endpoint certificate pin does not match")
			}
			return nil
		}
	}
	if t.settings.DisableHTTP2 {
		config.NextProtos = []string{"http/1.1"}
	}
	return config, nil
}

func (t *Transport) baseTLSConfig() *tls.Config {
	minimumVersion := uint16(tls.VersionTLS12)
	if t.settings.TLSMinimumVersion == TLSMinimumVersion13 {
		minimumVersion = tls.VersionTLS13
	}
	return &tls.Config{
		MinVersion: minimumVersion,
		RootCAs:    t.material.rootCAs,
	}
}

func (t *Transport) proxyTLSConfig(host string) *tls.Config {
	config := t.baseTLSConfig()
	config.ServerName = host
	// CONNECT is deliberately HTTP/1.1 so proxy ALPN can never route the
	// hand-written tunnel request onto an HTTP/2 connection.
	config.NextProtos = []string{"http/1.1"}
	config.Certificates = nil
	config.GetClientCertificate = nil
	config.InsecureSkipVerify = false
	config.VerifyConnection = nil
	return config
}

func tlsClientForProxy(conn net.Conn, config *tls.Config) *tls.Conn {
	return tls.Client(conn, config)
}

type errorRoundTripper struct {
	err error
}

func (t *errorRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("network transport is unavailable: %w", t.err)
}

func (t *errorRoundTripper) CloseIdleConnections() {}

type redactedProxyError struct {
	message string
	cause   error
}

func (e *redactedProxyError) Error() string {
	return e.message
}

func (e *redactedProxyError) Is(target error) bool {
	return errors.Is(e.cause, target)
}

func (e *redactedProxyError) Timeout() bool {
	timeoutError, ok := e.cause.(interface{ Timeout() bool })
	return ok && timeoutError.Timeout()
}

func (e *redactedProxyError) Temporary() bool {
	temporaryError, ok := e.cause.(interface{ Temporary() bool })
	return ok && temporaryError.Temporary()
}

func redactProxyError(err error, proxyURL *url.URL) error {
	if err == nil || proxyURL == nil || proxyURL.User == nil {
		return err
	}
	message := err.Error()
	redactedURL := *proxyURL
	redactedURL.User = url.User("redacted")
	message = strings.ReplaceAll(message, proxyURL.String(), redactedURL.String())

	username := proxyURL.User.Username()
	password, _ := proxyURL.User.Password()
	secrets := []string{
		proxyURL.User.String(),
		url.QueryEscape(proxyURL.User.String()),
		username,
		url.QueryEscape(username),
		password,
		url.QueryEscape(password),
	}
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	return &redactedProxyError{message: message, cause: err}
}
