package mcpclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"crude/network"
	captypes "crude/types/capability"
)

const maxDiscovery = 512
const maxResultBytes = MaxResultBytes

// MaxResultBytes bounds one MCP result as received, including its JSON
// encoding. Built-in servers size their pages to fit it.
const MaxResultBytes = 1 << 20

var identityPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type connection struct {
	config          captypes.ConnectionConfig
	state           captypes.ConnectionState
	session         *mcp.ClientSession
	epoch           uint64
	cancel          context.CancelFunc
	discovered      bool
	interactiveAuth bool
	secrets         map[string]struct{}
	outputSchemas   map[string]*jsonschema.Resolved
	// stderr belongs to the exact launch epoch that created it.
	stderr      *stderrTail
	stderrEpoch uint64
	// builtinEffects are the effects Crude's own in-process server declares.
	builtinEffects map[string]captypes.Effect
	// connectedOnce, retryAt and retryAttempt drive Recover's reconnect backoff.
	connectedOnce bool
	retryAt       time.Time
	retryAttempt  int
	// parked marks a user Disconnect; automatic recovery respects it.
	parked bool
	// connecting closes when the in-flight connect attempt settles, so a
	// concurrent connect joins it instead of failing.
	connecting chan struct{}
}

type Manager struct {
	mu            sync.RWMutex
	root          string
	connections   map[string]*connection
	closed        bool
	credentialDir string
	credentialMu  sync.Mutex
	authCallbacks map[string]*authorizationFlow
	// Admission under mu precedes every add; Close rejects new work before
	// joining connects and detached reconfiguration cleanup.
	lifetimeTasks sync.WaitGroup
}

func New(root string) *Manager {
	return &Manager{root: root, connections: make(map[string]*connection), authCallbacks: make(map[string]*authorizationFlow)}
}

func Validate(config captypes.ConnectionConfig) error {
	if !identityPattern.MatchString(config.ID) || strings.TrimSpace(config.Name) == "" {
		return errors.New("connection needs a stable alphanumeric ID and a name")
	}
	if config.Host != "" && config.Host != "core" && config.Host != "shell" {
		return errors.New("MCP host must be core or shell")
	}
	if config.Host == "shell" && config.Transport != "stdio" {
		return errors.New("shell MCP host supports stdio only")
	}
	switch config.Transport {
	case "http", TransportSSE:
		endpoint, err := url.Parse(config.URL)
		if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") {
			return errors.New("connection URL must be HTTP(S), with no embedded credentials")
		}
		if endpoint.Scheme == "http" && endpoint.Hostname() != "localhost" && endpoint.Hostname() != "127.0.0.1" && endpoint.Hostname() != "::1" {
			return errors.New("unencrypted HTTP is only allowed for loopback servers")
		}
	case "stdio":
		if strings.TrimSpace(config.Command) == "" {
			return errors.New("stdio connection requires an executable")
		}
	case captypes.TransportBuiltin:
		if _, exists := builtinFactory(config.Command); !exists {
			return errors.New("built-in MCP server " + config.Command + " is not part of this Crude build")
		}
		if config.Host == "shell" {
			return errors.New("built-in MCP servers run in the core")
		}
	default:
		return errors.New("supported transports are stdio, Streamable HTTP and legacy HTTP+SSE")
	}
	for name, value := range config.Headers {
		if strings.ContainsAny(name+value, "\r\n") || strings.TrimSpace(name) == "" {
			return errors.New("invalid connection header")
		}
	}
	for name := range config.Environment {
		if name == "" || strings.ContainsAny(name, "=\x00") {
			return errors.New("invalid environment name")
		}
	}
	for name := range config.SecretRefs {
		if name == "" || strings.ContainsAny(name, "\r\n\x00") || (config.Transport == "stdio" && strings.Contains(name, "=")) {
			return errors.New("invalid credential target")
		}
	}
	for _, name := range config.RequiredEnv {
		if name == "" || strings.ContainsAny(name, "=\r\n\x00") {
			return errors.New("invalid required environment name")
		}
	}
	return nil
}

func (manager *Manager) Configure(configs map[string]captypes.ConnectionConfig) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return
	}
	for id, existing := range manager.connections {
		next, exists := configs[id]
		next.ID = id
		before, _ := json.Marshal(existing.config)
		after, _ := json.Marshal(next)
		if !exists || string(before) != string(after) {
			existing.epoch++
			if existing.cancel != nil {
				existing.cancel()
			}
			if existing.session != nil {
				session := existing.session
				manager.lifetimeTasks.Add(1)
				go func() { defer manager.lifetimeTasks.Done(); _ = session.Close() }()
			}
			existing.session = nil
			existing.cancel = nil
			existing.interactiveAuth = false
			existing.state.AuthPending, existing.state.AuthURL = false, ""
			existing.state.State = "not started"
			existing.state.Cached = hasCatalog(existing.state)
			if !exists {
				delete(manager.connections, id)
			}
		}
	}
	for id, config := range configs {
		config.ID = id
		cloned := captypes.Clone(captypes.Settings{Connections: map[string]captypes.ConnectionConfig{id: config}}).Connections[id]
		entry := manager.connections[id]
		if entry == nil {
			entry = &connection{state: captypes.ConnectionState{ID: id, State: "not started"}}
			manager.connections[id] = entry
		}
		entry.config = cloned
		if config.Disabled {
			entry.state.State = "disabled"
		}
	}
}

