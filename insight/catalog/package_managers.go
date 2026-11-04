package catalog

import (
	_ "embed"
	"encoding/json"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
)

// packageManagerData describes source types, not individual tools or languages.
// It describes metadata only; it does not make a package installable.
//
//go:embed package_managers.json
var packageManagerData []byte

type packageManagerMethod struct {
	Manager      string            `json:"manager"`
	Registry     string            `json:"registry"`
	Runtime      string            `json:"runtime"`
	NameForm     string            `json:"name_form"`
	ProgramForms map[string]string `json:"program_forms"`
	Qualifiers   map[string]string `json:"qualifiers"`
	Subpath      bool              `json:"subpath"`
}

var packageManagerMethods = sync.OnceValues(func() (map[string]packageManagerMethod, error) {
	methods := make(map[string]packageManagerMethod)
	err := json.Unmarshal(packageManagerData, &methods)
	return methods, err
})

// PackageManagerRecipe is a detached description of the imported package URL
// and its selected program. It does not grant authority or establish that
// Crude can install the package, its dependencies, or its runtime.
type PackageManagerRecipe struct {
	Kind, Manager, Registry, Runtime string
	Name, Version, Subpath           string
	ProgramKind, Program             string
	Qualifiers                       map[string]string
}

var packagePart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~+-]{0,127}$`)

func safePackagePath(value string) bool {
	if value == "" || path.IsAbs(value) || path.Clean(value) != value || strings.ContainsAny(value, "\\:") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if !packagePart.MatchString(part) {
			return false
		}
	}
	return true
}

func safePackageName(value, form string) bool {
	switch form {
	case "simple":
		return packagePart.MatchString(value)
	case "path":
		return safePackagePath(value)
	case "scoped":
		if !strings.HasPrefix(value, "@") {
			return packagePart.MatchString(value)
		}
		scope, name, ok := strings.Cut(value[1:], "/")
		return ok && packagePart.MatchString(scope) && packagePart.MatchString(name)
	default:
		return false
	}
}

func safePackageQualifier(value, form string) bool {
	switch form {
	case "names":
		for _, part := range strings.Split(value, ",") {
			if !packagePart.MatchString(part) {
				return false
			}
		}
		return value != ""
	case "https":
		u, err := url.Parse(value)
		return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil &&
			u.Fragment == "" && u.RawQuery == "" && !u.ForceQuery
	default:
		return false
	}
}

// PackageManagerFor resolves only the literal catalog metadata for command.
// Unknown forms and ambiguous or unsafe identities fail closed. Availability,
// runtime versions, dependency sources and installation are separate checks.
func (p Package) PackageManagerFor(command string) (PackageManagerRecipe, bool) {
	var out PackageManagerRecipe
	if len(p.Source.ID) > 4096 {
		return out, false
	}
	u, err := url.Parse(p.Source.ID)
	if err != nil || u.Scheme != "pkg" || u.Opaque == "" || u.Host != "" || u.User != nil {
		return out, false
	}
	kind, identity, ok := strings.Cut(u.Opaque, "/")
	if !ok || strings.Count(identity, "@") != 1 {
		return out, false
	}
	methods, err := packageManagerMethods()
	if err != nil {
		return out, false
	}
	method, ok := methods[kind]
	if !ok || method.Manager == "" || method.Registry == "" || method.Runtime == "" {
		return out, false
	}
	encodedName, encodedVersion, ok := strings.Cut(identity, "@")
	if !ok {
		return out, false
	}
	name, nameErr := url.PathUnescape(encodedName)
	version, versionErr := url.PathUnescape(encodedVersion)
	if nameErr != nil || versionErr != nil || !safePackageName(name, method.NameForm) || !packagePart.MatchString(version) {
		return out, false
	}
	if u.Fragment != "" && (!method.Subpath || !safePackagePath(u.Fragment)) {
		return out, false
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return out, false
	}
	qualifiers := make(map[string]string, len(query))
	for key, values := range query {
		form, allowed := method.Qualifiers[key]
		if !allowed || len(values) != 1 || !safePackageQualifier(values[0], form) {
			return PackageManagerRecipe{}, false
		}
		qualifiers[key] = values[0]
	}
	bin, ok := p.Bin[command]
	if !ok || !packagePart.MatchString(command) {
		return out, false
	}
	programKind, program, ok := strings.Cut(bin, ":")
	if !ok {
		return out, false
	}
	switch method.ProgramForms[programKind] {
	case "name":
		if !packagePart.MatchString(program) {
			return out, false
		}
	case "path":
		if !safePackagePath(program) {
			return out, false
		}
	default:
		return out, false
	}
	return PackageManagerRecipe{
		Kind: kind, Manager: method.Manager, Registry: method.Registry, Runtime: method.Runtime,
		Name: name, Version: version, Subpath: u.Fragment, ProgramKind: programKind,
		Program: program, Qualifiers: qualifiers,
	}, true
}
