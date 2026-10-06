package agent

import (
	"testing"
)

// A streamed tool payload carries Go's JSON HTML-escaping, so a shell "&&" is
// written "\u0026\u0026". The partial-stream fallback must decode it through the
// shared unescaper; its previous local escape switch omitted \uXXXX and printed a
// literal "u0026" into the live title.
func TestFallbackKVScanDecodesJSONEscapes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		key  string
		want string
	}{
		{"shell operators", `{"command":"cd /a \u0026\u0026 go test`, "command", "cd /a && go test"},
		{"redirect and pipe", `{"command":"go build 2\u003e\u00261 | tail`, "command", "go build 2>&1 | tail"},
		{"multibyte unicode", `{"query":"caf\u00e9 \u2013 query`, "query", "café – query"},
		{"surrogate pair", `{"query":"\ud83d\ude00 emoji`, "query", "😀 emoji"},
		{"escaped quotes", `{"command":"echo \"hi there\" | wc`, "command", `echo "hi there" | wc`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, _ := fallbackKVScan(c.raw)[c.key].(string); got != c.want {
				t.Fatalf("fallbackKVScan(%q)[%q] = %q, want %q", c.raw, c.key, got, c.want)
			}
		})
	}
}

// Text truncated mid-escape is not yet decodable; it must stay exactly as it
// arrived rather than being dropped or half-decoded, and it must never acquire a
// stray character where an escape was partially written.
func TestUnescLeavesUndecodableTextIntact(t *testing.T) {
	for _, raw := range []string{
		`trailing backslash \`,
		`partial escape \u00`,
		`unknown escape \q`,
	} {
		if got := unesc(raw); got != raw {
			t.Fatalf("unesc(%q) = %q, want it unchanged", raw, got)
		}
	}
	if got := unesc(`tab\there\nnext`); got != "tab\there\nnext" {
		t.Fatalf("unesc newline/tab = %q", got)
	}
}

// The live command title is the user-visible acceptance point, so it belongs
// with the workspace conversation that owns the row. See
// workspace/agent_event_escape_test.go.
