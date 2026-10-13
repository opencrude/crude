package settings

import (
	"strconv"
	"strings"

	"crude/network"
	"crude/tui/ui"
	"crude/types"
)

const (
	fieldNetworkConnectTimeout         = "network-connect-timeout"
	fieldNetworkTLSHandshakeTimeout    = "network-tls-handshake-timeout"
	fieldNetworkKeepAlive              = "network-keep-alive"
	fieldNetworkKeepAliveInterval      = "network-keep-alive-interval"
	fieldNetworkKeepAliveCount         = "network-keep-alive-count"
	fieldNetworkIdleConnTimeout        = "network-idle-connection-timeout"
	fieldNetworkMaxIdleConnsPerHost    = "network-max-idle-connections-per-host"
	fieldNetworkDisableHTTP2           = "network-disable-http2"
	fieldNetworkHTTP2PingInterval      = "network-http2-ping-interval"
	fieldNetworkHTTP2PingTimeout       = "network-http2-ping-timeout"
	fieldNetworkProxyMode              = "network-proxy-mode"
	fieldNetworkProxyURL               = "network-proxy-url"
	fieldNetworkNoProxy                = "network-no-proxy"
	fieldNetworkTLSMinimumVersion      = "network-tls-minimum-version"
	fieldNetworkCAFile                 = "network-ca-file"
	fieldNetworkClientCertificateFile  = "network-client-certificate-file"
	fieldNetworkClientKeyFile          = "network-client-key-file"
	fieldNetworkClientCertificateHost  = "network-client-certificate-host"
	fieldNetworkTrustedCertificateHost = "network-trusted-certificate-host"
	fieldNetworkTrustedCertificatePin  = "network-trusted-certificate-sha256"
)

func (m *Model) buildNetworkConnectionForm(settings network.Settings) *ui.FormModel {
	settings = m.networkDisplaySettings(settings)
	fields := []*ui.FieldState{
		ui.SeparatorFieldState("network-connect", "Connection establishment"),
		networkPositiveIntField(fieldNetworkConnectTimeout, "Connect timeout", "seconds", settings.ConnectTimeoutSeconds,
			"Time allowed to open each TCP connection. Waiting for a model's answer is governed separately by each account."),
		networkPositiveIntField(fieldNetworkTLSHandshakeTimeout, "TLS handshake", "seconds", settings.TLSHandshakeTimeoutSeconds,
			"Time allowed to negotiate and verify TLS once TCP connects."),
		ui.SeparatorFieldState("network-reuse", "Connection reuse"),
		networkPositiveIntField(fieldNetworkKeepAlive, "Keep-alive after", "seconds", settings.KeepAliveSeconds,
			"Idle time before the first TCP keep-alive probe on an open connection."),
		networkPositiveIntField(fieldNetworkKeepAliveInterval, "Keep-alive interval", "seconds", settings.KeepAliveIntervalSeconds,
			"Time between unanswered TCP keep-alive probes."),
		networkPositiveIntField(fieldNetworkKeepAliveCount, "Keep-alive probes", "probes", settings.KeepAliveCount,
			"Unanswered probes before a dead connection is closed and redialed. This is how HTTP/1.1 connections are health-checked."),
		networkPositiveIntField(fieldNetworkIdleConnTimeout, "Close idle after", "seconds", settings.IdleConnTimeoutSeconds,
			"How long an unused pooled connection may stay open."),
		networkPositiveIntField(fieldNetworkMaxIdleConnsPerHost, "Idle per host", "connections", settings.MaxIdleConnsPerHost,
			"The most reusable idle connections kept for one destination."),
		ui.SeparatorFieldState("network-http", "HTTP protocol"),
		ui.BoolFieldState(fieldNetworkDisableHTTP2, "Force HTTP/1.1", settings.DisableHTTP2,
			"Compatibility fallback that forces HTTP/1.1. Leave off unless an endpoint or proxy requires it."),
		networkPositiveIntField(fieldNetworkHTTP2PingInterval, "Health check after", "seconds", settings.HTTP2PingIntervalSeconds,
			"Read silence before an HTTP/2 connection is pinged. A dead connection is then redialed instead of reused after a retry."),
		networkPositiveIntField(fieldNetworkHTTP2PingTimeout, "Health check timeout", "seconds", settings.HTTP2PingTimeoutSeconds,
			"Time to wait for the ping reply before the HTTP/2 connection is closed."),
	}
	return m.networkForm("connection", fields...)
}

