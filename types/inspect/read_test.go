package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestReadBoundedImmutableOwnerPages(t *testing.T) {
	var s Store
	input := Observation{Source: json.RawMessage(`{"process":"core"}`)}
	for i := range 100 {
		input.Records = append(input.Records, Record{Kind: "sample", ID: fmt.Sprint(i), Value: json.RawMessage(`{"label":"` + strings.Repeat(`\u003c`, 80) + `"}`)})
	}
	collects := 0
	page, err := s.Read(t.Context(), "one", Request{View: "events", MaxBytes: 2048}, func(context.Context) (Observation, error) { collects++; return input, nil })
	if err != nil {
		t.Fatal(err)
	}
	input.Records[0].ID = "changed"
	seen := 0
	for {
		raw, _ := json.Marshal(page)
		if len(raw) > 2048 || page.Returned == 0 || page.Offset != seen || page.Total != 100 {
			t.Fatalf("bad page %d/%d bytes=%d", page.Offset, page.Returned, len(raw))
		}
		for _, record := range page.Records {
			if record.ID != fmt.Sprint(seen) {
				t.Fatal("evidence changed", record.ID, seen)
			}
			seen++
		}
		if page.Next == nil {
			break
		}
		if _, err := s.Read(t.Context(), "other", *page.Next, nil); err == nil {
			t.Fatal("foreign owner accepted")
		}
		page, err = s.Read(t.Context(), "one", *page.Next, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if seen != 100 || collects != 1 || !page.Complete {
		t.Fatal("incomplete immutable read", seen, collects)
	}
	if _, err := s.Read(t.Context(), "one", Request{SnapshotID: page.SnapshotID, Offset: 101}, nil); err == nil {
		t.Fatal("out of bounds accepted")
	}
}

func TestReadExpiryEvictionAndBoundedRetention(t *testing.T) {
	var s Store
	collect := func(context.Context) (Observation, error) {
		return Observation{Records: []Record{{ID: "first", Value: json.RawMessage(`1`)}, {ID: "second", Value: json.RawMessage(`2`)}}}, nil
	}
	page, err := s.Read(t.Context(), "one", Request{Limit: 1}, collect)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	old := s.items[page.SnapshotID]
	old.at = time.Now().Add(-retention)
	s.items[page.SnapshotID] = old
	s.mu.Unlock()
	if _, err := s.Read(t.Context(), "one", *page.Next, nil); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatal("expired evidence admitted", err)
	}
	for range maxOwnerSnapshots + 2 {
		if _, err := s.Read(t.Context(), "one", Request{}, collect); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) != maxOwnerSnapshots || s.bytes > maxRetainedBytes {
		t.Fatal("retention exceeded", len(s.items), s.bytes)
	}
	for _, item := range s.items {
		if time.Since(item.at) >= retention {
			t.Fatal("expired evidence retained after collection")
		}
	}
}

func TestReadRejectsOversizeAndBadRanges(t *testing.T) {
	var s Store
	for _, req := range []Request{{Offset: -1}, {Offset: 1}, {Limit: 65}, {MaxBytes: 999}, {MaxBytes: MaxBytes + 1}} {
		if _, err := s.Read(t.Context(), "one", req, nil); err == nil {
			t.Fatalf("accepted %+v", req)
		}
	}
	_, err := s.Read(t.Context(), "one", Request{MaxBytes: 1024}, func(context.Context) (Observation, error) {
		raw, _ := json.Marshal(strings.Repeat("x", 2048))
		return Observation{Records: []Record{{ID: "large", Value: raw}}}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "record") {
		t.Fatal(err)
	}
	// A byte-bound rejection must not force recollection of changing evidence.
	const marker = "arguments.page="
	start := strings.Index(err.Error(), marker)
	if start < 0 {
		t.Fatal("oversize result omitted an exact recovery request", err)
	}
	end := strings.Index(err.Error()[start+len(marker):], ". If")
	if end < 0 {
		t.Fatal("oversize result omitted an exact recovery request", err)
	}
	var retry Request
	if json.Unmarshal([]byte(err.Error()[start+len(marker):start+len(marker)+end]), &retry) != nil {
		t.Fatal("bad recovery request", err)
	}
	if page, err := s.Read(t.Context(), "one", retry, nil); err != nil || page.Returned != 1 {
		t.Fatal("same observation unavailable after byte rejection", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Read(ctx, "one", Request{}, func(context.Context) (Observation, error) {
		t.Fatal("cancelled collector ran")
		return Observation{}, nil
	}); err == nil {
		t.Fatal("cancelled accepted")
	}
}
