package codexio

import (
	"errors"
	"testing"
)

func TestNormalizeUpstreamBehavior(t *testing.T) {
	for _, tc := range []struct{ input, diff, want string }{
		{"a\nb\n", "@@\n-a\n+A", "A\nb\n"},
		{"a\n\n", "@@\n+tail", "a\ntail\n"},
		{"a  \nb\n", "@@\n a\n-b\n+c", "a\nc\n"},
		{"a\r\nb\r\n", "@@\n a\n-b\n+c", "a\nc\n"},
		{"a", "@@\n-a\n+b", "b\n"},
		{"", "@@\n+x", "x\n"},
		{"a\n", "@@\n-a", ""},
	} {
		got, err := NormalizeToLf.Apply(tc.input, tc.diff)
		if err != nil || got != tc.want {
			t.Fatalf("%q + %q = %q, %v; want %q", tc.input, tc.diff, got, err, tc.want)
		}
	}
}

func TestValidateEveryHunkBeforeMutating(t *testing.T) {
	input := "*** Begin Patch\n*** Add File: a\n+ok\n*** Update File: b\n@@\nbad\n*** End Patch"
	_, err := Parse(input)
	var parse *ParseError
	if !errors.As(err, &parse) || parse.Line != 6 {
		t.Fatalf("diagnostic: %v", err)
	}
	files, err := PreserveLineEndings.ApplyFiles(map[string]string{"b": "old\n"}, input)
	if err == nil || len(files) != 1 || files["b"] != "old\n" {
		t.Fatalf("syntax error applied prefix: %v, %v", files, err)
	}
}

func TestPublicPartialResultAndNoOp(t *testing.T) {
	files, err := PreserveLineEndings.ApplyFiles(nil, "*** Begin Patch\n*** Add File: a\n+ok\n*** Delete File: missing\n*** End Patch")
	if err == nil || files["a"] != "ok\n" {
		t.Fatalf("lost partial result: %v, %v", files, err)
	}
	files, err = PreserveLineEndings.ApplyFiles(files, "*** Begin Patch\n*** Update File: a\n@@\n-ok\n+ok\n*** End Patch")
	if err != nil || files["a"] != "ok\n" {
		t.Fatalf("upstream no-op rejected: %v, %v", files, err)
	}
}
