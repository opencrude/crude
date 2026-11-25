package providersim

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
)

//go:embed schemas/*.json schemas/*.license.txt
var schemaFiles embed.FS

const schemaDirectory = "schemas/"

const defsPointer = "#/$defs/"

type bundleRecord struct {
	Source            string              `json:"source"`
	AdditionalSources []string            `json:"additional_sources"`
	Roots             map[string][]string `json:"roots"`
	Components        []string            `json:"components"`
	SHA256            string              `json:"sha256"`
}

type sourceRecord struct {
	LicenseFile   string `json:"license_file"`
	LicenseSHA256 string `json:"license_output_sha256"`
}

type schemaManifest struct {
	Bundles map[string]bundleRecord `json:"bundles"`
	Sources map[string]sourceRecord `json:"sources"`
}

func readManifest() (schemaManifest, error) {
	raw, err := schemaFiles.ReadFile(schemaDirectory + "manifest.json")
	if err != nil {
		return schemaManifest{}, err
	}
	var manifest schemaManifest
	err = json.Unmarshal(raw, &manifest)
	return manifest, err
}

type bundle struct {
	roots map[string]*jsonschema.Resolved
	// Top-level keys for opt-in per-wire strictness. Sources still own the
	// assertions, and open objects at deeper levels stay open.
	fields map[string]map[string]bool
}

var schemaLoaders = sync.OnceValues(func() (map[string]func() (*bundle, error), error) {
	manifest, err := readManifest()
	if err != nil {
		return nil, err
	}
	loaders := make(map[string]func() (*bundle, error), len(manifest.Bundles))
	for name, record := range manifest.Bundles {
		loaders[name] = sync.OnceValues(func() (*bundle, error) { return loadBundle(name, record) })
	}
	return loaders, nil
})

func wireBundle(wire Wire) (*bundle, error) {
	loaders, err := schemaLoaders()
	if err != nil {
		return nil, err
	}
	load := loaders[string(wire)+".json"]
	if load == nil {
		return nil, fmt.Errorf("no schema bundle for %s", wire)
	}
	return load()
}

func loadBundle(name string, record bundleRecord) (*bundle, error) {
	raw, err := schemaFiles.ReadFile(schemaDirectory + name)
	if err != nil {
		return nil, err
	}
	var document jsonschema.Schema
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	b := &bundle{
		roots:  make(map[string]*jsonschema.Resolved, len(record.Roots)),
		fields: make(map[string]map[string]bool, len(record.Roots)),
	}
	for component, dependencies := range record.Roots {
		defs := make(map[string]*jsonschema.Schema, len(dependencies))
		for _, dependency := range dependencies {
			if document.Defs[dependency] == nil {
				return nil, fmt.Errorf("%s: missing component %s for %s", name, dependency, component)
			}
			defs[dependency] = document.Defs[dependency]
		}
		if defs[component] == nil {
			return nil, fmt.Errorf("%s: missing root %s", name, component)
		}
		root := document
		root.Ref = defsPointer + component
		root.Defs = defs
		// No loader: external references fail closed and cannot access a network.
		resolved, err := root.Resolve(nil)
		if err != nil {
			return nil, fmt.Errorf("resolve %s %s: %w", name, component, err)
		}
		b.roots[component] = resolved
		b.fields[component] = topLevelFields(defs, defs[component])
	}
	return b, nil
}

// Derive known keys from each top-level branch. A union requires a key only
// when every arm requires it; keys unique to one arm still count as known.
func topLevelFields(defs map[string]*jsonschema.Schema, root *jsonschema.Schema) map[string]bool {
	visited := make(map[*jsonschema.Schema]bool)
	var walk func(*jsonschema.Schema) map[string]bool
	walk = func(s *jsonschema.Schema) map[string]bool {
		if s == nil || visited[s] {
			return nil
		}
		visited[s] = true
		defer delete(visited, s)
		fields := make(map[string]bool, len(s.Properties))
		merge := func(other map[string]bool) {
			for name, required := range other {
				fields[name] = fields[name] || required
			}
		}
		for name := range s.Properties {
			fields[name] = false
		}
		for _, name := range s.Required {
			fields[name] = true
		}
		if name, ok := strings.CutPrefix(s.Ref, defsPointer); ok {
			merge(walk(defs[name]))
		}
		for _, child := range s.AllOf {
			merge(walk(child))
		}
		for _, union := range [][]*jsonschema.Schema{s.AnyOf, s.OneOf} {
			counts := make(map[string]int)
			for _, child := range union {
				for name, required := range walk(child) {
					if _, known := fields[name]; !known {
						fields[name] = false
					}
					if required {
						counts[name]++
					}
				}
			}
			for name, count := range counts {
				fields[name] = fields[name] || count == len(union)
			}
		}
		return fields
	}
	return walk(root)
}

func (b *bundle) validate(component string, value any) error {
	root := b.roots[component]
	if root == nil {
		return fmt.Errorf("unknown schema root %s", component)
	}
	instance, _, err := schemaNumbers(value)
	if err != nil {
		return err
	}
	return root.Validate(instance)
}

// jsonschema-go v0.4.3 classifies json.Number as a string. Give it Go
// numeric values while retaining the exact JSON numbers for the journal and
// prefix rendering. Copy only containers whose contents need conversion.
func schemaNumbers(value any) (any, bool, error) {
	switch v := value.(type) {
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i, true, nil
		}
		if u, err := strconv.ParseUint(v.String(), 10, 64); err == nil {
			return u, true, nil
		}
		f, err := v.Float64()
		return f, true, err
	case map[string]any:
		var out map[string]any
		for key, child := range v {
			normalized, changed, err := schemaNumbers(child)
			if err != nil {
				return nil, false, fmt.Errorf("property %q: %w", key, err)
			}
			if changed {
				if out == nil {
					out = maps.Clone(v)
				}
				out[key] = normalized
			}
		}
		if out != nil {
			return out, true, nil
		}
	case []any:
		var out []any
		for i, child := range v {
			normalized, changed, err := schemaNumbers(child)
			if err != nil {
				return nil, false, fmt.Errorf("item %d: %w", i, err)
			}
			if changed {
				if out == nil {
					out = slices.Clone(v)
				}
				out[i] = normalized
			}
		}
		if out != nil {
			return out, true, nil
		}
	}
	return value, false, nil
}

func (b *bundle) unknownField(component string, value map[string]any, extra string) (string, bool) {
	fields := b.fields[component]
	var first string
	var found bool
	for name := range value {
		if _, known := fields[name]; known || (extra != "" && name == extra) {
			continue
		}
		if !found || name < first {
			first, found = name, true
		}
	}
	return first, found
}

func (b *bundle) missingField(component string, value map[string]any) string {
	fields := b.fields[component]
	var missing []string
	for name, required := range fields {
		if _, present := value[name]; required && !present {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return slices.Min(missing)
}

func decodeJSON(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		if err == nil {
			err = errors.New("more than one JSON value")
		}
		return nil, err
	}
	return value, nil
}

// jsonBytes accepts only values decoded by decodeJSON or simulator-owned wire
// values. Neither source can contain an unsupported Go value.
func jsonBytes(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("encode simulator JSON: %v", err))
	}
	return raw
}
