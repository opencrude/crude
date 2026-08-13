package rendering

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// DebugLogEnabled gates the floating-window trace below. It is off by
// default: rendering.Log is called on every window render and appends to
// /tmp/floating_debug.log, which is production hot-path filesystem I/O
// (previously open/write/close per frame at 60 Hz). Set
// CRUDE_DEBUG_FLOATING=1 when actively debugging WM geometry.
var DebugLogEnabled = os.Getenv("CRUDE_DEBUG_FLOATING") != ""

var mu sync.Mutex

func Log(format string, args ...interface{}) {
	if !DebugLogEnabled {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
	f, err := os.OpenFile("/tmp/floating_debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(line)
}
