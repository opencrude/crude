package network

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNormalizeAndValidateSettings(t *testing.T) {
	normalized := Normalize(Settings{
		ProxyMode:                  " CUSTOM ",
		ProxyURL:                   " socks5h://user:pass@example.com ",
		ClientCertificateHost:      "EXAMPLE.COM.",
		TrustedCertificateHost:     "EXAMPLE.NET.",
		TrustedCertificateSHA256:   "SHA256:AA:BB",
		ConnectTimeoutSeconds:      0,
		TLSHandshakeTimeoutSeconds: 0,
		KeepAliveSeconds:           0,
		IdleConnTimeoutSeconds:     0,
		MaxIdleConnsPerHost:        0,
	})
	defaults := DefaultSettings()
	if normalized.ProxyMode != ProxyModeCustom {
		t.Fatalf("ProxyMode = %q, want %q", normalized.ProxyMode, ProxyModeCustom)
	}
	if normalized.ClientCertificateHost != "example.com" {
		t.Fatalf("ClientCertificateHost = %q", normalized.ClientCertificateHost)
	}
	if normalized.TrustedCertificateHost != "example.net" {
		t.Fatalf("TrustedCertificateHost = %q", normalized.TrustedCertificateHost)
	}
	if normalized.TrustedCertificateSHA256 != "aabb" {
		t.Fatalf("TrustedCertificateSHA256 = %q", normalized.TrustedCertificateSHA256)
	}
	if normalized.ConnectTimeoutSeconds != defaults.ConnectTimeoutSeconds ||
		normalized.TLSHandshakeTimeoutSeconds != defaults.TLSHandshakeTimeoutSeconds ||
		normalized.KeepAliveSeconds != defaults.KeepAliveSeconds ||
		normalized.IdleConnTimeoutSeconds != defaults.IdleConnTimeoutSeconds ||
		normalized.MaxIdleConnsPerHost != defaults.MaxIdleConnsPerHost {
		t.Fatalf("zero values did not normalize to defaults: %#v", normalized)
	}

	valid := DefaultSettings()
	valid.ProxyMode = ProxyModeCustom
	valid.ProxyURL = "socks5h://user:pass@example.com"
	valid.NoProxy = "localhost,.example.org,10.0.0.0/8,[::1]:8443"
	valid.ClientCertificateFile = "client.pem"
	valid.ClientKeyFile = "client.key"
	valid.ClientCertificateHost = "client.example"
	valid.TrustedCertificateHost = "legacy.example"
	valid.TrustedCertificateSHA256 = strings.Repeat("ab", 32)
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsMalformedSettingsWithoutLeakingProxyCredentials(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*Settings)
		contains string
	}{
		{
			name: "proxy mode",
			mutate: func(settings *Settings) {
				settings.ProxyMode = "automatic"
			},
			contains: "proxy_mode",
		},
		{
			name: "custom proxy missing",
			mutate: func(settings *Settings) {
				settings.ProxyMode = ProxyModeCustom
			},
			contains: "proxy_url",
		},
		{
			name: "unsupported proxy",
			mutate: func(settings *Settings) {
				settings.ProxyMode = ProxyModeCustom
				settings.ProxyURL = "ftp://secret-user:secret-pass@example.com"
			},
			contains: "scheme",
		},
		{
			name: "socks4 password",
			mutate: func(settings *Settings) {
				settings.ProxyMode = ProxyModeCustom
				settings.ProxyURL = "socks4://secret-user:secret-pass@example.com"
			},
			contains: "SOCKS4",
		},
		{
			name: "empty proxy port",
			mutate: func(settings *Settings) {
				settings.ProxyMode = ProxyModeCustom
				settings.ProxyURL = "http://example.com:"
			},
			contains: "port",
		},
		{
			name: "invalid no proxy",
			mutate: func(settings *Settings) {
				settings.NoProxy = "example.com:not-a-port"
			},
			contains: "no_proxy",
		},
		{
			name: "empty no proxy port",
			mutate: func(settings *Settings) {
				settings.NoProxy = "example.com:"
			},
			contains: "no_proxy",
		},
		{
			name: "connect bound",
			mutate: func(settings *Settings) {
				settings.ConnectTimeoutSeconds = -1
			},
			contains: "connect_timeout_seconds",
		},
		{
			name: "TLS version",
			mutate: func(settings *Settings) {
				settings.TLSMinimumVersion = "1.1"
			},
			contains: "tls_minimum_version",
		},
		{
			name: "partial client identity",
			mutate: func(settings *Settings) {
				settings.ClientCertificateFile = "client.pem"
			},
			contains: "set together",
		},
		{
			name: "wildcard client host",
			mutate: func(settings *Settings) {
				settings.ClientCertificateFile = "client.pem"
				settings.ClientKeyFile = "client.key"
				settings.ClientCertificateHost = "*.example.com"
			},
			contains: "client_certificate_host",
		},
		{
			name: "partial pin",
			mutate: func(settings *Settings) {
				settings.TrustedCertificateHost = "example.com"
			},
			contains: "set together",
		},
		{
			name: "short pin",
			mutate: func(settings *Settings) {
				settings.TrustedCertificateHost = "example.com"
				settings.TrustedCertificateSHA256 = "abcd"
			},
			contains: "32 bytes",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := DefaultSettings()
			test.mutate(&settings)
			err := settings.Validate()
			if err == nil {
				t.Fatal("Validate() unexpectedly succeeded")
			}
			if !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("Validate() error = %q, want substring %q", err, test.contains)
			}
			if strings.Contains(err.Error(), "secret-user") || strings.Contains(err.Error(), "secret-pass") {
				t.Fatalf("Validate() leaked proxy credentials: %v", err)
			}
		})
	}
}

