// Package transport provides JSON-RPC 2.0 transport for LSP communication.
package transport

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sourcegraph/jsonrpc2"
)

// Connection represents a managed connection to a language server.
type Connection struct {
	conn      *jsonrpc2.Conn
	stream    io.ReadWriteCloser
	transport *Transport
	router    *Router
	logger    *slog.Logger

	// State management
	closed   atomic.Bool
	closeMu  sync.Mutex
	closeErr error

	// Request tracking
	requestMu sync.Mutex
	requests  map[jsonrpc2.ID]chan *Message

	// cancelable numbers requests that CallCancelable can cancel by ID.
	cancelable atomic.Uint64
}

// NewConnection creates a new managed connection.
func NewConnection(ctx context.Context, stream io.ReadWriteCloser, logger *slog.Logger) (*Connection, error) {
	c := &Connection{
		stream:   stream,
		router:   NewRouter(),
		logger:   logger,
		requests: make(map[jsonrpc2.ID]chan *Message),
	}

	// Suppress or redirect jsonrpc2 log messages to our logger.
	// Otherwise, jsonrpc2 might print to stderr and mess with the application
	// view if any.
	stdLogger := log.New(io.Discard, "", 0)
	if logger != nil {
		stdLogger = slog.NewLogLogger(logger.Handler(), slog.LevelDebug)
	}

	// Create JSON-RPC connection
	conn := jsonrpc2.NewConn(
		ctx,
		jsonrpc2.NewBufferedStream(stream, jsonrpc2.VSCodeObjectCodec{}),
		jsonrpc2.HandlerWithError(c.handleRequest),
		jsonrpc2.SetLogger(stdLogger),
	)

	c.conn = conn
	c.transport = NewWithConn(conn)

	return c, nil
}

// Call makes a request to the language server and waits for a response.
func (c *Connection) Call(ctx context.Context, method string, params any, result any) error {
	if c.closed.Load() {
		return fmt.Errorf("connection is closed")
	}

	return c.conn.Call(ctx, method, params, result) //nolint:wrapcheck
}

// CallCancelable is Call for requests whose caller may stop waiting, such as
// a diagnostic pull superseded by a newer document version. It picks its own
// string ID, which never collides with the default numeric sequence, and when
// ctx ends before the response it sends $/cancelRequest for that ID so the
// server can stop the work, as the LSP base protocol describes.
func (c *Connection) CallCancelable(ctx context.Context, method string, params any, result any) error {
	if c.closed.Load() {
		return fmt.Errorf("connection is closed")
	}
	id := jsonrpc2.ID{Str: "cancelable-" + strconv.FormatUint(c.cancelable.Add(1), 10), IsString: true}
	waiter, err := c.conn.DispatchCall(ctx, method, params, jsonrpc2.PickID(id))
	if err != nil {
		return err //nolint:wrapcheck
	}
	err = waiter.Wait(ctx, result)
	if ctx.Err() != nil && !c.closed.Load() {
		notify, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = c.conn.Notify(notify, "$/cancelRequest", map[string]any{"id": id.Str})
		cancel()
	}
	return err //nolint:wrapcheck
}

// Notify sends a notification to the language server.
func (c *Connection) Notify(ctx context.Context, method string, params any) error {
	if c.closed.Load() {
		return fmt.Errorf("connection is closed")
	}

	return c.conn.Notify(ctx, method, params) //nolint:wrapcheck
}

// handleRequest handles incoming requests from the language server.
func (c *Connection) handleRequest(ctx context.Context, _ *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
	if c.logger != nil {
		c.logger.Debug("Handling request", "method", req.Method)
	}

	return c.router.Route(ctx, req)
}

// RegisterHandler registers a handler for a specific method.
func (c *Connection) RegisterHandler(method string, handler Handler) {
	c.router.Handle(method, handler)
}

// RegisterNotificationHandler registers a notification handler.
func (c *Connection) RegisterNotificationHandler(method string, handler NotificationHandler) {
	c.router.HandleNotification(method, handler)
}

// Close closes the connection.
func (c *Connection) Close() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()

	if c.closed.Load() {
		return c.closeErr
	}

	c.closed.Store(true)

	// The upstream reader must be the sole pending-call completion owner.
	// Conn.Close closes pending channels without clearing the map: a response
	// already read when Close races can then send on one of those channels.
	// Closing the stream interrupts I/O and lets the reader finish that response
	// before its EOF path closes the remaining calls. Join that reader boundary.
	if c.stream != nil {
		c.closeErr = c.stream.Close()
	}
	if c.conn != nil {
		<-c.conn.DisconnectNotify()
	}

	// Close any pending requests
	c.requestMu.Lock()
	for _, ch := range c.requests {
		close(ch)
	}
	c.requests = nil
	c.requestMu.Unlock()

	return c.closeErr
}

// IsConnected returns true if the connection is still active.
func (c *Connection) IsConnected() bool {
	return !c.closed.Load()
}
