package network

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"

	"golang.org/x/net/idna"
)

const (
	ProxyModeEnvironment = "environment"
	ProxyModeDirect      = "direct"
	ProxyModeCustom      = "custom"

	TLSMinimumVersion12 = "1.2"
	TLSMinimumVersion13 = "1.3"

	maxProxyURLBytes = 8 << 10
	maxNoProxyBytes  = 64 << 10
	maxNoProxyItems  = 1024
	maxPathBytes     = 32 << 10
)

// Settings contains the process-wide network transport policy. All fields are
// scalar so callers can persist and atomically publish a complete snapshot.
type Settings struct {
	ProxyMode string `json:"proxy_mode,omitempty"`
	ProxyURL  string `json:"proxy_url,omitempty"`
	NoProxy   string `json:"no_proxy,omitempty"`

	ConnectTimeoutSeconds      int  `json:"connect_timeout_seconds,omitempty"`
	TLSHandshakeTimeoutSeconds int  `json:"tls_handshake_timeout_seconds,omitempty"`
	KeepAliveSeconds           int  `json:"keep_alive_seconds,omitempty"`
	IdleConnTimeoutSeconds     int  `json:"idle_conn_timeout_seconds,omitempty"`
	MaxIdleConnsPerHost        int  `json:"max_idle_conns_per_host,omitempty"`
	DisableHTTP2               bool `json:"disable_http2,omitempty"`

	TLSMinimumVersion string `json:"tls_minimum_version,omitempty"`
	CAFile            string `json:"ca_file,omitempty"`

	ClientCertificateFile string `json:"client_certificate_file,omitempty"`
	ClientKeyFile         string `json:"client_key_file,omitempty"`
	ClientCertificateHost string `json:"client_certificate_host,omitempty"`

	TrustedCertificateHost   string `json:"trusted_certificate_host,omitempty"`
	TrustedCertificateSHA256 string `json:"trusted_certificate_sha256,omitempty"`
}

// DefaultSettings returns the secure application defaults.
func DefaultSettings() Settings {
	return Settings{
		ProxyMode:                  ProxyModeEnvironment,
		ConnectTimeoutSeconds:      30,
		TLSHandshakeTimeoutSeconds: 10,
		KeepAliveSeconds:           30,
		IdleConnTimeoutSeconds:     90,
		MaxIdleConnsPerHost:        10,
		TLSMinimumVersion:          TLSMinimumVersion12,
	}
}

// Normalize fills unset values and canonicalizes case-insensitive scalar
// values. It performs no filesystem or network I/O.
func Normalize(s Settings) Settings {
	defaults := DefaultSettings()

	s.ProxyMode = strings.ToLower(strings.TrimSpace(s.ProxyMode))
	if s.ProxyMode == "" {
		s.ProxyMode = defaults.ProxyMode
	}
	s.ProxyURL = strings.TrimSpace(s.ProxyURL)
	s.NoProxy = strings.TrimSpace(s.NoProxy)

	if s.ConnectTimeoutSeconds == 0 {
		s.ConnectTimeoutSeconds = defaults.ConnectTimeoutSeconds
	}
	if s.TLSHandshakeTimeoutSeconds == 0 {
		s.TLSHandshakeTimeoutSeconds = defaults.TLSHandshakeTimeoutSeconds
	}
	if s.KeepAliveSeconds == 0 {
		s.KeepAliveSeconds = defaults.KeepAliveSeconds
	}
	if s.IdleConnTimeoutSeconds == 0 {
		s.IdleConnTimeoutSeconds = defaults.IdleConnTimeoutSeconds
	}
	if s.MaxIdleConnsPerHost == 0 {
		s.MaxIdleConnsPerHost = defaults.MaxIdleConnsPerHost
	}

	s.TLSMinimumVersion = strings.TrimSpace(s.TLSMinimumVersion)
	if s.TLSMinimumVersion == "" {
		s.TLSMinimumVersion = defaults.TLSMinimumVersion
	}
	s.CAFile = strings.TrimSpace(s.CAFile)
	s.ClientCertificateFile = strings.TrimSpace(s.ClientCertificateFile)
	s.ClientKeyFile = strings.TrimSpace(s.ClientKeyFile)
	s.ClientCertificateHost = normalizeHostIfValid(s.ClientCertificateHost)
	s.TrustedCertificateHost = normalizeHostIfValid(s.TrustedCertificateHost)
	s.TrustedCertificateSHA256 = normalizeFingerprint(s.TrustedCertificateSHA256)
	return s
}