func (manager *Manager) States() []captypes.ConnectionState {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	states := make([]captypes.ConnectionState, 0, len(manager.connections))
	for _, entry := range manager.connections {
		state := entry.state
		if entry.stderr != nil {
			state.Stderr = redactSecrets(entry.config, entry.secrets, manager.resolveSecret, entry.stderr.Text(0))
		}
		encoded, _ := json.Marshal(state)
		var detached captypes.ConnectionState
		if json.Unmarshal(encoded, &detached) == nil {
			states = append(states, detached)
		}
	}
	sort.Slice(states, func(first, second int) bool { return states[first].ID < states[second].ID })
	return states
}

func (manager *Manager) Connect(ctx context.Context, id string) error {
	return manager.connect(ctx, id, false)
}

type launchAuthorizationKey struct{}

// ConnectAuthorized admits one exact, internally reviewed launch without
// persisting LaunchApproved. It is not an agent parameter or a durable grant.
func (manager *Manager) ConnectAuthorized(ctx context.Context, id string, expected captypes.ConnectionConfig) error {
	return manager.connect(context.WithValue(ctx, launchAuthorizationKey{}, expected), id, false)
}

func (manager *Manager) connect(ctx context.Context, id string, interactive bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	manager.mu.Lock()
	entry := manager.connections[id]
	if manager.closed || entry == nil {
		manager.mu.Unlock()
		return errors.New("connection is unavailable")
	}
	if entry.session != nil {
		manager.mu.Unlock()
		return nil
	}
	if entry.state.State == "connecting" && entry.connecting != nil {
		// Join the attempt already in flight (for example an automatic
		// reconnect) rather than failing or starting a second launch.
		wait := entry.connecting
		manager.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
		manager.mu.Lock()
		connected := manager.connections[id] == entry && entry.session != nil
		failure := entry.state.Error
		manager.mu.Unlock()
		if connected {
			return nil
		}
		if failure == "" {
			failure = "connection attempt did not complete"
		}
		return errors.New(failure)
	}
	config := entry.config
	if config.Host == "shell" {
		manager.mu.Unlock()
		return errors.New("shell MCP connection requires exact connected-shell dispatcher")
	}
	if expected, authorized := ctx.Value(launchAuthorizationKey{}).(captypes.ConnectionConfig); authorized && !reflect.DeepEqual(expected, config) {
		manager.mu.Unlock()
		return errors.New("MCP launch configuration changed after authorization")
	}
	if config.Disabled {
		manager.mu.Unlock()
		return errors.New("connection is disabled")
	}
	if err := Validate(config); err != nil {
		manager.mu.Unlock()
		return err
	}
	missing := MissingConfiguration(config)
	if len(missing) > 0 {
		manager.mu.Unlock()
		return fmt.Errorf("connection needs required configuration: %s", strings.Join(missing, ", "))
	}
	if config.Transport == "stdio" && !config.LaunchApproved {
		if _, authorized := ctx.Value(launchAuthorizationKey{}).(captypes.ConnectionConfig); !authorized {
			manager.mu.Unlock()
			return errors.New("local server launch requires approval/authorization for its exact configuration")
		}
	}
	entry.epoch++
	epoch := entry.epoch
	lifetime, cancel := context.WithCancel(context.Background())
	entry.cancel = cancel
	entry.interactiveAuth = interactive
	entry.state.State, entry.state.Error = "connecting", ""
	entry.state.AuthPending, entry.state.AuthURL = false, ""
	settled := make(chan struct{})
	entry.connecting = settled
	manager.lifetimeTasks.Add(1)
	manager.mu.Unlock()
	defer manager.lifetimeTasks.Done()
	defer close(settled)
	defer func() {
		manager.mu.Lock()
		if current := manager.connections[id]; current == entry && current.epoch == epoch {
			current.interactiveAuth = false
		}
		manager.mu.Unlock()
	}()
	seconds := config.TimeoutSeconds
	if seconds <= 0 {
		seconds = 30
	}
	if interactive && seconds < 300 {
		seconds = 300
	}
	connectCtx, connectCancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	stopLifetime := context.AfterFunc(lifetime, connectCancel)
	stopRequest := context.AfterFunc(connectCtx, cancel)
	defer func() {
		stopRequest()
		stopLifetime()
		connectCancel()
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "Crude", Version: "pre-alpha"}, &mcp.ClientOptions{
		Capabilities:               &mcp.ClientCapabilities{},
		MultiRoundTrip:             &mcp.MultiRoundTripOptions{Disabled: true},
		ToolListChangedHandler:     func(context.Context, *mcp.ToolListChangedRequest) { manager.markChanged(id, epoch) },
		ResourceListChangedHandler: func(context.Context, *mcp.ResourceListChangedRequest) { manager.markChanged(id, epoch) },
		PromptListChangedHandler:   func(context.Context, *mcp.PromptListChangedRequest) { manager.markChanged(id, epoch) },
		ProgressNotificationHandler: func(_ context.Context, request *mcp.ProgressNotificationClientRequest) {
			manager.log(id, epoch, fmt.Sprintf("Progress: %v", request.Params.Progress))
		},
	})
	if manager.root != "" {
		//nolint:staticcheck // Roots remain interoperable for negotiated pre-2026-07-28 servers during the official SDK deprecation window; newer protocols do not use this callback.
		client.AddRoots(&mcp.Root{URI: (&url.URL{Scheme: "file", Path: filepath.ToSlash(manager.root)}).String(), Name: filepath.Base(manager.root)})
	}
	transport, cleanup, err := manager.transport(config, lifetime, epoch, interactive)
	if err != nil {
		cancel()
		manager.failed(id, epoch, err)
		return manager.safeError(config, err)
	}
	defer cleanup()
	probe := probeHandshake(transport)
	session, err := client.Connect(connectCtx, transport, nil)
	if err != nil && connectCtx.Err() == nil && probe.legacyServer() {
		// A 2024-11-05 server answers the Streamable HTTP initialize POST with
		// a client error; the spec's compatibility path is the HTTP+SSE stream.
		manager.log(id, epoch, "Server rejected Streamable HTTP; connecting with legacy HTTP+SSE")
		session, err = client.Connect(connectCtx, legacySSE(transport), nil)
	}
	if err != nil {
		if connectCtx.Err() != nil {
			err = connectCtx.Err()
		}
		cancel()
		err = manager.diagnosed(id, epoch, err)
		manager.failed(id, epoch, err)
		return manager.safeError(config, err)
	}
	manager.mu.Lock()
	if manager.closed || manager.connections[id] != entry || entry.epoch != epoch || connectCtx.Err() != nil {
		manager.mu.Unlock()
		cancel()
		_ = session.Close()
		return errors.New("connection request was superseded or cancelled")
	}
	entry.session = session
	entry.state.State, entry.state.Cached = "connected", false
	entry.connectedOnce, entry.parked = true, false
	manager.mu.Unlock()
	if err := manager.Refresh(connectCtx, id); err != nil {
		manager.mu.Lock()
		if manager.connections[id] == entry && entry.epoch == epoch && entry.session == session {
			entry.session = nil
		}
		manager.mu.Unlock()
		cancel()
		_ = session.Close()
		err = manager.diagnosed(id, epoch, err)
		manager.failed(id, epoch, err)
		return err
	}
	go manager.watchSession(id, epoch, session, cancel)
	return nil
}

