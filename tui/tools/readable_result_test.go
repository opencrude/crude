package tools

import (
	"strings"
	"testing"
)

func readableEvidence(result StructuredResult) string {
	var parts []string
	for _, section := range result.Sections {
		parts = append(parts, section.Title, section.Metadata, section.Body)
		for _, field := range section.Details {
			parts = append(parts, field.Label, field.Value)
		}
	}
	return strings.Join(parts, "\n")
}

func TestBuildReadableResultNestedAndDeterministic(t *testing.T) {
	content := `{"z":false,"a":"complete long evidence","nested":{"b":[{"x":"nested detail"},null],"a":2}}`
	result := BuildReadableResult(content)
	if result.Summary != "Result" || len(result.Sections) != 4 {
		t.Fatalf("result = %+v", result)
	}
	if first := result.Sections[0].Details; len(first) != 2 || first[0].Label != "a" || first[1].Label != "z" {
		t.Fatalf("root fields not sorted: %+v", first)
	}
	for _, expected := range []string{"complete long evidence", "nested detail", "nested · b · 1", "null"} {
		if !strings.Contains(readableEvidence(result), expected) {
			t.Fatalf("lost %q in %+v", expected, result)
		}
	}
}

func TestBuildReadableResultMixedPartsAndMalformed(t *testing.T) {
	result := BuildReadableResult("source text\n{\"key\":\"value\"}\n[1,2]")
	for _, expected := range []string{"source text", "key", "value", "1", "2"} {
		if !strings.Contains(readableEvidence(result), expected) {
			t.Fatalf("mixed parts lost %q: %+v", expected, result)
		}
	}
	bad := BuildReadableResult(`{"secret":"unterminated`)
	if strings.Contains(readableEvidence(bad), "secret") || !strings.Contains(readableEvidence(bad), "invalid JSON") {
		t.Fatalf("malformed structured payload exposed: %+v", bad)
	}
	prose := BuildReadableResult("{this is ordinary prose} and JSON-like text")
	if !strings.Contains(readableEvidence(prose), "{this is ordinary prose}") {
		t.Fatalf("prose lost: %+v", prose)
	}
	if BuildReadableResult("").Summary != "No result available" {
		t.Fatal("missing result should not be success")
	}
	if got := readableEvidence(BuildReadableResult(`"quoted value"`)); !strings.Contains(got, "quoted value") || strings.Contains(got, `"quoted value"`) {
		t.Fatalf("JSON scalar string not decoded: %q", got)
	}
}

func TestReadableResultPreservesControlEvidence(t *testing.T) {
	text := "source \x1b[31mred\x1b[0m\nnext"
	if got := readableEvidence(BuildReadableResult(text)); !strings.Contains(got, text) {
		t.Fatalf("source evidence changed before paint sanitization: %q", got)
	}
}
