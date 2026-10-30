package facts_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"crude/insight/facts"
	"crude/types"
)

func TestFactByteBudgetRejectsWithoutEvictionOrFalseEmpty(t *testing.T) {
	s := store(t, 512, 512)
	message := strings.Repeat("x", facts.MaxTextBytes)
	accepted := 0
	for i := range 512 {
		b := boundary()
		b.Source.DocumentID = fmt.Sprintf("doc-%d", i)
		b.Source.Path = fmt.Sprintf("/project/%d.go", i)
		if err := s.Observe(b); err != nil {
			if !errors.Is(err, facts.ErrLimit) {
				t.Fatal(err)
			}
			break
		}
		p := publication(b, 1)
		p.Diagnostics[0].Message = message
		a, err := s.Publish(p)
		if errors.Is(err, facts.ErrLimit) {
			if a.ID != "" || a.State != types.InsightLimited || !a.Limited {
				t.Fatalf("byte limit manufactured a clean observation: %+v", a)
			}
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		accepted++
	}
	if accepted == 0 || accepted >= facts.MaxRetainedBytes/facts.MaxTextBytes {
		t.Fatalf("global payload bytes not bounded: accepted=%d", accepted)
	}
	first, ok := s.Snapshot("doc-0")
	if !ok || first.ID == "" || first.Diagnostics[0].Message != message {
		t.Fatal("byte rejection evicted retained warnings")
	}
	v := s.Version()
	if !s.InvalidateAll("Context changed.") || s.Version() <= v {
		t.Fatal("context invalidation did not advance source generation")
	}
	if a, ok := s.SnapshotPath("/project/0.go"); !ok || a.ID != first.ID || a.State != types.InsightStale {
		t.Fatal("stale source lookup lost original publication")
	}
	v = s.Version()
	_ = s.All()
	_, _ = s.Snapshot("doc-0")
	if s.Version() != v {
		t.Fatal("reads invalidated facts")
	}
}