func (manager *Manager) watchSession(id string, epoch uint64, session *mcp.ClientSession, cancel context.CancelFunc) {
	err := session.Wait()
	if err == nil {
		err = errors.New("MCP server disconnected")
	}
	err = manager.diagnosed(id, epoch, err)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if entry := manager.connections[id]; entry != nil && entry.epoch == epoch && entry.session == session {
		cancel()
		entry.session, entry.cancel = nil, nil
		entry.state.State, entry.state.Cached = "failed", hasCatalog(entry.state)
		entry.state.Error = redactSecrets(entry.config, entry.secrets, manager.resolveSecret, err.Error())
	}
}

func hasCatalog(state captypes.ConnectionState) bool {
	return len(state.Tools)+len(state.Resources)+len(state.Prompts) > 0
}

func (manager *Manager) markChanged(id string, epoch uint64) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if entry := manager.connections[id]; entry != nil && entry.epoch == epoch {
		entry.discovered = false
		entry.state.Cached = true
	}
}

func (manager *Manager) log(id string, epoch uint64, message string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if entry := manager.connections[id]; entry != nil && entry.epoch == epoch {
		entry.state.Logs = append(entry.state.Logs, redactSecrets(entry.config, entry.secrets, manager.resolveSecret, message))
		if len(entry.state.Logs) > 100 {
			entry.state.Logs = entry.state.Logs[len(entry.state.Logs)-100:]
		}
	}
}

