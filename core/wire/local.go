package corewire

import (
	"context"
	"net"
	"path/filepath"
	"sync"
)

func canonicalDir(dir string) (string, error) {
	p, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

type ownedListener struct {
	net.Listener
	once    sync.Once
	cleanup func() error
	stop    func() bool
}

func (l *ownedListener) Close() error {
	var err error
	l.once.Do(func() {
		if l.stop != nil {
			l.stop()
		}
		err = l.Listener.Close()
		if e := l.cleanup(); err == nil {
			err = e
		}
	})
	return err
}

func bindListenerContext(ctx context.Context, l *ownedListener) net.Listener {
	// Assign before registering cancellation to avoid racing Close with initialization.
	// The watcher exits on explicit close as well as context cancellation.
	done := make(chan struct{})
	l.stop = func() bool { close(done); return true }
	go func() {
		select {
		case <-ctx.Done():
			_ = l.Close()
		case <-done:
		}
	}()
	return l
}
