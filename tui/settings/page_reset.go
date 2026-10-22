package settings

import (
	"reflect"
	"slices"
	"strings"

	"crude/types"
)

// pageOwnership is the set of stored settings paths a page edits. Several
// pages share a stored section (Memory and the Memory Agent, Security and
// the Auto Review Agent, Code Intelligence and Language Servers), so a reset
// removes exactly the page's own paths and nothing a neighbor owns.
type pageOwnership struct {
	include [][]string
	exclude [][]string
}

// autoReviewIdentity are the Auto Review Agent fields edited on Agents; the
// rest of safety.auto_permission is the Auto Review Policy page.
var autoReviewIdentity = [][]string{
	{"safety", "auto_permission", "enabled"}, {"safety", "auto_permission", "account_id"},
	{"safety", "auto_permission", "model"}, {"safety", "auto_permission", "provider"},
	{"safety", "auto_permission", "instructions"},
}

// pageOwnershipFor reports the paths a preference page owns. Collections
// (Accounts, Skills, MCP, Sources, Language Servers) and Network have no page
// reset: their items are reset or removed individually.
func pageOwnershipFor(category int, scope types.SettingsScope) (pageOwnership, bool) {
	switch category {
	case catAppearance:
		return pageOwnership{include: [][]string{{"theme"}}}, true
	case catEditor:
		return pageOwnership{include: [][]string{{"editor"}}}, true
	case catLSP:
		return pageOwnership{include: [][]string{{"lsp"}}, exclude: [][]string{{"lsp", "languages"}}}, true
	case catUI:
		return pageOwnership{include: [][]string{{"ui"}}}, true
	case catMemory:
		return pageOwnership{include: [][]string{{"memory"}}, exclude: [][]string{{"memory", "memory_category"}}}, true
	case catSafety:
		return pageOwnership{include: [][]string{{"safety"}}, exclude: [][]string{{"safety", "auto_permission"}}}, true
	case catAutoReviewPolicy:
		return pageOwnership{include: [][]string{{"safety", "auto_permission"}}, exclude: autoReviewIdentity}, true
	case catHistory:
		return pageOwnership{include: [][]string{{"local_history"}}}, true
	case catTerminal:
		return pageOwnership{include: [][]string{{"terminal"}}}, true
	case catAcquisition:
		return pageOwnership{include: [][]string{{"capabilities", "acquisition"}}}, true
	case catAgents, catMain, catResearcher, catCrafter, catAeron:
		// The application agent catalog has no reset: built-in roles are edited
		// and named agents removed individually. A workspace returns the whole
		// catalog to the application's agents.
		if scope != types.SettingsScopeWorkspace {
			return pageOwnership{}, false
		}
		include := append([][]string{{"agents"}, {"memory", "memory_category"}}, autoReviewIdentity...)
		return pageOwnership{include: include}, true
	}
	return pageOwnership{}, false
}

// owns reports whether path is one of the page's settings.
func (o pageOwnership) owns(path []string) bool {
	under := func(prefix []string) bool {
		return len(path) >= len(prefix) && slices.Equal(path[:len(prefix)], prefix)
	}
	if !slices.ContainsFunc(o.include, under) {
		return false
	}
	return !slices.ContainsFunc(o.exclude, under)
}

// resetEdits removes every stored value the page owns. Paths come from the
// settings type rather than stored values, so explicit false/zero choices are
// removed too and absent keys are harmless no-ops.
func (o pageOwnership) resetEdits() []types.SettingEdit {
	var edits []types.SettingEdit
	root := reflect.TypeFor[types.AppSettings]()
	for _, include := range o.include {
		t, ok := fieldType(root, include)
		if !ok {
			continue
		}
		edits = appendRemovals(edits, include, t, o.exclude)
	}
	return edits
}

func appendRemovals(edits []types.SettingEdit, path []string, t reflect.Type, exclude [][]string) []types.SettingEdit {
	if slices.ContainsFunc(exclude, func(e []string) bool { return slices.Equal(e, path) }) {
		return edits
	}
	nested := slices.ContainsFunc(exclude, func(e []string) bool {
		return len(e) > len(path) && slices.Equal(e[:len(path)], path)
	})
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if !nested || t.Kind() != reflect.Struct {
		return append(edits, types.SettingEdit{Path: slices.Clone(path)})
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := jsonName(f)
		if name == "" {
			continue
		}
		edits = appendRemovals(edits, append(slices.Clone(path), name), f.Type, exclude)
	}
	return edits
}

func fieldType(t reflect.Type, path []string) (reflect.Type, bool) {
	for _, key := range path {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return nil, false
		}
		found := false
		for i := 0; i < t.NumField(); i++ {
			if jsonName(t.Field(i)) == key {
				t, found = t.Field(i).Type, true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	return t, true
}

// jsonName is the field's settings path segment, matching SettingsEdits.
func jsonName(f reflect.StructField) string {
	if f.PkgPath != "" {
		return ""
	}
	name := strings.Split(f.Tag.Get("json"), ",")[0]
	if name == "-" {
		return ""
	}
	if name == "" {
		return f.Name
	}
	return name
}

// PageResetEdits returns the edits that make the selected page inherit: in
// Workspace scope it drops the page's workspace choices, in Application scope
// its explicit application choices (built-in defaults apply).
func (m *Model) PageResetEdits() ([]types.SettingEdit, bool) {
	ownership, ok := pageOwnershipFor(m.selected, m.Scope())
	if !ok {
		return nil, false
	}
	return ownership.resetEdits(), true
}

// CanResetPage reports whether the selected page has a page reset.
func (m *Model) CanResetPage() bool {
	_, ok := pageOwnershipFor(m.selected, m.Scope())
	return ok
}
