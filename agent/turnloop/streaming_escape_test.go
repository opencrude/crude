package turnloop

import (
	"strings"
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

// The display owner's command facts must come from decoded characters, not
// the argument wire. Incomplete escape tokens must not publish a new frame
// with the entire decoded command suddenly replaced by escaped JSON.
func TestStreamedCommandDoesNotRedrawForAnUnfinishedEscape(t *testing.T) {
	for _, tool := range []struct{ name, key string }{
		{"execute_command", "command"}, {"async_execute_command", "command"}, {"exec_command", "cmd"},
	} {
		t.Run(tool.name, func(t *testing.T) {
			var operands []string
			delta := (&Agent{}).makeStreamingDeltaCallback(func(evt AgentEvent) {
				if command, ok := evt.Data.(map[string]any)["command"].(string); ok {
					operands = append(operands, command)
				}
			}, make(map[string]string))
			base := `{"` + tool.key + `":"python - <<'PY'\n\tprint(\"done\")\nPY`
			send := func(suffix string) { delta("row", "call", tool.name, base+suffix) }
			send("")
			for _, unfinished := range []string{`\`, `\u`, `\u00`, `\ud83d`, `\ud83d\`, `\ud83d\ude0`} {
				send(unfinished)
			}
			baseText := "python - <<'PY'\n\tprint(\"done\")\nPY"
			if len(operands) != 1 || operands[0] != baseText {
				t.Fatalf("partial tokens changed the decoded operand: %q", operands)
			}
			send(`\u0026\u0026 echo \ud83d\ude00`)
			send(`\u0026\u0026 echo \ud83d\ude00"}`)
			if len(operands) != 2 || operands[1] != baseText+"&& echo 😀" {
				t.Fatalf("accepted characters did not continue streaming: %q", operands)
			}
		})
	}
}

// The live command title is the user-visible acceptance point, so it belongs
// with the workspace conversation that owns the row. See
// workspace/agent_event_escape_test.go.

func BenchmarkLongCommandPreview(b *testing.B) {
	base := `{"command":"python - <<'PY'\n` + strings.Repeat(`\tprint(\"escaped text\")\n`, 512)
	for _, tc := range []struct{ name, suffix string }{{"complete escape", `\u0026`}, {"unfinished escape", `\u00`}} {
		b.Run(tc.name, func(b *testing.B) {
			input := base + tc.suffix
			b.ReportAllocs()
			for b.Loop() {
				ExtractPreview("call", "execute_command", input, nil)
			}
		})
	}
}
