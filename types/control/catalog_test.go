package control

import (
	"encoding/json"
	"testing"
)

func TestCatalogExactDetachedDescriptors(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range Catalog("") {
		if seen[d.ID] {
			t.Fatalf("duplicate %s", d.ID)
		}
		seen[d.ID] = true
		if d.ID == "shell.control" {
			if d.Effect != "" || d.Validate() == nil {
				t.Fatal("unresolved control accepted")
			}
			continue
		}
		if f := d.Validate(); f != nil {
			t.Fatalf("%s: %v", d.ID, f)
		}
		var schema map[string]any
		if err := json.Unmarshal(d.Input.Document, &schema); err != nil {
			t.Fatal(err)
		}
		if schema["description"] == "" || schema["properties"] == nil {
			t.Fatalf("missing real schema %s", d.ID)
		}
	}
	for _, id := range []string{"settings.get", "settings.set", "settings.reset", "files.read", "shell.describe_control", "tree.reveal", "tree.filter"} {
		if !seen[id] {
			t.Fatalf("missing %s", id)
		}
	}
	d, _ := Lookup("files.read")
	d.Input.Document[0] = 'x'
	again, _ := Lookup("files.read")
	if !json.Valid(again.Input.Document) {
		t.Fatal("lookup aliases catalog")
	}
	if _, ok := Lookup("core.call"); ok {
		t.Fatal("arbitrary RPC offered")
	}
}

func TestCatalogRelevantQuery(t *testing.T) {
	got := Catalog("explicit single-field settings")
	if len(got) != 2 || got[0].ID != "settings.set" || got[1].ID != "settings.reset" {
		t.Fatalf("irrelevant catalog: %#v", got)
	}
	if len(Catalog("definitely-not-a-command")) != 0 {
		t.Fatal("unknown query returned commands")
	}
}

func TestExecutionCatalogInstalledAndExactEffects(t *testing.T) {
	for id, effect := range map[string]Effect{"execution.list": Read, "execution.integrations": Read, "execution.create": Execute, "execution.inspect": Read, "execution.review": Read, "execution.apply": Write, "execution.keep": Write, "execution.cleanup": Delete, "execution.undo": Write, "execution.recover": Write} {
		d, ok := Lookup(id)
		if !ok || !d.Availability.Available || d.Effect != effect {
			t.Fatalf("not installed %s: %#v", id, d)
		}
		var schema struct {
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(d.Input.Document, &schema); err != nil {
			t.Fatal(err)
		}
		if _, ok := schema.Properties["owner"]; ok {
			t.Fatal("caller-supplied authority offered", id)
		}
		if id == "execution.undo" || id == "execution.recover" {
			if _, ok := schema.Properties["operation_id"]; !ok {
				t.Fatal("missing operation identity", id)
			}
		}
	}
}

func TestExecutionSelectionCatalogRequiresExactSession(t *testing.T) {
	for id, effect := range map[string]Effect{"execution.selection": Read, "execution.select": Write} {
		d, ok := Lookup(id)
		if !ok || d.Effect != effect || !d.Requires.Session || d.Requires.Resource {
			t.Fatalf("selection descriptor %s: %#v", id, d)
		}
		var schema struct {
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
		}
		if err := json.Unmarshal(d.Input.Document, &schema); err != nil {
			t.Fatal(err)
		}
		if id == "execution.select" && (len(schema.Required) != 2 || schema.Properties["id"] == nil || schema.Properties["expected_revision"] == nil) {
			t.Fatal("missing exact selection preimage", schema)
		}
		if id == "execution.selection" && len(schema.Properties) != 0 {
			t.Fatal("read accepts mutation fields")
		}
	}
}

func TestCatalogMatchesNaturalPhrasesByTerms(t *testing.T) {
	results := Catalog("capability secret store")
	if len(results) == 0 || results[0].ID != "capabilities.set_secret" {
		ids := make([]string, 0, len(results))
		for _, result := range results {
			ids = append(ids, result.ID)
		}
		t.Fatalf("natural phrase did not rank capabilities.set_secret first: %v", ids)
	}
	if exact := Catalog("settings.get"); len(exact) == 0 || exact[0].ID != "settings.get" {
		t.Fatalf("exact command id did not rank first: %+v", exact)
	}
	if none := Catalog("zzzqqq"); len(none) != 0 {
		t.Fatalf("unrelated query matched %d commands", len(none))
	}
}
