package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

type progressTimeouts struct {
	Startup time.Duration
	Floor   time.Duration
	Ceiling time.Duration
	Margin  time.Duration
}

var defaultProgressTimeouts = progressTimeouts{
	Startup: 60 * time.Second,
	Floor:   45 * time.Second,
	Ceiling: 5 * time.Minute,
	Margin:  5 * time.Second,
}

// ProgressStallError reports an established HTTP attempt whose positive byte
// count stopped advancing. It is retryable and distinct from a total-duration
// timeout: slow transfers that keep moving remain alive.
type ProgressStallError struct {
	Phase   string
	Bytes   int64
	Silence time.Duration
}

func (e *ProgressStallError) Error() string {
	return fmt.Sprintf("HTTP %s stalled after %d bytes with no progress for %s", e.Phase, e.Bytes, e.Silence.Round(time.Millisecond))
}

type progressEstimator struct {
	initialized bool
	smoothed    time.Duration
	variation   time.Duration
	policy      progressTimeouts
}

func (e *progressEstimator) observe(sample time.Duration) {
	if sample <= 0 {
		return
	}
	if !e.initialized {
		e.smoothed = sample
		e.variation = sample / 2
		e.initialized = true
		return
	}
	delta := sample - e.smoothed
	e.smoothed += delta / 8
	if delta < 0 {
		delta = -delta
	}
	e.variation += (delta - e.variation) / 4
}

func (e progressEstimator) timeout() time.Duration {
	timeout := e.policy.Startup
	if e.initialized {
		timeout = e.smoothed + 4*e.variation + e.policy.Margin
	}
	return min(e.policy.Ceiling, max(e.policy.Floor, timeout))
}

type progressWatch struct {
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelCauseFunc
	phase     string
	bytes     int64
	last      time.Time
	estimator progressEstimator
	wake      chan struct{}
	done      chan struct{}
	stopOnce  sync.Once
}

func newProgressWatch(parent context.Context, policy progressTimeouts) (*progressWatch, context.Context) {
	if policy.Startup <= 0 {
		policy = defaultProgressTimeouts
	}
	if policy.Floor <= 0 {
		policy.Floor = defaultProgressTimeouts.Floor
	}
	if policy.Ceiling < policy.Floor {
		policy.Ceiling = max(defaultProgressTimeouts.Ceiling, policy.Floor)
	}
	ctx, cancel := context.WithCancelCause(parent)
	watch := &progressWatch{
		ctx: ctx, cancel: cancel, phase: "upload", last: time.Now(),
		estimator: progressEstimator{policy: policy},
		wake:      make(chan struct{}, 1), done: make(chan struct{}),
	}
	go watch.run()
	return watch, ctx
}

func (w *progressWatch) run() {
	timer := time.NewTimer(w.currentTimeout())
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			w.mu.Lock()
			silence := time.Since(w.last)
			timeout := w.estimator.timeout()
			phase, bytes := w.phase, w.bytes
			w.mu.Unlock()
			if silence >= timeout {
				w.cancel(&ProgressStallError{Phase: phase, Bytes: bytes, Silence: silence})
				return
			}
			timer.Reset(max(time.Millisecond, timeout-silence))
		case <-w.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(w.currentTimeout())
		case <-w.done:
			return
		case <-w.ctx.Done():
			return
		}
	}
}

func (w *progressWatch) currentTimeout() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.estimator.timeout()
}

func (w *progressWatch) setPhase(phase string) {
	w.mu.Lock()
	w.phase = phase
	w.bytes = 0
	w.last = time.Now()
	w.estimator.initialized = false
	w.estimator.smoothed = 0
	w.estimator.variation = 0
	w.mu.Unlock()
	w.signal()
}

func (w *progressWatch) progress(bytes int64) {
	w.mu.Lock()
	if bytes <= w.bytes {
		w.mu.Unlock()
		return
	}
	now := time.Now()
	w.estimator.observe(now.Sub(w.last))
	w.last = now
	w.bytes = bytes
	w.mu.Unlock()
	w.signal()
}

func (w *progressWatch) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *progressWatch) stop() {
	w.stopOnce.Do(func() {
		close(w.done)
		w.cancel(context.Canceled)
	})
}

type watchedReadCloser struct {
	inner io.ReadCloser
	watch *progressWatch
	ctx   context.Context
	read  int64
}

type watchedUploadReader struct {
	inner io.Reader
	watch *progressWatch
	total int64
	read  int64
}

func (r *watchedUploadReader) Read(buffer []byte) (int, error) {
	n, err := r.inner.Read(buffer)
	if n > 0 {
		r.read += int64(n)
		r.watch.progress(r.read)
	}
	if err == io.EOF || (r.total > 0 && r.read >= r.total) {
		r.watch.setPhase("response headers")
	}
	return n, err
}

func (r *watchedReadCloser) Read(buffer []byte) (int, error) {
	n, err := r.inner.Read(buffer)
	if n > 0 {
		r.read += int64(n)
		r.watch.progress(r.read)
	}
	if err != nil {
		var stalled *ProgressStallError
		if errors.As(context.Cause(r.ctx), &stalled) {
			return n, stalled
		}
	}
	return n, err
}

func (r *watchedReadCloser) Close() error {
	r.watch.stop()
	return r.inner.Close()
}
