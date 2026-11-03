package toolstore

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cancelAfterChecks forces cancellation inside a traversal, not just at entry.
type cancelAfterChecks struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelAfterChecks) Err() error {
	c.checks--
	if c.checks == 0 {
		c.cancel()
	}
	return c.Context.Err()
}
func cancelAfter(n int) *cancelAfterChecks {
	ctx, cancel := context.WithCancel(context.Background())
	return &cancelAfterChecks{Context: ctx, cancel: cancel, checks: n}
}

func TestInspectionCancelsWhileStoreLocked(t *testing.T) {
	s := newStore(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := []func(context.Context) error{
		func(ctx context.Context) error { _, _, err := s.List(ctx); return err },
		func(ctx context.Context) error { _, _, err := s.History(ctx, 0, 1); return err },
		func(ctx context.Context) error { _, err := s.Unrecognized(ctx); return err },
		func(ctx context.Context) error { _, err := s.PreviewRemove(ctx, []string{"a"}); return err },
		func(ctx context.Context) error { _, err := s.PreviewRemoveUnused(ctx); return err },
		func(ctx context.Context) error {
			return s.Remove(ctx, RemovalPreview{Tools: []Tool{{ID: "a", Package: "a", Version: "1"}}, Token: "x"}, "user")
		},
	}
	for i, call := range calls {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		err := call(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}

func TestInspectionCancelsDuringTraversal(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 30; i++ {
		if err := os.Mkdir(filepath.Join(s.root, fmt.Sprintf("pkg%d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx := cancelAfter(8)
	defer ctx.cancel()
	if _, _, err := s.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("scan: %v", err)
	}
	ctx = cancelAfter(8)
	defer ctx.cancel()
	if _, err := fingerprintTree(ctx, sha256.New(), s.root); !errors.Is(err, context.Canceled) {
		t.Fatalf("fingerprint: %v", err)
	}
	name := filepath.Join(s.root, historyName)
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		_, _ = fmt.Fprintln(f, `{"kind":"use"}`)
	}
	_ = f.Close()
	ctx = cancelAfter(8)
	defer ctx.cancel()
	if _, _, err := s.History(ctx, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("history: %v", err)
	}
}

func TestHistoryLargePages(t *testing.T) {
	s := newStore(t)
	f, err := os.Create(filepath.Join(s.root, historyName))
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	for i := 0; i < 20000; i++ {
		_, _ = fmt.Fprintf(w, "{\"kind\":\"use\",\"tool_id\":\"%d\"}\n", i)
	}
	_, _ = w.WriteString("{bad\n{\"kind\":")
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	page, total, err := s.History(t.Context(), 9999, 3)
	if err != nil || total != 20000 || len(page) != 3 || page[0].ToolID != "10000" || page[2].ToolID != "9998" {
		t.Fatalf("page %v total %d error %v", page, total, err)
	}
}

type cancelAfterMove struct {
	context.Context
	cancel context.CancelFunc
	path   string
}

func (c *cancelAfterMove) Err() error {
	if _, err := os.Stat(c.path); errors.Is(err, os.ErrNotExist) {
		c.cancel()
	}
	return c.Context.Err()
}

func TestRemoveCancellationAfterMoveRollsBack(t *testing.T) {
	s := newStore(t)
	server := serve(t, map[string][]byte{})
	a := installVersion(t, s, server, "1.0", "one")
	b := installVersion(t, s, server, "2.0", "two")
	if err := s.MarkUsed(a.ID, "/project"); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewRemove(t.Context(), []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAfterMove{Context: base, cancel: cancel, path: preview.Tools[0].Dir}
	if err := s.Remove(ctx, preview, "user"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Remove: %v", err)
	}
	for _, tool := range []Tool{a, b} {
		if _, err := os.Stat(tool.Executable); err != nil {
			t.Fatalf("rollback %s: %v", tool.ID, err)
		}
	}
	fresh, err := s.PreviewRemove(t.Context(), []string{a.ID, b.ID})
	if err != nil || fresh.Token != preview.Token {
		t.Fatalf("preimage changed: %v", err)
	}
	requireEmptyStaging(t, s)
}
