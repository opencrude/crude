package types

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"crude/agent/provider"
	captypes "crude/types/capability"
)

// accountField reports whether a settings field names an account by ID.
func accountField(field reflect.StructField) bool {
	name := strings.Split(field.Tag.Get("json"), ",")[0]
	return field.Type.Kind() == reflect.String && (name == "account_id" || name == "account")
}

// fillAccountFields sets every account field reachable from v, creating one
// entry in each map and slice on the way, so a new consumer is covered by
// this test without being named in it.
func fillAccountFields(v reflect.Value, id string, depth int) {
	if depth > 8 {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		fillAccountFields(v.Elem(), id, depth+1)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			if accountField(field) {
				v.Field(i).SetString(id)
				continue
			}
			fillAccountFields(v.Field(i), id, depth+1)
		}
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return
		}
		if v.IsNil() {
			v.Set(reflect.MakeMap(v.Type()))
		}
		elem := reflect.New(v.Type().Elem()).Elem()
		fillAccountFields(elem, id, depth+1)
		v.SetMapIndex(reflect.ValueOf("probe").Convert(v.Type().Key()), elem)
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return
		}
		elem := reflect.New(v.Type().Elem()).Elem()
		fillAccountFields(elem, id, depth+1)
		v.Set(reflect.Append(v, elem))
	}
}

func accountReferencesLeft(v reflect.Value, id, path string, found *[]string) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			accountReferencesLeft(v.Elem(), id, path, found)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			if accountField(field) && v.Field(i).String() == id {
				*found = append(*found, path+"."+field.Name)
				continue
			}
			accountReferencesLeft(v.Field(i), id, path+"."+field.Name, found)
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			accountReferencesLeft(v.MapIndex(key), id, path+"["+key.String()+"]", found)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			accountReferencesLeft(v.Index(i), id, path+"[]", found)
		}
	}
}

// Every setting that can name an account, including one added later, is
// released. Accounts themselves (and their OAuth identities) are not settings
// that use an account.
func TestReleaseAccountLeavesNoReference(t *testing.T) {
	var settings AppSettings
	value := reflect.ValueOf(&settings).Elem()
	for i := 0; i < value.NumField(); i++ {
		if value.Type().Field(i).Name == "Accounts" {
			continue
		}
		fillAccountFields(value.Field(i), "gone", 0)
	}
	var before []string
	accountReferencesLeft(value, "gone", "AppSettings", &before)
	if len(before) < 7 {
		t.Fatalf("fixture reached only %d account fields: %v", len(before), before)
	}
	uses := AccountUses(settings, "gone")
	if len(uses) == 0 || !ReleaseAccount(&settings, "gone") {
		t.Fatalf("uses = %v", uses)
	}
	var left []string
	accountReferencesLeft(value, "gone", "AppSettings", &left)
	if len(left) != 0 {
		t.Fatalf("removing the account left references: %v", left)
	}
	if ReleaseAccount(&settings, "gone") || len(AccountUses(settings, "gone")) != 0 {
		t.Fatal("a released account is still reported as used")
	}
}