func (manager *Manager) failed(id string, epoch uint64, err error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if entry := manager.connections[id]; entry != nil && entry.epoch == epoch {
		entry.state.State, entry.state.Error = "failed", redactSecrets(entry.config, entry.secrets, manager.resolveSecret, err.Error())
		entry.state.AuthPending, entry.state.AuthURL = false, ""
		entry.state.Cached = hasCatalog(entry.state)
		message := strings.ToLower(err.Error())
		if errors.Is(err, errNeedsSignIn) || strings.Contains(message, "401") || strings.Contains(message, "unauthorized") || strings.Contains(message, "sign-in") || strings.Contains(message, "sign in again") {
			entry.state.State = "needs sign-in"
		}
		// Cancellation is not a server fault. A missed deadline is: keep its
		// reason and diagnostics visible instead of resetting to "not started".
		if errors.Is(err, context.Canceled) {
			entry.state.State, entry.state.Error = "not started", ""
		}
	}
}

type headerTransport struct {
	base     http.RoundTripper
	headers  map[string]string
	endpoint *url.URL
	lifetime context.Context
}

func (transport headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := transport.lifetime.Err(); err != nil {
		return nil, err
	}
	if request.URL.Scheme != transport.endpoint.Scheme || request.URL.Host != transport.endpoint.Host {
		return nil, errors.New("credentials are bound to the configured MCP endpoint")
	}
	cloned := request.Clone(request.Context())
	ctx, cancel := context.WithCancel(cloned.Context())
	stop := context.AfterFunc(transport.lifetime, cancel)
	cloned = cloned.WithContext(ctx)
	for name, value := range transport.headers {
		cloned.Header.Set(name, value)
	}
	response, err := transport.base.RoundTrip(cloned)
	if err != nil {
		stop()
		cancel()
		return nil, err
	}
	response.Body = &boundedBody{ReadCloser: response.Body, remaining: maxResultBytes, cancel: func() { stop(); cancel() }}
	return response, nil
}

type boundedBody struct {
	io.ReadCloser
	remaining int64
	cancel    context.CancelFunc
}

func (body *boundedBody) Read(buffer []byte) (int, error) {
	if int64(len(buffer)) > body.remaining+1 {
		buffer = buffer[:body.remaining+1]
	}
	count, err := body.ReadCloser.Read(buffer)
	if int64(count) > body.remaining {
		return int(body.remaining), errors.New("MCP HTTP response exceeds the 1 MiB budget")
	}
	body.remaining -= int64(count)
	return count, err
}

func (body *boundedBody) Close() error {
	if body.cancel != nil {
		body.cancel()
	}
	return body.ReadCloser.Close()
}

func (manager *Manager) transport(config captypes.ConnectionConfig, lifetime context.Context, epoch uint64, interactive bool) (mcp.Transport, func(), error) {
	cleanup := func() {}
	if config.IsBuiltin() {
		transport, err := manager.builtinTransport(config, lifetime, epoch)
		return transport, cleanup, err
	}
	if config.Transport == "stdio" {
		// The pipe owner owns cancellation, termination and Wait together. A
		// CommandContext kill races graceful EOF and can report Access denied on
		// Windows after the process has already exited.
		command := exec.Command(config.Command, config.Args...)
		command.Dir = config.Directory
		if command.Dir == "" {
			command.Dir = manager.root
		} else if !filepath.IsAbs(command.Dir) {
			command.Dir = filepath.Join(manager.root, command.Dir)
		}
		command.Env = stdioBaseEnvironment()
		for name, value := range config.Environment {
			command.Env = append(command.Env, name+"="+value)
		}
		for name, reference := range config.SecretRefs {
			value, err := manager.resolveSecret(reference)
			if err != nil {
				return nil, cleanup, err
			}
			command.Env = append(command.Env, name+"="+value)
		}
		tail := newStderrTail()
		manager.mu.Lock()
		if entry := manager.connections[config.ID]; entry != nil && entry.epoch == epoch {
			entry.stderr, entry.stderrEpoch = tail, epoch
		}
		manager.mu.Unlock()
		return &boundedCommandTransport{command: command, lifetime: lifetime, stderr: tail}, cleanup, nil
	}
	headers := make(map[string]string)
	for name, value := range config.Headers {
		headers[name] = value
	}
	for name, reference := range config.SecretRefs {
		value, err := manager.resolveSecret(reference)
		if err != nil {
			return nil, cleanup, err
		}
		headers[name] = value
	}
	manager.mu.Lock()
	if entry := manager.connections[config.ID]; entry != nil && entry.epoch == epoch {
		if entry.secrets == nil {
			entry.secrets = make(map[string]struct{})
		}
		for _, value := range headers {
			entry.secrets[value] = struct{}{}
		}
	}
	manager.mu.Unlock()
	endpoint, _ := url.Parse(config.URL)
	client := network.NewClient(0)
	client.Transport = headerTransport{base: client.Transport, headers: headers, endpoint: endpoint, lifetime: lifetime}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if config.Transport == TransportSSE {
		return &mcp.SSEClientTransport{Endpoint: config.URL, HTTPClient: client, MaxEventSize: maxResultBytes}, cleanup, nil
	}
	transport := &mcp.StreamableClientTransport{Endpoint: config.URL, HTTPClient: client, MaxRetries: -1, MaxEventSize: maxResultBytes}
	if config.Auth == "oauth" {
		handler, closeAuth, err := manager.oauthHandler(config, lifetime, epoch, interactive)
		if err != nil {
			return nil, cleanup, err
		}
		transport.OAuthHandler = handler
		cleanup = closeAuth
	}
	return transport, cleanup, nil
}

