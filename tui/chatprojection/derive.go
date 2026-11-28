package chatprojection

import (
	"runtime"
	"sync"
)

// deriveParallelMin is the number of messages from which a sync derives
// per-message data on several cores. Below it the caller's own pass is
// cheaper than starting goroutines.
const deriveParallelMin = 64

// derivePerMessage runs derive for every index. It is for pure derivations
// of one message each, such as parsing a tool result, while Update waits:
// the messages cannot change meanwhile, and the caller installs the results
// on its own goroutine. From deriveParallelMin messages the work splits
// across cores; opening a dense session brings thousands at once.
func derivePerMessage(count int, derive func(int)) {
	if count < deriveParallelMin {
		for i := range count {
			derive(i)
		}
		return
	}
	workers := min(runtime.GOMAXPROCS(0), (count+deriveParallelMin-1)/deriveParallelMin)
	var wg sync.WaitGroup
	for worker := range workers {
		wg.Go(func() {
			for i := worker; i < count; i += workers {
				derive(i)
			}
		})
	}
	wg.Wait()
}