func TestAccountUsesDescribeTheCleanup(t *testing.T) {
	enabled := true
	settings := AppSettings{
		Agents: AgentsSettings{
			Categories: map[string]CategorySettings{
				"default":    {AccountID: "keep", Model: "main-model"},
				"crafter":    {AccountID: "removed-account", Model: "other-model", Enabled: &enabled},
				ErrandRoleID: {AccountID: "removed-account", Model: "errand-model"},
			},
			AgentProfiles: map[string]AgentProfileSettings{
				"reviewer": {Category: "researcher", AccountID: "removed-account", Model: "other-model", Description: "Reads"},
				"builder":  {Category: "crafter", AccountID: "keep"},
			},
			Suggestions: SuggestionSettings{AccountID: "removed-account", Model: "fim", Format: SuggestionFormatDeepSeek, EndpointPath: "beta/completions", TimeoutSeconds: 4},
		},
		Memory: MemorySettings{
			MemoryCategory: CategorySettings{AccountID: "removed-account", Model: "memory-model", Instructions: "Be brief."},
			Ranker:         MemoryRankerSettings{Enabled: "on", AccountID: "removed-account", Model: "example-decider", TopK: 3},
		},
		Safety: SafetySettings{AutoPermission: AutoPermissionSettings{Enabled: "on", ThinkingEffort: "low"}},
		Capabilities: captypes.Settings{WebSearchRoutes: []captypes.WebSearchRoute{
			{ID: "r1", Account: "removed-account"}, {ID: "r2", Account: "keep", Model: "m"}, {ID: "r3", Account: "removed-account", Model: "x"},
		}},
	}
	want := []string{
		"Crafter will use Main Agent's account and model.",
		"Errand will use Main Agent's account and model.",
		`Agent "reviewer" will use Researcher's account and model.`,
		"Editor suggestions will use Errand's account and model.",
		"Memory Agent will use Errand's account and model.",
		"Memory ranking will turn off.",
		"Auto Review Agent will turn off. Crude will ask you instead.",
		"2 search methods will be removed.",
	}
	if got := AccountUses(settings, "removed-account"); !slices.Equal(got, want) {
		t.Fatalf("uses:\n got %q\nwant %q", got, want)
	}
	if settings.Agents.Categories["crafter"].AccountID != "removed-account" || len(settings.Capabilities.WebSearchRoutes) != 3 {
		t.Fatal("listing uses changed the settings")
	}
	ReleaseAccount(&settings, "removed-account")
	crafter := settings.Agents.Categories["crafter"]
	if crafter.AccountID != "" || crafter.Model != "" || crafter.Enabled == nil || !*crafter.Enabled {
		t.Fatalf("crafter = %+v", crafter)
	}
	if main := settings.Agents.Categories["default"]; main.AccountID != "keep" || main.Model != "main-model" {
		t.Fatalf("an unrelated role changed: %+v", main)
	}
	if p := settings.Agents.AgentProfiles["reviewer"]; p.AccountID != "" || p.Model != "" || p.Description != "Reads" {
		t.Fatalf("reviewer = %+v", p)
	}
	if p := settings.Agents.AgentProfiles["builder"]; p.AccountID != "keep" {
		t.Fatalf("builder = %+v", p)
	}
	if s := settings.Agents.Suggestions; s != (SuggestionSettings{TimeoutSeconds: 4}) {
		t.Fatalf("suggestions keep the removed protocol's choices: %+v", s)
	}
	if m := settings.Memory.MemoryCategory; m.AccountID != "" || m.Model != "" || m.Instructions != "Be brief." {
		t.Fatalf("memory agent = %+v", m)
	}
	if r := settings.Memory.Ranker; r.Enabled != "off" || r.AccountID != "" || r.Model != "" || r.TopK != 3 {
		t.Fatalf("ranker = %+v", r)
	}
	if a := settings.Safety.AutoPermission; a.Enabled != "off" || a.ThinkingEffort != "low" {
		t.Fatalf("auto review = %+v", a)
	}
	if routes := settings.Capabilities.WebSearchRoutes; len(routes) != 1 || routes[0].ID != "r2" {
		t.Fatalf("routes = %+v", routes)
	}
}

func TestAccountRemovalDoesNotReassignAutoReview(t *testing.T) {
	settings := AppSettings{
		Agents: AgentsSettings{Categories: map[string]CategorySettings{ErrandRoleID: {AccountID: "keep", Model: "e"}}},
		Safety: SafetySettings{AutoPermission: AutoPermissionSettings{Enabled: "on", AccountID: "removed-account", Provider: "ollama-cloud", Model: "example-decider"}},
		Memory: MemorySettings{Ranker: MemoryRankerSettings{AccountID: "removed-account", Model: "example-decider"}},
	}
	want := []string{"Memory ranking will have no account.", "Auto Review Agent will turn off. Crude will ask you instead."}
	if got := AccountUses(settings, "removed-account"); !slices.Equal(got, want) {
		t.Fatalf("uses = %q", got)
	}
	ReleaseAccount(&settings, "removed-account")
	if a := settings.Safety.AutoPermission; a.Enabled != "off" || a.AccountID != "" || a.Provider != "" || a.Model != "" {
		t.Fatalf("auto review = %+v", a)
	}
	if r := settings.Memory.Ranker; r.Enabled != "" {
		t.Fatalf("an off ranker gained an explicit choice: %+v", r)
	}
	if uses := AccountUses(settings, "missing"); len(uses) != 0 {
		t.Fatalf("an unused account has uses: %q", uses)
	}
}

func TestAccountNameNeverShowsTheID(t *testing.T) {
	accounts := map[string]provider.Account{"a1": {ID: "a1", Label: "Example account"}}
	for id, want := range map[string]string{"a1": "Example account", "d1d2d3d4e5e6f7f8": RemovedAccountName, "": ""} {
		if got := AccountName(accounts, id); got != want {
			t.Fatalf("AccountName(%q) = %q, want %q", id, got, want)
		}
	}
}
