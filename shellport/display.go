package shellport

import "time"

// DisplayPacer accepts the shell's measured presentation interval. It performs
// no I/O on Update: current and future streams use the latest declaration.
type DisplayPacer interface{ SetDisplayInterval(time.Duration) }
