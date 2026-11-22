package lspclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/transport"
)

// A pipe can have a complete response buffered when its owner closes it. Hold
// that response until Close to deterministically exercise the shutdown race,
// rather than hoping a concurrent response wins a scheduler lottery.
type bufferedShutdownStream struct {
	reader *bytes.Reader
	wrote  chan struct{}
	closed chan struct{}
	write  sync.Once
	close  sync.Once
}

func (s *bufferedShutdownStream) Read(p []byte) (int, error) {
	<-s.closed
	return s.reader.Read(p)
}

func (s *bufferedShutdownStream) Write(p []byte) (int, error) {
	s.write.Do(func() { close(s.wrote) })
	return len(p), nil
}

func (s *bufferedShutdownStream) Close() error {
	s.close.Do(func() { close(s.closed) })
	return nil
}

var _ io.ReadWriteCloser = (*bufferedShutdownStream)(nil)

func TestLSPShutdownSettlesBufferedResponseBeforeDisconnect(t *testing.T) {
	response := `{"jsonrpc":"2.0","id":0,"result":42}`
	stream := &bufferedShutdownStream{
		reader: bytes.NewReader([]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(response), response))),
		wrote:  make(chan struct{}), closed: make(chan struct{}),
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	conn, err := transport.NewConnection(ctx, stream, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	result := make(chan error, 1)
	var value int
	go func() { result <- conn.Call(ctx, "shutdown-race", nil, &value) }()
	select {
	case <-stream.wrote:
	case <-ctx.Done():
		t.Fatal("request was not sent")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil || value != 42 {
		t.Fatalf("buffered response was lost during shutdown: %d %v", value, err)
	}
	if err := conn.Call(ctx, "after-close", nil, &value); err == nil {
		t.Fatal("closed connection accepted another call")
	}
}
