package setting

import (
	"crude/insight/catalog"
	"crude/types/jsondoc"
	"path/filepath"
	"strings"
)

// LSPServerSpec is the resolved launch contract shared by workspace and transport.
type LSPServerSpec struct {
	LanguageID string
	// ServerID is the catalog server, empty for a custom command.
	ServerID string
	// DiagnosticScope is the catalog's declared scope: "project" when the
	// server reports files that are not open.
	DiagnosticScope string
	Command         string
	Args            []string
	RootMarkers     []string
	// WorkspaceRootMarkers win over nearer RootMarkers (for example a
	// workspace file above a module file).
	WorkspaceRootMarkers  []string
	Environment           map[string]string
	Settings              jsondoc.Object
	ConfigurationScopes   []LSPConfigurationScope
	InitializationOptions jsondoc.Object
}

// LSPConfigurationScope is a complete effective object for a directory URI.
// The project-context owner resolves it; it is not a sparse preference overlay,
// an execution grant or evidence that a complete build context was established.
type LSPConfigurationScope struct {
	URI      string
	Settings jsondoc.Object
}

// CloneLSPServerSpec returns a detached copy.
func CloneLSPServerSpec(spec LSPServerSpec) LSPServerSpec {
	spec.Args = append([]string(nil), spec.Args...)
	spec.RootMarkers = append([]string(nil), spec.RootMarkers...)
	spec.WorkspaceRootMarkers = append([]string(nil), spec.WorkspaceRootMarkers...)
	spec.Environment = cloneStringMap(spec.Environment)
	spec.Settings = cloneJSONObject(spec.Settings)
	spec.InitializationOptions = cloneJSONObject(spec.InitializationOptions)
	scopes := spec.ConfigurationScopes
	spec.ConfigurationScopes = nil
	for _, scope := range scopes {
		spec.ConfigurationScopes = append(spec.ConfigurationScopes, LSPConfigurationScope{URI: scope.URI, Settings: cloneJSONObject(scope.Settings)})
	}
	return spec
}

// ResolveServer returns detached launch settings from the catalog and the
// user's explicit choices. AutoDetect gates only catalog selection; explicit
// definitions remain usable when it is off.
func (s LSP) ResolveServer(path string) (LSPServerSpec, bool) {
	if !s.Enabled || s.Validate() != nil {
		return LSPServerSpec{}, false
	}
	id, language, builtin := s.languageForPath(path)
	if id == "" {
		return LSPServerSpec{}, false
	}
	cfg, configured := s.Languages[id]
	if configured && !cfg.Enabled {
		return LSPServerSpec{}, false
	}
	var server catalog.Server
	if builtin {
		var ok bool
		if server, ok = defaultServer(language, cfg.Command); !ok && cfg.Command == "" {
			return LSPServerSpec{}, false
		}
		first, _ := defaultServer(language, "")
		explicit := cfg.Command != "" && (cfg.Command != first.Command || cfg.Args != nil && !equalStrings(cfg.Args, first.Args))
		if !explicit && !s.AutoDetect {
			return LSPServerSpec{}, false
		}
	}
	if cfg.Command != "" {
		server.Command = cfg.Command
		if cfg.Args != nil || server.ID == "" {
			server.Args = append([]string(nil), cfg.Args...)
		}
	}
	if server.Command == "" {
		return LSPServerSpec{}, false
	}
	server.Environment = mergeStringMap(server.Environment, cfg.Environment)
	server.Settings = mergeJSONOver(server.Settings, cfg.Settings)
	server.InitializationOptions = mergeJSONOver(server.InitializationOptions, cfg.InitializationOptions)
	server = server.WithHints(s.ServerThreads, s.ServerMemoryMB)

	wireID := language.LSPLanguageID
	if cfg.LanguageID != "" {
		wireID = cfg.LanguageID
	}
	if wireID == "" {
		wireID = strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	}
	markers := language.Roots
	if cfg.RootMarkers != nil {
		markers = cfg.RootMarkers
	}
	spec := LSPServerSpec{LanguageID: wireID, ServerID: server.ID, DiagnosticScope: server.DiagnosticScope,
		Command: server.Command, Args: append([]string(nil), server.Args...),
		RootMarkers: append([]string(nil), markers...), WorkspaceRootMarkers: append([]string(nil), language.WorkspaceRoots...),
		Environment: server.Environment, Settings: server.Settings, InitializationOptions: server.InitializationOptions}
	if len(spec.Environment) == 0 {
		spec.Environment = nil
	}
	return spec, true
}

func mergeStringMap(base, top map[string]string) map[string]string {
	out := cloneStringMap(base)
	for k, v := range top {
		if out == nil {
			out = map[string]string{}
		}
		out[k] = v
	}
	return out
}

// CloneLSP returns a copy with a detached completion delay and language list.
func CloneLSP(s LSP) LSP {
	if s.CompletionDelayMS != nil {
		delay := *s.CompletionDelayMS
		s.CompletionDelayMS = &delay
	}
	s.Languages = cloneLanguages(s.Languages)
	return s
}
