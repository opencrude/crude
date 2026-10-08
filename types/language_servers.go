package types

import (
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// LSPLanguage describes a built-in language server the settings UI can
// enable, disable, or retarget.
type LSPLanguage struct {
	ID          string
	Name        string
	Exts        []string
	DefaultCmd  string
	RootMarkers []string
	DefaultArgs []string
}

// DefaultLSPLanguages is the catalog of servers Crude knows how to start.
func DefaultLSPLanguages() []LSPLanguage {
	return []LSPLanguage{
		{ID: "go", Name: "Go (gopls)", Exts: []string{".go"}, RootMarkers: []string{"go.work", "go.mod"}, DefaultCmd: "gopls"},
		{ID: "rust", Name: "Rust (rust-analyzer)", Exts: []string{".rs"}, RootMarkers: []string{"Cargo.toml"}, DefaultCmd: "rust-analyzer"},
		{ID: "python", Name: "Python (pyright)", Exts: []string{".py"}, RootMarkers: []string{"pyproject.toml", "pyrightconfig.json", "setup.py", "setup.cfg"}, DefaultCmd: "pyright-langserver", DefaultArgs: []string{"--stdio"}},
		{ID: "typescript", Name: "TypeScript / JavaScript", Exts: []string{".ts", ".js", ".tsx", ".jsx", ".mjs", ".cjs", ".mts", ".cts"}, RootMarkers: []string{"tsconfig.json", "jsconfig.json", "package.json"}, DefaultCmd: "typescript-language-server", DefaultArgs: []string{"--stdio"}},
		{ID: "html", Name: "HTML", Exts: []string{".html", ".htm"}, DefaultCmd: "vscode-html-language-server", DefaultArgs: []string{"--stdio"}},
		{ID: "css", Name: "CSS / SCSS / Less", Exts: []string{".css", ".scss", ".less"}, DefaultCmd: "vscode-css-language-server", DefaultArgs: []string{"--stdio"}},
		{ID: "json", Name: "JSON", Exts: []string{".json"}, DefaultCmd: "vscode-json-language-server", DefaultArgs: []string{"--stdio"}},
		{ID: "cpp", Name: "C / C++ (clangd)", Exts: []string{".c", ".h", ".cpp", ".hpp", ".cc", ".hh", ".cxx", ".hxx", ".c++", ".h++"}, RootMarkers: []string{"compile_commands.json", ".clangd", "CMakeLists.txt"}, DefaultCmd: "clangd"},
		{ID: "csharp", Name: "C# (csharp-ls / Roslyn)", Exts: []string{".cs"}, RootMarkers: []string{"*.sln", "*.slnx", "*.csproj"}, DefaultCmd: "csharp-ls"},
		{ID: "zig", Name: "Zig (zls)", Exts: []string{".zig"}, RootMarkers: []string{"build.zig", "zls.json"}, DefaultCmd: "zls"},
		{ID: "odin", Name: "Odin (ols)", Exts: []string{".odin"}, DefaultCmd: "ols"},
		{ID: "lua", Name: "Lua", Exts: []string{".lua"}, DefaultCmd: "lua-language-server"},
	}
}

// languageForExtension has one deterministic extension authority. Validation
// rejects collisions, including custom languages shadowing built-in extensions.
func (s LSPSettings) languageForExtension(ext string) (string, bool) {
	if id := languageIDForExt(ext); id != "" {
		return id, true
	}
	for id, cfg := range s.Languages {
		for _, candidate := range cfg.Extensions {
			if candidate == ext {
				return id, true
			}
		}
	}
	return "", false
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
	extensions := map[string]string{}
	for _, lang := range DefaultLSPLanguages() {
		for _, ext := range lang.Exts {
			extensions[ext] = lang.ID
		}
	}
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
		if languageByID(id).ID == "" && (len(cfg.Extensions) == 0 || cfg.LanguageID == "" || strings.TrimSpace(cfg.Command) == "") {
			return fmt.Errorf("custom language %s needs extensions, language_id and command", id)
		}
		for _, ext := range cfg.Extensions {
			if !validLSPExtension(ext) {
				return fmt.Errorf("%s: extension %q must be lowercase and start with a dot", id, ext)
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
	for ext := range s.Servers {
		if !validLSPExtension(ext) {
			return fmt.Errorf("invalid server extension %q", ext)
		}
	}
	for ext := range s.ServerArgs {
		if !validLSPExtension(ext) {
			return fmt.Errorf("invalid server_args extension %q", ext)
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

// applyServerHints supplies conservative soft guidance only to known executables.
// Explicit environment/configuration/arguments always win; these aren't RSS caps.
func applyServerHints(spec *LSPServerSpec, cfg LSPSettings) {
	threads, memory := cfg.ServerThreads, cfg.ServerMemoryMB
	if threads == 0 {
		threads = 2
	}
	if memory == 0 {
		memory = 1024
	}
	command := strings.TrimSuffix(strings.ToLower(filepath.Base(spec.Command)), ".exe")
	switch command {
	case "gopls":
		// Compiler/type diagnostics stay live; whole-dependency Staticcheck fact
		// construction is opt-in. Unset enables heavyweight subsets in newer
		// gopls, which can monopolize two cores on a cold large workspace.
		spec.Settings = mergeJSONDefaults(spec.Settings, map[string]any{"gopls": map[string]any{"staticcheck": false, "diagnosticsDelay": "1s"}})
		if spec.Environment == nil {
			spec.Environment = map[string]string{}
		}
		if _, ok := spec.Environment["GOMAXPROCS"]; !ok {
			spec.Environment["GOMAXPROCS"] = strconv.Itoa(threads)
		}
		if _, ok := spec.Environment["GOMEMLIMIT"]; !ok {
			spec.Environment["GOMEMLIMIT"] = strconv.Itoa(memory) + "MiB"
		}
	case "clangd":
		hasJobs := false
		for _, arg := range spec.Args {
			if arg == "-j" || strings.HasPrefix(arg, "-j=") || (strings.HasPrefix(arg, "-j") && len(arg) > 2) {
				hasJobs = true
			}
		}
		if !hasJobs {
			spec.Args = append(spec.Args, "-j="+strconv.Itoa(threads))
		}
	case "rust-analyzer":
		// Cargo's documented environment budget covers build scripts/checks
		// without disabling either. Cargo CLI/config overrides still win.
		if spec.Environment == nil {
			spec.Environment = map[string]string{}
		}
		if _, exists := spec.Environment["CARGO_BUILD_JOBS"]; !exists {
			spec.Environment["CARGO_BUILD_JOBS"] = strconv.Itoa(threads)
		}
		defaults := map[string]any{"numThreads": threads, "cachePriming": map[string]any{"numThreads": threads}}
		// didChangeConfiguration wraps rust-analyzer options; initialization uses
		// the unwrapped object documented by rust-analyzer.
		effective := mergeJSONDefaults(cloneJSONObject(spec.Settings), map[string]any{"rust-analyzer": defaults})
		if options, ok := effective["rust-analyzer"].(map[string]any); ok {
			spec.InitializationOptions = mergeJSONDefaults(spec.InitializationOptions, options)
		}
		spec.InitializationOptions = mergeJSONDefaults(spec.InitializationOptions, defaults)
		// Keep startup and later configuration consistent: an explicit init
		// option must not be overwritten by our default notification.
		effective["rust-analyzer"] = cloneJSONObject(spec.InitializationOptions)
		spec.Settings = effective
	case "pyright-langserver", "basedpyright-langserver":
		name := "python"
		if command == "basedpyright-langserver" {
			name = "basedpyright"
		}
		spec.Settings = mergeJSONDefaults(spec.Settings, map[string]any{name: map[string]any{"analysis": map[string]any{"diagnosticMode": "openFilesOnly"}}})
	case "typescript-language-server":
		spec.InitializationOptions = mergeJSONDefaults(spec.InitializationOptions, map[string]any{"maxTsServerMemory": memory, "tsserver": map[string]any{"useSyntaxServer": "never"}})
	}
}

func mergeJSONDefaults(explicit, defaults map[string]any) map[string]any {
	out := cloneJSONObject(explicit)
	if out == nil {
		out = map[string]any{}
	}
	for key, value := range defaults {
		current, exists := out[key]
		if !exists {
			out[key] = cloneJSONValue(value)
			continue
		}
		a, aok := current.(map[string]any)
		b, bok := value.(map[string]any)
		if aok && bok {
			out[key] = mergeJSONDefaults(a, b)
		}
	}
	return out
}
