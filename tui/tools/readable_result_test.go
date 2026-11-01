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

// A string field carrying a JSON document (an event body, an encoded payload)
// is structure: it nests as readable fields, never paints as raw JSON. JSON
// fragments inside prose and ordinary strings stay verbatim.
func TestReadableResultNestsEmbeddedJSONStrings(t *testing.T) {
	content := `{"event":{"kind":"command_outcome","body":"Background command \"lint\" settled after 1m11s: {\"cwd\":\"/repo\",\"exit_code\":0,\"output_descriptor\":{\"status\":\"completed\"}}"},"payload":"{\"handle\":\"lint\"}","note":"use {braces} freely","command":"echo '{\"a\":1}' > x.json"}`
	result := BuildReadableResult(content)
	got := readableEvidence(result)
	for _, want := range []string{"Background command \"lint\" settled after 1m11s", "event · body", "cwd", "/repo", "exit code", "completed", "payload", "handle", "use {braces} freely", `echo '{"a":1}' > x.json`} {
		if !strings.Contains(got, want) {
			t.Fatalf("lost %q in %s", want, got)
		}
	}
	for _, raw := range []string{`{"cwd"`, `"exit_code":`, `{"handle"`} {
		if strings.Contains(got, raw) {
			t.Fatalf("embedded JSON painted raw (%q): %s", raw, got)
		}
	}
}

// A command-outcome style body: prose lead, the outcome document, then a
// trailing prose line; and the same body cut off by an upstream length bound.
func TestReadableResultEmbeddedJSONTrailingAndTruncated(t *testing.T) {
	whole := BuildReadableResult(`{"body":"Background command \"x\" settled after 3s: {\"exit_code\":0,\"cwd\":\"/repo\"}\nCommand: go test ./..."}`)
	got := readableEvidence(whole)
	for _, want := range []string{"Background command \"x\" settled after 3s", "Command: go test ./...", "exit code", "/repo"} {
		if !strings.Contains(got, want) {
			t.Fatalf("lost %q in %s", want, got)
		}
	}
	cut := BuildReadableResult(`{"body":"Background command \"x\" settled after 3s: {\"cwd\":\"/repo\",\"output\":{\"command\":\"go te"}`)
	got = readableEvidence(cut)
	if !strings.Contains(got, "Background command \"x\" settled after 3s (structured detail truncated)") || strings.Contains(got, `{"cwd"`) || strings.Contains(got, `"/repo"`) {
		t.Fatalf("truncated embedded document painted raw: %s", got)
	}
	if got := readableEvidence(BuildReadableResult(`{"note":"Summary: {not json} here"}`)); !strings.Contains(got, "Summary: {not json} here") {
		t.Fatalf("prose with braces was altered: %s", got)
	}
}
