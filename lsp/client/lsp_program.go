package lspclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/x/powernap/pkg/lsp"

	"crude/insight/catalog"
	"crude/workspace/fileio"
)

var (
	ErrServerScript  = errors.New("language server script needs an interpreter")
	ErrServerPackage = errors.New("language server package entry point is invalid")
	ErrServerRuntime = errors.New("language server runtime is unavailable")
)

// RuntimeUnavailableError identifies the missing runtime from catalog data.
type RuntimeUnavailableError struct{ Runtime string }

func (e *RuntimeUnavailableError) Error() string { return e.Runtime + ": " + ErrServerRuntime.Error() }
func (*RuntimeUnavailableError) Unwrap() error   { return ErrServerRuntime }

// WithPackageProgram supplies already-validated catalog package metadata, not an
// installation request or execution grant. Unknown package methods stay native.
func WithPackageProgram(program catalog.PackageManagerRecipe) LSPIntegrationOption {
	return func(l *LSPIntegration) {
		program.Qualifiers = nil
		l.program = program
	}
}

// LocateServerProgram uses the launcher's environment policy and program
// resolver for read-only inventory. A hosted script keeps its own path as tool
// identity, not its shared interpreter's path. The complete launch must resolve;
// success does not establish protocol health or authorize execution.
func LocateServerProgram(ctx context.Context, command, rootURI string, explicit map[string]string, program catalog.PackageManagerRecipe) (string, error) {
	environment, err := prepareAnalyzerEnvironment(explicit, rootURI)
	if err != nil {
		return "", err
	}
	executable, prefix, err := resolveServerProgram(ctx, command, nil, environment, program)
	if err == nil && len(prefix) != 0 {
		return prefix[0], nil
	}
	return executable, err
}

func resolveServerProgram(ctx context.Context, command string, args []string, environment lsp.ProcessEnvironment, program catalog.PackageManagerRecipe) (string, []string, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if executable, err := environment.ResolveExecutable(command); err == nil {
		return executable, append([]string(nil), args...), nil
	}
	if !filepath.IsAbs(command) && (command == "." || command == ".." || strings.ContainsAny(command, `/\:`)) {
		return "", nil, &exec.Error{Name: command, Err: exec.ErrNotFound}
	}
	dirs, extensions := serverSearchPath(environment)
	if runtime.GOOS == "windows" && !filepath.IsAbs(command) && program.Kind == "npm" &&
		(program.ProgramKind == "npm" || program.ProgramKind == "node") {
		for _, dir := range dirs {
			modules := filepath.Join(dir, "node_modules")
			if strings.EqualFold(filepath.Base(dir), ".bin") && strings.EqualFold(filepath.Base(filepath.Dir(dir)), "node_modules") {
				modules = filepath.Dir(dir)
			}
			script, err := npmEntryPoint(ctx, modules, program)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				if ctx.Err() != nil {
					return "", nil, ctx.Err()
				}
				if errors.Is(err, ErrServerScript) {
					return "", nil, err
				}
				return "", nil, ErrServerPackage
			}
			// Match npm's adjacent-runtime preference, without reading its shim.
			node, err := environment.ResolveExecutable(filepath.Join(dir, "node.exe"))
			if err != nil {
				node, err = environment.ResolveExecutable("node.exe")
			}
			if err != nil {
				return "", nil, &RuntimeUnavailableError{Runtime: program.Runtime}
			}
			return node, append([]string{script}, args...), nil
		}
	}
	// Unsupported script files are installed, not missing. This classification
	// never supplies an executable or enables shell interpretation.
	if runtime.GOOS == "windows" {
		candidates := []string{command}
		if !filepath.IsAbs(command) && filepath.Base(command) == command {
			candidates = nil
			for _, dir := range dirs {
				if filepath.Ext(command) != "" {
					candidates = append(candidates, filepath.Join(dir, command))
				} else {
					for _, extension := range extensions {
						candidates = append(candidates, filepath.Join(dir, command+extension))
					}
				}
			}
		}
		for _, candidate := range candidates {
			if ext := strings.ToLower(filepath.Ext(candidate)); ext == "" || ext == ".exe" || ext == ".com" {
				continue
			}
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
				return "", nil, ErrServerScript
			}
		}
	}
	return "", nil, &exec.Error{Name: command, Err: exec.ErrNotFound}
}

func serverSearchPath(environment lsp.ProcessEnvironment) (dirs, extensions []string) {
	for _, entry := range environment.Env() {
		key, value, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "PATH":
			dirs = filepath.SplitList(value)
		case "PATHEXT":
			extensions = filepath.SplitList(value)
		}
	}
	return dirs, extensions
}

// npm's documented Windows layout places global packages in
// <PATH-prefix>/node_modules and local bins in node_modules/.bin. The manifest,
// not a parsed shell/CMD shim, identifies the installed version's entry point.
func npmEntryPoint(ctx context.Context, modules string, program catalog.PackageManagerRecipe) (string, error) {
	pkg := filepath.Join(modules, filepath.FromSlash(program.Name))
	data, err := fileio.ReadRegularNoFollow(ctx, filepath.Join(pkg, filepath.FromSlash(program.Manifest)), 1<<20)
	if err != nil {
		return "", err
	}
	var manifest struct {
		Name string          `json:"name"`
		Bin  json.RawMessage `json:"bin"`
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Name != program.Name {
		return "", ErrServerPackage
	}
	var entry string
	if program.ProgramKind == "node" {
		prefix := "node_modules/" + program.Name + "/"
		if !strings.HasPrefix(program.Program, prefix) {
			return "", ErrServerPackage
		}
		entry = strings.TrimPrefix(program.Program, prefix)
	} else if json.Unmarshal(manifest.Bin, &entry) == nil {
		if path.Base(manifest.Name) != program.Program {
			return "", ErrServerPackage
		}
	} else {
		var bins map[string]string
		if json.Unmarshal(manifest.Bin, &bins) != nil {
			return "", ErrServerPackage
		}
		entry = bins[program.Program]
	}
	if !filepath.IsLocal(entry) || strings.ContainsAny(entry, "\\:\x00") {
		return "", ErrServerPackage
	}
	root, err := os.OpenRoot(pkg)
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := fileio.OpenRootRegular(ctx, root, filepath.FromSlash(entry))
	if err != nil {
		// An installed manifest with a missing target is a broken package,
		// not permission to silently select a different installation.
		return "", ErrServerPackage
	}
	defer f.Close()
	if program.ProgramKind != "node" {
		header, err := io.ReadAll(io.LimitReader(f, 256))
		if err != nil {
			return "", ErrServerPackage
		}
		line, _, _ := bytes.Cut(header, []byte("\n"))
		line = bytes.TrimSuffix(line, []byte("\r"))
		// npm bins can use other interpreters. Only standard Node entry points
		// select Node; other scripts need explicit interpreter launch settings.
		if !bytes.Equal(line, []byte("#!/usr/bin/env node")) && !bytes.Equal(line, []byte("#!/usr/bin/node")) {
			return "", ErrServerScript
		}
	}
	return filepath.Join(pkg, filepath.FromSlash(entry)), nil
}
