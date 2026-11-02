package types

import (
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"crude/insight/catalog"
)

// languageForPath returns the language of a file: a catalog language first,
// then a custom language by extension. Validation rejects a custom language
// that claims a catalog extension, so the order is unambiguous.
func (s LSPSettings) languageForPath(path string) (id string, language catalog.Language, builtin bool) {
	if language, ok := catalog.Current().LanguageForPath(path, nil); ok {
		return language.ID, language, true
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return "", catalog.Language{}, false
	}
	ids := make([]string, 0, len(s.Languages))
	for id := range s.Languages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for _, candidate := range s.Languages[id].Extensions {
			if candidate == ext {
				return id, catalog.Language{}, false
			}
		}
	}
	return "", catalog.Language{}, false
}

// LanguageForPath names the language of a file the way launch resolution
// sees it: a catalog language, or a custom language by extension.
func (s LSPSettings) LanguageForPath(path string) (id, name string, ok bool) {
	id, language, builtin := s.languageForPath(path)
	if id == "" {
		return "", "", false
	}
	name = id
	if builtin && language.Name != "" {
		name = language.Name
	}
	return id, name, true
}

// defaultServer returns the catalog server for a language: the one whose
// command matches command when it is set, otherwise the first server.
func defaultServer(language catalog.Language, command string) (catalog.Server, bool) {
	c := catalog.Current()
	for _, id := range language.Servers {
		server, ok := c.Server(id)
		if !ok {
			continue
		}
		if command == "" || filepath.Base(command) == server.Command {
			return server, true
		}
	}
	return catalog.Server{}, false
}

func cloneJSONObject(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = cloneJSONValue(value)
	}
	return out
}

func cloneJSONValue(value any) any {
	// Preserve numeric types and typed maps/slices instead of JSON round-tripping.
	if value == nil {
		return nil
	}
	return cloneJSONReflect(reflect.ValueOf(value)).Interface()
}

func cloneJSONReflect(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.New(value.Type()).Elem()
		out.Set(cloneJSONReflect(value.Elem()))
		return out
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), cloneJSONReflect(iter.Value()))
		}
		return out
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			out.Index(i).Set(cloneJSONReflect(value.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			out.Index(i).Set(cloneJSONReflect(value.Index(i)))
		}
		return out
	default:
		return value
	}
}

// Validate checks persisted configuration before any launch. Zero resource
// values mean inherit; negative and excessive values are not normalized away.
func (s LSPSettings) Validate() error {
	for _, bound := range []struct {
		name           string
		value, maximum int
	}{
		{"max_servers", s.MaxServers, 32}, {"idle_timeout_seconds", s.IdleTimeoutSeconds, 86400},
		{"max_document_bytes", s.MaxDocumentBytes, 64 * 1024 * 1024}, {"server_threads", s.ServerThreads, 64},
		{"server_memory_mb", s.ServerMemoryMB, 65536}, {"update_debounce_ms", s.UpdateDebounceMS, 60000},
	} {
		if bound.value < 0 || bound.value > bound.maximum {
			return fmt.Errorf("%s must be between 0 (inherit) and %d", bound.name, bound.maximum)
		}
	}
	switch s.MinSeverity {
	case "", "error", "warning", "info", "hint":
	default:
		return fmt.Errorf("invalid minimum severity")
	}
	c := catalog.Current()
	extensions := map[string]string{}
	ids := make([]string, 0, len(s.Languages))
	for id := range s.Languages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		cfg := s.Languages[id]
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("language identity is required")
		}
		_, builtin := c.Language(id)
		if !builtin && len(cfg.Extensions) == 0 {
			// A language the catalog does not have and no file
			// extension: the entry matches no file and cannot launch
			// anything, so it must not disable every other language.
			continue
		}
		if !builtin && (len(cfg.Extensions) == 0 || cfg.LanguageID == "" || strings.TrimSpace(cfg.Command) == "") {
			return fmt.Errorf("custom language %s needs extensions, language_id and command", id)
		}
		for _, ext := range cfg.Extensions {
			if !validLSPExtension(ext) {
				return fmt.Errorf("%s: extension %q must be lowercase and start with a dot", id, ext)
			}
			if owners := c.ExtensionOwners(ext); len(owners) > 0 && !slices.Contains(owners, id) {
				return fmt.Errorf("extension %s belongs to %s, not %s", ext, owners[0], id)
			}
			if owner, exists := extensions[ext]; exists && owner != id {
				return fmt.Errorf("extension %s belongs to %s, not %s", ext, owner, id)
			}
			extensions[ext] = id
		}
		for _, marker := range cfg.RootMarkers {
			if marker == "" || filepath.IsAbs(marker) || strings.ContainsAny(marker, "/\\\\") || strings.Contains(marker, "..") {
				return fmt.Errorf("%s: root markers must be filenames or filename globs", id)
			}
			if _, err := filepath.Match(marker, "file"); err != nil {
				return fmt.Errorf("%s: invalid root marker: %w", id, err)
			}
		}
		for name, value := range cfg.Environment {
			if name == "" || strings.ContainsAny(name, "=\\x00") || strings.ContainsRune(value, 0) {
				return fmt.Errorf("%s: invalid environment variable", id)
			}
		}
		for _, object := range []map[string]any{cfg.Settings, cfg.InitializationOptions} {
			if !validJSONValue(reflect.ValueOf(object)) {
				return fmt.Errorf("%s: settings and initialization_options must contain JSON values", id)
			}
		}
	}
	return nil
}

func validLSPExtension(ext string) bool {
	return len(ext) > 1 && ext[0] == '.' && strings.ToLower(ext) == ext && !strings.ContainsAny(ext[1:], ". /\\\\*?[]\t\r\n")
}

func validJSONValue(value reflect.Value) bool {
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return true
		}
		return validJSONValue(value.Elem())
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return false
		}
		iter := value.MapRange()
		for iter.Next() {
			if !validJSONValue(iter.Value()) {
				return false
			}
		}
		return true
	case reflect.Array, reflect.Slice:
		for i := 0; i < value.Len(); i++ {
			if !validJSONValue(value.Index(i)) {
				return false
			}
		}
		return true
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	case reflect.Float32, reflect.Float64:
		return !math.IsNaN(value.Float()) && !math.IsInf(value.Float(), 0)
	default:
		return false
	}
}

// mergeJSONOver returns base with top overlaid. Nested objects merge; any
// other value in top wins.
func mergeJSONOver(base, top map[string]any) map[string]any {
	out := cloneJSONObject(base)
	if out == nil && len(top) > 0 {
		out = map[string]any{}
	}
	for key, value := range top {
		a, aok := out[key].(map[string]any)
		b, bok := value.(map[string]any)
		if aok && bok {
			out[key] = mergeJSONOver(a, b)
			continue
		}
		out[key] = cloneJSONValue(value)
	}
	return out
}
