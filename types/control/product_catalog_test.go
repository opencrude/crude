package control

import (
	"encoding/json"
	"testing"
)

func TestProductCatalogClosedSemanticEffects(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range ProductCommands() {
		if seen[d.ID] || d.HumanOnly || d.Scope != Shell || d.Effect == "" || d.Effect == Authority {
			t.Fatalf("invalid product descriptor: %+v", d)
		}
		seen[d.ID] = true
		if d.ID == "composer.send" && (d.Effect != Communication || d.Availability.Reason != "") {
			t.Fatal("semantic Send not available as communication", d)
		}
		var schema map[string]any
		if err := json.Unmarshal(d.Input.Document, &schema); err != nil {
			t.Fatal(err)
		}
		if schema["additionalProperties"] != false {
			t.Fatal("open input", d.ID)
		}
		if d.Effect != Read && (!d.Requires.Revision || !d.Requires.OpenEpoch) {
			t.Fatal("unfenced mutation", d.ID)
		}
	}
	commands := ProductCommands()
	commands[0].Input.Document[0] = 'x'
	if !json.Valid(ProductCommands()[0].Input.Document) {
		t.Fatal("shared catalog bytes")
	}
}
