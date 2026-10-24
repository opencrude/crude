package perf

import (
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestDisplayCadenceMeasuresWritesNotIdleGaps(t *testing.T) {
	observer := NewLoopObserver()
	if got := observer.DisplayInterval(); got != 16*time.Millisecond {
		t.Fatal(got)
	}
	for _, tc := range []struct {
		duration, want time.Duration
		bytes          int
	}{
		{2 * time.Millisecond, 16 * time.Millisecond, 200},
		{80 * time.Millisecond, 80 * time.Millisecond, 200},
		{time.Second, 80 * time.Millisecond, 0},
		{time.Second, 250 * time.Millisecond, 200},
		{3 * time.Millisecond, 16 * time.Millisecond, 200},
	} {
		observer.ObserveFlush(tea.FlushSample{Duration: tc.duration, Bytes: tc.bytes})
		if got := observer.DisplayInterval(); got != tc.want {
			t.Fatalf("flush=%v bytes=%d got %v want %v", tc.duration, tc.bytes, got, tc.want)
		}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 1000 {
			observer.ObserveFlush(tea.FlushSample{Duration: 30 * time.Millisecond, Bytes: 50})
		}
	}()
	for range 1000 {
		_ = observer.DisplayInterval()
	}
	wg.Wait()
}