func redactSecrets(config captypes.ConnectionConfig, secrets map[string]struct{}, resolve func(string) (string, error), text string) string {
	values := make(map[string]struct{}, len(secrets))
	for value := range secrets {
		values[value] = struct{}{}
	}
	for _, value := range config.Headers {
		values[value] = struct{}{}
		if scheme, token, found := strings.Cut(value, " "); found && (strings.EqualFold(scheme, "bearer") || strings.EqualFold(scheme, "basic")) {
			values[token] = struct{}{}
		}
	}
	for _, value := range config.Environment {
		values[value] = struct{}{}
	}
	for _, reference := range config.SecretRefs {
		if resolve == nil {
			break
		}
		if value, err := resolve(reference); err == nil && value != "" {
			values[value] = struct{}{}
		}
	}
	if config.URL != "" {
		text = strings.ReplaceAll(text, config.URL, "[MCP endpoint]")
	}
	ordered := make([]string, 0, len(values)*3)
	for value := range values {
		if value != "" {
			encoded, _ := json.Marshal(value)
			ordered = append(ordered, value, url.QueryEscape(value), string(encoded[1:len(encoded)-1]))
		}
	}
	sort.Slice(ordered, func(first, second int) bool { return len(ordered[first]) > len(ordered[second]) })
	for _, value := range ordered {
		text = strings.ReplaceAll(text, value, "[redacted]")
	}
	return text
}

func (manager *Manager) redact(config captypes.ConnectionConfig, text string) string {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	var secrets map[string]struct{}
	if entry := manager.connections[config.ID]; entry != nil {
		secrets = entry.secrets
	}
	return redactSecrets(config, secrets, manager.resolveSecret, text)
}

func (manager *Manager) safeError(config captypes.ConnectionConfig, err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		if err.Error() == context.DeadlineExceeded.Error() {
			return context.DeadlineExceeded
		}
		// Preserve the deadline identity and the attached server diagnostics.
		return redactedError{text: manager.redact(config, err.Error()), cause: context.DeadlineExceeded}
	}
	return errors.New(manager.redact(config, err.Error()))
}

type redactedError struct {
	text  string
	cause error
}

func (e redactedError) Error() string { return e.text }
func (e redactedError) Unwrap() error { return e.cause }

// diagnosticTailBytes bounds stderr quoted inside one error message.
const diagnosticTailBytes = 2 << 10

// diagnosed attaches the exact launch's redacted stderr tail. Without it a
// crashed local server is indistinguishable from a protocol EOF, and neither
// the agent nor the user can repair its environment without reproducing the
// launch by hand.
func (manager *Manager) diagnosed(id string, epoch uint64, err error) error {
	if err == nil {
		return nil
	}
	manager.mu.RLock()
	var tail *stderrTail
	var config captypes.ConnectionConfig
	var secrets map[string]struct{}
	if entry := manager.connections[id]; entry != nil && entry.stderr != nil && entry.stderrEpoch == epoch {
		tail, config, secrets = entry.stderr, entry.config, maps.Clone(entry.secrets)
	}
	manager.mu.RUnlock()
	text := tail.Text(250 * time.Millisecond)
	if text == "" {
		return err
	}
	if len(text) > diagnosticTailBytes {
		text = "…" + strings.ToValidUTF8(text[len(text)-diagnosticTailBytes:], "")
	}
	return fmt.Errorf("%w; server stderr (tail): %s", err, redactSecrets(config, secrets, manager.resolveSecret, text))
}

func (manager *Manager) session(id string) (*mcp.ClientSession, captypes.ConnectionConfig, uint64, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	entry := manager.connections[id]
	if manager.closed || entry == nil || entry.session == nil || entry.config.Disabled {
		return nil, captypes.ConnectionConfig{}, 0, errors.New("MCP connection is not connected")
	}
	return entry.session, entry.config, entry.epoch, nil
}

