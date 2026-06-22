package rendering

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var mu sync.Mutex

func Log(format string, args ...interface{}) {
	mu.Lock()
	defer mu.Unlock()
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
	f, err := os.OpenFile("/tmp/floating_debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(line)
}
