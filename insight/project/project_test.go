package project_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"crude/insight/project"
)

const single = `[{"directory":".","file":"src/a.cc","arguments":["cc","-DA=1"]}]`

func parse(t *testing.T, text string) project.CompilationDatabase {
	t.Helper()
	d, err := project.ParseCompilationDatabase(context.Background(), strings.NewReader(text), project.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func requireZero(t testing.TB, d project.CompilationDatabase) {
	t.Helper()
	if d.Len() != 0 || d.AccountedBytes() != 0 || d.Artifact() != (project.ArtifactIdentity{}) {
		t.Fatalf("failure exposed partial database: len=%d artifact=%+v accounted=%d",
			d.Len(), d.Artifact(), d.AccountedBytes())
	}
}

func TestLiteralObservationsPreserveAlternativesAndOpaqueInputs(t *testing.T) {
	const input = `[
 {"directory":"build dir/../debug","file":"../src/雪 space.cc",
  "arguments":["/outside/wrappers/ccache","clang++","-DMSG=hello world","-I../inc dir","@/external/flags.rsp","-Xclang","-load","/external/plugin.so","雪 space.cc",""],
  "output":"objects/generated-looking.o"},
 {"directory":"build dir/../debug","file":"../src/雪 space.cc",
  "arguments":["clang++","-DMSG=incompatible","-DOTHER=1"]},
 {"directory":"C:\\build dir","file":"D:\\external\\a.cc",
  "command":"wrapper.exe clang-cl.exe /DMSG=\"hello world\" @D:\\external\\flags.rsp && never-execute",
  "output":""},
 {"directory":"/external/build","file":"/outside/source.cc",
  "arguments":["wrapper","cc","-fplugin=/outside/plugin.so"],
  "command":"UNTRUSTED_ENV=secret-ish shell-text 'do not tokenize'"},
 {"directory":".","file":"x.cc","command":"cc x.cc",
  "extension":{"target":"not inferred","generated":true,"contextComplete":true,"semanticSuccess":true}}
]`
	d := parse(t, input)
	want := []project.Entry{
		{Directory: "build dir/../debug", File: "../src/雪 space.cc",
			Arguments: []string{"/outside/wrappers/ccache", "clang++", "-DMSG=hello world", "-I../inc dir", "@/external/flags.rsp", "-Xclang", "-load", "/external/plugin.so", "雪 space.cc", ""},
			Output:    "objects/generated-looking.o", HasArguments: true, HasOutput: true},
		{Directory: "build dir/../debug", File: "../src/雪 space.cc",
			Arguments: []string{"clang++", "-DMSG=incompatible", "-DOTHER=1"}, HasArguments: true},
		{Directory: `C:\build dir`, File: `D:\external\a.cc`,
			Command:    `wrapper.exe clang-cl.exe /DMSG="hello world" @D:\external\flags.rsp && never-execute`,
			HasCommand: true, HasOutput: true},
		{Directory: "/external/build", File: "/outside/source.cc",
			Arguments:    []string{"wrapper", "cc", "-fplugin=/outside/plugin.so"},
			Command:      "UNTRUSTED_ENV=secret-ish shell-text 'do not tokenize'",
			HasArguments: true, HasCommand: true},
		{Directory: ".", File: "x.cc", Command: "cc x.cc", HasCommand: true},
	}
	if d.Len() != 5 || !reflect.DeepEqual(d.Entries(), want) {
		t.Fatalf("observations changed:\n got %#v\nwant %#v", d.Entries(), want)
	}
	for i, e := range d.Entries() {
		inv := e.PreferredInvocation()
		if inv.UseArguments != e.HasArguments || inv.Command != e.Command ||
			!reflect.DeepEqual(inv.Arguments, e.Arguments) || inv.Dialect != project.DialectUnknown {
			t.Fatalf("wrong projection for row %d: %+v", i, inv)
		}
		wantNormalization := project.NormalizationUnknown
		if e.HasArguments {
			wantNormalization = project.NormalizationArgvPreserved
		}
		if inv.Normalization != wantNormalization {
			t.Fatalf("guessed command normalization for row %d: %+v", i, inv)
		}
	}
	// Extension assertions are deliberately absent from the observation schema:
	// neither a filename nor a producer's unknown extension certifies context.
	if got := d.Entries()[4]; !reflect.DeepEqual(got, want[4]) {
		t.Fatalf("extension inflated observation: %+v", got)
	}
}

func TestExactArtifactIdentityIncludesAllWhitespaceAndReaderChunks(t *testing.T) {
	inputs := []string{single, " \r\n" + single + "\t  \n", "[]", "[ \n ]\r\n"}
	for _, input := range inputs {
		for _, r := range []io.Reader{
			strings.NewReader(input), iotest.OneByteReader(strings.NewReader(input)),
			iotest.HalfReader(strings.NewReader(input)), iotest.DataErrReader(strings.NewReader(input)),
		} {
			d, err := project.ParseCompilationDatabase(context.Background(), r, project.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			want := project.ArtifactIdentity{
				SHA256: sha256.Sum256([]byte(input)), ObservedBytes: int64(len(input)),
				Format: "json-compilation-database", ImporterVersion: "1",
			}
			if got := d.Artifact(); got != want {
				t.Fatalf("identity = %+v, want %+v", got, want)
			}
		}
	}
	a, b := parse(t, inputs[0]), parse(t, inputs[1])
	if !reflect.DeepEqual(a.Entries(), b.Entries()) || a.Artifact().SHA256 == b.Artifact().SHA256 {
		t.Fatal("identity lost exact whitespace or rows changed")
	}
	d, err := project.ParseCompilationDatabase(context.Background(), strings.NewReader(single),
		project.Limits{MaxInputBytes: int64(len(single))})
	if err != nil || d.Artifact().ObservedBytes != int64(len(single)) {
		t.Fatalf("exact input bound rejected: %v", err)
	}
}

func TestEscapesAndUnknownJSONGrammar(t *testing.T) {
	d := parse(t, `[{"directory":".","file":"雪\u0020\ud83d\ude00.cc",
		"arguments":["c\u0063","quote:\" slash:\/ backslash:\\ controls:\b\f\n\r\t"],
		"extension":{"array":[0,-0,12,-12,0.25,1E20,-0.5e+2,1e-2,true,false,null,{},[],{"a":"b"}]}}]`)
	e, _ := d.Entry(0)
	if e.File != "雪 😀.cc" || e.Arguments[0] != "cc" ||
		e.Arguments[1] != "quote:\" slash:/ backslash:\\ controls:\b\f\n\r\t" {
		t.Fatalf("escapes changed: %+v", e)
	}
	// Literal and escaped duplicate field names must compare AFTER decoding.
	for _, key := range []string{`"file"`, `"f\u0069le"`, `"\u0066ile"`} {
		input := `[{"directory":".","file":"x","command":"cc",` + key + `:"x"}]`
		_, err := project.ParseCompilationDatabase(context.Background(), strings.NewReader(input), project.Limits{})
		if !errors.Is(err, project.ErrInvalid) {
			t.Fatalf("duplicate %s accepted: %v", key, err)
		}
	}
}

func TestMalformedAndUnsafeShapesFailAtomically(t *testing.T) {
	cases := map[string]string{
		"empty": "", "object root": `{}`, "null root": `null`, "scalar entry": `[1]`,
		"no directory":                 `[{"file":"x","command":"cc"}]`,
		"no file":                      `[{"directory":".","command":"cc"}]`,
		"no invocation":                `[{"directory":".","file":"x"}]`,
		"empty directory":              `[{"directory":"","file":"x","command":"cc"}]`,
		"empty file":                   `[{"directory":".","file":"","command":"cc"}]`,
		"empty command":                `[{"directory":".","file":"x","command":""}]`,
		"empty args":                   `[{"directory":".","file":"x","arguments":[]}]`,
		"empty executable":             `[{"directory":".","file":"x","arguments":[""]}]`,
		"null directory":               `[{"directory":null,"file":"x","command":"cc"}]`,
		"number file":                  `[{"directory":".","file":5,"command":"cc"}]`,
		"array command":                `[{"directory":".","file":"x","command":["cc"]}]`,
		"null output":                  `[{"directory":".","file":"x","command":"cc","output":null}]`,
		"null args":                    `[{"directory":".","file":"x","arguments":null}]`,
		"string args":                  `[{"directory":".","file":"x","arguments":"cc x"}]`,
		"object arg":                   `[{"directory":".","file":"x","arguments":["cc",{}]}]`,
		"bad command beside argv":      `[{"directory":".","file":"x","arguments":["cc"],"command":false}]`,
		"equal duplicate":              `[{"directory":".","file":"x","file":"x","command":"cc"}]`,
		"conflicting duplicate":        `[{"directory":".","file":"x","file":"y","command":"cc"}]`,
		"escaped duplicate":            `[{"directory":".","file":"x","f\u0069le":"y","command":"cc"}]`,
		"unknown duplicate":            `[{"directory":".","file":"x","command":"cc","ext":1,"ext":2}]`,
		"nested duplicate":             `[{"directory":".","file":"x","command":"cc","ext":{"x":1,"\u0078":1}}]`,
		"trailing entry comma":         strings.TrimSuffix(single, "]") + ",]",
		"trailing args comma":          `[{"directory":".","file":"x","arguments":["cc",]}]`,
		"trailing object comma":        `[{"directory":".","file":"x","command":"cc",}]`,
		"unknown array trailing comma": `[{"directory":".","file":"x","command":"cc","ext":[1,]}]`,
		"second document":              single + " []",
		"trailing garbage":             single + "not JSON",
		"trailing invalid utf8":        single + string([]byte{0xff}),
		"bad utf8 string":              `[{"directory":".","file":"` + string([]byte{0xff}) + `","command":"cc"}]`,
		"overlong utf8":                `[{"directory":".","file":"` + string([]byte{0xc0, 0xaf}) + `","command":"cc"}]`,
		"bad utf8 extension":           `[{"directory":".","file":"x","command":"cc","ext":"` + string([]byte{0xed, 0xa0, 0x80}) + `"}]`,
		"surrogate high":               `[{"directory":".","file":"\ud800","command":"cc"}]`,
		"surrogate low":                `[{"directory":".","file":"\udfff","command":"cc"}]`,
		"wrong surrogate pair":         `[{"directory":".","file":"\ud800\u0041","command":"cc"}]`,
		"bad hex":                      `[{"directory":".","file":"\u00xz","command":"cc"}]`,
		"bad escape":                   `[{"directory":".","file":"\x20","command":"cc"}]`,
		"raw control":                  "[{\"directory\":\".\",\"file\":\"x\n\",\"command\":\"cc\"}]",
		"missing separator":            `[{"directory":"." "file":"x","command":"cc"}]`,
		"truncated string":             `[{"directory":".","file":"x`,
		"no object key":                `[{:1}]`,
		"numeric object key":           `[{1:2}]`,
		"valid then invalid":           strings.TrimSuffix(single, "]") + `,{"directory":"."}]`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			d, err := project.ParseCompilationDatabase(context.Background(), strings.NewReader(input), project.Limits{})
			if !errors.Is(err, project.ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
			var detail *project.Error
			if !errors.As(err, &detail) || detail.Reason == "" || detail.Offset < 0 {
				t.Fatalf("missing structured error: %v", err)
			}
			requireZero(t, d)
		})
	}
	for _, number := range []string{"01", "-01", ".1", "1.", "1e", "1e+", "+1", "--1", "NaN", "Infinity", "1x", "tru", "falsee", "nul", "undefined"} {
		t.Run("unknown "+number, func(t *testing.T) {
			input := `[{"directory":".","file":"x","command":"cc","ext":` + number + `}]`
			d, err := project.ParseCompilationDatabase(context.Background(), strings.NewReader(input), project.Limits{})
			if !errors.Is(err, project.ErrInvalid) {
				t.Fatalf("unsafe unknown grammar accepted: %v", err)
			}
			requireZero(t, d)
		})
	}
	// Every proper prefix of a nonempty database is incomplete, not successful.
	for i := 0; i < len(single); i++ {
		d, err := project.ParseCompilationDatabase(context.Background(), strings.NewReader(single[:i]), project.Limits{})
		if !errors.Is(err, project.ErrInvalid) {
			t.Fatalf("prefix %d: %v", i, err)
		}
		requireZero(t, d)
	}
}

func TestBoundsCoverKnownAndSkippedMaterialization(t *testing.T) {
	ext := func(v string) string { return `[{"directory":".","file":"x","command":"cc","ext":` + v + `}]` }
	cases := []struct {
		name   string
		input  string
		limits project.Limits
	}{
		{"input", single, project.Limits{MaxInputBytes: int64(len(single) - 1)}},
		{"trailing whitespace input", single + " ", project.Limits{MaxInputBytes: int64(len(single))}},
		{"retained base", "[]", project.Limits{MaxAccountedBytes: 255}},
		{"retained row", single, project.Limits{MaxAccountedBytes: 512}},
		{"entries", strings.TrimSuffix(single, "]") + `,{"directory":".","file":"x","command":"cc"}]`, project.Limits{MaxEntries: 1}},
		{"entry depth", single, project.Limits{MaxDepth: 1}},
		{"argv depth", single, project.Limits{MaxDepth: 2}},
		{"unknown depth", ext(`{"a":[[[null]]]}`), project.Limits{MaxDepth: 5}},
		{"decoded string", ext(`"` + strings.Repeat("a", 41) + `"`), project.Limits{MaxStringBytes: 40}},
		{"decoded utf8 bytes", ext(`"雪雪雪雪"`), project.Limits{MaxStringBytes: 10}},
		{"escaped token", ext(`"` + strings.Repeat(`\u0061`, 8) + `"`), project.Limits{MaxTokenBytes: 40}},
		{"number token", ext(strings.Repeat("1", 41)), project.Limits{MaxTokenBytes: 40}},
		{"argv count", single, project.Limits{MaxArguments: 1}},
		{"members", single, project.Limits{MaxObjectMembers: 2}},
		{"unknown members", ext(`{"a":1,"b":2,"c":3,"d":4,"e":5}`), project.Limits{MaxObjectMembers: 4}},
		{"values", ext(`[0,0,0,0,0,0,0]`), project.Limits{MaxValues: 10}},
		{"literal token", ext(`false`), project.Limits{MaxTokenBytes: 4}},
		{"default depth", ext(strings.Repeat("[", 31) + "0" + strings.Repeat("]", 31)), project.Limits{}},
		{"default string", ext(`"` + strings.Repeat("a", (256<<10)+1) + `"`), project.Limits{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := project.ParseCompilationDatabase(context.Background(), strings.NewReader(tc.input), tc.limits)
			if !errors.Is(err, project.ErrLimit) {
				t.Fatalf("got %v, want ErrLimit", err)
			}
			requireZero(t, d)
		})
	}
	// Known literal values: array + object + directory + file + argv array + 2
	// arguments = 7 values. Keys are NOT values.
	for _, l := range []project.Limits{
		{MaxValues: 7}, {MaxDepth: 3}, {MaxStringBytes: 9}, {MaxObjectMembers: 3}, {MaxArguments: 2},
	} {
		if _, err := project.ParseCompilationDatabase(context.Background(), strings.NewReader(single), l); err != nil {
			t.Fatalf("inclusive exact bound %+v: %v", l, err)
		}
	}
	// A retained-budget success reports charges including skipped values and
	// staging; replay at the exact charge succeeds, one less fails atomically.
	withExtension := ext(`{"index":["discarded payload","other"]}`)
	d := parse(t, withExtension)
	without := parse(t, `[{"directory":".","file":"x","command":"cc"}]`)
	if d.AccountedBytes() <= without.AccountedBytes() {
		t.Fatal("discarded staging/index data went unaccounted")
	}
	for _, n := range []int64{d.AccountedBytes(), d.AccountedBytes() - 1} {
		got, err := project.ParseCompilationDatabase(context.Background(), strings.NewReader(withExtension),
			project.Limits{MaxAccountedBytes: n})
		if n == d.AccountedBytes() {
			if err != nil || got.AccountedBytes() != n {
				t.Fatalf("exact accounting boundary: %v", err)
			}
		} else {
			if !errors.Is(err, project.ErrLimit) {
				t.Fatalf("missing accounted rejection: %v", err)
			}
			requireZero(t, got)
		}
	}
}

func TestInvalidLimitsAndNilInputs(t *testing.T) {
	for _, l := range []project.Limits{
		{MaxInputBytes: -1}, {MaxInputBytes: (16 << 20) + 1},
		{MaxAccountedBytes: -1}, {MaxAccountedBytes: (16 << 20) + 1},
		{MaxEntries: -1}, {MaxEntries: 100001}, {MaxDepth: -1}, {MaxDepth: 33},
		{MaxStringBytes: -1}, {MaxStringBytes: (256 << 10) + 1},
		{MaxTokenBytes: -1}, {MaxTokenBytes: (1 << 20) + 1},
		{MaxArguments: -1}, {MaxArguments: 4097},
		{MaxObjectMembers: -1}, {MaxObjectMembers: 257},
		{MaxValues: -1}, {MaxValues: 1000001},
	} {
		r := &countingReader{r: strings.NewReader(single)}
		d, err := project.ParseCompilationDatabase(context.Background(), r, l)
		if !errors.Is(err, project.ErrInvalid) || r.bytes != 0 {
			t.Fatalf("invalid limits read/accepted: %+v, %v, reads=%d", l, err, r.bytes)
		}
		requireZero(t, d)
	}
	for _, tc := range []struct {
		ctx context.Context
		r   io.Reader
	}{
		{nil, strings.NewReader(single)}, {context.Background(), nil},
	} {
		d, err := project.ParseCompilationDatabase(tc.ctx, tc.r, project.Limits{})
		if !errors.Is(err, project.ErrInvalid) {
			t.Fatalf("nil input: %v", err)
		}
		requireZero(t, d)
	}
}

type countingReader struct {
	r     io.Reader
	bytes int
}

func (r *countingReader) Read(b []byte) (int, error) {
	n, err := r.r.Read(b)
	r.bytes += n
	return n, err
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(b []byte) (int, error) { return f(b) }

// This context cancels its real underlying context at a deterministic check,
// without a timing-dependent goroutine or a reader-induced cancellation.
type cancelOnCheck struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (c *cancelOnCheck) Err() error {
	c.remaining--
	if c.remaining == 0 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestReaderFailuresAndCancellationAreAtomic(t *testing.T) {
	sentinel := errors.New("handcrafted reader failure")
	for _, r := range []io.Reader{
		iotest.ErrReader(sentinel),
		io.MultiReader(strings.NewReader(single), iotest.ErrReader(sentinel)),
		io.MultiReader(strings.NewReader(single[:len(single)/2]), iotest.ErrReader(sentinel)),
		readerFunc(func(b []byte) (int, error) { return copy(b, single), sentinel }),
	} {
		d, err := project.ParseCompilationDatabase(context.Background(), r, project.Limits{})
		if !errors.Is(err, sentinel) {
			t.Fatalf("reader failure hidden: %v", err)
		}
		requireZero(t, d)
	}
	d, err := project.ParseCompilationDatabase(context.Background(), readerFunc(func([]byte) (int, error) {
		return 0, nil
	}), project.Limits{})
	if !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("non-progressing reader: %v", err)
	}
	requireZero(t, d)
	for _, n := range []int{-1, 4097} {
		d, err := project.ParseCompilationDatabase(context.Background(), readerFunc(func([]byte) (int, error) {
			return n, nil
		}), project.Limits{})
		if !errors.Is(err, project.ErrInvalid) {
			t.Fatalf("invalid reader count: %v", err)
		}
		requireZero(t, d)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &countingReader{r: strings.NewReader(single)}
	d, err = project.ParseCompilationDatabase(ctx, r, project.Limits{})
	if !errors.Is(err, context.Canceled) || r.bytes != 0 {
		t.Fatalf("pre-canceled parse performed reads: %v, %d", err, r.bytes)
	}
	requireZero(t, d)
	expired, stop := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer stop()
	d, err = project.ParseCompilationDatabase(expired, strings.NewReader(single), project.Limits{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline lost: %v", err)
	}
	requireZero(t, d)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	d, err = project.ParseCompilationDatabase(ctx, readerFunc(func(b []byte) (int, error) {
		n := copy(b, single)
		cancel() // cancel in the bounded read, before hashing/acceptance
		return n, nil
	}), project.Limits{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("read cancellation hidden: %v", err)
	}
	requireZero(t, d)
	// Midstream cancellation, after a full parser buffer has been processed.
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	long := strings.NewReader(`[{"directory":".","file":"x","command":"cc","extension":"` + strings.Repeat("a", 9000) + `"}]`)
	reads := 0
	d, err = project.ParseCompilationDatabase(ctx, readerFunc(func(b []byte) (int, error) {
		reads++
		n, err := long.Read(b)
		if reads == 2 {
			cancel()
		}
		return n, err
	}), project.Limits{})
	if !errors.Is(err, context.Canceled) || reads != 2 {
		t.Fatalf("midstream cancellation hidden: %v, reads=%d", err, reads)
	}
	requireZero(t, d)
	// Cancellation while consuming already-buffered bytes must not wait for the
	// next Reader.Read. The reader can supply the whole artifact on its first call.
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	buffered := &cancelOnCheck{Context: ctx, cancel: cancel, remaining: 40}
	r = &countingReader{r: strings.NewReader(single)}
	d, err = project.ParseCompilationDatabase(buffered, r, project.Limits{})
	if !errors.Is(err, context.Canceled) || r.bytes != len(single) {
		t.Fatalf("buffered parser cancellation hidden: %v, read bytes=%d", err, r.bytes)
	}
	requireZero(t, d)
}

func TestBoundsStopReadingWithoutMaterializingTheRemainder(t *testing.T) {
	r := &countingReader{r: strings.NewReader(`[{"directory":".","file":"x","command":"` + strings.Repeat("a", 1000000))}
	d, err := project.ParseCompilationDatabase(context.Background(), r, project.Limits{MaxStringBytes: 16})
	if !errors.Is(err, project.ErrLimit) || r.bytes > 4096 {
		t.Fatalf("oversized string consumed unbounded bytes: %v reads=%d", err, r.bytes)
	}
	requireZero(t, d)
	r = &countingReader{r: strings.NewReader(single + strings.Repeat(" ", 100000))}
	d, err = project.ParseCompilationDatabase(context.Background(), r, project.Limits{MaxInputBytes: 20})
	if !errors.Is(err, project.ErrLimit) || r.bytes != 21 {
		t.Fatalf("input read past single probe byte: %v reads=%d", err, r.bytes)
	}
	requireZero(t, d)
}

func TestDetachedObservationsAndConcurrentCopies(t *testing.T) {
	input := []byte(single)
	d, err := project.ParseCompilationDatabase(context.Background(), bytes.NewReader(input), project.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	wantIdentity := d.Artifact()
	for i := range input {
		input[i] = 'x'
	}
	rows := d.Entries()
	projection := rows[0].PreferredInvocation()
	projection.Arguments[0] = "projection mutation"
	if rows[0].Arguments[0] != "cc" {
		t.Fatal("projection aliases detached row")
	}
	rows[0].Arguments[0] = "rows mutation"
	rows[0].Directory = "mutated"
	row, _ := d.Entry(0)
	row.Arguments[1] = "row mutation"
	valueCopy := d
	fresh, _ := valueCopy.Entry(0)
	if fresh.Directory != "." || !reflect.DeepEqual(fresh.Arguments, []string{"cc", "-DA=1"}) ||
		d.Artifact() != wantIdentity {
		t.Fatalf("owned output was mutable: %+v", fresh)
	}
	identityCopy := d.Artifact()
	identityCopy.SHA256[0]++
	if d.Artifact() != wantIdentity {
		t.Fatal("artifact identity aliases caller")
	}
	for _, index := range []int{-1, 1, 100} {
		if _, ok := d.Entry(index); ok {
			t.Fatalf("out-of-bounds entry %d returned", index)
		}
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				rows := valueCopy.Entries()
				rows[0].Arguments[0] = "concurrent detached mutation"
			}
		})
	}
	wg.Wait()
	fresh, _ = d.Entry(0)
	if fresh.Arguments[0] != "cc" {
		t.Fatal("concurrent snapshots changed owned storage")
	}
	// A failed replacement cannot mutate any previously returned observation.
	failed, err := project.ParseCompilationDatabase(context.Background(), strings.NewReader(single+"[]"), project.Limits{})
	if !errors.Is(err, project.ErrInvalid) || d.Artifact() != wantIdentity {
		t.Fatalf("failed replacement disturbed previous observation: %v", err)
	}
	requireZero(t, failed)
}

func FuzzCompilationDatabase(f *testing.F) {
	for _, seed := range []string{single, "[]", single + " []",
		`[{"directory":".","file":"x","command":"cc","ext":[0,-1.2e+3,true,null,{"a":"b"}]}]`,
		`[{"directory":".","file":"x","f\u0069le":"x","command":"cc"}]`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 16384 {
			t.Skip()
		}
		l := project.Limits{MaxInputBytes: 16384, MaxAccountedBytes: 65536, MaxEntries: 32,
			MaxArguments: 32, MaxStringBytes: 1024, MaxTokenBytes: 4096, MaxValues: 1024}
		d, err := project.ParseCompilationDatabase(context.Background(), strings.NewReader(input), l)
		chunked, chunkErr := project.ParseCompilationDatabase(context.Background(), iotest.OneByteReader(strings.NewReader(input)), l)
		if (err == nil) != (chunkErr == nil) {
			t.Fatalf("reader chunking changes acceptance: %v vs %v", err, chunkErr)
		}
		if err != nil {
			requireZero(t, d)
			requireZero(t, chunked)
			return
		}
		if !json.Valid([]byte(input)) {
			t.Fatal("accepted invalid JSON")
		}
		if d.Artifact().SHA256 != sha256.Sum256([]byte(input)) || d.Artifact().ObservedBytes != int64(len(input)) {
			t.Fatal("accepted wrong identity")
		}
		if !reflect.DeepEqual(d.Entries(), chunked.Entries()) || d.Artifact() != chunked.Artifact() ||
			d.AccountedBytes() != chunked.AccountedBytes() {
			t.Fatal("reader chunking changes observation")
		}
	})
}

func benchmarkInput(n int) string {
	var s strings.Builder
	s.WriteByte('[')
	for i := range n {
		if i != 0 {
			s.WriteByte(',')
		}
		fmt.Fprintf(&s, `{"directory":"build/debug","file":"../src/unit%d.cc","arguments":["ccache","clang++","-DVARIANT=debug","-I../include dir","@flags.rsp","-c","../src/unit%d.cc"],"output":"obj/unit%d.o"}`, i, i, i)
	}
	s.WriteByte(']')
	return s.String()
}

func BenchmarkParseCompilationDatabase(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{{"medium_500", 500}, {"dense_5000", 5000}} {
		b.Run(size.name, func(b *testing.B) {
			input := benchmarkInput(size.n)
			b.SetBytes(int64(len(input)))
			b.ReportAllocs()
			b.ResetTimer()
			var d project.CompilationDatabase
			for i := 0; i < b.N; i++ {
				var err error
				d, err = project.ParseCompilationDatabase(context.Background(), strings.NewReader(input), project.Limits{})
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if d.Len() != size.n {
				b.Fatal("missing rows")
			}
			// Modeled surviving payload separately from the monotonic staged work.
			var payload int
			for _, e := range d.Entries() {
				payload += len(e.Directory) + len(e.File) + len(e.Command) + len(e.Output)
				for _, a := range e.Arguments {
					payload += len(a)
				}
			}
			b.ReportMetric(float64(d.AccountedBytes()), "accounted-B/op")
			b.ReportMetric(float64(payload), "retained-string-B/op")
		})
	}
}