func TestConfigureDefaultPublishesNormalizedCopy(t *testing.T) {
	previous := Current()
	t.Cleanup(func() {
		if err := ConfigureDefault(previous); err != nil {
			t.Fatalf("restore settings: %v", err)
		}
	})

	settings := Settings{ProxyMode: " DIRECT "}
	if err := ConfigureDefault(settings); err != nil {
		t.Fatalf("ConfigureDefault() error = %v", err)
	}
	settings.ProxyMode = ProxyModeCustom
	current := Current()
	if current.ProxyMode != ProxyModeDirect {
		t.Fatalf("Current().ProxyMode = %q, want direct", current.ProxyMode)
	}
	if current.ConnectTimeoutSeconds != DefaultSettings().ConnectTimeoutSeconds {
		t.Fatalf("Current() did not contain normalized defaults: %#v", current)
	}
}

func TestValidateFilesAndNewClientFailClosed(t *testing.T) {
	previous := Current()
	t.Cleanup(func() {
		if err := ConfigureDefault(previous); err != nil {
			t.Fatalf("restore settings: %v", err)
		}
	})

	settings := DefaultSettings()
	settings.ProxyMode = ProxyModeDirect
	settings.CAFile = t.TempDir() + "/missing-ca.pem"
	if err := settings.Validate(); err != nil {
		t.Fatalf("scalar Validate() error = %v", err)
	}
	if err := settings.ValidateFiles(); err == nil {
		t.Fatal("ValidateFiles() unexpectedly succeeded")
	}
	if err := ConfigureDefault(settings); err != nil {
		t.Fatalf("ConfigureDefault() error = %v", err)
	}

	client := NewClient(time.Second)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:1", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(request)
	if err == nil {
		t.Fatal("NewClient request unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "network transport is unavailable") ||
		!strings.Contains(err.Error(), "read CA file") {
		t.Fatalf("NewClient error = %v", err)
	}
}

func TestNewClientCapturesCurrentSnapshot(t *testing.T) {
	previous := Current()
	t.Cleanup(func() {
		if err := ConfigureDefault(previous); err != nil {
			t.Fatalf("restore settings: %v", err)
		}
	})

	direct := DefaultSettings()
	direct.ProxyMode = ProxyModeDirect
	if err := ConfigureDefault(direct); err != nil {
		t.Fatal(err)
	}
	client := NewClient(2 * time.Second)

	custom := DefaultSettings()
	custom.ProxyMode = ProxyModeCustom
	custom.ProxyURL = "http://127.0.0.1:1"
	if err := ConfigureDefault(custom); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("captured client changed with Current(): %v", err)
	}
	response.Body.Close()

	_, err = NewClient(2 * time.Second).Get(server.URL)
	if err == nil {
		t.Fatal("new client did not capture the replacement proxy settings")
	}
}

func TestNoProxyMatching(t *testing.T) {
	matcher, err := compileNoProxy("example.com,.sub.example.net,10.0.0.0/8,192.0.2.1:8080,[2001:db8::1]:443")
	if err != nil {
		t.Fatalf("compileNoProxy() error = %v", err)
	}
	tests := []struct {
		rawURL string
		want   bool
	}{
		{"https://example.com", true},
		{"https://api.example.com", true},
		{"https://sub.example.net", false},
		{"https://api.sub.example.net", true},
		{"http://10.2.3.4", true},
		{"http://192.0.2.1:8080", true},
		{"http://192.0.2.1:80", false},
		{"https://[2001:db8::1]", true},
		{"https://other.example", false},
	}
	for _, test := range tests {
		request, err := http.NewRequest(http.MethodGet, test.rawURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := matcher.match(request.URL); got != test.want {
			t.Errorf("match(%q) = %v, want %v", test.rawURL, got, test.want)
		}
	}
}
