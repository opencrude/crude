package cmake

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strconv"
	"unicode/utf8"

	"crude/insight/project"
)

// Import follows exactly one caller-selected index's objects table and exactly
// one codemodel-v2 entry. It never discovers/selects the newest index. All
// references in the supported tables must resolve in the supplied set; duplicate
// identities, unsafe leaf names, inconsistent bindings and unsupported codemodel
// majors reject the entire observation. Query reply mirrors are checked against
// objects, not used as additional independent observations.
//
// Any failure returns a zero Observation and an explicit error, including
// missing artifacts, cancellation and reader errors after valid JSON. Successful
// import is only a partial format observation, never exhaustive build knowledge.
// Unknown fields/features are validated as JSON and retained in raw artifacts.
func Import(ctx context.Context, index string, inputs []Artifact, limits Limits) (Observation, error) {
	if ctx == nil {
		return Observation{}, ErrInvalid
	}
	l, err := limits.validated()
	if err != nil {
		return Observation{}, err
	}
	if err := ctx.Err(); err != nil {
		return Observation{}, err
	}
	if len(index) > l.MaxStringBytes {
		return Observation{}, ErrLimit
	}
	if !safeName(index) || len(inputs) == 0 {
		return Observation{}, ErrInvalid
	}
	if len(inputs) > l.MaxArtifacts {
		return Observation{}, ErrLimit
	}
	d := decoder{budget: budget{ctx: ctx, limits: l}, docs: make(map[string]map[string]any), roles: make(map[string]string)}
	var result Observation
	total := 0
	for _, in := range inputs {
		if len(in.Name) > l.MaxStringBytes || len(in.Provenance) > l.MaxStringBytes {
			return Observation{}, ErrLimit
		}
		if !safeName(in.Name) || in.Reader == nil || !utf8.ValidString(in.Provenance) {
			return Observation{}, ErrInvalid
		}
		if _, exists := d.docs[in.Name]; exists {
			return Observation{}, ErrInvalid
		}
		raw, err := read(ctx, in.Reader, min(l.MaxArtifactBytes, l.MaxTotalBytes-total))
		if err != nil {
			return Observation{}, err
		}
		total += len(raw)
		doc, err := parse(&d.budget, raw)
		if err != nil {
			return Observation{}, err
		}
		d.docs[in.Name] = doc
		result.artifacts = append(result.artifacts, retained{
			identity: Identity{Name: in.Name, Provenance: in.Provenance, Artifact: project.ArtifactIdentity{
				SHA256: sha256.Sum256(raw), ObservedBytes: int64(len(raw)),
				Format: "cmake-file-api-v1-json", ImporterVersion: ImporterVersion,
			}}, raw: raw,
		})
	}
	root := d.use(index, "index")
	objects := d.list(root, "objects", true)
	refs := make(map[string]reference)
	objectFiles := make(map[string]bool)
	codemodel := ""
	var version Version
	for _, v := range objects {
		r := d.reference(d.obj(v))
		key := r.kind + ":" + strconv.Itoa(r.version.Major) + ":" + strconv.Itoa(r.version.Minor)
		if _, exists := refs[key]; exists {
			d.fail(ErrInvalid)
		}
		if objectFiles[r.file] {
			d.fail(ErrInvalid)
		}
		objectFiles[r.file] = true
		refs[key] = r
		obj := d.use(r.file, r.kind)
		if d.text(obj, "kind", true) != r.kind || d.version(obj, "version") != r.version {
			d.fail(ErrInvalid)
		}
		if r.kind == "codemodel" {
			if r.version.Major != 2 {
				d.fail(ErrUnsupported)
			}
			if codemodel != "" {
				d.fail(ErrInvalid)
			}
			codemodel, version = r.file, r.version
		}
	}
	if reply, ok := root["reply"]; ok {
		d.reply(d.obj(reply), refs)
	}
	if codemodel == "" {
		d.fail(ErrIncomplete)
	}
	model := d.ref(codemodel)
	result.model = Model{Index: index, Codemodel: codemodel, Version: version, Paths: d.paths(model)}
	configs := d.list(model, "configurations", true)
	if len(configs) == 0 {
		d.fail(ErrIncomplete)
	}
	names := make(map[string]bool)
	for _, v := range configs {
		if d.err != nil {
			break
		}
		c := d.configuration(d.obj(v), version)
		if names[c.Name] {
			d.fail(ErrInvalid)
		}
		names[c.Name] = true
		result.model.Configurations = append(result.model.Configurations, c)
	}
	if d.err == nil {
		d.err = ctx.Err()
	}
	if d.err != nil {
		return Observation{}, d.err
	}
	return result, nil
}