// Validate checks scalar syntax and bounds. It deliberately performs no file,
// DNS, environment, or other external I/O.
func (s Settings) Validate() error {
	s = Normalize(s)

	switch s.ProxyMode {
	case ProxyModeEnvironment, ProxyModeDirect, ProxyModeCustom:
	default:
		return fmt.Errorf("proxy_mode must be %q, %q, or %q", ProxyModeEnvironment, ProxyModeDirect, ProxyModeCustom)
	}
	if len(s.ProxyURL) > maxProxyURLBytes {
		return errors.New("proxy_url is too long")
	}
	if s.ProxyMode == ProxyModeCustom && s.ProxyURL == "" {
		return errors.New("proxy_url is required when proxy_mode is custom")
	}
	if s.ProxyURL != "" {
		if _, err := parseProxyURL(s.ProxyURL, true); err != nil {
			return fmt.Errorf("proxy_url is invalid: %w", err)
		}
	}
	if len(s.NoProxy) > maxNoProxyBytes {
		return errors.New("no_proxy is too long")
	}
	if _, err := compileNoProxy(s.NoProxy); err != nil {
		return fmt.Errorf("no_proxy is invalid: %w", err)
	}

	if err := validateBoundedInt("connect_timeout_seconds", s.ConnectTimeoutSeconds, 1, 600); err != nil {
		return err
	}
	if err := validateBoundedInt("tls_handshake_timeout_seconds", s.TLSHandshakeTimeoutSeconds, 1, 300); err != nil {
		return err
	}
	if err := validateBoundedInt("keep_alive_seconds", s.KeepAliveSeconds, 1, 3600); err != nil {
		return err
	}
	if err := validateBoundedInt("idle_conn_timeout_seconds", s.IdleConnTimeoutSeconds, 1, 86400); err != nil {
		return err
	}
	if err := validateBoundedInt("max_idle_conns_per_host", s.MaxIdleConnsPerHost, 1, 1000); err != nil {
		return err
	}

	switch s.TLSMinimumVersion {
	case TLSMinimumVersion12, TLSMinimumVersion13:
	default:
		return fmt.Errorf("tls_minimum_version must be %q or %q", TLSMinimumVersion12, TLSMinimumVersion13)
	}
	for _, field := range []struct {
		name string
		path string
	}{
		{name: "ca_file", path: s.CAFile},
		{name: "client_certificate_file", path: s.ClientCertificateFile},
		{name: "client_key_file", path: s.ClientKeyFile},
	} {
		if err := validatePathScalar(field.name, field.path); err != nil {
			return err
		}
	}

	clientFields := countSet(s.ClientCertificateFile, s.ClientKeyFile, s.ClientCertificateHost)
	if clientFields != 0 && clientFields != 3 {
		return errors.New("client_certificate_file, client_key_file, and client_certificate_host must be set together")
	}
	if s.ClientCertificateHost != "" {
		if _, err := canonicalHost(s.ClientCertificateHost); err != nil {
			return fmt.Errorf("client_certificate_host is invalid: %w", err)
		}
	}

	trustedFields := countSet(s.TrustedCertificateHost, s.TrustedCertificateSHA256)
	if trustedFields != 0 && trustedFields != 2 {
		return errors.New("trusted_certificate_host and trusted_certificate_sha256 must be set together")
	}
	if s.TrustedCertificateHost != "" {
		if _, err := canonicalHost(s.TrustedCertificateHost); err != nil {
			return fmt.Errorf("trusted_certificate_host is invalid: %w", err)
		}
	}
	if s.TrustedCertificateSHA256 != "" {
		if len(s.TrustedCertificateSHA256) != 64 {
			return errors.New("trusted_certificate_sha256 must contain exactly 32 bytes of hexadecimal SHA-256")
		}
		if _, err := hex.DecodeString(s.TrustedCertificateSHA256); err != nil {
			return errors.New("trusted_certificate_sha256 must be hexadecimal")
		}
	}
	return nil
}

// ValidateFiles checks every configured certificate file and key pair. Callers
// should run it away from latency-sensitive UI paths.
func (s Settings) ValidateFiles() error {
	s = Normalize(s)
	if err := s.Validate(); err != nil {
		return err
	}
	_, err := loadTLSMaterial(s)
	return err
}