func (m *Model) buildNetworkProxyForm(settings network.Settings) *ui.FormModel {
	settings = m.networkDisplaySettings(settings)
	mode := ui.SegmentedFieldState(fieldNetworkProxyMode, "Proxy", []ui.FormOption{
		{ID: network.ProxyModeEnvironment, Label: "From environment"},
		{ID: network.ProxyModeDirect, Label: "None"},
		{ID: network.ProxyModeCustom, Label: "Custom URL"},
	}, settings.ProxyMode, "From environment uses HTTP_PROXY, HTTPS_PROXY and NO_PROXY. None connects directly. Custom URL uses the proxy below.")
	proxyURL := ui.PasswordFieldState(fieldNetworkProxyURL, "Proxy URL", settings.ProxyURL,
		"Used in Custom mode. HTTP(S), SOCKS4/SOCKS5 use local DNS; SOCKS4a/SOCKS5h use proxy/remote DNS. Credentials are masked and never copied.")
	proxyURL.Placeholder = "http://proxy.example:8080 or socks5h://host:1080"
	proxyURL.Validator = func(value ui.FieldValue) []string {
		if proxyURL.ReadOnly {
			return nil
		}
		raw := strings.TrimSpace(value.String())
		if m.value(catNetworkProxy, fieldNetworkProxyMode) == network.ProxyModeCustom && raw == "" {
			return []string{"Enter a proxy URL for Custom mode."}
		}
		return nil
	}
	noProxy := ui.TextAreaFieldState(fieldNetworkNoProxy, "Proxy bypass", settings.NoProxy,
		"Comma-separated hosts, domain suffixes, IP addresses, or CIDRs that connect directly. Direct mode ignores proxy routing.")
	noProxy.Rows = 3
	return m.networkForm("proxy",
		ui.SeparatorFieldState("network-proxy-routing", "Routing"),
		mode,
		proxyURL,
		noProxy,
	)
}

func (m *Model) buildNetworkCertificatesForm(settings network.Settings) *ui.FormModel {
	settings = m.networkDisplaySettings(settings)
	systemTrust := ui.TextFieldState("network-system-trust", "System trust", "System certificates",
		"Secure certificate-chain and hostname verification stays enabled by default.")
	systemTrust.ReadOnly = true
	caFile := ui.TextFieldState(fieldNetworkCAFile, "Custom CA file", settings.CAFile,
		"Optional PEM certificate authorities added to system trust. This trusts their issuers normally; it is not a certificate pin or TLS bypass.")
	trustedHost := ui.TextFieldState(fieldNetworkTrustedCertificateHost, "Exception host", settings.TrustedCertificateHost,
		"Exact hostname for the leaf certificate exception; no wildcard or other destination is covered.")
	trustedPin := ui.TextFieldState(fieldNetworkTrustedCertificatePin, "Leaf SHA-256", settings.TrustedCertificateSHA256,
		"Exact leaf DER pin only; accepts self-signed/invalid certificates for this host, never a blanket TLS bypass.")
	trustedPin.Validator = optionalSHA256Validator
	clientHost := ui.TextFieldState(fieldNetworkClientCertificateHost, "Client host", settings.ClientCertificateHost,
		"Exact hostname allowed to receive this mTLS certificate and key. The identity is never offered to another host.")
	return m.networkForm("certificates",
		ui.SeparatorFieldState("network-server-trust", "Server trust"),
		systemTrust,
		networkTLSMinimumField(settings.TLSMinimumVersion),
		caFile,
		ui.SeparatorFieldState("network-certificate-exception", "Exact certificate exception"),
		trustedHost,
		trustedPin,
		ui.SeparatorFieldState("network-client-identity", "Mutual TLS identity"),
		ui.TextFieldState(fieldNetworkClientCertificateFile, "Client cert (PEM)", settings.ClientCertificateFile,
			"Optional PEM client certificate for mutual TLS (mTLS). Save validates files off the UI thread."),
		ui.TextFieldState(fieldNetworkClientKeyFile, "Client key (PEM)", settings.ClientKeyFile,
			"PEM private-key path paired with the client certificate. This application-owned credential is never stored in workspace settings."),
		clientHost,
	)
}

