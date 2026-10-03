package capability

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"crude/mcpclient"
	captypes "crude/types/capability"
)

func ImportConnections(data []byte, scope captypes.Scope) (map[string]captypes.ConnectionConfig, error) {
	if len(data) > 1<<20 {
		return nil, errors.New("import exceeds the 1 MiB configuration budget")
	}
	var document struct {
		Servers    map[string]json.RawMessage `json:"servers"`
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("MCP import: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("MCP import contains trailing data")
	}
	servers := document.MCPServers
	if len(servers) == 0 {
		servers = document.Servers
	}
	if len(servers) == 0 || len(servers) > 64 {
		return nil, errors.New("MCP import needs between 1 and 64 server definitions")
	}
	result := make(map[string]captypes.ConnectionConfig)
	identifier := regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	for name, raw := range servers {
		var definition struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			Headers map[string]string `json:"headers"`
			CWD     string            `json:"cwd"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&definition); err != nil {
			return nil, fmt.Errorf("connection %q: %w", name, err)
		}
		transport := definition.Type
		if transport == "streamable-http" {
			transport = "http"
		}
		if transport == "" && definition.Command != "" {
			transport = "stdio"
		}
		id := identifier.ReplaceAllString(name, "_")
		if _, exists := result[id]; exists {
			return nil, errors.New("connection names collide after identity normalization")
		}
		config := captypes.ConnectionConfig{ID: id, Name: name, Scope: scope, Transport: transport, URL: definition.URL, Command: definition.Command, Args: definition.Args, Directory: definition.CWD, Environment: definition.Env, Headers: definition.Headers, SecretRefs: make(map[string]string)}
		for key, value := range config.Headers {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "auth") || strings.Contains(lower, "token") || strings.Contains(lower, "key") {
				if !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") {
					return nil, fmt.Errorf("connection %q header %q contains credentials: use ${ENV_NAME} references", name, key)
				}
				config.SecretRefs[key] = "env:" + strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
				delete(config.Headers, key)
			}
		}
		if err := mcpclient.Validate(config); err != nil {
			return nil, fmt.Errorf("connection %q: %w", name, err)
		}
		result[id] = config
	}
	return result, nil
}
