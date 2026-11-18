package mcpclient

import (
	"context"
	"errors"
	"os"
	"sync"

	captypes "crude/types/capability"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// BuiltinFactory builds one in-process server. lookup resolves the
// connection's optional settings; effects declares each tool's effect, which
// stands in for a durable grant because the server is Crude's own code.
type BuiltinFactory func(lookup func(string) string, maxResultBytes int) (server *mcp.Server, effects map[string]captypes.Effect)

var (
	builtinMu sync.RWMutex
	builtins  = make(map[string]BuiltinFactory)
)

// RegisterBuiltin installs the implementation behind a built-in connection's
// command name.
func RegisterBuiltin(name string, factory BuiltinFactory) {
	builtinMu.Lock()
	defer builtinMu.Unlock()
	builtins[name] = factory
}

func builtinFactory(name string) (BuiltinFactory, bool) {
	builtinMu.RLock()
	defer builtinMu.RUnlock()
	factory, exists := builtins[name]
	return factory, exists
}

// builtinLookup resolves a built-in setting from the connection's explicit
// environment, then Crude's secret store, then the core's environment, the
// same inheritance a local server process gets.
func (manager *Manager) builtinLookup(config captypes.ConnectionConfig, record func(string)) func(string) string {
	return func(name string) string {
		value := config.Environment[name]
		if value == "" {
			if reference := config.SecretRefs[name]; reference != "" {
				value, _ = manager.resolveSecret(reference)
			}
		}
		if value == "" {
			value, _ = manager.storedSecret(name)
		}
		if value == "" {
			value = os.Getenv(name)
		}
		if value != "" {
			record(value)
		}
		return value
	}
}

// builtinTransport connects to a fresh in-process server session. The server
// session ends when the client session closes or the connection lifetime ends.
func (manager *Manager) builtinTransport(config captypes.ConnectionConfig, lifetime context.Context, epoch uint64) (mcp.Transport, error) {
	factory, exists := builtinFactory(config.Command)
	if !exists {
		return nil, errors.New("built-in MCP server " + config.Command + " is not part of this Crude build")
	}
	record := func(value string) {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		if entry := manager.connections[config.ID]; entry != nil && entry.epoch == epoch {
			if entry.secrets == nil {
				entry.secrets = make(map[string]struct{})
			}
			entry.secrets[value] = struct{}{}
		}
	}
	server, effects := factory(manager.builtinLookup(config, record), MaxResultBytes)
	manager.mu.Lock()
	if entry := manager.connections[config.ID]; entry != nil && entry.epoch == epoch {
		entry.builtinEffects = effects
	}
	manager.mu.Unlock()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(lifetime, serverTransport, nil); err != nil {
		return nil, err
	}
	return clientTransport, nil
}
