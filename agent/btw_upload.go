package agent

import (
	"context"
	"errors"
	"sync"
)

const btwUploadRestartPercent int64 = 90

var errBtwUploadRestart = errors.New("restart provider upload with queued BTW messages")

// btwUploadController owns request-local BTW coalescing while one provider
// request is being uploaded. It never consumes BTW ownership itself: the agent
// loop admits queued messages only after the interrupted request has stopped.
type btwUploadController struct {
	mu sync.Mutex

	cancel context.CancelCauseFunc
	queued []BtwMessage

	uploaded         int64
	total            int64
	accepted         bool
	outputStarted    bool
	restartRequested bool
}

func newBtwUploadController(cancel context.CancelCauseFunc) *btwUploadController {
	return &btwUploadController{cancel: cancel}
}

func (c *btwUploadController) Queue(message BtwMessage) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.queued = append(c.queued, message)
	restart := c.requestRestartLocked()
	c.mu.Unlock()
	c.cancelForRestart(restart)
}

func (c *btwUploadController) Progress(uploaded, total int64) (outputStarted, restarting bool) {
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

func (c *btwUploadController) Accepted() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.accepted = true
	c.mu.Unlock()
}

func (c *btwUploadController) OutputStarted() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.outputStarted = true
	c.mu.Unlock()
}

func (c *btwUploadController) RestartRequested() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.restartRequested
}

func (c *btwUploadController) TakeQueued() []BtwMessage {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	queued := append([]BtwMessage(nil), c.queued...)
	c.queued = nil
	return queued
}

func (c *btwUploadController) requestRestartLocked() bool {
	if c.restartRequested || c.accepted || c.outputStarted || len(c.queued) == 0 {
		return false
	}
	if c.total > 0 && c.uploaded*100 > c.total*btwUploadRestartPercent {
		return false
	}
	c.restartRequested = true
	return true
}

func (c *btwUploadController) cancelForRestart(restart bool) {
	if restart && c.cancel != nil {
		c.cancel(errBtwUploadRestart)
	}
}