func (manager *Manager) Refresh(ctx context.Context, id string) error {
	session, config, epoch, err := manager.session(id)
	if err != nil {
		return err
	}
	seconds := config.TimeoutSeconds
	if seconds <= 0 {
		seconds = 30
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	state := captypes.ConnectionState{ID: id, State: "connected"}
	outputSchemas := make(map[string]*jsonschema.Resolved)
	manager.mu.RLock()
	var builtinEffects map[string]captypes.Effect
	if entry := manager.connections[id]; entry != nil && entry.epoch == epoch {
		builtinEffects = entry.builtinEffects
	}
	manager.mu.RUnlock()
	cursor := ""
	seen := make(map[string]bool)
	pages := 0
	capabilities := session.InitializeResult().Capabilities
	if capabilities != nil && capabilities.Tools != nil {
		for {
			pages++
			if pages > maxDiscovery {
				return errors.New("MCP tool discovery exceeded the bounded page budget")
			}
			page, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
			if err != nil {
				return manager.safeError(config, err)
			}
			for _, tool := range page.Tools {
				if len(state.Tools) >= maxDiscovery {
					return errors.New("MCP tool discovery exceeded the bounded catalog")
				}
				schema, err := json.Marshal(tool.InputSchema)
				if err != nil || len(schema) > 64<<10 {
					return fmt.Errorf("tool %q has an invalid or oversized input schema", tool.Name)
				}
				var output json.RawMessage
				var resolved *jsonschema.Resolved
				if tool.OutputSchema != nil {
					output, err = json.Marshal(tool.OutputSchema)
					if err != nil || len(schema)+len(output) > 64<<10 {
						return fmt.Errorf("tool %q has an invalid or oversized output schema", tool.Name)
					}
					resolved, err = resolveOutputSchema(output)
					if err != nil {
						return fmt.Errorf("tool %q has an invalid output schema: %w", tool.Name, err)
					}
				}
				identity, _ := json.Marshal(struct {
					Name, Description string
					Input, Output     json.RawMessage
				}{tool.Name, tool.Description, schema, output})
				fingerprint := digest(identity)
				outputSchemas[fingerprint] = resolved
				projected := captypes.Tool{Name: ToolName(id, tool.Name), OriginalName: tool.Name, ConnectionID: id, Description: tool.Description, InputSchema: schema, OutputSchema: output, Fingerprint: fingerprint, Reason: "Needs explicit effect and scope grant"}
				if grant, found := config.Grants[tool.Name]; found && !grant.Disabled && grant.Fingerprint == fingerprint && grant.Scope != "" && validEffect(grant.Effect) {
					projected.Effect, projected.Scope, projected.Selected, projected.Reason = grant.Effect, grant.Scope, true, ""
				}
				if config.IsBuiltin() {
					// Crude's own server: tools keep their plain names and are
					// usable with their declared effect unless the user turned
					// that tool off.
					projected.Name = tool.Name
					if effect, declared := builtinEffects[tool.Name]; declared && !config.Grants[tool.Name].Disabled {
						projected.Effect, projected.Scope, projected.Selected, projected.Reason = effect, "workspace", true, ""
					} else {
						projected.Selected, projected.Reason = false, "Turned off for this connection"
					}
				}
				state.Tools = append(state.Tools, projected)
			}
			if page.NextCursor == "" {
				break
			}
			if seen[page.NextCursor] {
				return errors.New("MCP discovery returned a repeated cursor")
			}
			seen[page.NextCursor], cursor = true, page.NextCursor
		}
	}
	if capabilities != nil {
		if capabilities.Resources != nil {
			cursor, seen, pages = "", make(map[string]bool), 0
			for {
				pages++
				if pages > maxDiscovery {
					return errors.New("MCP resource discovery exceeded the bounded page budget")
				}
				page, err := session.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
				if err != nil {
					return manager.safeError(config, err)
				}
				for _, resource := range page.Resources {
					if len(state.Resources) >= maxDiscovery {
						return errors.New("MCP resource discovery exceeded the bounded catalog")
					}
					state.Resources = append(state.Resources, captypes.Resource{ConnectionID: id, URI: resource.URI, Name: resource.Name, Description: resource.Description, MIMEType: resource.MIMEType})
				}
				if page.NextCursor == "" {
					break
				}
				if seen[page.NextCursor] {
					return errors.New("MCP resource discovery returned a repeated cursor")
				}
				seen[page.NextCursor], cursor = true, page.NextCursor
			}
		}
		if capabilities.Prompts != nil {
			cursor, seen, pages = "", make(map[string]bool), 0
			for {
				pages++
				if pages > maxDiscovery {
					return errors.New("MCP prompt discovery exceeded the bounded page budget")
				}
				page, err := session.ListPrompts(ctx, &mcp.ListPromptsParams{Cursor: cursor})
				if err != nil {
					return manager.safeError(config, err)
				}
				for _, prompt := range page.Prompts {
					if len(state.Prompts) >= maxDiscovery {
						return errors.New("MCP prompt discovery exceeded the bounded catalog")
					}
					projected := captypes.Prompt{ConnectionID: id, Name: prompt.Name, Description: prompt.Description}
					for _, argument := range prompt.Arguments {
						projected.Arguments = append(projected.Arguments, captypes.PromptArgument{Name: argument.Name, Description: argument.Description, Required: argument.Required})
					}
					state.Prompts = append(state.Prompts, projected)
				}
				if page.NextCursor == "" {
					break
				}
				if seen[page.NextCursor] {
					return errors.New("MCP prompt discovery returned a repeated cursor")
				}
				seen[page.NextCursor], cursor = true, page.NextCursor
			}
		}
	}
	encoded, _ := json.Marshal(state.Tools)
	state.Fingerprint = digest(encoded)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.connections[id]
	if entry == nil || entry.epoch != epoch || entry.session != session {
		return errors.New("MCP discovery was superseded")
	}
	state.Logs = entry.state.Logs
	entry.state, entry.discovered = state, true
	entry.outputSchemas = outputSchemas
	return nil
}

func digest(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func ToolName(connectionID, name string) string {
	if !identityPattern.MatchString(name) {
		name = "tool_" + digest([]byte(name))[:16]
	}
	return "mcp__" + connectionID + "__" + name
}

func validEffect(effect captypes.Effect) bool {
	switch effect {
	case captypes.EffectRead, captypes.EffectWrite, captypes.EffectDelete, captypes.EffectExecute, captypes.EffectCommunication:
		return true
	}
	return false
}

func (manager *Manager) Call(ctx context.Context, tool captypes.Tool, arguments map[string]any) (captypes.Result, error) {
	return manager.call(ctx, tool, arguments, nil)
}

// CallAuthorized uses a task-local grant that the capability owner has already
// checked. Live discovery and the entire connection preimage are still required.
// It never modifies the saved connection or its durable grants.
func (manager *Manager) CallAuthorized(ctx context.Context, tool captypes.Tool, arguments map[string]any, expected captypes.ConnectionConfig, grant captypes.ToolGrant) (captypes.Result, error) {
	return manager.call(ctx, tool, arguments, &taskCallAuthorization{expected: expected, grant: grant})
}

type taskCallAuthorization struct {
	expected captypes.ConnectionConfig
	grant    captypes.ToolGrant
}

func (manager *Manager) call(ctx context.Context, tool captypes.Tool, arguments map[string]any, task *taskCallAuthorization) (captypes.Result, error) {
	session, config, epoch, err := manager.session(tool.ConnectionID)
	if err != nil {
		return captypes.Result{}, err
	}
	manager.mu.RLock()
	entry := manager.connections[tool.ConnectionID]
	currentGrant := false
	var outputSchema *jsonschema.Resolved
	if entry != nil && entry.discovered {
		for _, current := range entry.state.Tools {
			if current.Name == tool.Name && current.Fingerprint == tool.Fingerprint && (task != nil || current.Effect == tool.Effect && current.Scope == tool.Scope && current.Selected) {
				currentGrant = true
				outputSchema = entry.outputSchemas[current.Fingerprint]
				break
			}
		}
	}
	manager.mu.RUnlock()
	if !currentGrant {
		return captypes.Result{}, errors.New("MCP operation changed or discovery is unverified; refresh and review access before execution")
	}
	grant, exists := config.Grants[tool.OriginalName]
	if config.IsBuiltin() && task == nil {
		manager.mu.RLock()
		effect, declared := entry.builtinEffects[tool.OriginalName]
		manager.mu.RUnlock()
		grant, exists = captypes.ToolGrant{Effect: effect, Scope: "workspace", Fingerprint: tool.Fingerprint, Disabled: grant.Disabled}, declared
	}
	if task != nil {
		if !reflect.DeepEqual(task.expected, config) {
			return captypes.Result{}, errors.New("MCP connection changed after task authorization")
		}
		grant, exists = task.grant, true
	}
	if !exists || grant.Disabled || grant.Fingerprint != tool.Fingerprint || grant.Scope != tool.Scope || grant.Effect != tool.Effect || !validEffect(tool.Effect) {
		return captypes.Result{}, errors.New("MCP tool grant is missing, revoked, or changed")
	}
	seconds := config.TimeoutSeconds
	if seconds <= 0 {
		seconds = 30
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool.OriginalName, Arguments: arguments, Meta: mcp.Meta{"progressToken": tool.Name + "/" + fmt.Sprint(time.Now().UnixNano())}})
	if err != nil {
		unknown := tool.Effect != captypes.EffectRead
		return captypes.Result{Unknown: unknown, IsError: true}, manager.safeError(config, manager.diagnosed(tool.ConnectionID, epoch, err))
	}
	projection := newContentProjection(manager, config)
	projection.result.IsError = result.IsError
	var evidenceError error
	for _, item := range result.Content {
		if err := projection.appendContent(item); err != nil {
			if evidenceError == nil {
				evidenceError = err
			}
		}
	}
	if err := projection.structured(result.StructuredContent); err != nil {
		if evidenceError == nil {
			evidenceError = err
		}
	}
	// Validate the received JSON, before credential redaction can change its types.
	// This is local evidence failure after an ACK, never an unknown remote effect.
	if outputSchema != nil {
		if err := validateStructuredOutput(outputSchema, result.StructuredContent); err != nil && evidenceError == nil {
			evidenceError = err
		}
	}
	if evidenceError != nil {
		return projection.acknowledgedFailure(tool.Effect, evidenceError)
	}
	return projection.finish()
}

func (manager *Manager) ReadResource(ctx context.Context, id, uri string) (captypes.Result, error) {
	session, config, _, err := manager.session(id)
	if err != nil {
		return captypes.Result{}, err
	}
	seconds := config.TimeoutSeconds
	if seconds <= 0 {
		seconds = 30
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	result, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		return captypes.Result{}, manager.safeError(config, err)
	}
	projection := newContentProjection(manager, config)
	for _, content := range result.Contents {
		if err := projection.appendResource(content); err != nil {
			return captypes.Result{}, err
		}
	}
	return projection.finish()
}

func (manager *Manager) GetPrompt(ctx context.Context, id, name string, arguments map[string]string) (captypes.Result, error) {
	session, config, _, err := manager.session(id)
	if err != nil {
		return captypes.Result{}, err
	}
	seconds := config.TimeoutSeconds
	if seconds <= 0 {
		seconds = 30
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	result, err := session.GetPrompt(ctx, &mcp.GetPromptParams{Name: name, Arguments: arguments})
	if err != nil {
		return captypes.Result{}, manager.safeError(config, err)
	}
	projection := newContentProjection(manager, config)
	// Roles describe external source messages, never local instruction authority.
	attribution := promptAttribution{ConnectionID: id, Name: name}
	for _, message := range result.Messages {
		if message == nil {
			return captypes.Result{}, errors.New("MCP prompt contains a nil message")
		}
		attribution.Messages = append(attribution.Messages, promptMessageAttribution{Role: string(message.Role), ContentIndex: len(projection.result.Content)})
		if err := projection.appendContent(message.Content); err != nil {
			return captypes.Result{}, err
		}
	}
	if err := projection.structured(attribution); err != nil {
		return captypes.Result{}, err
	}
	return projection.finish()
}

// Disconnect closes a connection at the user's request. Recover leaves it
// closed until something connects it explicitly again.
func (manager *Manager) Disconnect(id string) error { return manager.disconnect(id, true) }

// Restart reconnects a live connection so changed settings or secrets apply.
func (manager *Manager) Restart(ctx context.Context, id string) error {
	if err := manager.disconnect(id, false); err != nil {
		return err
	}
	return manager.Recover(ctx, id)
}

func (manager *Manager) disconnect(id string, park bool) error {
	manager.mu.Lock()
	entry := manager.connections[id]
	if entry == nil {
		manager.mu.Unlock()
		return errors.New("connection is unavailable")
	}
	entry.parked = park
	entry.epoch++
	cancel := entry.cancel
	transport := entry.config.Transport
	session := entry.session
	if session != nil {
		manager.lifetimeTasks.Add(1)
	}
	entry.session = nil
	entry.cancel = nil
	entry.interactiveAuth = false
	entry.state.AuthPending, entry.state.AuthURL = false, ""
	entry.state.State, entry.state.Cached = "not started", hasCatalog(entry.state)
	manager.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if session != nil {
		defer manager.lifetimeTasks.Done()
		err := session.Close()
		var exitError *exec.ExitError
		if cancel != nil && transport == "stdio" && errors.As(err, &exitError) &&
			(exitError.ExitCode() == -1 || runtime.GOOS == "windows" && exitError.ExitCode() == 1) {
			return nil
		}
		return err
	}
	return nil
}

func (manager *Manager) Test(ctx context.Context, config captypes.ConnectionConfig) (captypes.ConnectionState, error) {
	detached := New(manager.root)
	defer detached.Close()
	detached.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := detached.Connect(ctx, config.ID); err != nil {
		return captypes.ConnectionState{}, err
	}
	return detached.States()[0], nil
}

func (manager *Manager) Close() {
	manager.mu.Lock()
	manager.closed = true
	ids := make([]string, 0, len(manager.connections))
	for id := range manager.connections {
		ids = append(ids, id)
	}
	manager.mu.Unlock()
	for _, id := range ids {
		_ = manager.Disconnect(id)
	}
	manager.lifetimeTasks.Wait()
}
