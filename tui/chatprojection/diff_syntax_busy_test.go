package chatprojection

import (
	"testing"
	"time"
)

// View asks DiffSyntaxBusy on every frame. A live sample caught the UI
// goroutine waiting 100 ms in View for the background lexer's lock, so the
// answer must not need that lock.
func TestDiffSyntaxBusyNeverWaitsForTheLexerLock(t *testing.T) {
	diffSyntax.mu.Lock()
	defer diffSyntax.mu.Unlock()
	answered := make(chan bool, 1)
	go func() { answered <- DiffSyntaxBusy() }()
	select {
	case <-answered:
	case <-time.After(time.Second):
		t.Fatal("DiffSyntaxBusy waited for the lexer lock")
	}
}