func networkTLSMinimumField(current string) *ui.FieldState {
	return ui.SegmentedFieldState(fieldNetworkTLSMinimumVersion, "Minimum TLS", []ui.FormOption{
		{ID: network.TLSMinimumVersion12, Label: "TLS 1.2"},
		{ID: network.TLSMinimumVersion13, Label: "TLS 1.3"},
	}, current, "Reject older TLS versions. TLS 1.2 is the compatibility baseline; TLS 1.3 is stricter.")
}

func networkPositiveIntField(id, label, unit string, value int, description string) *ui.FieldState {
	field := ui.NumberFieldState(id, label, value, unit, description)
	field.Validator = func(value ui.FieldValue) []string {
		parsed, err := strconv.Atoi(strings.TrimSpace(value.String()))
		if err != nil || parsed < 1 {
			return []string{"Enter a positive whole number."}
		}
		return nil
	}
	return field
}

func optionalSHA256Validator(value ui.FieldValue) []string {
	fingerprint := canonicalSHA256(value.String())
	if fingerprint == "" {
		return nil
	}
	if len(fingerprint) != 64 {
		return []string{"Enter the 64 hexadecimal digits of the leaf DER SHA-256."}
	}
	for _, r := range fingerprint {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return []string{"Enter the 64 hexadecimal digits of the leaf DER SHA-256."}
		}
	}
	return nil
}

func canonicalSHA256(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "sha256:")
	value = strings.ReplaceAll(value, ":", "")
	value = strings.ReplaceAll(value, " ", "")
	return value
}

func (m *Model) networkDisplaySettings(settings network.Settings) network.Settings {
	if m != nil && m.Scope() == types.SettingsScopeWorkspace {
		settings = m.appDefaults.Network
	}
	return network.Normalize(settings)
}

func (m *Model) networkForm(page string, fields ...*ui.FieldState) *ui.FormModel {
	if m == nil || m.Scope() != types.SettingsScopeWorkspace {
		return formModel(fields...)
	}
	for _, field := range fields {
		if field == nil || field.Type == ui.FieldSeparator || field.Type == ui.FieldSpacer {
			continue
		}
		field.ReadOnly = true
		field.Inherited = true
		field.InheritedFrom = "Application"
	}
	scope := ui.TextFieldState("network-"+page+"-scope", "Workspace policy", "Inherited from Application Settings",
		"Network routing, trust, and credential paths are application-owned. This workspace cannot edit or override them.")
	scope.ReadOnly = true
	return formModel(append([]*ui.FieldState{
		ui.SeparatorFieldState("network-"+page+"-inheritance", "Application-owned network policy"),
		scope,
	}, fields...)...)
}

