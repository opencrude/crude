package lsp

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

const (
	MaxConfigurationScopes       = 128
	MaxConfigurationBytes        = 1 << 20
	MaxConfigurationItems        = 128
	MaxConfigurationRequestBytes = 64 << 10
	maxConfigurationURIBytes     = 8192
	maxConfigurationSectionBytes = 1024
)

var ErrConfiguration = errors.New("invalid or oversized language server configuration")

// ConfigurationScope supplies the complete effective settings for a directory
// URI and its descendants. It is not an overlay: the project-context owner must
// resolve inheritance before constructing it. URI matching is lexical, performs
// no filesystem access and establishes no read/execute authority.
type ConfigurationScope struct {
	URI      string
	Settings map[string]any
}

// Configuration is an immutable, launch-bound configuration table. Its zero
// value has no root or settings. Use NewConfiguration to bind nonempty settings;
// a table cannot be reused by a client with a different root.
type Configuration struct {
	root   *configurationURI
	global map[string]any
	scopes []configurationScope
}

type configurationURI struct {
	host string
	path string
}

type configurationScope struct {
	uri      configurationURI
	settings map[string]any
}

// NewConfiguration detaches JSON settings from caller-owned maps. Bounds cover
// encoded configuration and response bytes, not allocator overhead or the work
// an arbitrary caller-owned MarshalJSON implementation may do. No project file
// is read and no executable is constructed, including on validation failure.
func NewConfiguration(rootURI string, settings map[string]any, scopes []ConfigurationScope) (Configuration, error) {
	if len(scopes) > MaxConfigurationScopes {
		return Configuration{}, ErrConfiguration
	}
	c := Configuration{}
	if rootURI != "" {
		root, ok := parseConfigurationURI(rootURI)
		if !ok {
			return Configuration{}, ErrConfiguration
		}
		c.root = &root
	}
	if len(scopes) > 0 && c.root == nil {
		return Configuration{}, ErrConfiguration
	}
	remaining := MaxConfigurationBytes - len(rootURI)
	var err error
	c.global, err = copyConfigurationSettings(settings, &remaining)
	if err != nil {
		return Configuration{}, err
	}
	seen := make(map[configurationURI]bool, len(scopes))
	c.scopes = make([]configurationScope, 0, len(scopes))
	for _, input := range scopes {
		uri, ok := parseConfigurationURI(input.URI)
		if !ok || !configurationContains(*c.root, uri) || seen[uri] {
			return Configuration{}, ErrConfiguration
		}
		seen[uri] = true
		remaining -= len(input.URI)
		effective, err := copyConfigurationSettings(input.Settings, &remaining)
		if err != nil {
			return Configuration{}, err
		}
		c.scopes = append(c.scopes, configurationScope{uri: uri, settings: effective})
	}
	return c, nil
}

func copyConfigurationSettings(input map[string]any, remaining *int) (map[string]any, error) {
	data, err := json.Marshal(input)
	if err != nil || len(data) > *remaining {
		return nil, ErrConfiguration
	}
	*remaining -= len(data)
	var out map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
		return nil, ErrConfiguration
	}
	return out, nil
}

func parseConfigurationURI(raw string) (configurationURI, bool) {
	if len(raw) == 0 || len(raw) > maxConfigurationURIBytes || !utf8.ValidString(raw) {
		return configurationURI{}, false
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "file") || u.Opaque != "" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		strings.ContainsRune(raw, '#') ||
		!utf8.ValidString(u.Path) || !strings.HasPrefix(u.Path, "/") || strings.ContainsAny(u.Host, ":\\") {
		return configurationURI{}, false
	}
	for _, r := range u.Path {
		if r == '\\' || unicode.IsControl(r) {
			return configurationURI{}, false
		}
	}
	host := strings.ToLower(u.Host)
	if host == "localhost" {
		host = ""
	}
	clean := path.Clean(u.Path)
	// Drive-letter spelling is URI syntax, not a native filesystem case guess.
	if len(clean) >= 3 && clean[0] == '/' && clean[2] == ':' &&
		(clean[1] >= 'a' && clean[1] <= 'z') {
		clean = "/" + strings.ToUpper(clean[1:2]) + clean[2:]
	}
	return configurationURI{host: host, path: clean}, true
}

func configurationContains(directory, resource configurationURI) bool {
	return directory.host == resource.host && (directory.path == resource.path ||
		strings.HasPrefix(resource.path, strings.TrimSuffix(directory.path, "/")+"/"))
}

func (c Configuration) boundTo(rootURI string) bool {
	if c.root == nil {
		return rootURI == ""
	}
	root, ok := parseConfigurationURI(rootURI)
	return ok && *c.root == root
}

func (c Configuration) settingsFor(scope *protocol.URI) map[string]any {
	if scope == nil {
		return c.global
	}
	uri, ok := parseConfigurationURI(*scope)
	if !ok || c.root == nil || !configurationContains(*c.root, uri) {
		return nil
	}
	settings := c.global
	longest := -1
	for _, candidate := range c.scopes {
		if len(candidate.uri.path) > longest && configurationContains(candidate.uri, uri) {
			settings, longest = candidate.settings, len(candidate.uri.path)
		}
	}
	return settings
}

// answer preserves requested order and null results. Each result has its own
// encoded bytes; callers cannot mutate the retained launch table through a reply.
func (c Configuration) answer(params json.RawMessage) ([]any, error) {
	if len(params) > MaxConfigurationRequestBytes {
		return nil, ErrConfiguration
	}
	var request protocol.ConfigurationParams
	if err := json.Unmarshal(params, &request); err != nil || len(request.Items) > MaxConfigurationItems {
		return nil, ErrConfiguration
	}
	out := make([]any, 0, len(request.Items))
	bytes := 2 // array delimiters
	for _, item := range request.Items {
		if len(item.Section) > maxConfigurationSectionBytes {
			return nil, ErrConfiguration
		}
		value := configurationSection(c.settingsFor(item.ScopeURI), item.Section)
		data, err := json.Marshal(value)
		bytes += len(data) + 1
		if err != nil || bytes > MaxConfigurationBytes {
			return nil, ErrConfiguration
		}
		out = append(out, json.RawMessage(data))
	}
	return out, nil
}

func configurationSection(config map[string]any, section string) any {
	if section == "" {
		return config
	}
	if value, ok := config[section]; ok {
		return value
	}
	var value any = config
	for _, part := range strings.Split(section, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[part]
	}
	return value
}
