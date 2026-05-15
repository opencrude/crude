package logger

import (
	"bufio"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// BufferedWriter is a thread-safe, periodically-flushed file writer.
// It batches writes in a 4KB buffer and flushes every 2 seconds
// to minimize I/O overhead during high-frequency logging.
type BufferedWriter struct {
	mu   sync.Mutex
	file *os.File
	buf  *bufio.Writer
	done chan struct{}
}

// NewBufferedWriter creates a buffered writer for the given file path.
func NewBufferedWriter(path string) (*BufferedWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	w := &BufferedWriter{
		file: f,
		buf:  bufio.NewWriterSize(f, 4096),
		done: make(chan struct{}),
	}
	go w.flushLoop()
	return w, nil
}

// Write appends a string to the buffer. Thread-safe.
func (w *BufferedWriter) Write(s string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.WriteString(s)
}

// WriteString appends a string to the buffer. Thread-safe. Alias for Write.
func (w *BufferedWriter) WriteString(s string) {
	w.Write(s)
}

// Flush forces buffered data to disk. Thread-safe.
func (w *BufferedWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Flush()
}

// Close signals the flush goroutine to stop, flushes, and closes the file.
func (w *BufferedWriter) Close() error {
	close(w.done)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Flush()
	return w.file.Close()
}

func (w *BufferedWriter) flushLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.Flush()
		case <-w.done:
			return
		}
	}
}
