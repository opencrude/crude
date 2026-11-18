package provider

import (
	"io"
	"strings"
	"testing"
)

func TestUploadProgressReaderReportsMonotonicPercentSteps(t *testing.T) {
	var progress []int64
	reader := newUploadProgressReader(strings.NewReader("0123456789"), 10, func(uploaded, total int64) {
		if total != 10 {
			t.Fatalf("total = %d, want 10", total)
		}
		progress = append(progress, uploaded)
	})
	reader.report(0)
	buffer := make([]byte, 3)
	for {
		_, err := reader.Read(buffer)
		if err != nil {
			break
		}
	}
	reportUploadComplete(10, func(uploaded, total int64) {
		progress = append(progress, uploaded)
	})
	if len(progress) < 3 || progress[0] != 0 || progress[len(progress)-1] != 10 {
		t.Fatalf("progress = %v, want 0..10", progress)
	}
	for index := 1; index < len(progress); index++ {
		if progress[index] < progress[index-1] {
			t.Fatalf("progress went backwards: %v", progress)
		}
	}
}

func TestUploadProgressReaderIgnoresUnknownTotal(t *testing.T) {
	var calls int
	reader := newUploadProgressReader(strings.NewReader("hello"), 0, func(uploaded, total int64) {
		calls++
		if total != 0 {
			t.Fatalf("total = %d, want 0", total)
		}
	})
	if _, err := io.Copy(io.Discard, reader); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if calls != 0 {
		t.Fatalf("progress callbacks = %d, want 0 for unknown total", calls)
	}
}
