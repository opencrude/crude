package editor

import (
	"testing"
	"time"
)

// Exercise the same owned command/completion path as the shell, without making
// production Render block on syntax or maintaining a test-only lexer.
func settleSyntax(tb testing.TB, e *Editor) {
	tb.Helper()
	for attempts := 0; attempts < 4; attempts++ {
		command := e.SyntaxCommand()
		if command == nil {
			if e.syntaxJob == nil {
				return
			}
			tb.Fatal("syntax completion already claimed by another owner")
		}
		job := e.syntaxJob
		select {
		case <-job.done:
		case <-time.After(10 * time.Second):
			tb.Fatal("syntax did not finish")
		}
		command().(SyntaxReadyMsg).Apply()
		if job.generation == e.syntaxGeneration && job.err != nil {
			tb.Fatalf("syntax: %v", job.err)
		}
	}
	tb.Fatal("syntax did not settle")
}

func lineColors(tb testing.TB, e *Editor, line string, width int) []string {
	tb.Helper()
	if e.chromaLexer == nil || e.chromaStyle == nil || line == "" || width <= 0 {
		return nil
	}
	if e.syntaxPlain {
		return make([]string, width)
	}
	e.buf.ReplaceAll(line)
	e.invalidateSyntax()
	settleSyntax(tb, e)
	e.ensureChromaColors(0, 1)
	return e.lineColorMap(0)
}

func closeSyntax(e *Editor) {
	e.Close()
	if done := e.SyntaxDone(); done != nil {
		<-done
	}
}
