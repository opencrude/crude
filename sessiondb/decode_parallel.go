package sessiondb

import (
	"runtime"
	"sync"
	"sync/atomic"
)

// decodeParallelMinRows keeps small sessions on the calling goroutine, where
// worker startup would cost more than the decode it spreads.
const decodeParallelMinRows = 256

// decodeParallel calls decode once for every index in [0, n). Each call must
// touch only its own index; callers keep SQLite iteration sequential and use
// this for the independent per-row JSON decode that dominates dense resumes.
func decodeParallel(n int, decode func(int)) {
	workers := min(runtime.GOMAXPROCS(0), 8, n/decodeParallelMinRows)
	if workers <= 1 {
		for i := range n {
			decode(i)
		}
		return
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= n {
					return
				}
				decode(i)
			}
		}()
	}
	wg.Wait()
}
