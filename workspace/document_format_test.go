package workspace

import (
	"context"
	"testing"
)

func TestFormatDocumentGoUsesSnapshotWithoutDiskMutation(t *testing.T) {
	input := []byte("package main\nfunc f(){println(1)}\n")
	got, err := FormatDocument(context.Background(), "/missing/source.go", input)
	if err != nil {
		t.Fatal(err)
	}
	want := "package main\n\nfunc f() { println(1) }\n"
	if string(got) != want {
		t.Fatalf("formatted=%q want %q", got, want)
	}
}

func TestFormatDocumentUnknownLanguagePreservesExactBytes(t *testing.T) {
	input := []byte{0xef, 0xbb, 0xbf, 'a', '\r', '\n'}
	got, err := FormatDocument(context.Background(), "notes.txt", input)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(input) {
		t.Fatalf("formatted=%q want exact %q", got, input)
	}
}
