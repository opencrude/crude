package lsp

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/transport"
)

func TestProcessCloseHelper(t *testing.T) {
	switch os.Getenv("POWERNAP_CLOSE_HELPER") {
	case "exit-7":
		os.Exit(7)
	case "exit-0":
		os.Exit(0)
	case "1":
	default:
		return
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestServerTransportJoinedClose(t *testing.T) {
	for _, autoClose := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit", true: "cancelled"}[autoClose], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream, err := startServerProcess(ctx, ClientConfig{
				Command: os.Args[0], Args: []string{"-test.run=^TestProcessCloseHelper$"},
				Environment: map[string]string{"POWERNAP_CLOSE_HELPER": "1"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if autoClose {
				cancel()
			}
			for range 2 {
				if err := stream.Close(); err != nil {
					t.Fatalf("expected killed process or closed pipe became a failure: %v", err)
				}
			}
		})
	}
}

func TestServerTransportPreservesNaturalFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := startServerProcess(ctx, ClientConfig{
		Command: os.Args[0], Args: []string{"-test.run=^TestProcessCloseHelper$"},
		Environment: map[string]string{"POWERNAP_CLOSE_HELPER": "exit-7"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// EOF proves the helper exited naturally before teardown requests a kill.
	if _, err := io.ReadAll(stream); err != nil && !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
	err = stream.Close()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("natural exit failure lost: %v", err)
	}
	if again := stream.Close(); !errors.As(again, &exit) {
		t.Fatalf("natural failure not retained: %v", again)
	}
}

func TestClientExitJoinsEvenAfterNotificationFailure(t *testing.T) {
	for _, disconnected := range []bool{false, true} {
		t.Run(map[bool]string{false: "notification-sent", true: "notification-failed"}[disconnected], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r, w := io.Pipe()
			defer func() { _ = w.Close() }()
			want := errors.New("exit teardown failed")
			conn, err := transport.NewConnection(ctx, &closeErrorStream{reader: r, err: want}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if disconnected {
				_ = conn.Close()
			}
			client := &Client{ctx: ctx, cancel: cancel, conn: conn}
			if err := client.Exit(); !errors.Is(err, want) {
				t.Fatalf("Exit discarded joined close error: %v", err)
			}
			if err := client.Kill(); !errors.Is(err, want) {
				t.Fatalf("Kill after Exit discarded original close error: %v", err)
			}
		})
	}
}

func TestMissingServerDoesNotMarkCleanupUnverified(t *testing.T) {
	_, err := NewClient(ClientConfig{Command: os.TempDir() + "/powernap-no-such-server"})
	if err == nil || errors.Is(err, ErrProcessCleanupUnverified) {
		t.Fatalf("ordinary failed start misclassified: %v", err)
	}
}

func startCloseTestProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessCloseHelper$")
	cmd.Env = append(os.Environ(), "POWERNAP_CLOSE_HELPER=1")
	prepareProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

type failingPipe struct {
	err    error
	closes atomic.Int32
}

func (p *failingPipe) Read([]byte) (int, error)    { return 0, io.EOF }
func (p *failingPipe) Write(b []byte) (int, error) { return len(b), nil }
func (p *failingPipe) Close() error {
	p.closes.Add(1)
	return p.err
}

func TestProcessCloserJoinsAndCachesPipeErrors(t *testing.T) {
	cmd := startCloseTestProcess(t)
	tree, err := ownProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tree.stop)
	inErr, outErr, stderrErr := errors.New("stdin close failed"), errors.New("stdout close failed"), errors.New("stderr close failed")
	in, out, stderr := &failingPipe{err: inErr}, &failingPipe{err: outErr}, &failingPipe{err: stderrErr}
	closer := &processCloser{stdin: in, stdout: out, stderr: stderr, tree: tree}
	results := make(chan error, 12)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- closer.Close()
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		for _, want := range []error{inErr, outErr, stderrErr} {
			if !errors.Is(err, want) {
				t.Errorf("Close error %v lost %v", err, want)
			}
		}
		select {
		case <-tree.done:
		default:
			t.Error("Close returned before the leader was joined")
		}
	}
	for _, pipe := range []*failingPipe{in, out, stderr} {
		if got := pipe.closes.Load(); got != 1 {
			t.Errorf("pipe closed %d times", got)
		}
	}
}

type closeErrorStream struct {
	reader *io.PipeReader
	once   sync.Once
	err    error
}

func (s *closeErrorStream) Read(b []byte) (int, error)  { return s.reader.Read(b) }
func (s *closeErrorStream) Write(b []byte) (int, error) { return len(b), nil }
func (s *closeErrorStream) Close() error {
	s.once.Do(func() { _ = s.reader.Close() })
	return s.err
}

func TestClientKillReturnsCachedTransportError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, w := io.Pipe()
	defer func() { _ = w.Close() }()
	want := errors.New("owned transport teardown failed")
	stream := &closeErrorStream{reader: r, err: want}
	conn, err := transport.NewConnection(ctx, stream, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client := &Client{ctx: ctx, cancel: cancel, conn: conn}
	// Check the public contract at runtime so this regression also runs against
	// the former void-returning Kill.
	kill, ok := any(client.Kill).(func() error)
	if !ok {
		t.Fatal("Kill cannot report the owned transport's close outcome")
	}
	// Cancellation/EOF can close the stream before the public teardown call.
	cancel()
	if err := stream.Close(); !errors.Is(err, want) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := kill(); !errors.Is(err, want) {
				t.Errorf("Kill lost original close error: %v", err)
			}
		}()
	}
	wg.Wait()
}
