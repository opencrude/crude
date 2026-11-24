package main

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeSchemaFingerprintUsesPortableNames(t *testing.T) {
	fingerprint := func(path string, separator rune, content string) []byte {
		h := sha256.New()
		writeRuntimeSchemaInput(h, path, []byte(content), separator)
		writeRuntimeSchemaInput(h, "types/jsondoc/jsondoc.go", []byte("json"), separator)
		return h.Sum(nil)
	}
	linux := fingerprint("third_party/msgp/msgp/runtime.go", '/', "runtime")
	windows := fingerprint(`third_party\msgp\msgp\runtime.go`, '\\', "runtime")
	want := sha256.Sum256([]byte("runtime third_party/msgp/msgp/runtime.go 7\nruntime" +
		"runtime types/jsondoc/jsondoc.go 4\njson"))
	if !bytes.Equal(linux, want[:]) {
		t.Fatal("schema fingerprint did not retain the canonical names and bytes")
	}
	if !bytes.Equal(linux, windows) {
		t.Fatal("identical runtime inputs produced different schemas for Linux and Windows paths")
	}
	if bytes.Equal(linux, fingerprint("third_party/msgp/msgp/runtime.go", '/', "changed")) ||
		bytes.Equal(linux, fingerprint("third_party/msgp/msgp/other.go", '/', "runtime")) {
		t.Fatal("schema fingerprint lost runtime content or file identity")
	}
	if bytes.Equal(fingerprint(`third_party/msgp/msgp/a\b.go`, '/', "runtime"),
		fingerprint("third_party/msgp/msgp/a/b.go", '/', "runtime")) {
		t.Fatal("a literal backslash in a Unix file name lost its identity")
	}
}

func TestRepeatedGenerationKeepsUnchangedFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wire_gen.go")
	data := []byte("package exact\n")
	if err := publishGenerated(p, data); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(100000, 0)
	if err := os.Chtimes(p, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = publishGenerated(p, data); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(p)
	if err != nil || !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("unchanged codec sent a file replacement to watchers", err)
	}
	next := []byte("package changed\n")
	if err = publishGenerated(p, next); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(got, next) {
		t.Fatal("changed file did not publish", err)
	}
}
