// Package cmake passively observes caller-supplied CMake File API v1 replies.
//
// It has no filesystem, path resolution, shell, process or network operations.
// JSON references are exact artifact-set keys, never read permissions. Source,
// build, include and executable paths remain uninterpreted producer spellings.
// Parsing proves neither freshness, producer authenticity, native file identity,
// semantic correctness nor complete project/build coverage. It does not select a
// configuration, infer commands, or create another authoritative project model.
//
// Format: https://cmake.org/cmake/help/latest/manual/cmake-file-api.7.html
package cmake

import (
	"errors"
	"io"

	"crude/insight/project"
)

const ImporterVersion = "cmake-file-api-v1/codemodel-v2:1"

var (
	ErrInvalid     = errors.New("invalid CMake observation")
	ErrIncomplete  = errors.New("incomplete CMake artifact set")
	ErrUnsupported = errors.New("unsupported CMake codemodel major version")
	ErrLimit       = errors.New("CMake observation limit exceeded")
)

// Artifact supplies one inert JSON document. Name is an opaque, portable leaf
// identifier (ASCII letters/digits, dot, underscore, dash; not "." or "..").
// Provenance is a caller label, not a verified producer, root, revision or trust.
// Use bytes.NewReader for bytes. Import never closes a reader or starts workers.
type Artifact struct {
	Name, Provenance string
	Reader           io.Reader
}

// Limits are tighter-only overrides; zero selects the default. Byte bounds
// include whitespace. MaxValues is an aggregate JSON-value AND projection-work
// budget, including ignored extension values, object keys and repeated target
// projections. MaxItems bounds each array/object, MaxStringBytes each decoded
// string/key. All documents, even unused supplied artifacts, are validated.
//
// These bounds constrain work and retained data, not exact Go heap/RSS.
// Decoder token allocation is bounded by MaxArtifactBytes before parsing;
// strings are checked before token decoding. Caller input and explicit detached
// exports are outside the retained-data bounds.
type Limits struct {
	MaxTotalBytes, MaxArtifactBytes int
	MaxArtifacts, MaxValues         int
	MaxDepth, MaxItems              int
	MaxStringBytes                  int
}

func DefaultLimits() Limits {
	return Limits{16 << 20, 4 << 20, 128, 200000, 32, 32768, 64 << 10}
}

// Identity is exact over the entire accepted artifact, including whitespace.
// Digest equality grants neither read/execute authority nor freshness.
type Identity struct {
	Name, Provenance string
	Artifact         project.ArtifactIdentity
}

type Version struct{ Major, Minor int }
type Paths struct{ Source, Build string }

// Model is a detached format projection. Indices are scoped to their containing
// configuration/target, not global product IDs. Module is a CMake "project"
// entry, not a language module or a derived semantic identity.
type Model struct {
	Index, Codemodel string
	Version          Version
	Paths            Paths
	Configurations   []Configuration
}

type Configuration struct {
	Name        string
	Directories []Directory
	Modules     []Module
	Targets     []Target
}

type Directory struct {
	Paths
	JSONFile                    string
	ProjectIndex                int
	ParentIndex                 *int
	ChildIndexes, TargetIndexes []int
}

type Module struct {
	Name                           string
	ParentIndex                    *int
	ChildIndexes, DirectoryIndexes []int
	TargetIndexes                  []int
}

type Target struct {
	Name, ID, Type, JSONFile     string
	DirectoryIndex, ProjectIndex int
	Paths                        Paths
	IsGeneratorProvided          *bool
	Sources                      []Source
	SourceGroups                 []SourceGroup
	CompileGroups                []CompileGroup
	Dependencies                 []string
}

type Source struct {
	Path                                string
	CompileGroupIndex, SourceGroupIndex *int
	// Nil means absent, not a guessed non-generated source.
	IsGenerated *bool
	Backtrace   *int
}

type SourceGroup struct {
	Name          string
	SourceIndexes []int
}

