package control

import (
	"encoding/json"
	"testing"
)

func TestDiagnosticCommandsAreOnDemandReadOwners(t *testing.T) {
	for _, id := range []string{"performance.core", "performance.shell", "diagnostics.core", "diagnostics.workspace", "connection.inspect"} {
		d, ok := Lookup(id)
		if !ok || d.Effect != Read || d.HumanOnly || !d.Availability.Available {
			t.Fatalf("bad diagnostic descriptor %s: %+v", id, d)
		}
		if f := d.Validate(); f != nil {
			t.Fatal(id, f)
		}
		if !json.Valid(d.Input.Document) || !json.Valid(d.Output.Document) {
			t.Fatal("invalid schema", id)
		}
	}
	for _, query := range []string{"performance", "metrics", "diagnostics", "replication"} {
		if len(Catalog(query)) == 0 {
			t.Fatal("undiscoverable", query)
		}
	}
}