func (m *Model) networkSettingsFromForms() network.Settings {
	defaults := network.DefaultSettings()
	return network.Settings{
		ProxyMode:                  defaultString(strings.TrimSpace(m.value(catNetworkProxy, fieldNetworkProxyMode)), defaults.ProxyMode),
		ProxyURL:                   strings.TrimSpace(m.value(catNetworkProxy, fieldNetworkProxyURL)),
		NoProxy:                    strings.TrimSpace(m.value(catNetworkProxy, fieldNetworkNoProxy)),
		ConnectTimeoutSeconds:      networkIntValue(m.value(catNetworkConnection, fieldNetworkConnectTimeout), defaults.ConnectTimeoutSeconds),
		TLSHandshakeTimeoutSeconds: networkIntValue(m.value(catNetworkConnection, fieldNetworkTLSHandshakeTimeout), defaults.TLSHandshakeTimeoutSeconds),
		KeepAliveSeconds:           networkIntValue(m.value(catNetworkConnection, fieldNetworkKeepAlive), defaults.KeepAliveSeconds),
		KeepAliveIntervalSeconds:   networkIntValue(m.value(catNetworkConnection, fieldNetworkKeepAliveInterval), defaults.KeepAliveIntervalSeconds),
		KeepAliveCount:             networkIntValue(m.value(catNetworkConnection, fieldNetworkKeepAliveCount), defaults.KeepAliveCount),
		IdleConnTimeoutSeconds:     networkIntValue(m.value(catNetworkConnection, fieldNetworkIdleConnTimeout), defaults.IdleConnTimeoutSeconds),
		MaxIdleConnsPerHost:        networkIntValue(m.value(catNetworkConnection, fieldNetworkMaxIdleConnsPerHost), defaults.MaxIdleConnsPerHost),
		DisableHTTP2:               m.value(catNetworkConnection, fieldNetworkDisableHTTP2) == "on",
		HTTP2PingIntervalSeconds:   networkIntValue(m.value(catNetworkConnection, fieldNetworkHTTP2PingInterval), defaults.HTTP2PingIntervalSeconds),
		HTTP2PingTimeoutSeconds:    networkIntValue(m.value(catNetworkConnection, fieldNetworkHTTP2PingTimeout), defaults.HTTP2PingTimeoutSeconds),
		TLSMinimumVersion:          defaultString(strings.TrimSpace(m.value(catNetworkCertificates, fieldNetworkTLSMinimumVersion)), defaults.TLSMinimumVersion),
		CAFile:                     strings.TrimSpace(m.value(catNetworkCertificates, fieldNetworkCAFile)),
		ClientCertificateFile:      strings.TrimSpace(m.value(catNetworkCertificates, fieldNetworkClientCertificateFile)),
		ClientKeyFile:              strings.TrimSpace(m.value(catNetworkCertificates, fieldNetworkClientKeyFile)),
		ClientCertificateHost:      strings.TrimSpace(m.value(catNetworkCertificates, fieldNetworkClientCertificateHost)),
		TrustedCertificateHost:     strings.TrimSpace(m.value(catNetworkCertificates, fieldNetworkTrustedCertificateHost)),
		TrustedCertificateSHA256:   canonicalSHA256(m.value(catNetworkCertificates, fieldNetworkTrustedCertificatePin)),
	}
}

