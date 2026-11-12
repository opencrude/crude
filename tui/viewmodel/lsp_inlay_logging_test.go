package viewmodel

import (
	"errors"
	"sync"
	"testing"
	"time"

	"crude/workspace"
)

type blockingInlayLogger struct {
	started, release chan struct{}
	startedOnce      sync.Once
}

func (*blockingInlayLogger) Info(string, string, ...any) {}
func (l *blockingInlayLogger) Warn(string, string, ...any) {
	l.startedOnce.Do(func() { close(l.started) })
	<-l.release
}
func (*blockingInlayLogger) Error(string, string, ...any) {}
func (*blockingInlayLogger) Debug(string, string, ...any) {}

type inlayLoggingPort struct {
	*retryInlayPort
	logger workspace.Logger
}

func (p *inlayLoggingPort) Logger() workspace.Logger { return p.logger }

// A logger can block on disk or on its own flush lock. Reporting a failed hint
// must still return from the actual Update and show the actionable error before
// that independent diagnostic sink becomes available.
func TestInlayFailureDoesNotBlockUpdateOnLogger(t *testing.T) {
	app, ed, port := inlayRetryFixture(t, nil)
	app.resetInlayHints()
	logger := &blockingInlayLogger{started: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	unblock := func() { once.Do(func() { close(logger.release) }) }
	defer unblock()
	app.ws = &inlayLoggingPort{retryInlayPort: port, logger: logger}
	app.openInlayInspectorCmd()
	reader := app.inlayInspector
	if reader == nil || reader.request == nil {
		t.Fatal("reader not opened")
	}
	request := reader.request
	done := make(chan struct{})
	go func() {
		app.Update(inlayResultMsg{owner: request, err: errors.New("provider unavailable")})
		close(done)
	}()
	select {
	case <-logger.started:
	case <-time.After(5 * time.Second):
		unblock()
		<-done
		t.Fatal("failure was not sent to the logger")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		unblock()
		<-done
		t.Fatal("inlay failure blocked Update on the logger")
	}
	if !reader.shown || reader.screen.InList() || inlayVisualReader(reader) == "" || ed.Value() != "abc\n" || ed.Modified() {
		t.Fatal("failure did not show its reader without changing source")
	}
	unblock()
}
