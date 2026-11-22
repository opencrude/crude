package lspclient

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
)

func TestLSPServerEvidenceRetainsStderrAndWarningsAfterStop(t *testing.T) {
	for _, mode := range []string{"evidence", "evidence-fail"} {
		t.Run(mode, func(t *testing.T) {
			l := protocolIntegration(t, mode, t.TempDir())
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			err := l.Start(ctx)
			if (err != nil) != (mode == "evidence-fail") {
				t.Fatalf("start: %v", err)
			}
			if mode == "evidence" && l.ErrorMessage() != "Missing build tool" {
				t.Fatalf("unsolicited error not retained: %q", l.ErrorMessage())
			}
			// Stop joins the stderr drain, so final evidence has a deterministic boundary.
			if err := l.Stop(); err != nil {
				t.Fatal(err)
			}
			got := strings.Join(l.Evidence(), "\n")
			for _, want := range []string{"stderr: server could not load configuration", "window/showMessage error: Missing build tool", "window/logMessage warning: Using incomplete project context"} {
				if !strings.Contains(got, want) {
					t.Fatalf("missing %q in %q", want, got)
				}
			}
			if strings.Contains(got, "normal chatter") {
				t.Fatal("informational chatter retained")
			}
			if l.ErrorMessage() != "" {
				t.Fatal("stopped instance kept a live error message")
			}
			l.evidence.stderr([]byte("old launch marker\n"))
			_ = l.Start(ctx)
			if strings.Contains(strings.Join(l.Evidence(), "\n"), "old launch marker") {
				t.Fatal("new launch inherited old evidence")
			}
		})
	}
}

func TestLSPServerEvidenceIsBoundedDetachedAndLaunchOwned(t *testing.T) {
	e := &serverEvidence{}
	e.stderr([]byte("split"))
	e.stderr([]byte(" line\npartial"))
	if got := e.snapshot(); !slices.Equal(got, []string{"stderr: split line", "stderr: partial"}) {
		t.Fatal(got)
	}
	e.stderr([]byte("\n"))
	for i := range 100 {
		e.stderr([]byte(fmt.Sprintf("line %d %s\n", i, strings.Repeat("😀", 600))))
	}
	e.stderr([]byte(strings.Repeat("x", 100_000)))
	got := e.snapshot()
	n := 0
	for _, line := range got {
		n += len(line)
		if !utf8.ValidString(line) {
			t.Fatal("invalid UTF-8")
		}
	}
	if len(got) > serverEvidenceLines || n > serverEvidenceBytes {
		t.Fatalf("unbounded evidence: %d lines, %d bytes", len(got), n)
	}
	if !strings.Contains(got[len(got)-1], "truncated") {
		t.Fatal("truncation was not marked")
	}
	got[0] = "mutated"
	if e.snapshot()[0] == "mutated" {
		t.Fatal("snapshot aliases the ring")
	}
	// Concurrent reads and notification/stderr producers share only this ring.
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				e.stderr([]byte("message\n"))
				_ = e.snapshot()
			}
		})
	}
	wg.Wait()
}

func TestLSPServerEvidenceDoesNotChangeCommandCapture(t *testing.T) {
	client := &lsp.Client{}
	launch := &lspLaunch{evidence: &serverEvidence{}}
	l := &LSPIntegration{client: client, launch: launch, evidence: launch.evidence, command: &commandCapture{}}
	params := json.RawMessage(`{"type":1,"message":"command explanation"}`)
	l.captureServerMessage(launch, "window/showMessage", params)
	l.captureShowMessage(client, params)
	if !slices.Equal(l.command.messages, []string{"command explanation"}) || l.ErrorMessage() != "" {
		t.Fatal("command message became unsolicited error")
	}
	l.command = nil
	l.captureServerMessage(launch, "window/showMessage", json.RawMessage(`{"type":1,"message":"live error"}`))
	if l.ErrorMessage() != "live error" {
		t.Fatal(l.ErrorMessage())
	}
	old := launch
	l.launch = &lspLaunch{evidence: &serverEvidence{}}
	l.evidence = l.launch.evidence
	l.captureServerMessage(old, "window/showMessage", params)
	if len(l.Evidence()) != 0 || l.ErrorMessage() != "" {
		t.Fatal("retired notification reached a new launch")
	}
}
