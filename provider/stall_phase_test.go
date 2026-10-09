package provider

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestLateUploadEOFDoesNotRewindDownloadProgress(t *testing.T) {
	watch, _ := newProgressWatch(t.Context(), defaultProgressTimeouts)
	defer watch.stop()
	reader := &watchedUploadReader{inner: strings.NewReader("request"), total: 7, watch: watch}
	if n, err := reader.Read(make([]byte, 7)); n != 7 || err != nil {
		t.Fatalf("initial upload = %d, %v", n, err)
	}
	watch.setPhase("download")
	watch.progress(17)
	watch.mu.Lock()
	last := watch.last
	watch.mu.Unlock()
	// A transport's request-writing goroutine may perform its EOF read after
	// the response-reading goroutine has already delivered headers and bytes.
	if n, err := reader.Read(make([]byte, 1)); n != 0 || err != io.EOF {
		t.Fatalf("terminal upload read = %d, %v", n, err)
	}
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.phase != "download" || watch.bytes != 17 || watch.last != last {
		t.Fatalf("late EOF rewound accepted download: phase=%q bytes=%d last=%s want=%s", watch.phase, watch.bytes, watch.last.Format(time.RFC3339Nano), last.Format(time.RFC3339Nano))
	}
}

func TestLateUploadBytesCannotAdvanceDownloadClock(t *testing.T) {
	watch, _ := newProgressWatch(t.Context(), defaultProgressTimeouts)
	defer watch.stop()
	watch.setPhase("download")
	watch.progressInPhase("download", 3)
	watch.mu.Lock()
	last := watch.last
	watch.mu.Unlock()
	reader := &watchedUploadReader{inner: strings.NewReader("request"), total: 7, watch: watch}
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatal(err)
	}
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.phase != "download" || watch.bytes != 3 || watch.last != last {
		t.Fatal("upload bytes advanced the response progress clock")
	}
}
