package agent

import (
	"context"
	"errors"
	"sync"
)

const runtimeUploadRestartPercent int64 = 90

var errRuntimeUploadRestart = errors.New("restart provider upload with queued human steering")

// runtimeUploadController owns request-local human-steering coalescing while one
// provider request is being uploaded. It never consumes delivery ownership: the
// agent loop admits queued messages only after the interrupted request stops.
type runtimeUploadController struct {
	mu sync.Mutex

	cancel context.CancelCauseFunc
	queued []RuntimeMessage

	uploaded         int64
	total            int64
	accepted         bool
	outputStarted    bool
	restartRequested bool
}

func newRuntimeUploadController(cancel context.CancelCauseFunc) *runtimeUploadController {
	return &runtimeUploadController{cancel: cancel}
}

func (c *runtimeUploadController) Queue(message RuntimeMessage) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.queued = append(c.queued, message)
	restart := c.requestRestartLocked()
	c.mu.Unlock()
	c.cancelForRestart(restart)
}

func (c *runtimeUploadController) Progress(uploaded, total int64) (outputStarted, restarting bool) {
	if c == nil {
		return false, false
	}
	c.mu.Lock()
	if total > 0 {
		uploaded = max(0, min(uploaded, total))
	}
	c.uploaded = uploaded
	c.total = total
	restart := c.requestRestartLocked()
	outputStarted = c.outputStarted
	restarting = c.restartRequested
	c.mu.Unlock()
	c.cancelForRestart(restart)
	return outputStarted, restarting
}

func (c *runtimeUploadController) Accepted() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.accepted = true
	c.mu.Unlock()
}

func (c *runtimeUploadController) OutputStarted() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.outputStarted = true
	c.mu.Unlock()
}

func (c *runtimeUploadController) RestartRequested() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.restartRequested
}

func (c *runtimeUploadController) TakeQueued() []RuntimeMessage {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	queued := append([]RuntimeMessage(nil), c.queued...)
	c.queued = nil
	return queued
}

func (c *runtimeUploadController) requestRestartLocked() bool {
	if c.restartRequested || c.accepted || c.outputStarted || len(c.queued) == 0 {
		return false
	}
	if c.total > 0 && c.uploaded*100 > c.total*runtimeUploadRestartPercent {
		return false
	}
	c.restartRequested = true
	return true
}

func (c *runtimeUploadController) cancelForRestart(restart bool) {
	if restart && c.cancel != nil {
		c.cancel(errRuntimeUploadRestart)
	}
}
