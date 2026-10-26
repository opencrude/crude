// Command wiregen generates the MessagePack codecs for every Go type the core
// protocol carries. The Go domain types are the schema: it computes the set of
// named types reachable from the protocol roots and emits msgp tuple codecs
// for exactly those types into each owning package's wire_gen.go. Decoders are
// hardened so a collection header can never allocate more elements than the
// remaining bytes could hold. It also writes coreprotocol's schema digest,
// which peers compare exactly at session start.
//
//	go run ./internal/wiregen
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/tinylib/msgp/gen"
	"github.com/tinylib/msgp/parse"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"
)

const module = "crude"

// roots are the packages whose exported types are protocol messages.
var roots = []string{"crude/coreprotocol"}

// payloadRoots are domain types that methods carry directly as request or
// result values without a coreprotocol wrapper.
var payloadRoots = []string{
	"crude/document.Receipt",
	"crude/document.Snapshot",
	"crude/document.SyncResult",
	"crude/localhistory.Manifest",
	"crude/localhistory.PruneResult",
	"crude/localhistory.RestorePlan",
	"crude/localhistory.RestoreResult",
	"crude/localhistory.Stats",
	"crude/workspace.DirectoryListing",
	"crude/workspace.TerminalProjection",
	"crude/metrics.Query",
	"crude/metrics.Snapshot",
	"crude/types.CompactResultMsg",
	"crude/types/capability.HostReply",
	"crude/types/control.Result",
	"crude/types/inspect.Page",
	"crude/sessiondb.NullInt64",
	"crude/sessiondb.NullString",
	"crude/workspace.BrowserCatalog",
	"crude/workspace.NewSessionResult",
	"crude/workspace.TerminalCreateOpts",
	"crude/workspace.TerminalInfo",
	"crude/workspace.WorkspaceDeletion",
}

// shims carry opaque JSON documents as their own bytes.
var shims = map[string]string{
	"json.RawMessage": "json.RawMessage as:[]byte using:rawJSONToBytes/bytesToRawJSON mode:cast",
}

func main() {
	check := flag.Bool("check", false, "verify the source cohort without writing or generating files")
	flag.Parse()
	if err := runMode(*check); err != nil {
		fmt.Fprintln(os.Stderr, "wiregen:", err)
		os.Exit(1)
	}
}

func runMode(check bool) error {
	if check {
		return checkCohort(".")
	}
	return run()
}

func run() error {
	// Earlier output may no longer match the types; regenerate from source.
	stale, err := filepath.Glob("*/wire_gen.go")
	if err != nil {
		return err
	}
	nested, _ := filepath.Glob("*/*/wire_gen.go")
	for _, path := range append(stale, nested...) {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	cfg := &packages.Config{Fset: token.NewFileSet(), Mode: packages.NeedName | packages.NeedTypes | packages.NeedFiles | packages.NeedDeps | packages.NeedImports}
	load := append([]string{}, roots...)
	for _, root := range payloadRoots {
		load = append(load, root[:strings.LastIndex(root, ".")])
	}
	pkgs, err := packages.Load(cfg, load...)
	if err != nil {
		return err
	}
	// Code that calls the codecs does not type-check until they exist again;
	// the type declarations themselves are all that matters here.
	reach := newReach(cfg.Fset)
	for _, p := range pkgs {
		for _, root := range payloadRoots {
			if path, name := root[:strings.LastIndex(root, ".")], root[strings.LastIndex(root, ".")+1:]; path == p.PkgPath {
				tn, ok := p.Types.Scope().Lookup(name).(*types.TypeName)
				if !ok {
					return fmt.Errorf("payload root %s not found", root)
				}
				reach.named(tn.Type().(*types.Named))
			}
		}
		if !slices.Contains(roots, p.PkgPath) {
			continue
		}
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			if tn, ok := scope.Lookup(name).(*types.TypeName); ok && tn.Exported() && !tn.IsAlias() {
				if _, iface := tn.Type().Underlying().(*types.Interface); !iface {
					reach.named(tn.Type().(*types.Named))
				}
			}
		}
	}
	if len(reach.problems) > 0 {
		sort.Strings(reach.problems)
		return fmt.Errorf("unsupported protocol types:\n  %s", strings.Join(reach.problems, "\n  "))
	}
	files := map[string][]string{}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if reach.byPkg[p.PkgPath] != nil {
			files[p.PkgPath] = p.GoFiles
		}
	})
	paths := make([]string, 0, len(reach.byPkg))
	for path := range reach.byPkg {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	digest := sha256.New()
	// Generated calls alone do not identify their wire semantics: a runtime
	// primitive can change bytes without changing any generated field method.
	// Include the complete forked runtime source, in stable filename order.
	primitives, err := filepath.Glob("third_party/msgp/msgp/*.go")
	if err != nil {
		return err
	}
	if len(primitives) == 0 {
		return fmt.Errorf("msgp runtime source missing from schema fingerprint")
	}
	// These opaque primitives deliberately own their encoding rather than a
	// generated tuple. Their semantics must be negotiated just as strictly.
	primitives = append(primitives, "types/jsondoc/jsondoc.go", "coreprotocol/wire.go")
	sort.Strings(primitives)
	for _, source := range primitives {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		fmt.Fprintf(digest, "runtime %s %d\n", source, len(data))
		digest.Write(data)
	}
	for _, path := range paths {
		out, err := generate(path, files[path], reach.byPkg[path])
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		digest.Write(out)
		fmt.Printf("%-28s %4d types\n", path, len(reach.byPkg[path]))
	}
	if err := writeRegistry(paths, reach.byPkg); err != nil {
		return err
	}
	if err := writeSchema(hex.EncodeToString(digest.Sum(nil))[:16]); err != nil {
		return err
	}
	// The cohort includes codecs and Schema, so publish it last.
	return writeCohort(".")
}

