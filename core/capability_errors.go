package core

import (
	"net/url"
	"sort"
	"strings"

	"crude/types"
	captypes "crude/types/capability"
)

// Capability errors are actionable diagnostics, not a generic settings failure.
// Keep the reason while removing private connection/source/proxy operands. The
// worker captures these immutable values before execution; no live maps or
// credentials are consulted from the result's apply phase.
func publicCapabilityFailure(err error, settings types.AppSettings, action captypes.Action) string {
	text := err.Error()
	private := make(map[string]string)
	secret := func(value string) {
		if value != "" {
			private[value] = "[redacted]"
		}
	}
	endpoint := func(value string) {
		if value == "" {
			return
		}
		if public := publicURL(value); public != value {
			private[value] = public
		}
		if parsed, e := url.Parse(value); e == nil {
			if parsed.User != nil {
				password, _ := parsed.User.Password()
				secret(password)
			}
			for _, values := range parsed.Query() {
				for _, value := range values {
					secret(value)
				}
			}
			secret(parsed.Fragment)
		}
	}
	connection := func(value captypes.ConnectionConfig) {
		endpoint(value.URL)
		secret(value.CredentialRef)
		for _, values := range []map[string]string{value.Environment, value.Headers, value.SecretRefs} {
			for _, value := range values {
				secret(value)
			}
		}
		for _, value := range value.Args {
			secret(value)
		}
	}
	endpoint(settings.Network.ProxyURL)
	for _, value := range settings.Capabilities.Sources {
		endpoint(value.Location)
	}
	for _, value := range settings.Capabilities.Connections {
		connection(value)
	}
	if action.Connection != nil {
		connection(*action.Connection)
	}
	if action.Source != nil {
		endpoint(action.Source.Location)
	}
	// Longest first: redact a complete URL before its individual secret values.
	keys := make([]string, 0, len(private))
	for value := range private {
		keys = append(keys, value)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, value := range keys {
		text = strings.ReplaceAll(text, value, private[value])
	}
	return text
}
