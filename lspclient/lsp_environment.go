package lspclient

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

var errAnalyzerEnvironment = errors.New("analyzer environment could not be prepared")

const (
	maxAnalyzerEnvironmentEntries = 256
	maxAnalyzerAmbientEntries     = 4096
	maxAnalyzerPathBytes          = 32 << 10
	maxAnalyzerPathEntries        = 64
)

// analyzerEnvironment is the single ambient-inheritance policy for analyzers.
// It does not change terminal, MCP or capability-host environment contracts.
// Only OS locations, locale and toolchain locations cross this boundary by
// default. Arbitrary provider credentials, agent sockets, proxy credentials,
// loader/preload options and executable flag/configuration variables do not.
//
// Explicit environment choices belong to the human-owned launch configuration,
// not a recipe's execution grant. They are detached here and validated by the
// complete process-environment primitive before launch. A user may deliberately
// supply sensitive values; neither errors nor published facts should echo them.
// This is environment minimization, not a filesystem or network sandbox: a
// trusted analyzer still has the user's native access, including home files.
func analyzerEnvironment(ambient []string, explicit map[string]string, rootURI string) (map[string]string, error) {
	if len(ambient) > maxAnalyzerAmbientEntries || len(explicit) > maxAnalyzerEnvironmentEntries {
		return nil, errAnalyzerEnvironment
	}
	root := ""
	if rootURI != "" {
		var err error
		root, err = protocol.DocumentURI(rootURI).Path()
		if err != nil || !filepath.IsAbs(root) {
			return nil, errAnalyzerEnvironment
		}
		root = filepath.Clean(root)
	}
	result := make(map[string]string)
	ambientKeys := make(map[string]bool)
	for _, entry := range ambient {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !inheritedAnalyzerKey(key) {
			continue
		}
		key = analyzerEnvironmentKey(key)
		if ambientKeys[key] {
			return nil, errAnalyzerEnvironment
		}
		ambientKeys[key] = true
		if key == "PATH" {
			var err error
			value, err = analyzerSearchPath(value, root)
			if err != nil {
				return nil, err
			}
			if value == "" {
				continue // absent PATH cannot implicitly search the current directory
			}
		}
		result[key] = value
	}
	explicitKeys := make(map[string]bool, len(explicit))
	for key, value := range explicit {
		canonical := analyzerEnvironmentKey(key)
		if explicitKeys[canonical] {
			return nil, errAnalyzerEnvironment
		}
		explicitKeys[canonical] = true
		result[canonical] = value
	}
	return result, nil
}

func analyzerEnvironmentKey(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}

func inheritedAnalyzerKey(key string) bool {
	// Case sensitivity follows the actual process environment, not the server
	// language. These are locations only, not arbitrary tool flags or secrets.
	switch analyzerEnvironmentKey(key) {
	case "PATH", "PATHEXT", "HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH",
		"APPDATA", "LOCALAPPDATA", "SYSTEMROOT", "WINDIR",
		"PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMW6432",
		"TEMP", "TMP", "TMPDIR", "LANG", "LC_ALL", "LC_CTYPE", "TZ",
		"GOROOT", "GOPATH", "GOMODCACHE", "GOCACHE",
		"CARGO_HOME", "RUSTUP_HOME", "JAVA_HOME", "DOTNET_ROOT",
		"SDKROOT", "DEVELOPER_DIR", "VIRTUAL_ENV", "CONDA_PREFIX",
		"PNPM_HOME", "NPM_CONFIG_PREFIX":
		return true
	default:
		return false
	}
}

// Ambient PATH is data to minimize, not an implicit project-execution choice.
// Empty/relative entries and lexical workspace descendants are omitted. The
// process primitive validates the remaining count/bytes and absolute paths;
// only its resolver may select the executable, never exec.LookPath's ambient
// environment. No filesystem I/O or symlink-identity claim occurs here.
func analyzerSearchPath(value, root string) (string, error) {
	if len(value) > maxAnalyzerPathBytes || strings.Count(value, string(filepath.ListSeparator)) >= maxAnalyzerPathEntries {
		return "", errAnalyzerEnvironment
	}
	seen := make(map[string]bool)
	var paths []string
	for _, item := range filepath.SplitList(value) {
		if !filepath.IsAbs(item) {
			continue
		}
		item = filepath.Clean(item)
		if root != "" {
			relative, err := filepath.Rel(root, item)
			if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue
			}
		}
		if !seen[item] {
			seen[item] = true
			paths = append(paths, item)
		}
	}
	return strings.Join(paths, string(filepath.ListSeparator)), nil
}
