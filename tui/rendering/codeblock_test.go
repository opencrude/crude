package rendering

import "testing"

func TestExtractLastCodeBlock(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantLang  string
		wantCode  string
		wantOK    bool
	}{
		{
			name:     "empty",
			input:    "",
			wantOK:   false,
		},
		{
			name:     "no fences",
			input:    "just some plain text\nwith newlines",
			wantOK:   false,
		},
		{
			name: "single block with language",
			input: "Here is some code:\n```go\nfunc main() {}\n```\nDone.",
			wantLang: "go",
			wantCode: "func main() {}",
			wantOK:   true,
		},
		{
			name: "single block no language",
			input: "```\nplain code\n```",
			wantLang: "",
			wantCode: "plain code",
			wantOK:   true,
		},
		{
			name: "multi-line block",
			input: "```python\ndef hello():\n    print('hi')\n    return 0\n```",
			wantLang: "python",
			wantCode: "def hello():\n    print('hi')\n    return 0",
			wantOK:   true,
		},
		{
			name: "last of two blocks",
			input: "```js\nconst a = 1;\n```\n\nAnd another:\n\n```rust\nfn main() {}\n```\n",
			wantLang: "rust",
			wantCode: "fn main() {}",
			wantOK:   true,
		},
		{
			name: "nested-looking but flat",
			input: "```\nsome ``` inline-looking text\n```",
			wantLang: "",
			wantCode: "some ``` inline-looking text",
			wantOK:   true,
		},
		{
			name: "opening only",
			input: "```go\nfunc broken()",
			wantOK:   false,
		},
		{
			name: "closing only",
			input: "stuff\n```",
			wantOK:   false,
		},
		{
			name:     "inline code only",
			input:    "Use the `foo()` function to do that.",
			wantOK:   false,
		},
		{
			name: "trailing newline after closing",
			input: "```\nx = 1\n```\n",
			wantLang: "",
			wantCode: "x = 1",
			wantOK:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lang, code, ok := ExtractLastCodeBlock(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (lang=%q code=%q)", ok, tt.wantOK, lang, code)
			}
			if !ok {
				return
			}
			if lang != tt.wantLang {
				t.Errorf("lang = %q, want %q", lang, tt.wantLang)
			}
			if code != tt.wantCode {
				t.Errorf("code = %q, want %q", code, tt.wantCode)
			}
		})
	}
}