var currentSettings atomic.Pointer[Settings]
var startupFailure atomic.Pointer[error]

func init() {
	defaults := DefaultSettings()
	currentSettings.Store(&defaults)
}

// ConfigureDefault atomically publishes a normalized scalar snapshot. File I/O
// remains the responsibility of ValidateFiles or NewTransport.
func ConfigureDefault(s Settings) error {
	s = Normalize(s)
	if err := s.Validate(); err != nil {
		return err
	}
	snapshot := s
	currentSettings.Store(&snapshot)
	startupFailure.Store(nil)
	return nil
}

// ConfigureStartupDefault publishes the saved policy only when its scalar and
// certificate material are usable. On failure it installs a process-wide
// closed policy: every subsequently constructed outbound transport fails until
// a valid settings save calls ConfigureDefault. The rejected settings remain
// with their caller for repair and are never replaced with defaults here.
func ConfigureStartupDefault(s Settings) error {
	s = Normalize(s)
	if err := s.ValidateFiles(); err != nil {
		failure := fmt.Errorf("saved Network settings are invalid: %w", err)
		startupFailure.Store(&failure)
		return failure
	}
	return ConfigureDefault(s)
}

func currentStartupFailure() error {
	if failure := startupFailure.Load(); failure != nil {
		return *failure
	}
	return nil
}

// StartupPolicyError reports why newly constructed outbound requests are blocked.
func StartupPolicyError() error { return currentStartupFailure() }

// BlockStartupDefault keeps network activity closed when configuration cannot
// be interpreted. It preserves all saved bytes and never substitutes direct I/O.
func BlockStartupDefault(reason error) {
	if reason == nil {
		return
	}
	startupFailure.Store(&reason)
}

// Current returns a copy of the currently configured scalar snapshot.
func Current() Settings {
	if current := currentSettings.Load(); current != nil {
		return *current
	}
	return DefaultSettings()
}

func validateBoundedInt(name string, value, minimum, maximum int) error {
	if value < minimum || value > maximum {
		return fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
	}
	return nil
}

func validatePathScalar(name, path string) error {
	if strings.ContainsRune(path, '\x00') {
		return fmt.Errorf("%s contains a NUL byte", name)
	}
	if len(path) > maxPathBytes {
		return fmt.Errorf("%s is too long", name)
	}
	return nil
}

func countSet(values ...string) int {
	var count int
	for _, value := range values {
		if value != "" {
			count++
		}
	}
	return count
}

func normalizeFingerprint(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "sha256:")
	value = strings.ReplaceAll(value, ":", "")
	value = strings.ReplaceAll(value, " ", "")
	return value
}

func normalizeHostIfValid(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if host, err := canonicalHost(value); err == nil {
		return host
	}
	return strings.ToLower(value)
}

func canonicalHost(value string) (string, error) {
	host := strings.TrimSpace(value)
	if host == "" {
		return "", errors.New("hostname is empty")
	}
	if strings.ContainsAny(host, "\x00\r\n/@") {
		return "", errors.New("hostname contains invalid characters")
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	if zoneAt := strings.LastIndexByte(host, '%'); zoneAt >= 0 {
		ip, zone := host[:zoneAt], host[zoneAt+1:]
		if net.ParseIP(ip) == nil || zone == "" {
			return "", errors.New("hostname contains an invalid IP zone")
		}
		return strings.ToLower(host), nil
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Unmap().String(), nil
	}
	if strings.Contains(host, ":") {
		return "", errors.New("hostname must not include a port")
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", errors.New("hostname is empty")
	}
	ascii, err := idna.Lookup.ToASCII(strings.ToLower(host))
	if err != nil {
		return "", errors.New("hostname is not valid IDNA")
	}
	if len(ascii) > 253 {
		return "", errors.New("hostname is too long")
	}
	for _, label := range strings.Split(ascii, ".") {
		if label == "" || len(label) > 63 {
			return "", errors.New("hostname contains an invalid label")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("hostname labels must not begin or end with a hyphen")
		}
		for _, char := range label {
			if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
				continue
			}
			return "", errors.New("hostname contains an invalid character")
		}
	}
	return ascii, nil
}

func parsePort(value string) (string, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("port must be between 1 and 65535")
	}
	return strconv.Itoa(port), nil
}

func hostPort(host, port string) string {
	return net.JoinHostPort(host, port)
}