// writeRegistry lists every generated type for the codec property tests.
func writeRegistry(paths []string, byPkg map[string]map[string]*types.Named) error {
	var b bytes.Buffer
	b.WriteString("// Code generated by wiregen; DO NOT EDIT.\n\npackage coreprotocol_test\n\nimport (\n")
	alias := map[string]string{}
	for i, path := range paths {
		alias[path] = fmt.Sprintf("p%d", i)
		fmt.Fprintf(&b, "\t%s %q\n", alias[path], path)
	}
	b.WriteString(")\n\n// wireTypes holds a pointer to every type with a generated codec.\nvar wireTypes = []any{\n")
	for _, path := range paths {
		names := make([]string, 0, len(byPkg[path]))
		for name := range byPkg[path] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&b, "\tnew(%s.%s),\n", alias[path], name)
		}
	}
	b.WriteString("}\n")
	return os.WriteFile(filepath.Join("coreprotocol", "wire_types_gen_test.go"), b.Bytes(), 0o644)
}

type reach struct {
	fset     *token.FileSet
	seen     map[*types.TypeName]bool
	byPkg    map[string]map[string]*types.Named
	problems []string
}

func newReach(fset *token.FileSet) *reach {
	return &reach{fset: fset, seen: map[*types.TypeName]bool{}, byPkg: map[string]map[string]*types.Named{}}
}

func (r *reach) named(t *types.Named) {
	obj := t.Obj()
	if obj.Pkg() == nil || r.seen[obj] {
		return
	}
	r.seen[obj] = true
	path := obj.Pkg().Path()
	if path != module && !strings.HasPrefix(path, module+"/") {
		return // time, json.RawMessage and shimmed library types
	}
	if hasCodec(r.fset, t) {
		return // carries its own MessagePack codec (an opaque document)
	}
	if t.TypeParams().Len() > 0 {
		r.problems = append(r.problems, path+"."+obj.Name()+": generic type")
		return
	}
	if r.byPkg[path] == nil {
		r.byPkg[path] = map[string]*types.Named{}
	}
	r.byPkg[path][obj.Name()] = t
	r.walk(t.Underlying(), path+"."+obj.Name())
}

// hasCodec reports a hand-written MessagePack codec (an opaque document such
// as jsondoc.Object); codecs this tool generated earlier do not count.
func hasCodec(fset *token.FileSet, t *types.Named) bool {
	for i := range t.NumMethods() {
		m := t.Method(i)
		if m.Name() == "MarshalMsg" {
			return filepath.Base(fset.Position(m.Pos()).Filename) != "wire_gen.go"
		}
	}
	return false
}

func (r *reach) walk(t types.Type, at string) {
	switch t := t.(type) {
	case *types.Named:
		r.named(t)
	case *types.Alias:
		r.walk(types.Unalias(t), at)
	case *types.Pointer:
		r.walk(t.Elem(), at)
	case *types.Slice:
		r.walk(t.Elem(), at)
	case *types.Array:
		r.walk(t.Elem(), at)
	case *types.Map:
		if b, ok := t.Key().Underlying().(*types.Basic); !ok || b.Kind() != types.String {
			r.problems = append(r.problems, at+": map key "+t.Key().String())
		}
		r.walk(t.Elem(), at)
	case *types.Struct:
		for i := range t.NumFields() {
			f := t.Field(i)
			if !f.Exported() || reflect.StructTag(t.Tag(i)).Get("json") == "-" {
				continue
			}
			r.walk(f.Type(), at+"."+f.Name())
		}
	case *types.Interface:
		r.problems = append(r.problems, at+": interface")
	case *types.Signature:
		r.problems = append(r.problems, at+": func")
	case *types.Chan:
		r.problems = append(r.problems, at+": chan")
	}
}

