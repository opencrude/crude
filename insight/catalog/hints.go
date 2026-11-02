package catalog

import (
	"slices"
	"strconv"
	"strings"
)

const (
	placeholderThreads  = "{threads}"
	placeholderMemoryMB = "{memory_mb}"
)

// WithHints returns a copy of s with its resource hints applied. Explicit
// values in s always win: hints only fill absent environment variables,
// arguments, settings and initialization options. Zero threads or memory
// use the conservative defaults 2 and 1024.
func (s Server) WithHints(threads, memoryMB int) Server {
	if threads <= 0 {
		threads = 2
	}
	if memoryMB <= 0 {
		memoryMB = 1024
	}
	out := s
	out.Args = slices.Clone(s.Args)
	out.Environment = cloneStrings(s.Environment)
	out.Settings = cloneObject(s.Settings)
	out.InitializationOptions = cloneObject(s.InitializationOptions)
	h := s.Hints
	values := map[string]int{placeholderThreads: threads, placeholderMemoryMB: memoryMB}

	for name, value := range h.Environment {
		if out.Environment == nil {
			out.Environment = map[string]string{}
		}
		if _, exists := out.Environment[name]; !exists {
			out.Environment[name] = expandText(value, values)
		}
	}
	explicit := false
	for _, arg := range out.Args {
		for _, prefix := range h.ArgPrefixes {
			if strings.HasPrefix(arg, prefix) {
				explicit = true
			}
		}
	}
	if !explicit {
		for _, arg := range h.Args {
			out.Args = append(out.Args, expandText(arg, values))
		}
	}
	if len(h.Settings) > 0 {
		out.Settings = mergeDefaults(out.Settings, expandObject(h.Settings, values))
	}
	if section := h.InitializationFromSection; section != "" {
		// The server reads one settings section at startup as its
		// initialization options. Keep both views equal so a later
		// configuration notification cannot undo an explicit startup value.
		if effective, ok := out.Settings[section].(map[string]any); ok {
			out.InitializationOptions = mergeDefaults(out.InitializationOptions, effective)
		}
		out.Settings[section] = cloneObject(out.InitializationOptions)
	}
	if len(h.InitializationOptions) > 0 {
		out.InitializationOptions = mergeDefaults(out.InitializationOptions, expandObject(h.InitializationOptions, values))
	}
	return out
}

func expandText(value string, values map[string]int) string {
	for placeholder, n := range values {
		value = strings.ReplaceAll(value, placeholder, strconv.Itoa(n))
	}
	return value
}

func expandObject(object map[string]any, values map[string]int) map[string]any {
	out := make(map[string]any, len(object))
	for key, value := range object {
		out[key] = expandValue(value, values)
	}
	return out
}

func expandValue(value any, values map[string]int) any {
	switch v := value.(type) {
	case string:
		if n, ok := values[v]; ok {
			return n
		}
		return expandText(v, values)
	case map[string]any:
		return expandObject(v, values)
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = expandValue(v[i], values)
		}
		return out
	default:
		return v
	}
}

// mergeDefaults returns explicit plus every default key it lacks. Nested
// objects merge recursively; explicit scalars and arrays always win.
func mergeDefaults(explicit, defaults map[string]any) map[string]any {
	out := cloneObject(explicit)
	if out == nil {
		out = map[string]any{}
	}
	for key, value := range defaults {
		current, exists := out[key]
		if !exists {
			out[key] = cloneValue(value)
			continue
		}
		a, aok := current.(map[string]any)
		b, bok := value.(map[string]any)
		if aok && bok {
			out[key] = mergeDefaults(a, b)
		}
	}
	return out
}