func safeName(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

type decoder struct {
	budget
	docs  map[string]map[string]any
	roles map[string]string
	err   error
}

func (d *decoder) fail(err error) {
	if d.err == nil {
		d.err = err
	}
}
func (d *decoder) charge(n int) {
	if d.err == nil {
		d.fail(d.work(n))
	}
}
func (d *decoder) obj(v any) map[string]any {
	d.charge(1)
	m, ok := v.(map[string]any)
	if !ok {
		d.fail(ErrInvalid)
	}
	return m
}
func (d *decoder) list(o map[string]any, k string, required bool) []any {
	v, ok := o[k]
	if !ok && !required {
		return nil
	}
	a, ok := v.([]any)
	if !ok {
		d.fail(ErrInvalid)
	}
	d.charge(len(a))
	if d.err != nil {
		return nil
	}
	return a
}
func (d *decoder) text(o map[string]any, k string, nonempty bool) string {
	v, ok := o[k].(string)
	if !ok || nonempty && v == "" {
		d.fail(ErrInvalid)
	}
	return v
}
func (d *decoder) number(v any) int {
	n, ok := v.(json.Number)
	if !ok {
		d.fail(ErrInvalid)
		return 0
	}
	i, err := strconv.ParseUint(string(n), 10, 31)
	if err != nil {
		d.fail(ErrInvalid)
	}
	return int(i)
}
func (d *decoder) optionalIndex(o map[string]any, k string, max int) *int {
	v, ok := o[k]
	if !ok {
		return nil
	}
	i := d.number(v)
	if i >= max {
		d.fail(ErrInvalid)
	}
	return &i
}
func (d *decoder) index(o map[string]any, k string, max int) int {
	p := d.optionalIndex(o, k, max)
	if p == nil {
		d.fail(ErrInvalid)
		return 0
	}
	return *p
}
func (d *decoder) indexes(o map[string]any, k string, max int, required bool) []int {
	a := d.list(o, k, required)
	var out []int
	seen := make(map[int]bool)
	for _, v := range a {
		i := d.number(v)
		if i >= max || seen[i] {
			d.fail(ErrInvalid)
		}
		seen[i] = true
		out = append(out, i)
	}
	return out
}
func (d *decoder) flag(o map[string]any, k string) *bool {
	v, ok := o[k]
	if !ok {
		return nil
	}
	b, ok := v.(bool)
	if !ok {
		d.fail(ErrInvalid)
	}
	return &b
}
func (d *decoder) version(o map[string]any, k string) Version {
	m := d.obj(o[k])
	return Version{d.number(m["major"]), d.number(m["minor"])}
}
func (d *decoder) paths(o map[string]any) Paths {
	p := d.obj(o["paths"])
	return Paths{Source: d.text(p, "source", true), Build: d.text(p, "build", true)}
}
func (d *decoder) ref(name string) map[string]any {
	if !safeName(name) {
		d.fail(ErrInvalid)
		return nil
	}
	v, ok := d.docs[name]
	if !ok {
		d.fail(ErrIncomplete)
	}
	return v
}
func (d *decoder) use(name, role string) map[string]any {
	if old, ok := d.roles[name]; ok && old != role {
		d.fail(ErrInvalid)
	}
	d.roles[name] = role
	return d.ref(name)
}

type reference struct {
	kind, file string
	version    Version
}

func (d *decoder) reference(o map[string]any) reference {
	return reference{d.text(o, "kind", true), d.text(o, "jsonFile", true), d.version(o, "version")}
}

// reply contains documented client/query nesting. Do not interpret opaque
// "client" passthrough or request preferences as artifact references.
func (d *decoder) reply(o map[string]any, refs map[string]reference) {
	for key, v := range o {
		if d.err != nil {
			return
		}
		if key == "client" || key == "requests" {
			continue
		}
		if key == "responses" {
			if _, ok := v.([]any); ok {
				for _, item := range d.list(o, key, true) {
					d.replyEntry(d.obj(item), refs)
				}
			} else {
				d.replyEntry(d.obj(v), refs)
			}
		} else {
			d.replyEntry(d.obj(v), refs)
		}
	}
}
func (d *decoder) replyEntry(o map[string]any, refs map[string]reference) {
	if _, ok := o["error"]; ok {
		if _, ambiguous := o["jsonFile"]; ambiguous {
			d.fail(ErrInvalid)
		}
		_ = d.text(o, "error", false) // Unrelated query errors do not certify coverage.
		return
	}
	if _, ok := o["jsonFile"]; ok {
		r := d.reference(o)
		if !safeName(r.file) {
			d.fail(ErrInvalid)
		}
		key := r.kind + ":" + strconv.Itoa(r.version.Major) + ":" + strconv.Itoa(r.version.Minor)
		if actual, ok := refs[key]; !ok || actual != r {
			d.fail(ErrInvalid)
		}
		return
	}
	d.reply(o, refs)
}

func (d *decoder) configuration(o map[string]any, version Version) Configuration {
	c := Configuration{Name: d.text(o, "name", false)}
	dirs, mods, targets := d.list(o, "directories", true), d.list(o, "projects", true), d.list(o, "targets", true)
	if len(dirs) == 0 || len(mods) == 0 {
		d.fail(ErrInvalid)
	}
	for _, v := range dirs {
		m := d.obj(v)
		dir := Directory{
			Paths:         Paths{Source: d.text(m, "source", true), Build: d.text(m, "build", true)},
			ProjectIndex:  d.index(m, "projectIndex", len(mods)),
			ParentIndex:   d.optionalIndex(m, "parentIndex", len(dirs)),
			ChildIndexes:  d.indexes(m, "childIndexes", len(dirs), false),
			TargetIndexes: d.indexes(m, "targetIndexes", len(targets), false),
		}
		if _, ok := m["jsonFile"]; ok {
			dir.JSONFile = d.text(m, "jsonFile", true)
			doc := d.use(dir.JSONFile, "directory")
			d.checkVersion(doc, version)
			if d.paths(doc) != dir.Paths {
				d.fail(ErrInvalid)
			}
		} else if version.Minor >= 3 {
			d.fail(ErrIncomplete)
		}
		c.Directories = append(c.Directories, dir)
	}
	for _, v := range mods {
		m := d.obj(v)
		c.Modules = append(c.Modules, Module{
			Name:             d.text(m, "name", true),
			ParentIndex:      d.optionalIndex(m, "parentIndex", len(mods)),
			ChildIndexes:     d.indexes(m, "childIndexes", len(mods), false),
			DirectoryIndexes: d.indexes(m, "directoryIndexes", len(dirs), true),
			TargetIndexes:    d.indexes(m, "targetIndexes", len(targets), false),
		})
	}
	ids, files := make(map[string]bool), make(map[string]bool)
	// Abstract targets (2.9+) are validated as references but remain raw, not
	// projected as buildable targets or silently merged into target indices.
	for _, table := range []struct {
		values  []any
		project bool
	}{
		{targets, true}, {d.list(o, "abstractTargets", false), false},
	} {
		for _, v := range table.values {
			if d.err != nil {
				break
			}
			m := d.obj(v)
			name, id, file := d.text(m, "name", true), d.text(m, "id", true), d.text(m, "jsonFile", true)
			if ids[id] || files[file] {
				d.fail(ErrInvalid)
			}
			ids[id], files[file] = true, true
			ti, pi := d.index(m, "directoryIndex", len(dirs)), d.index(m, "projectIndex", len(mods))
			doc := d.use(file, "target")
			if d.text(doc, "id", true) != id || d.text(doc, "name", true) != name {
				d.fail(ErrInvalid)
			}
			d.checkVersion(doc, version)
			if table.project {
				t := d.target(doc)
				t.JSONFile, t.DirectoryIndex, t.ProjectIndex = file, ti, pi
				c.Targets = append(c.Targets, t)
			}
		}
	}
	for _, t := range c.Targets {
		for _, id := range t.Dependencies {
			if !ids[id] {
				d.fail(ErrInvalid)
			}
		}
	}
	// Validate both directions of the target-to-module/directory binding.
	dirSeen, modSeen := make([]bool, len(c.Targets)), make([]bool, len(c.Targets))
	parents, children := make([]*int, len(c.Directories)), make([][]int, len(c.Directories))
	for i, dir := range c.Directories {
		parents[i], children[i] = dir.ParentIndex, dir.ChildIndexes
		for _, ti := range dir.TargetIndexes {
			if ti < len(c.Targets) {
				if c.Targets[ti].DirectoryIndex != i || dirSeen[ti] {
					d.fail(ErrInvalid)
				}
				dirSeen[ti] = true
			}
		}
	}
	d.tree(parents, children)
	parents, children = make([]*int, len(c.Modules)), make([][]int, len(c.Modules))
	for i, mod := range c.Modules {
		parents[i], children[i] = mod.ParentIndex, mod.ChildIndexes
		for _, ti := range mod.TargetIndexes {
			if ti < len(c.Targets) {
				if c.Targets[ti].ProjectIndex != i || modSeen[ti] {
					d.fail(ErrInvalid)
				}
				modSeen[ti] = true
			}
		}
	}
	d.tree(parents, children)
	for i := range c.Targets {
		if !dirSeen[i] || !modSeen[i] {
			d.fail(ErrInvalid)
		}
	}
	directorySeen := make([]bool, len(c.Directories))
	for i, mod := range c.Modules {
		for _, di := range mod.DirectoryIndexes {
			if di < len(c.Directories) {
				if c.Directories[di].ProjectIndex != i || directorySeen[di] {
					d.fail(ErrInvalid)
				}
				directorySeen[di] = true
			}
		}
	}
	for _, seen := range directorySeen {
		if !seen {
			d.fail(ErrInvalid)
		}
	}
	for _, t := range c.Targets {
		if t.DirectoryIndex < len(c.Directories) && c.Directories[t.DirectoryIndex].ProjectIndex != t.ProjectIndex {
			d.fail(ErrInvalid)
		}
	}
	return c
}

// Validate reciprocal parent/child references and cycles in linear work.
func (d *decoder) tree(parents []*int, children [][]int) {
	if d.err != nil {
		return
	}
	seen, state := make([]bool, len(parents)), make([]byte, len(parents))
	for i, kids := range children {
		for _, k := range kids {
			if k >= len(parents) || parents[k] == nil || *parents[k] != i || seen[k] {
				d.fail(ErrInvalid)
				return
			}
			seen[k] = true
		}
	}
	for i, p := range parents {
		if (p != nil) != seen[i] || (i == 0) != (p == nil) {
			d.fail(ErrInvalid)
			return
		}
	}
	for i := range parents {
		j := i
		for state[j] == 0 {
			state[j] = 1
			if parents[j] == nil {
				break
			}
			j = *parents[j]
		}
		if state[j] == 1 && parents[j] != nil {
			d.fail(ErrInvalid)
			return
		}
		j = i
		for state[j] == 1 {
			state[j] = 2
			if parents[j] == nil {
				break
			}
			j = *parents[j]
		}
	}
}
func (d *decoder) checkVersion(o map[string]any, expected Version) {
	if _, ok := o["codemodelVersion"]; ok {
		v := d.version(o, "codemodelVersion")
		if v.Major != 2 {
			d.fail(ErrUnsupported)
		} else if v != expected {
			d.fail(ErrInvalid)
		}
	} else if expected.Minor >= 9 {
		d.fail(ErrIncomplete)
	}
}

func (d *decoder) target(o map[string]any) Target {
	t := Target{Name: d.text(o, "name", true), ID: d.text(o, "id", true), Type: d.text(o, "type", true),
		Paths: d.paths(o), IsGeneratorProvided: d.flag(o, "isGeneratorProvided")}
	sources, groups, compiles := d.list(o, "sources", false), d.list(o, "sourceGroups", false), d.list(o, "compileGroups", false)
	backtraces := 0
	if graph, ok := o["backtraceGraph"]; ok {
		backtraces = d.backtraces(d.obj(graph))
	}
	for _, v := range sources {
		s := d.obj(v)
		t.Sources = append(t.Sources, Source{Path: d.text(s, "path", true),
			CompileGroupIndex: d.optionalIndex(s, "compileGroupIndex", len(compiles)),
			SourceGroupIndex:  d.optionalIndex(s, "sourceGroupIndex", len(groups)),
			IsGenerated:       d.flag(s, "isGenerated"), Backtrace: d.optionalIndex(s, "backtrace", backtraces)})
	}
	for _, v := range groups {
		g := d.obj(v)
		t.SourceGroups = append(t.SourceGroups, SourceGroup{Name: d.text(g, "name", false), SourceIndexes: d.indexes(g, "sourceIndexes", len(sources), true)})
	}
	for _, v := range compiles {
		g := d.obj(v)
		group := CompileGroup{Language: d.text(g, "language", true), SourceIndexes: d.indexes(g, "sourceIndexes", len(sources), true)}
		for _, v := range d.list(g, "compileCommandFragments", false) {
			m := d.obj(v)
			group.Fragments = append(group.Fragments, Fragment{d.text(m, "fragment", false), d.optionalIndex(m, "backtrace", backtraces)})
		}
		for _, v := range d.list(g, "defines", false) {
			m := d.obj(v)
			group.Defines = append(group.Defines, Define{d.text(m, "define", false), d.optionalIndex(m, "backtrace", backtraces)})
		}
		for _, v := range d.list(g, "includes", false) {
			m := d.obj(v)
			group.Includes = append(group.Includes, Include{d.text(m, "path", true), d.flag(m, "isSystem"), d.optionalIndex(m, "backtrace", backtraces)})
		}
		t.CompileGroups = append(t.CompileGroups, group)
	}
	for _, v := range d.list(o, "dependencies", false) {
		t.Dependencies = append(t.Dependencies, d.text(d.obj(v), "id", true))
	}
	compileSeen, sourceSeen := make([]bool, len(t.Sources)), make([]bool, len(t.Sources))
	for i, g := range t.CompileGroups {
		for _, si := range g.SourceIndexes {
			if si < len(t.Sources) {
				if t.Sources[si].CompileGroupIndex == nil || *t.Sources[si].CompileGroupIndex != i || compileSeen[si] {
					d.fail(ErrInvalid)
				}
				compileSeen[si] = true
			}
		}
	}
	for i, g := range t.SourceGroups {
		for _, si := range g.SourceIndexes {
			if si < len(t.Sources) {
				if t.Sources[si].SourceGroupIndex == nil || *t.Sources[si].SourceGroupIndex != i || sourceSeen[si] {
					d.fail(ErrInvalid)
				}
				sourceSeen[si] = true
			}
		}
	}
	for i, s := range t.Sources {
		if s.CompileGroupIndex != nil && !compileSeen[i] {
			d.fail(ErrInvalid)
		}
		if s.SourceGroupIndex != nil && !sourceSeen[i] {
			d.fail(ErrInvalid)
		}
	}
	return t
}

func (d *decoder) backtraces(o map[string]any) int {
	nodes, files, commands := d.list(o, "nodes", true), d.list(o, "files", true), d.list(o, "commands", true)
	for _, table := range [][]any{files, commands} {
		for _, v := range table {
			if _, ok := v.(string); !ok {
				d.fail(ErrInvalid)
			}
		}
	}
	for _, v := range nodes {
		n := d.obj(v)
		_ = d.index(n, "file", len(files))
		_ = d.optionalIndex(n, "command", len(commands))
		_ = d.optionalIndex(n, "parent", len(nodes))
	}
	return len(nodes)
}