func networkIntValue(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func (m *Model) collectNetwork(settings *network.Settings) {
	if settings == nil {
		return
	}
	if m.Scope() == types.SettingsScopeWorkspace {
		*settings = network.Normalize(m.appDefaults.Network)
		return
	}
	*settings = network.Normalize(m.networkSettingsFromForms())
}

func (m *Model) rebuildNetworkForms() {
	if m == nil || len(m.categories) <= catNetworkCertificates {
		return
	}
	settings := m.appDefaults.Network
	forms := []struct {
		index int
		form  *ui.FormModel
	}{
		{catNetworkConnection, m.buildNetworkConnectionForm(settings)},
		{catNetworkProxy, m.buildNetworkProxyForm(settings)},
		{catNetworkCertificates, m.buildNetworkCertificatesForm(settings)},
	}
	for _, item := range forms {
		m.categories[item.index].Form = item.form
		category := item.index
		item.form.OnChanged(func(fieldID string) { m.handleFieldChanged(category, fieldID) })
	}
}

func (m *Model) isNetworkCategory(index int) bool {
	return index == catNetworkConnection || index == catNetworkProxy || index == catNetworkCertificates
}

func (m *Model) validateNetworkSettings() (int, string, bool) {
	if m == nil || m.Scope() != types.SettingsScopeApplication {
		return 0, "", true
	}
	for _, category := range []int{catNetworkConnection, catNetworkProxy, catNetworkCertificates} {
		form := m.categories[category].Form
		form.ValidateAll()
		for _, field := range form.Fields() {
			if field.HasErrors() {
				return category, field.ID, false
			}
		}
	}
	if err := m.networkSettingsFromForms().Validate(); err != nil {
		category, fieldID := networkValidationTarget(err.Error())
		if field := m.fieldAt(category, fieldID); field != nil {
			field.Errors = append(field.Errors, err.Error())
		}
		return category, fieldID, false
	}
	return 0, "", true
}

func networkValidationTarget(message string) (int, string) {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "no_proxy"), strings.Contains(lower, "no proxy"):
		return catNetworkProxy, fieldNetworkNoProxy
	case strings.Contains(lower, "proxy_mode"), strings.Contains(lower, "proxy mode"):
		return catNetworkProxy, fieldNetworkProxyMode
	case strings.Contains(lower, "proxy"):
		return catNetworkProxy, fieldNetworkProxyURL
	case strings.Contains(lower, "connect timeout"), strings.Contains(lower, "connect_timeout"):
		return catNetworkConnection, fieldNetworkConnectTimeout
	case strings.Contains(lower, "handshake"), strings.Contains(lower, "tls_handshake"):
		return catNetworkConnection, fieldNetworkTLSHandshakeTimeout
	case strings.Contains(lower, "keep_alive_interval"):
		return catNetworkConnection, fieldNetworkKeepAliveInterval
	case strings.Contains(lower, "keep_alive_count"):
		return catNetworkConnection, fieldNetworkKeepAliveCount
	case strings.Contains(lower, "keepalive"), strings.Contains(lower, "keep-alive"), strings.Contains(lower, "keep_alive"):
		return catNetworkConnection, fieldNetworkKeepAlive
	case strings.Contains(lower, "max_idle"):
		return catNetworkConnection, fieldNetworkMaxIdleConnsPerHost
	case strings.Contains(lower, "idle"):
		return catNetworkConnection, fieldNetworkIdleConnTimeout
	case strings.Contains(lower, "http2_ping_interval"):
		return catNetworkConnection, fieldNetworkHTTP2PingInterval
	case strings.Contains(lower, "http2_ping_timeout"):
		return catNetworkConnection, fieldNetworkHTTP2PingTimeout
	case strings.Contains(lower, "http2"), strings.Contains(lower, "http/2"):
		return catNetworkConnection, fieldNetworkDisableHTTP2
	case strings.Contains(lower, "client key"), strings.Contains(lower, "client_key"):
		return catNetworkCertificates, fieldNetworkClientKeyFile
	case strings.Contains(lower, "client certificate host"), strings.Contains(lower, "identity host"), strings.Contains(lower, "client_certificate_host"):
		return catNetworkCertificates, fieldNetworkClientCertificateHost
	case strings.Contains(lower, "client certificate"), strings.Contains(lower, "client_certificate"):
		return catNetworkCertificates, fieldNetworkClientCertificateFile
	case strings.Contains(lower, "sha"), strings.Contains(lower, "fingerprint"), strings.Contains(lower, "pin"):
		return catNetworkCertificates, fieldNetworkTrustedCertificatePin
	case strings.Contains(lower, "trusted certificate host"), strings.Contains(lower, "exception host"), strings.Contains(lower, "trusted_certificate_host"):
		return catNetworkCertificates, fieldNetworkTrustedCertificateHost
	case strings.Contains(lower, "ca_file"), strings.Contains(lower, "custom ca"):
		return catNetworkCertificates, fieldNetworkCAFile
	case strings.Contains(lower, "tls"):
		return catNetworkCertificates, fieldNetworkTLSMinimumVersion
	default:
		return catNetworkConnection, fieldNetworkConnectTimeout
	}
}