type CompileGroup struct {
	Language      string
	SourceIndexes []int
	// Fragments are native-shell strings, NOT argv. Do not join, tokenize,
	// execute or assume the compiler executable/complete command is present.
	Fragments []Fragment
	Defines   []Define
	Includes  []Include
}

type Fragment struct {
	Fragment  string
	Backtrace *int
}
type Define struct {
	Define    string
	Backtrace *int
}
type Include struct {
	Path      string
	IsSystem  *bool
	Backtrace *int
}

// Observation has private immutable storage; copies and concurrent reads are
// safe. Every export is detached, including pointers and raw bytes. Unknown
// fields and unprojected format features survive ONLY in exact raw artifacts.
// All accepted observations remain coverage-incomplete; there is intentionally
// no "current", "clean", "trusted", semantic, inferred-argv or complete flag.
// Zero Observation means no accepted evidence.
type Observation struct {
	model     Model
	artifacts []retained
}

type retained struct {
	identity Identity
	raw      []byte
}

func (o Observation) Identities() []Identity {
	out := make([]Identity, len(o.artifacts))
	for i, a := range o.artifacts {
		out[i] = a.identity
	}
	return out
}

func (o Observation) Artifact(name string) ([]byte, bool) {
	for _, a := range o.artifacts {
		if a.identity.Name == name {
			return append([]byte(nil), a.raw...), true
		}
	}
	return nil, false
}

func ptr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
func copySlice[T any](s []T) []T {
	if s == nil {
		return nil
	}
	return append(make([]T, 0, len(s)), s...)
}

func (o Observation) Snapshot() Model {
	m := o.model
	m.Configurations = copySlice(m.Configurations)
	for i := range m.Configurations {
		c := &m.Configurations[i]
		c.Directories = copySlice(c.Directories)
		for j := range c.Directories {
			d := &c.Directories[j]
			d.ParentIndex = ptr(d.ParentIndex)
			d.ChildIndexes, d.TargetIndexes = copySlice(d.ChildIndexes), copySlice(d.TargetIndexes)
		}
		c.Modules = copySlice(c.Modules)
		for j := range c.Modules {
			p := &c.Modules[j]
			p.ParentIndex = ptr(p.ParentIndex)
			p.ChildIndexes, p.DirectoryIndexes = copySlice(p.ChildIndexes), copySlice(p.DirectoryIndexes)
			p.TargetIndexes = copySlice(p.TargetIndexes)
		}
		c.Targets = copySlice(c.Targets)
		for j := range c.Targets {
			t := &c.Targets[j]
			t.IsGeneratorProvided = ptr(t.IsGeneratorProvided)
			t.Dependencies = copySlice(t.Dependencies)
			t.Sources = copySlice(t.Sources)
			for k := range t.Sources {
				s := &t.Sources[k]
				s.CompileGroupIndex, s.SourceGroupIndex = ptr(s.CompileGroupIndex), ptr(s.SourceGroupIndex)
				s.IsGenerated, s.Backtrace = ptr(s.IsGenerated), ptr(s.Backtrace)
			}
			t.SourceGroups = copySlice(t.SourceGroups)
			for k := range t.SourceGroups {
				t.SourceGroups[k].SourceIndexes = copySlice(t.SourceGroups[k].SourceIndexes)
			}
			t.CompileGroups = copySlice(t.CompileGroups)
			for k := range t.CompileGroups {
				g := &t.CompileGroups[k]
				g.SourceIndexes = copySlice(g.SourceIndexes)
				g.Fragments, g.Defines, g.Includes = copySlice(g.Fragments), copySlice(g.Defines), copySlice(g.Includes)
				for n := range g.Fragments {
					g.Fragments[n].Backtrace = ptr(g.Fragments[n].Backtrace)
				}
				for n := range g.Defines {
					g.Defines[n].Backtrace = ptr(g.Defines[n].Backtrace)
				}
				for n := range g.Includes {
					g.Includes[n].Backtrace, g.Includes[n].IsSystem = ptr(g.Includes[n].Backtrace), ptr(g.Includes[n].IsSystem)
				}
			}
		}
	}
	return m
}
