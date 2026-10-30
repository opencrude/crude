package transport

import (
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type errorCloser struct {
	err   error
	calls atomic.Int32
}

func (c *errorCloser) Close() error {
	c.calls.Add(1)
	return c.err
}
func (c *errorCloser) Write(b []byte) (int, error) { return len(b), nil }

func TestStreamCloseCachesBothOwnerAndWriterErrors(t *testing.T) {
	ownerErr, writerErr := errors.New("owner teardown failed"), errors.New("writer teardown failed")
	owner, writer := &errorCloser{err: ownerErr}, &errorCloser{err: writerErr}
	stream := NewStreamTransport(strings.NewReader(""), writer, owner)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := stream.Close()
			if !errors.Is(err, ownerErr) || !errors.Is(err, writerErr) {
				t.Errorf("Close dropped owner or writer outcome: %v", err)
			}
		}()
	}
	wg.Wait()
	if owner.calls.Load() != 1 || writer.calls.Load() != 1 {
		t.Errorf("owner closed %d times, writer %d", owner.calls.Load(), writer.calls.Load())
	}
}

func TestStreamCloseAcceptsAlreadyClosedOwnedWriter(t *testing.T) {
	stream := NewStreamTransport(strings.NewReader(""), &errorCloser{err: os.ErrClosed}, &errorCloser{})
	if err := stream.Close(); err != nil {
		t.Fatalf("owned pipe's expected duplicate close failed: %v", err)
	}
}
