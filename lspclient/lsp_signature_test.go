package lspclient

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
)

func TestSignatureProtocolNegotiationAndExactParameters(t *testing.T) {
	root := t.TempDir()
	integration := protocolIntegration(t, "signature", root)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := integration.Start(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "held-out.fixture")
	openTestFile(t, integration, ctx, path, "held-out", "join(\"a\", )")
	help, err := integration.RequestSignatureHelp(ctx, path, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(help.Signatures) != 2 || help.ActiveSignature != 0 ||
		help.Signatures[0].ActiveParameter != 1 || help.Signatures[1].ActiveParameter != 0 ||
		help.Signatures[0].Parameters[0].Label != "😀: str" ||
		help.Signatures[0].Parameters[1].Label != "tail: str" ||
		help.Signatures[0].Parameters[1].Documentation != "Remaining text" {
		t.Fatalf("wrong parameter information from framed provider: %#v", help)
	}
	trace, err := integration.RequestHover(ctx, path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var calls []map[string]any
	if err := json.Unmarshal([]byte(trace), &calls); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, call := range calls {
		if call["method"] == "initialize" {
			caps := call["params"].(map[string]any)["capabilities"].(map[string]any)["textDocument"].(map[string]any)["signatureHelp"].(map[string]any)
			info := caps["signatureInformation"].(map[string]any)
			if info["activeParameterSupport"] != true ||
				info["parameterInformation"].(map[string]any)["labelOffsetSupport"] != true ||
				caps["dynamicRegistration"] == true || caps["contextSupport"] == true {
				t.Fatal("client advertised unsupported signature help behavior", caps)
			}
		}
		if call["method"] == "textDocument/signatureHelp" {
			found = true
			position := call["params"].(map[string]any)["position"].(map[string]any)
			if position["line"] != float64(0) || position["character"] != float64(10) {
				t.Fatal("wire source position changed", position)
			}
		}
	}
	if !found {
		t.Fatal("parameter information did not use the standard provider request")
	}
}

func TestSignatureUnsupportedIsNotEmptyAndDoesNotProbe(t *testing.T) {
	root := t.TempDir()
	integration := protocolIntegration(t, "normal", root)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := integration.Start(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "main.go")
	openTestFile(t, integration, ctx, path, "go", "call()")
	if _, err := integration.RequestSignatureHelp(ctx, path, 0, 5); !errors.Is(err, lsp.ErrSignatureHelpUnsupported) {
		t.Fatal("unsupported provider became an empty successful answer", err)
	}
	trace, err := integration.RequestHover(ctx, path, 0, 0)
	if err != nil || strings.Contains(trace, "textDocument/signatureHelp") {
		t.Fatal("unsupported provider was probed", trace, err)
	}
}

func TestSignatureSupportedNullMeansNoInformation(t *testing.T) {
	root := t.TempDir()
	integration := protocolIntegration(t, "signature-empty", root)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := integration.Start(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "main.go")
	openTestFile(t, integration, ctx, path, "go", "call()")
	help, err := integration.RequestSignatureHelp(ctx, path, 0, 5)
	if err != nil || len(help.Signatures) != 0 {
		t.Fatal("valid null reply did not settle as absent information", help, err)
	}
}

func TestSignatureReplyCannotSurviveCloseReopen(t *testing.T) {
	root := t.TempDir()
	integration := protocolIntegration(t, "signature-stale", root)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := integration.Start(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "main.go")
	openTestFile(t, integration, ctx, path, "go", "call()")
	result := make(chan error, 1)
	go func() { _, err := integration.RequestSignatureHelp(ctx, path, 0, 5); result <- err }()
	waitProtocolFile(t, filepath.Join(root, "requested"))
	integration.CloseFile(ctx, path)
	openTestFile(t, integration, ctx, path, "go", "call()")
	if err := os.WriteFile(filepath.Join(root, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal("equal-text reopen retained former parameter information", err)
	}
}

func TestSignatureUTF16PositionsAndInvalidResults(t *testing.T) {
	for _, test := range []struct {
		text      string
		line, col int
		want      bool
	}{
		{"a😀b\r\nnext", 0, 3, true}, {"a😀b\r\nnext", 0, 2, false},
		{"a😀b\r\nnext", 1, 4, true}, {"a😀b\r\nnext", 1, 5, false},
		{"x", -1, 0, false}, {"x", 0, -1, false}, {"x", 2, 0, false},
	} {
		if got := signaturePosition(test.text, test.line, test.col); got != test.want {
			t.Fatal("invalid signature source position decision", test)
		}
	}
	for _, raw := range []string{
		`{}`,
		`{"signatures":null}`,
		`{"signatures":[{"label":"f(x)","parameters":[{"label":"x"}]}],"activeParameter":null}`,
		`{"signatures":[{"label":"f(x)","activeParameter":null,"parameters":[{"label":"x"}]}]}`,
		`{"signatures":[{"label":"f()","activeParameter":null}]}`,
		`{"signatures":[{"label":"f()","activeParameter":"zero"}]}`,
		`{"signatures":[{"label":"f()","activeParameter":{}}]}`,
		`{"signatures":[{"label":""}]}`,
		`{"signatures":[{"label":"f(😀)","parameters":[{"label":[2,3]}]}]}`,
		`{"signatures":[{"label":"f(😀)","parameters":[{"label":[3,4]}]}]}`,
		`{"signatures":[{"label":"f(x)","parameters":[{"label":[-1,2]}]}]}`,
		`{"signatures":[{"label":"f(x)","parameters":[{"label":[2,200]}]}]}`,
		`{"signatures":[{"label":"f(x)","parameters":[{"label":[2,2]}]}]}`,
		`{"signatures":[{"label":"f(x)","parameters":[{"label":"not-a-parameter"}]}]}`,
		`{"signatures":[{"label":"f(x)","documentation":{"kind":"html","value":"x"}}]}`,
		`{"signatures":[],"activeSignature":-1}`,
		strings.Repeat(" ", maxSignatureBytes+1),
		`{"signatures":[` + strings.Repeat(`{"label":"f()"},`, 32) + `{"label":"f()"}]}`,
		`{"signatures":[{"label":"f(x)","parameters":[` + strings.Repeat(`{"label":"x"},`, 64) + `{"label":"x"}]}]}`,
	} {
		if _, err := parseSignatureHelp([]byte(raw)); err == nil {
			t.Fatalf("invalid parameter information accepted: %.100s", raw)
		}
	}
}

func TestSignatureActiveParameterDefaultsAndOverridePresence(t *testing.T) {
	raw := `{"signatures":[{"label":"f(x,y)","parameters":[{"label":"x"},{"label":"y"}]},{"label":"g(a,b)","activeParameter":0,"parameters":[{"label":"a"},{"label":"b"}]},{"label":"none()"}],"activeSignature":99,"activeParameter":1}`
	help, err := parseSignatureHelp([]byte(raw))
	if err != nil || help.ActiveSignature != 0 || help.Signatures[0].ActiveParameter != 1 ||
		help.Signatures[1].ActiveParameter != 0 || help.Signatures[2].ActiveParameter != -1 {
		t.Fatal("optional zero/default semantics lost", help, err)
	}
}