func generate(path string, sources []string, wanted map[string]*types.Named) ([]byte, error) {
	if len(sources) == 0 {
		return nil, fmt.Errorf("no sources")
	}
	dir := filepath.Dir(sources[0])
	out := filepath.Join(dir, "wire_gen.go")
	// msgp parses a directory; give it exactly this build's sources, without
	// the previous output, tests or files for other platforms.
	tmp, err := os.MkdirTemp("", "wiregen")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	for _, src := range sources {
		if filepath.Base(src) == "wire_gen.go" {
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(tmp, filepath.Base(src)), data, 0o600); err != nil {
			return nil, err
		}
	}
	var structs []string
	for name, t := range wanted {
		if _, ok := t.Underlying().(*types.Struct); ok {
			structs = append(structs, name)
		}
	}
	sort.Strings(structs)
	directives := []string{"tag json", "replace sql.NullString with:sessiondb.NullString", "replace sql.NullInt64 with:sessiondb.NullInt64"}
	if fs := filepath.Base(dir); fs == "sessiondb" {
		directives = []string{"tag json", "replace sql.NullString with:NullString", "replace sql.NullInt64 with:NullInt64"}
	}
	for _, shim := range shims {
		directives = append(directives, "shim "+shim)
	}
	if len(structs) > 0 {
		directives = append(directives, "tuple "+strings.Join(structs, " "))
	}
	fs, err := parse.File(tmp, false, directives)
	if err != nil {
		return nil, err
	}
	for name, el := range fs.Identities {
		if wanted[name] == nil {
			delete(fs.Identities, name)
			continue
		}
		allowNil(el)
	}
	for name := range wanted {
		if fs.Identities[name] == nil {
			return nil, fmt.Errorf("msgp could not represent %s", name)
		}
	}
	var body bytes.Buffer
	if err := fs.PrintTo(gen.NewPrinter(gen.Marshal|gen.Unmarshal|gen.Size, &body, nil)); err != nil {
		return nil, err
	}
	var src bytes.Buffer
	fmt.Fprintf(&src, "// Code generated by wiregen; DO NOT EDIT.\n\npackage %s\n\nimport (\n\t\"github.com/tinylib/msgp/msgp\"\n", fs.Package)
	for _, imp := range fs.Imports {
		// cgo is a compiler pseudo-package, never a dependency of the neutral
		// wire types. goimports deliberately preserves import C even when unused;
		// carrying it here would silently exclude every codec from non-cgo builds.
		if imp.Path.Value == `"C"` {
			continue
		}
		if imp.Name != nil {
			fmt.Fprintf(&src, "\t%s %s\n", imp.Name.Name, imp.Path.Value)
		} else {
			fmt.Fprintf(&src, "\t%s\n", imp.Path.Value)
		}
	}
	src.WriteString(")\n\n")
	src.Write(guardCollections(body.Bytes()))
	formatted, err := imports.Process(out, src.Bytes(), nil)
	if err != nil {
		_ = os.WriteFile(out+".broken", src.Bytes(), 0o600)
		return nil, err
	}
	return formatted, os.WriteFile(out, formatted, 0o644)
}

// allowNil keeps nil and empty collections distinct on the wire for every
// struct field, including structs msgp inlined into their parents: in-core
// values and their replicas must compare equal, not merely look alike.
func allowNil(el gen.Elem) {
	switch e := el.(type) {
	case *gen.Struct:
		for i := range e.Fields {
			f := &e.Fields[i]
			if len(f.FieldTagParts) == 0 {
				f.FieldTagParts = []string{f.FieldTag}
			}
			if !slices.Contains(f.FieldTagParts[1:], "allownil") {
				f.FieldTagParts = append(f.FieldTagParts, "allownil")
			}
			allowNil(f.FieldElem)
		}
	case *gen.Ptr:
		allowNil(e.Value)
	case *gen.Slice:
		allowNil(e.Els)
	case *gen.Array:
		allowNil(e.Els)
	case *gen.Map:
		allowNil(e.Value)
	}
}

var collectionHeader = regexp.MustCompile(`(?m)^(\s*)(\w+), bts, err = msgp\.Read(Array|Map)HeaderBytes\(bts\)\n(\s*if err != nil \{\n(?:.*\n)*?\s*\}\n)`)

// guardCollections rejects a collection header larger than the bytes that
// remain before anything is allocated for it: every array element occupies
// at least one byte and every map entry at least two.
func guardCollections(code []byte) []byte {
	return collectionHeader.ReplaceAllFunc(code, func(m []byte) []byte {
		parts := collectionHeader.FindSubmatch(m)
		indent, name, kind := string(parts[1]), string(parts[2]), string(parts[3])
		per := "1"
		if kind == "Map" {
			per = "2"
		}
		guard := fmt.Sprintf("%sif uint64(%s)*%s > uint64(len(bts)) {\n%s\terr = msgp.ErrShortBytes\n%s\treturn\n%s}\n", indent, name, per, indent, indent, indent)
		return append(append([]byte{}, m...), guard...)
	})
}

func writeSchema(digest string) error {
	src := fmt.Sprintf("// Code generated by wiregen; DO NOT EDIT.\n\npackage coreprotocol\n\n// Schema identifies the exact wire layout of every protocol type. Peers\n// compare it at session start; any difference is a different protocol.\nconst Schema = %q\n", digest)
	return os.WriteFile(filepath.Join("coreprotocol", "schema_gen.go"), []byte(src), 0o644)
}
