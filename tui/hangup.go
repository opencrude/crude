package tui

import (
	"os"
	"os/signal"
	"syscall"
)

// stopOnHangup closes the shell like Quit when its terminal goes away (the
// window is closed). Without this the default SIGHUP action kills the process
// before it detaches, and the core keeps the session as a lost connection
// that blocks an explicit core stop until the reconnect window ends.
// Bubble Tea already turns SIGTERM into Quit.
func (t *TUI) stopOnHangup() {
	hangup := make(chan os.Signal, 1)
	signal.Notify(hangup, syscall.SIGHUP)
	go func() {
		defer signal.Stop(hangup)
		select {
		case <-hangup:
			t.Stop()
		case <-t.doneCh:
		}
	}()
}
