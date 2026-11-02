package catalog

import (
	"net/url"
	"runtime"
	"slices"
	"strings"
)

// CurrentTargets returns the registry platform names for this build in
// preference order. The most specific name comes first.
func CurrentTargets() []string {
	return targetsFor(runtime.GOOS, runtime.GOARCH)
}

func targetsFor(goos, goarch string) []string {
	arch := map[string]string{"amd64": "x64", "arm64": "arm64", "386": "x86", "arm": "arm"}[goarch]
	if arch == "" {
		return nil
	}
	switch goos {
	case "linux":
		// glibc is assumed. musl hosts are not detected yet.
		return []string{"linux_" + arch + "_gnu", "linux_" + arch, "linux", "unix"}
	case "darwin":
		return []string{"darwin_" + arch, "darwin", "unix"}
	case "windows":
		return []string{"win_" + arch, "win"}
	case "freebsd", "openbsd", "netbsd":
		return []string{goos + "_" + arch, "unix"}
	}
	return nil
}

// Type returns the package URL type, for example "github" or "npm".
func (s Source) Type() string {
	rest, ok := strings.CutPrefix(s.ID, "pkg:")
	if !ok {
		return ""
	}
	typ, _, _ := strings.Cut(rest, "/")
	return typ
}

// Name returns the package URL namespace and name, for example
// "owner/repo", without the version or qualifiers.
func (s Source) Name() string {
	rest, ok := strings.CutPrefix(s.ID, "pkg:")
	if !ok {
		return ""
	}
	_, rest, _ = strings.Cut(rest, "/")
	rest, _, _ = strings.Cut(rest, "?")
	rest, _, _ = strings.Cut(rest, "#")
	name, _, _ := strings.Cut(rest, "@")
	if unescaped, err := url.PathUnescape(name); err == nil {
		return unescaped
	}
	return name
}

// Version returns the version of the package URL, or "".
func (s Source) Version() string {
	_, version, ok := strings.Cut(s.ID, "@")
	if !ok {
		return ""
	}
	version, _, _ = strings.Cut(version, "?")
	version, _, _ = strings.Cut(version, "#")
	if unescaped, err := url.PathUnescape(version); err == nil {
		return unescaped
	}
	return version
}

// AssetFor returns the first asset that matches a target, tried in the
// caller's preference order, with version templates expanded. It reports
// false when no asset matches or a template cannot be expanded.
func (p Package) AssetFor(targets []string) (Asset, bool) {
	version := p.Source.Version()
	for _, target := range targets {
		for _, asset := range p.Source.Assets {
			if !slices.Contains(asset.Targets, target) {
				continue
			}
			file, okFile := expandTemplate(asset.File, version)
			bin, okBin := expandTemplate(asset.Bin, version)
			if !okFile || !okBin {
				return Asset{}, false
			}
			return Asset{Targets: slices.Clone(asset.Targets), File: file, Bin: bin}, true
		}
	}
	return Asset{}, false
}

// expandTemplate supports the registry forms {{version}} and
// {{ version | strip_prefix "v" }}. Any other template is unsupported.
func expandTemplate(value, version string) (string, bool) {
	var out strings.Builder
	for {
		start := strings.Index(value, "{{")
		if start < 0 {
			out.WriteString(value)
			return out.String(), true
		}
		end := strings.Index(value[start:], "}}")
		if end < 0 {
			return "", false
		}
		out.WriteString(value[:start])
		expression := strings.Join(strings.Fields(value[start+2:start+end]), " ")
		switch expression {
		case "version":
			out.WriteString(version)
		case `version | strip_prefix "v"`:
			out.WriteString(strings.TrimPrefix(version, "v"))
		default:
			return "", false
		}
		value = value[start+end+2:]
	}
}
