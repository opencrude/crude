package facts_test

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"crude/insight/facts"
	"crude/types"
)

func boundary() types.InsightBoundary {
	return types.InsightBoundary{
		Source: types.InsightSource{WorkspaceGeneration: "workspace-1", DocumentID: "doc-1",
			Path: "/project/main.go", Lifetime: "open-1", Revision: 1, Kind: "canonical"},
		Context:  types.InsightContext{ID: "module-linux", Root: "/project", Generation: 1},
		Producer: types.InsightProducer{InstanceID: "server-1", Provider: "gopls", ToolIdentity: "gopls@v1"},
	}
}

func publication(b types.InsightBoundary, sequence uint64) types.InsightDiagnosticPublication {
	v := 7 // LSP versions need not equal canonical revisions.
	return types.InsightDiagnosticPublication{
		Boundary: b, Sequence: sequence, WireVersion: 7, ReportedVersion: &v,
		Diagnostics: []types.LSPDiagnostic{{Line: 2, Col: 3, EndLine: 2, EndCol: 5,
			Message: "undefined: missing", Severity: types.DiagWarning, Source: "compiler", Code: "undefined"}},
	}
}

func store(t *testing.T, documents, diagnostics int) *facts.Store {
	t.Helper()
	s, err := facts.New(documents, diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func observe(t *testing.T, s *facts.Store, b types.InsightBoundary) {
	t.Helper()
	if err := s.Observe(b); err != nil {
		t.Fatal(err)
	}
}

func publish(t *testing.T, s *facts.Store, p types.InsightDiagnosticPublication) types.InsightDiagnostics {
	t.Helper()
	a, err := s.Publish(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func snapshot(t *testing.T, s *facts.Store, id string) types.InsightDiagnostics {
	t.Helper()
	a, ok := s.Snapshot(id)
	if !ok {
		t.Fatal("missing retained snapshot")
	}
	return a
}

func TestExactBoundaryFencesWithoutRelabeling(t *testing.T) {
	changes := map[string]func(*types.InsightBoundary){
		"equal-text ABA revision": func(b *types.InsightBoundary) { b.Source.Revision = 3 },
		"close reopen":            func(b *types.InsightBoundary) { b.Source.Lifetime = "open-2" },
		"rename":                  func(b *types.InsightBoundary) { b.Source.Path = "/project/renamed.go" },
		"workspace replacement":   func(b *types.InsightBoundary) { b.Source.WorkspaceGeneration = "workspace-2" },
		"disk lease": func(b *types.InsightBoundary) {
			b.Source.Kind, b.Source.Lifetime = "disk", "lease-2"
		},
		"context generation": func(b *types.InsightBoundary) { b.Context.Generation = 2 },
		"context identity":   func(b *types.InsightBoundary) { b.Context.ID = "module-windows" },
		"context root":       func(b *types.InsightBoundary) { b.Context.Root = "/other-project" },
		"unknown input":      func(b *types.InsightBoundary) { b.Context.UnknownInputs = []string{"generated header"} },
		"server replacement same provider": func(b *types.InsightBoundary) {
			b.Producer.InstanceID = "server-2"
		},
		"tool replacement": func(b *types.InsightBoundary) { b.Producer.ToolIdentity = "gopls@v2" },
		"provider":         func(b *types.InsightBoundary) { b.Producer.Provider = "other" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			s, old := store(t, 2, 4), boundary()
			observe(t, s, old)
			first := publish(t, s, publication(old, 9))
			next := boundary()
			change(&next)
			observe(t, s, next)
			stale := snapshot(t, s, "doc-1")
			if stale.State != types.InsightStale || stale.ID != first.ID ||
				!reflect.DeepEqual(stale.Boundary, old) ||
				!reflect.DeepEqual(stale.Diagnostics, first.Diagnostics) {
				t.Fatalf("old provenance or warning was lost: %#v", stale)
			}
			if _, err := s.Publish(publication(old, 10)); !errors.Is(err, facts.ErrBoundary) {
				t.Fatalf("retired boundary accepted: %v", err)
			}
			p := publication(next, 1)
			fresh := publish(t, s, p)
			if fresh.ID == first.ID || !reflect.DeepEqual(fresh.Boundary, next) {
				t.Fatalf("new publication identity/provenance missing: %#v", fresh)
			}
			if len(next.Context.UnknownInputs) > 0 && fresh.State == types.InsightCurrent {
				t.Fatal("unknown context became current")
			}
		})
	}
}

func TestLateVersionAndSequenceNeverOverwrite(t *testing.T) {
	s, b := store(t, 1, 4), boundary()
	observe(t, s, b)
	first := publish(t, s, publication(b, 10))
	for _, sequence := range []uint64{0, 9, 10} {
		if _, err := s.Publish(publication(b, sequence)); !errors.Is(err, facts.ErrSequence) {
			t.Fatalf("sequence %d accepted: %v", sequence, err)
		}
	}
	observe(t, s, b) // A duplicate observation must not reset the high-water mark.
	if _, err := s.Publish(publication(b, 1)); !errors.Is(err, facts.ErrSequence) {
		t.Fatal("Observe reset sequence")
	}
	for _, version := range []int{-1, 0, 6, 8} {
		p := publication(b, 11)
		p.ReportedVersion = &version
		if _, err := s.Publish(p); !errors.Is(err, facts.ErrVersion) {
			t.Fatalf("mismatched version %d accepted: %v", version, err)
		}
	}
	p := publication(b, 11)
	p.WireVersion = 0
	if _, err := s.Publish(p); !errors.Is(err, facts.ErrVersion) {
		t.Fatal("nonpositive synchronization version accepted")
	}
	if got := snapshot(t, s, "doc-1"); !reflect.DeepEqual(got, first) {
		t.Fatalf("rejected publication altered retained batch: %#v", got)
	}
}

func TestPendingProvisionalEmptyAndInvalidatedAreDistinct(t *testing.T) {
	s, b := store(t, 1, 4), boundary()
	if _, ok := s.Snapshot("doc-1"); ok {
		t.Fatal("unobserved document exists")
	}
	if _, err := s.Publish(publication(b, 1)); !errors.Is(err, facts.ErrBoundary) {
		t.Fatal("publication without authority accepted")
	}
	observe(t, s, b)
	if pending := snapshot(t, s, "doc-1"); pending.ID != "" || pending.State != types.InsightPending {
		t.Fatalf("pending presented as observed answer: %#v", pending)
	}
	p := publication(b, 1)
	p.ReportedVersion, p.Diagnostics = nil, nil
	unverified := publish(t, s, p)
	if unverified.ID == "" || unverified.State != types.InsightProvisional ||
		unverified.Binding != "unversioned" || unverified.ReportedVersion != nil {
		t.Fatalf("unversioned empty became clean/current: %#v", unverified)
	}
	p = publication(b, 2)
	p.Diagnostics = nil
	empty := publish(t, s, p)
	if empty.ID == "" || empty.ID == unverified.ID || empty.State != types.InsightCurrent ||
		empty.Binding != "exact-version" || empty.Diagnostics == nil || len(empty.Diagnostics) != 0 {
		t.Fatalf("explicit observed zero was lost: %#v", empty)
	}
	for _, reason := range []string{"failed: server exited", "blocked: trust unavailable"} {
		if err := s.Invalidate("doc-1", reason); err != nil {
			t.Fatal(err)
		}
		got := snapshot(t, s, "doc-1")
		if got.ID != empty.ID || got.State != types.InsightStale || got.Detail != reason {
			t.Fatalf("failure erased or relabeled old answer: %#v", got)
		}
		if _, err := s.Publish(publication(b, 3)); !errors.Is(err, facts.ErrBoundary) {
			t.Fatal("invalidated boundary accepted")
		}
		observe(t, s, b)
		if snapshot(t, s, "doc-1").State != types.InsightStale {
			t.Fatal("Observe made retained facts current without a publication")
		}
	}
	if err := s.Invalidate("missing", "closed"); !errors.Is(err, facts.ErrBoundary) {
		t.Fatal("unknown invalidation silently succeeded")
	}
}

func TestBoundsRetainWarningsAndExposeLimits(t *testing.T) {
	if _, err := facts.New(0, 1); !errors.Is(err, facts.ErrInvalid) {
		t.Fatal("invalid document capacity accepted")
	}
	if _, err := facts.New(1, -1); !errors.Is(err, facts.ErrInvalid) {
		t.Fatal("invalid diagnostic capacity accepted")
	}
	s, b := store(t, 2, 2), boundary()
	observe(t, s, b)
	first := publish(t, s, publication(b, 1))
	other := boundary()
	other.Source.DocumentID = "doc-2"
	observe(t, s, other)
	publish(t, s, publication(other, 1))
	third := boundary()
	third.Source.DocumentID = "doc-3"
	if err := s.Observe(third); !errors.Is(err, facts.ErrLimit) {
		t.Fatal("document capacity silently evicted an entry")
	}
	p := publication(b, 2)
	p.Diagnostics = append(p.Diagnostics, p.Diagnostics[0])
	limited, err := s.Publish(p)
	if !errors.Is(err, facts.ErrLimit) || limited.State != types.InsightLimited ||
		!limited.Limited || limited.ID != first.ID ||
		!reflect.DeepEqual(limited.Diagnostics, first.Diagnostics) {
		t.Fatalf("budget rejection lost warnings/limit: %#v, %v", limited, err)
	}
	if _, err := s.Publish(publication(b, 2)); !errors.Is(err, facts.ErrSequence) {
		t.Fatal("capacity rejection did not fence older notifications")
	}
	p = publication(b, 3)
	p.Diagnostics = nil
	publish(t, s, p) // A real empty observation releases only its own allocation.
	p = publication(other, 2)
	p.Diagnostics = append(p.Diagnostics, p.Diagnostics[0])
	if got := publish(t, s, p); len(got.Diagnostics) != 2 {
		t.Fatal("global budget was not released on replacement")
	}
	all := s.All()
	if len(all) != 2 || all[0].Boundary.Source.DocumentID != "doc-1" ||
		all[1].Boundary.Source.DocumentID != "doc-2" {
		t.Fatalf("bounded deterministic inventory lost: %#v", all)
	}
	zero := store(t, 1, 0)
	observe(t, zero, b)
	if got, err := zero.Publish(publication(b, 1)); !errors.Is(err, facts.ErrLimit) ||
		got.ID != "" || got.State != types.InsightLimited {
		t.Fatal("capacity before first batch looked clean")
	}
	p = publication(b, 2)
	p.Diagnostics, p.Limited = nil, true
	if got := publish(t, zero, p); got.State != types.InsightLimited || !got.Limited {
		t.Fatal("producer's limited empty batch became current")
	}
}

func TestValidationSeverityAndWireCoordinates(t *testing.T) {
	invalid := map[string]func(*types.LSPDiagnostic){
		"negative severity": func(d *types.LSPDiagnostic) { d.Severity = -1 },
		"unknown severity":  func(d *types.LSPDiagnostic) { d.Severity = 5 },
		"negative line":     func(d *types.LSPDiagnostic) { d.Line = -1 },
		"negative column":   func(d *types.LSPDiagnostic) { d.Col = -1 },
		"negative end":      func(d *types.LSPDiagnostic) { d.EndCol = -1 },
		"reversed lines":    func(d *types.LSPDiagnostic) { d.EndLine = 1 },
		"reversed columns":  func(d *types.LSPDiagnostic) { d.EndCol = 2 },
		"invalid utf8":      func(d *types.LSPDiagnostic) { d.Message = "\xff" },
	}
	for name, change := range invalid {
		t.Run(name, func(t *testing.T) {
			s, b := store(t, 1, 4), boundary()
			observe(t, s, b)
			first := publish(t, s, publication(b, 1))
			p := publication(b, 2)
			change(&p.Diagnostics[0])
			if _, err := s.Publish(p); !errors.Is(err, facts.ErrInvalid) {
				t.Fatalf("malformed finding accepted: %v", err)
			}
			if got := snapshot(t, s, "doc-1"); !reflect.DeepEqual(first, got) {
				t.Fatal("malformed finding removed previous warning")
			}
		})
	}
	s, b := store(t, 1, 4), boundary()
	observe(t, s, b)
	p := publication(b, 1)
	p.Diagnostics[0].Severity = 0
	p.Diagnostics[0].Message = "😀 undefined"
	got := publish(t, s, p)
	if got.Diagnostics[0].Severity != types.DiagError || p.Diagnostics[0].Severity != 0 ||
		got.Diagnostics[0].Col != 3 || got.Diagnostics[0].EndCol != 5 {
		t.Fatal("omitted severity not normalized once or wire columns converted")
	}
	p = publication(b, 2)
	p.Diagnostics[0].Message = strings.Repeat("x", facts.MaxTextBytes+1)
	if got, err := s.Publish(p); !errors.Is(err, facts.ErrLimit) ||
		got.State != types.InsightLimited || got.Diagnostics[0].Message != "😀 undefined" {
		t.Fatal("oversized finding silently truncated or erased old result")
	}
}

func TestBoundaryValidation(t *testing.T) {
	invalid := []func(*types.InsightBoundary){
		func(b *types.InsightBoundary) { b.Source.Kind = "syntax" },
		func(b *types.InsightBoundary) { b.Source.Revision = 0 },
		func(b *types.InsightBoundary) { b.Source.Lifetime = "" },
		func(b *types.InsightBoundary) { b.Producer.InstanceID = "" },
		func(b *types.InsightBoundary) { b.Context.Generation = 0 },
		func(b *types.InsightBoundary) { b.Context.Root = "\xff" },
		func(b *types.InsightBoundary) { b.Source.Path = strings.Repeat("p", facts.MaxIdentityBytes+1) },
		func(b *types.InsightBoundary) { b.Context.UnknownInputs = make([]string, facts.MaxUnknownInputs+1) },
	}
	for i, change := range invalid {
		s, b := store(t, 1, 4), boundary()
		change(&b)
		if err := s.Observe(b); err == nil || len(s.All()) != 0 {
			t.Fatalf("invalid boundary %d admitted: %v", i, err)
		}
	}
}

func TestDetachedInputsOutputsAndConcurrentAccess(t *testing.T) {
	s, b := store(t, 1, 4), boundary()
	b.Context.UnknownInputs = []string{"generated input unknown"}
	observe(t, s, b)
	p := publication(b, 1)
	first := publish(t, s, p)
	if first.State != types.InsightProvisional || first.Binding != "exact-version" {
		t.Fatal("unknown context became complete/current")
	}
	// Mutate every caller-owned nested value only after calls return.
	b.Context.UnknownInputs[0] = "caller changed observation"
	p.Diagnostics[0].Message = "caller changed diagnostic"
	*p.ReportedVersion = 99
	first.Boundary.Context.UnknownInputs[0] = "caller changed output"
	first.Diagnostics[0].Message = "caller changed output"
	*first.ReportedVersion = 101
	all := s.All()
	all[0].Boundary.Context.UnknownInputs[0] = "caller changed All"
	all[0].Diagnostics[0].Message = "caller changed All"
	*all[0].ReportedVersion = 102
	want := snapshot(t, s, "doc-1")
	if want.Boundary.Context.UnknownInputs[0] != "generated input unknown" ||
		want.Diagnostics[0].Message != "undefined: missing" || *want.ReportedVersion != 7 {
		t.Fatalf("mutable store memory escaped: %#v", want)
	}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			a, _ := s.Snapshot("doc-1")
			a.Diagnostics[0].Message = "private mutation"
			a.Boundary.Context.UnknownInputs[0] = "private mutation"
			*a.ReportedVersion = -1
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			a := s.All()[0]
			if a.Diagnostics[0].Message != "undefined: missing" || *a.ReportedVersion != 7 {
				t.Error("concurrent reader saw caller mutation")
				return
			}
			// Original inputs remain independently mutable during reads.
			p.Diagnostics[0].Message = "caller owns this"
			b.Context.UnknownInputs[0] = "caller owns this"
			*p.ReportedVersion = i
		}
	}()
	go func() {
		defer wg.Done()
		for i := uint64(2); i < 202; i++ {
			next := publication(want.Boundary, i)
			if _, err := s.Publish(next); err != nil {
				t.Error(err)
				return
			}
			next.Diagnostics[0].Message = "private input after publication"
		}
	}()
	wg.Wait()
}
