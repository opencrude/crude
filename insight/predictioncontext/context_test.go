package predictioncontext_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"crude/insight/predictioncontext"
	"crude/provider"
	"crude/types"
)

func exampleInput() predictioncontext.Input {
	source := types.InsightSource{WorkspaceGeneration: "workspace-7", DocumentID: "document-2",
		Path: "/project/example.go", Lifetime: "open-9", Revision: 14, Kind: "canonical"}
	version := 6
	return predictioncontext.Input{Source: source, Language: "go", Text: []byte("before(😀)\r\ncall(PLACE); after\r\n"),
		Caret: len("before(😀)\r\ncall("),
		Diagnostics: types.InsightDiagnostics{ID: "batch-5", Sequence: 5, Binding: "exact-version",
			State: types.InsightCurrent, WireVersion: 6, ReportedVersion: &version,
			Boundary: types.InsightBoundary{Source: source,
				Context: types.InsightContext{ID: "context-3", Root: "/project", Generation: 3,
					UnknownInputs: []string{"inherited-environment", "build-graph"}},
				Producer: types.InsightProducer{InstanceID: "lsp-4", Provider: "gopls", ToolIdentity: "launch-8"}},
			Diagnostics: []types.LSPDiagnostic{{Line: 1, Col: 5, EndLine: 1, EndCol: 10,
				Severity: types.DiagError, Message: "undefined: PLACE", Source: "gopls", Code: "undeclared"}}}}
}

