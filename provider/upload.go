package provider

import (
	"fmt"
	"io"
)

// uploadProgressReader reports upload progress as the request body streams
// to the transport, deduplicated by integer percent.
type uploadProgressReader struct {
	reader     io.Reader
	total      int64
	sent       int64
	lastReport int
	onProgress func(uploaded, total int64)
}

func newUploadProgressReader(reader io.Reader, total int64, onProgress func(uploaded, total int64)) *uploadProgressReader {
	return &uploadProgressReader{reader: reader, total: total, lastReport: -1, onProgress: onProgress}
}

func (r *uploadProgressReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	if n > 0 {
		r.sent += int64(n)
		r.report(r.sent)
	}
	return n, err
}

func (r *uploadProgressReader) report(uploaded int64) {
	// Without a known total there is no meaningful percentage to report.
	if r == nil || r.onProgress == nil || r.total <= 0 {
		return
	}
	uploaded = max(0, min(r.total, uploaded))
	percent := 0
	if r.total > 0 {
		percent = int(uploaded * 100 / r.total)
	}
	if percent == r.lastReport {
		return
	}
	r.lastReport = percent
	r.onProgress(uploaded, r.total)
}

// reportUploadComplete fires the final 100% progress event for a body that
// was fully handed to the transport.
func reportUploadComplete(total int64, onProgress func(uploaded, total int64)) {
	if total <= 0 || onProgress == nil {
		return
	}
	reader := newUploadProgressReader(nil, total, onProgress)
	reader.report(total)
}

// wrapExhausted converts the last attempt error into an ExhaustedError.
func wrapExhausted(cause error, attempts int) error {
	if cause == nil {
		cause = fmt.Errorf("no attempts made")
	}
	if _, exhausted := cause.(*ExhaustedError); exhausted {
		return cause
	}
	return &ExhaustedError{Attempts: attempts, Cause: cause}
}
