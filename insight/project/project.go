// Package project observes bounded project artifacts as untrusted metadata.
//
// It performs no filesystem access, path resolution, shell tokenization or
// process execution. An observation establishes neither read/execute authority
// nor freshness, a complete build graph, a project-check denominator, or semantic
// success. Selection, source/root identity and lifecycle belong to a future owner.
package project

import (
	"errors"
	"fmt"
)

const (
	// CompilationDatabaseFormat identifies the JSON compilation database format.
	// The upstream format has no embedded schema-version field.
	CompilationDatabaseFormat = "json-compilation-database"
	ImporterVersion           = "1"
)

var (
	ErrInvalid = errors.New("invalid compilation database")
	ErrLimit   = errors.New("compilation database limit exceeded")
)

// Error classifies invalid data/limits without echoing untrusted artifact text.
// Offset is the number of input bytes consumed, not a source line or JSON path.
// Invalid limit configuration has offset zero. Reader and context errors are
// returned wrapped separately and remain discoverable with errors.Is.
type Error struct {
	Kind   error
	Offset int64
	Reason string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at byte %d: %s", e.Kind, e.Offset, e.Reason)
}

func (e *Error) Unwrap() error { return e.Kind }

// Limits are tighter-only overrides of DefaultLimits; zero fields use defaults.
// Negative fields and fields above the default maximum are invalid. Limits count
// bytes, not runes. Depth counts containers (root array = 1, entry object = 2).
//
// MaxAccountedBytes is a conservative, monotonic model/work budget, not measured
// heap/RSS: it includes strings, string staging/copies, entry/argument slice
// capacity including superseded growth, staged entries, duplicate-key indexes
// and discarded extension strings/indexes. Nothing is refunded during a parse.
// Fixed parser/hash/buffer state (a 4 KiB input buffer plus small scalar state),
// Go allocator/GC overhead and caller-owned input or exported copies are outside
// this budget. There is no OS memory or blocking-Read wall-clock guarantee.
type Limits struct {
	MaxInputBytes     int64
	MaxAccountedBytes int64
	MaxEntries        int
	MaxDepth          int
	MaxStringBytes    int
	MaxTokenBytes     int
	MaxArguments      int
	MaxObjectMembers  int
	MaxValues         int
}

// DefaultLimits returns fresh defaults. MaxTokenBytes includes JSON string
// quotes/escapes; MaxStringBytes bounds the decoded UTF-8 string. MaxValues counts
// every JSON value, including skipped extension values and known field values.
func DefaultLimits() Limits {
	return Limits{
		MaxInputBytes: 16 << 20, MaxAccountedBytes: 16 << 20,
		MaxEntries: 100000, MaxDepth: 32, MaxStringBytes: 256 << 10,
		MaxTokenBytes: 1 << 20, MaxArguments: 4096,
		MaxObjectMembers: 256, MaxValues: 1000000,
	}
}

func (l Limits) validated() (Limits, error) {
	d := DefaultLimits()
	fields64 := []struct {
		v   *int64
		max int64
	}{
		{&l.MaxInputBytes, d.MaxInputBytes}, {&l.MaxAccountedBytes, d.MaxAccountedBytes},
	}
	for _, f := range fields64 {
		if *f.v < 0 || *f.v > f.max {
			return Limits{}, &Error{Kind: ErrInvalid, Reason: "invalid limits"}
		}
		if *f.v == 0 {
			*f.v = f.max
		}
	}
	fields := []struct {
		v   *int
		max int
	}{
		{&l.MaxEntries, d.MaxEntries}, {&l.MaxDepth, d.MaxDepth},
		{&l.MaxStringBytes, d.MaxStringBytes}, {&l.MaxTokenBytes, d.MaxTokenBytes},
		{&l.MaxArguments, d.MaxArguments}, {&l.MaxObjectMembers, d.MaxObjectMembers},
		{&l.MaxValues, d.MaxValues},
	}
	for _, f := range fields {
		if *f.v < 0 || *f.v > f.max {
			return Limits{}, &Error{Kind: ErrInvalid, Reason: "invalid limits"}
		}
		if *f.v == 0 {
			*f.v = f.max
		}
	}
	return l, nil
}

// ArtifactIdentity describes the accepted entire input including whitespace.
// SHA256 is not an authorization, producer identity or semantic-content hash.
type ArtifactIdentity struct {
	SHA256          [32]byte
	ObservedBytes   int64
	Format          string
	ImporterVersion string
}

// Entry is a detached row. Strings retain the decoded original spellings;
// paths, wrappers, @response files, plugins and flags remain uninterpreted data.
// Optional presence bits distinguish absent from an explicitly empty string.
type Entry struct {
	Directory    string
	File         string
	Arguments    []string
	Command      string
	Output       string
	HasArguments bool
	HasCommand   bool
	HasOutput    bool
}

// Invocation is an engine-use projection, NOT permission or a launch contract.
// Command is retained even when Arguments are preferred. Dialect is always
// unknown: neither host OS nor path spellings establish the producer's shell.
type Invocation struct {
	Arguments     []string
	Command       string
	UseArguments  bool
	Normalization string
	Dialect       string
}

const (
	NormalizationArgvPreserved = "argv-preserved"
	NormalizationUnknown       = "unknown"
	DialectUnknown             = "unknown"
)

// PreferredInvocation prefers the supplied argv, without normalizing it, and
// deep-copies its slice. Command-only rows stay opaque and un-tokenized.
func (e Entry) PreferredInvocation() Invocation {
	n := NormalizationUnknown
	if e.HasArguments {
		n = NormalizationArgvPreserved
	}
	return Invocation{
		Arguments: cloneStrings(e.Arguments), Command: e.Command,
		UseArguments: e.HasArguments, Normalization: n, Dialect: DialectUnknown,
	}
}

// CompilationDatabase has private immutable storage. Copies of this value may
// share that storage safely; every exported row/projection is detached.
// The zero value has no accepted artifact identity and no entries.
type CompilationDatabase struct {
	artifact       ArtifactIdentity
	entries        []Entry
	accountedBytes int64
}

func (d CompilationDatabase) Artifact() ArtifactIdentity { return d.artifact }
func (d CompilationDatabase) Len() int                   { return len(d.entries) }

// AccountedBytes reports the conservative parse/model work charge, including
// discarded staging/indexes, NOT the exact size of the surviving database.
func (d CompilationDatabase) AccountedBytes() int64 { return d.accountedBytes }

// Entries returns all configurations in input order, including repeated files.
// This explicitly requested full copy is outside the parser's memory budget.
func (d CompilationDatabase) Entries() []Entry {
	out := make([]Entry, len(d.entries))
	for i, e := range d.entries {
		out[i] = cloneEntry(e)
	}
	return out
}

// Entry returns a detached row without exporting/copying the whole database.
func (d CompilationDatabase) Entry(index int) (Entry, bool) {
	if index < 0 || index >= len(d.entries) {
		return Entry{}, false
	}
	return cloneEntry(d.entries[index]), true
}

func cloneEntry(e Entry) Entry {
	e.Arguments = cloneStrings(e.Arguments)
	return e
}

func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}