func TestLiteralInsertionContextAndQualifiedFacts(t *testing.T) {
	in := exampleInput()
	c, err := predictioncontext.Build(in, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if c.Language != "go" || c.Prefix != "before(😀)\r\ncall(" || c.Suffix != "PLACE); after\r\n" {
		t.Fatalf("literal insertion context changed: %#v", c)
	}
	if c.Facts == nil || c.Facts.ID != "batch-5" || len(c.Facts.Diagnostics) != 1 ||
		c.Facts.Diagnostics[0].Message != "undefined: PLACE" || c.Facts.Boundary.Source != in.Source {
		t.Fatalf("lost qualified exact fact: %#v", c.Facts)
	}
	if !c.Coverage.Partial || c.Coverage.Diagnostics != "exact-current-partial-context" ||
		c.Coverage.SelectedFacts != 1 || c.Coverage.ObservedFacts != 1 {
		t.Fatalf("false coverage: %#v", c.Coverage)
	}
	for _, missing := range []string{"enclosing-scope", "imports", "signatures", "recent-edits", "project-coverage"} {
		if !slices.Contains(c.MissingInputs, missing) || !strings.Contains(c.FactsJSON, `"`+missing+`"`) {
			t.Errorf("missing owner %q is not explicit", missing)
		}
	}
	if !strings.Contains(c.FactsJSON, "build-graph") || !json.Valid([]byte(c.JSON)) ||
		!json.Valid([]byte(c.FactsJSON)) {
		t.Fatal("invalid or incomplete fact metadata encoding")
	}
	var decoded predictioncontext.Context
	if err := json.Unmarshal([]byte(c.JSON), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Prefix != "before(😀)\r\ncall(" || decoded.Suffix != "PLACE); after\r\n" {
		t.Fatal("canonical JSON lost literal UTF-8/CRLF")
	}
}

func TestRejectUnassociatedOrUnknownDiagnosticEvidence(t *testing.T) {
	tests := []struct {
		name   string
		change func(*types.InsightDiagnostics)
		status string
	}{
		{"no-batch", func(d *types.InsightDiagnostics) { d.ID = "" }, "unavailable"},
		{"path", func(d *types.InsightDiagnostics) { d.Boundary.Source.Path = "/other.go" }, "foreign-source"},
		{"document", func(d *types.InsightDiagnostics) { d.Boundary.Source.DocumentID = "other-document" }, "foreign-source"},
		{"workspace", func(d *types.InsightDiagnostics) { d.Boundary.Source.WorkspaceGeneration = "workspace-8" }, "foreign-source"},
		{"reopened-equal-text", func(d *types.InsightDiagnostics) { d.Boundary.Source.Lifetime = "open-10" }, "foreign-source"},
		{"aba-undo-equal-text", func(d *types.InsightDiagnostics) { d.Boundary.Source.Revision = 12 }, "foreign-source"},
		{"stale", func(d *types.InsightDiagnostics) { d.State = types.InsightStale }, "not-current"},
		{"provisional", func(d *types.InsightDiagnostics) { d.State = types.InsightProvisional }, "not-current"},
		{"unknown", func(d *types.InsightDiagnostics) { d.State = "unknown" }, "not-current"},
		{"limited", func(d *types.InsightDiagnostics) { d.Limited = true }, "limited"},
		{"unversioned", func(d *types.InsightDiagnostics) { d.Binding = "unversioned" }, "unverified-version"},
		{"missing-version", func(d *types.InsightDiagnostics) { d.ReportedVersion = nil }, "unverified-version"},
		{"wrong-version", func(d *types.InsightDiagnostics) { *d.ReportedVersion = 5 }, "unverified-version"},
		{"missing-sequence", func(d *types.InsightDiagnostics) { d.Sequence = 0 }, "unqualified"},
		{"missing-context", func(d *types.InsightDiagnostics) { d.Boundary.Context.Generation = 0 }, "unqualified"},
		{"missing-producer", func(d *types.InsightDiagnostics) { d.Boundary.Producer.InstanceID = "" }, "unqualified"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := exampleInput()
			tt.change(&in.Diagnostics)
			c, err := predictioncontext.Build(in, 2000)
			if err != nil {
				t.Fatal(err)
			}
			if c.Facts != nil || strings.Contains(c.FactsJSON, "undefined: PLACE") ||
				!slices.Contains(c.MissingInputs, "diagnostics") || c.Coverage.Diagnostics != tt.status ||
				!c.Coverage.Partial {
				t.Fatalf("unqualified evidence leaked: %#v", c)
			}
		})
	}
}

func TestEmptyExactBatchRemainsPartialNotClean(t *testing.T) {
	in := exampleInput()
	in.Diagnostics.Diagnostics = nil
	c, err := predictioncontext.Build(in, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if c.Facts == nil || c.Facts.ID != "batch-5" || !c.Coverage.Partial ||
		c.Coverage.SelectedFacts != 0 || !slices.Contains(c.MissingInputs, "project-coverage") {
		t.Fatalf("empty batch became unavailable or clean: %#v", c)
	}
}

func TestBudgetIncludesFactAndCoverageEncoding(t *testing.T) {
	in := exampleInput()
	in.Text = bytes.Repeat([]byte("abcdefgh"), 4096)
	in.Caret = 17000
	in.Diagnostics.Diagnostics[0].Line, in.Diagnostics.Diagnostics[0].EndLine = 0, 0
	for _, budget := range []int{250, 400, 1000, 2000} {
		c, err := predictioncontext.Build(in, budget)
		if err != nil {
			t.Fatalf("budget %d: %v", budget, err)
		}
		actual := provider.EstimateTokens(c.JSON, "")
		components := provider.EstimateTokens(c.Language, "") + provider.EstimateTokens(c.Prefix, "") +
			provider.EstimateTokens(c.Suffix, "") + provider.EstimateTokens(c.FactsJSON, "")
		if c.EstimatedTokens != max(actual, components) || c.EstimatedTokens > budget ||
			len(c.JSON) > predictioncontext.MaxContextBytes ||
			len(c.Prefix)+len(c.Suffix)+len(c.FactsJSON)+len(c.Language) > predictioncontext.MaxContextBytes {
			t.Fatalf("budget %d failed serialized accounting: %d/%d/%d", budget, c.EstimatedTokens, actual, components)
		}
		if !strings.HasSuffix(c.Prefix, "abcdefgh") || !strings.HasPrefix(c.Suffix, "abcdefgh") {
			t.Fatalf("budget %d lost near-caret source: %q / %q", budget, c.Prefix, c.Suffix)
		}
	}
	for _, budget := range []int{-1, 0, 1, 10} {
		if _, err := predictioncontext.Build(in, budget); !errors.Is(err, predictioncontext.ErrBudget) {
			t.Fatalf("budget %d admitted unusable metadata: %v", budget, err)
		}
	}
}

func TestHardByteCeilingAndUTF8WindowCuts(t *testing.T) {
	for _, unit := range []string{"😀", "x\r\n", "<>&\x00", "á"} {
		t.Run(unit, func(t *testing.T) {
			in := exampleInput()
			in.Diagnostics = types.InsightDiagnostics{}
			in.Text = []byte(strings.Repeat(unit, 24000))
			in.Caret = len(unit) * 12000
			for _, budget := range []int{400, 2000, int(^uint(0) >> 1)} {
				c, err := predictioncontext.Build(in, budget)
				if err != nil {
					t.Fatal(err)
				}
				if !utf8.ValidString(c.Prefix) || !utf8.ValidString(c.Suffix) ||
					c.Prefix+c.Suffix != string(in.Text[c.Coverage.SourceStart:c.Coverage.SourceEnd]) ||
					len(c.JSON) > predictioncontext.MaxContextBytes || c.EstimatedTokens > budget {
					t.Fatalf("bad literal window or hard limit: %#v", c.Coverage)
				}
				if c.Coverage.SourceStart > 0 &&
					in.Text[c.Coverage.SourceStart-1] == '\r' && in.Text[c.Coverage.SourceStart] == '\n' {
					t.Fatal("prefix begins inside CRLF")
				}
				if c.Coverage.SourceEnd < len(in.Text) &&
					in.Text[c.Coverage.SourceEnd-1] == '\r' && in.Text[c.Coverage.SourceEnd] == '\n' {
					t.Fatal("suffix ends inside CRLF")
				}
			}
		})
	}
}

func TestCaretEdgesAndCRLFInteriorRemainExact(t *testing.T) {
	for _, tt := range []struct {
		caret     int
		pre, post string
	}{{0, "", "a😀\r\nz"}, {5, "a😀", "\r\nz"}, {6, "a😀\r", "\nz"}, {8, "a😀\r\nz", ""}} {
		in := exampleInput()
		in.Text, in.Caret, in.Diagnostics = []byte("a😀\r\nz"), tt.caret, types.InsightDiagnostics{}
		c, err := predictioncontext.Build(in, 2000)
		if err != nil || c.Prefix != tt.pre || c.Suffix != tt.post {
			t.Fatalf("caret %d changed bytes: %q / %q: %v", tt.caret, c.Prefix, c.Suffix, err)
		}
	}
}

func TestInputBoundsBeforeCloneAndSort(t *testing.T) {
	tests := []struct {
		name   string
		change func(*predictioncontext.Input)
		err    error
	}{
		{"source-too-large", func(i *predictioncontext.Input) { i.Text = make([]byte, predictioncontext.MaxSourceBytes+1) }, predictioncontext.ErrLimit},
		{"negative-caret", func(i *predictioncontext.Input) { i.Caret = -1 }, predictioncontext.ErrInvalid},
		{"past-eof", func(i *predictioncontext.Input) { i.Caret = len(i.Text) + 1 }, predictioncontext.ErrInvalid},
		{"mid-emoji", func(i *predictioncontext.Input) { i.Caret = len("before(") + 1 }, predictioncontext.ErrInvalid},
		{"bad-text", func(i *predictioncontext.Input) { i.Text[0] = 0xff }, predictioncontext.ErrInvalid},
		{"disk", func(i *predictioncontext.Input) { i.Source.Kind = "disk" }, predictioncontext.ErrInvalid},
		{"no-lifetime", func(i *predictioncontext.Input) { i.Source.Lifetime = "" }, predictioncontext.ErrInvalid},
		{"no-revision", func(i *predictioncontext.Input) { i.Source.Revision = 0 }, predictioncontext.ErrInvalid},
		{"identity", func(i *predictioncontext.Input) { i.Source.Path = strings.Repeat("x", 4097) }, predictioncontext.ErrLimit},
		{"batch-identity", func(i *predictioncontext.Input) { i.Diagnostics.ID = strings.Repeat("x", 4097) }, predictioncontext.ErrLimit},
		{"bad-identity", func(i *predictioncontext.Input) { i.Source.DocumentID = "\xff" }, predictioncontext.ErrInvalid},
		{"language", func(i *predictioncontext.Input) { i.Language = strings.Repeat("x", 129) }, predictioncontext.ErrLimit},
		{"bad-language", func(i *predictioncontext.Input) { i.Language = "\xff" }, predictioncontext.ErrInvalid},
		{"reason", func(i *predictioncontext.Input) { i.Diagnostics.Detail = strings.Repeat("x", 513) }, predictioncontext.ErrLimit},
		{"unknown-reason", func(i *predictioncontext.Input) {
			i.Diagnostics.Boundary.Context.UnknownInputs = []string{strings.Repeat("x", 513)}
		}, predictioncontext.ErrLimit},
		{"unknown-count", func(i *predictioncontext.Input) { i.Diagnostics.Boundary.Context.UnknownInputs = make([]string, 65) }, predictioncontext.ErrLimit},
		{"fact-count", func(i *predictioncontext.Input) { i.Diagnostics.Diagnostics = make([]types.LSPDiagnostic, 257) }, predictioncontext.ErrLimit},
		{"message", func(i *predictioncontext.Input) { i.Diagnostics.Diagnostics[0].Message = strings.Repeat("x", 4097) }, predictioncontext.ErrLimit},
		{"metadata-total", func(i *predictioncontext.Input) {
			i.Diagnostics.Diagnostics = make([]types.LSPDiagnostic, 33)
			for n := range i.Diagnostics.Diagnostics {
				i.Diagnostics.Diagnostics[n].Message = strings.Repeat("x", 4096)
			}
		}, predictioncontext.ErrLimit},
		{"bad-message", func(i *predictioncontext.Input) { i.Diagnostics.Diagnostics[0].Message = "\xff" }, predictioncontext.ErrInvalid},
		{"range-reversed", func(i *predictioncontext.Input) { i.Diagnostics.Diagnostics[0].EndCol = 1 }, predictioncontext.ErrInvalid},
		{"range-foreign-line", func(i *predictioncontext.Input) { i.Diagnostics.Diagnostics[0].EndLine = 100 }, predictioncontext.ErrInvalid},
		{"range-surrogate-half", func(i *predictioncontext.Input) {
			i.Diagnostics.Diagnostics[0] = types.LSPDiagnostic{Col: 8, EndCol: 9}
		}, predictioncontext.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := exampleInput()
			tt.change(&in)
			if _, err := predictioncontext.Build(in, 2000); !errors.Is(err, tt.err) {
				t.Fatalf("got %v, want %v", err, tt.err)
			}
		})
	}
	huge := exampleInput()
	huge.Text = make([]byte, predictioncontext.MaxSourceBytes+1)
	if n := testing.AllocsPerRun(10, func() { _, _ = predictioncontext.Build(huge, 2000) }); n != 0 {
		t.Fatalf("oversize source allocated before rejection: %g", n)
	}
}

func TestDetachedDeterministicDigestAndBoundedFacts(t *testing.T) {
	in := exampleInput()
	in.Diagnostics.Diagnostics = append(in.Diagnostics.Diagnostics,
		types.LSPDiagnostic{Line: 0, Col: 0, EndCol: 6, Severity: 2, Message: "early literal warning"})
	first, err := predictioncontext.Build(in, 2000)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(in.Diagnostics.Diagnostics)
	slices.Reverse(in.Diagnostics.Boundary.Context.UnknownInputs)
	second, err := predictioncontext.Build(in, 2000)
	if err != nil || first.JSON != second.JSON || first.Digest != second.Digest {
		t.Fatalf("input ordering changed stable selected data: %v", err)
	}
	hash := sha256.Sum256([]byte(first.JSON))
	if first.Digest != hex.EncodeToString(hash[:]) {
		t.Fatal("digest does not cover canonical selected envelope")
	}
	if first.Facts.Diagnostics[0].Message != "early literal warning" {
		t.Fatal("facts not deterministically ordered by literal position")
	}
	in.Text[0] = 'B'
	in.Diagnostics.Diagnostics[0].Message = "mutated"
	in.Diagnostics.Boundary.Context.UnknownInputs[0] = "mutated"
	*in.Diagnostics.ReportedVersion = 99
	if first.Prefix != "before(😀)\r\ncall(" || *first.Facts.ReportedVersion != 6 ||
		first.Facts.Diagnostics[0].Message != "early literal warning" ||
		slices.Contains(first.Facts.Boundary.Context.UnknownInputs, "mutated") {
		t.Fatal("input mutation changed returned context")
	}
	changed, err := predictioncontext.Build(in, 2000)
	if err != nil || changed.Digest == first.Digest {
		t.Fatal("changed selected content did not change digest")
	}
	first.Facts.Diagnostics[0].Message = "output mutation"
	if second.Facts.Diagnostics[0].Message != "early literal warning" {
		t.Fatal("outputs alias each other")
	}
	many := exampleInput()
	many.Diagnostics.Diagnostics = make([]types.LSPDiagnostic, 20)
	for n := range many.Diagnostics.Diagnostics {
		many.Diagnostics.Diagnostics[n] = types.LSPDiagnostic{Message: "literal warning"}
	}
	bounded, err := predictioncontext.Build(many, 4000)
	if err != nil || bounded.Facts == nil || len(bounded.Facts.Diagnostics) != 16 ||
		!bounded.Facts.Limited || bounded.Coverage.SelectedFacts != 16 || bounded.Coverage.ObservedFacts != 20 ||
		!slices.Contains(bounded.MissingInputs, "diagnostics") {
		t.Fatalf("fact selection hid missing coverage: %#v, %v", bounded.Coverage, err)
	}
}

func BenchmarkBuildTwoMiB(b *testing.B) {
	in := exampleInput()
	in.Text = bytes.Repeat([]byte("x"), predictioncontext.MaxSourceBytes)
	in.Caret = len(in.Text) / 2
	in.Diagnostics.Diagnostics[0].Line, in.Diagnostics.Diagnostics[0].EndLine = 0, 0
	b.ReportAllocs()
	b.SetBytes(int64(len(in.Text)))
	b.ResetTimer()
	for b.Loop() {
		if _, err := predictioncontext.Build(in, 2000); err != nil {
			b.Fatal(err)
		}
	}
}
