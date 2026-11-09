package fileio

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"crude/textencoding"
)

func TestEncodedFileReadPatchWriteSearchAndPreview(t *testing.T) {
	for _, encoding := range []textencoding.Encoding{textencoding.UTF8BOM, textencoding.UTF16LE, textencoding.UTF16BE} {
		t.Run(encoding.Label(), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "encoded.txt")
			text := "café 😀\r\nfind me\r\n"
			raw, err := textencoding.EncodeText([]byte(text), encoding)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			ed := newMutationEditor(t, "encoding")
			if read, err := ed.ReadRaw(path); err != nil || read != text {
				t.Fatalf("read = %q, %v", read, err)
			}
			if read, err := ed.Read(path); err != nil || !strings.Contains(read, "café 😀") || strings.ContainsRune(read, '\x00') {
				t.Fatalf("hashline read = %q, %v", read, err)
			}
			matches, err := SearchProject(t.Context(), root, "find", ProjectSearchOptions{})
			if err != nil || len(matches) != 1 || matches[0].Line != 2 || matches[0].Column != 0 {
				t.Fatalf("search = %+v, %v", matches, err)
			}
			replacements, _, err := PlanProjectReplace(matches, "find", "found", ProjectSearchOptions{})
			if err != nil || len(replacements) != 1 || replacements[0].OldContent != string(raw) {
				t.Fatalf("replacement plan lost its byte preimage: %+v, %v", replacements, err)
			}
			decoded, selected, err := textencoding.DecodeText([]byte(replacements[0].NewContent))
			if err != nil || selected != encoding || string(decoded) != "café 😀\r\nfound me\r\n" {
				t.Fatalf("replacement lost the encoding: %q %v", decoded, err)
			}
			var receipt MutationReceipt
			prepare := func(r MutationReceipt) (MutationComplete, error) { receipt = r; return nil, nil }
			if err := ed.PatchTracked(path, []SearchReplace{{Search: "find", Replace: "found"}}, prepare); err != nil {
				t.Fatal(err)
			}
			if len(receipt.Paths) != 1 || !bytes.Equal(receipt.Paths[0].Before.Bytes, raw) {
				t.Fatal("text patch altered the retained raw preimage")
			}
			if err := ed.Write(path, "next é😀\r\n"); err != nil {
				t.Fatal(err)
			}
			raw, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			decoded, selected, err = textencoding.DecodeText(raw)
			if err != nil || selected != encoding || string(decoded) != "next é😀\r\n" {
				t.Fatalf("full write changed encoding or text: %q, %q, %v", decoded, selected, err)
			}
			for budget := int64(1); budget < 14; budget++ {
				preview, err := InspectFile(path, budget, 16)
				if err != nil || preview.Binary || !utf8.ValidString(preview.Text) ||
					!strings.HasPrefix("next é😀\r\n", preview.Text) || int64(len(preview.Text)) > budget {
					t.Fatalf("preview split a character at %d: %+v, %v", budget, preview, err)
				}
			}
		})
	}
}

func TestGrepContinuesPastUnsupportedAndMalformedText(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		"a-utf32.txt":  {0xff, 0xfe, 0, 0, 'A', 0, 0, 0},
		"b-broken.txt": []byte("match\n\xff"),
		"c-good.txt":   []byte("match café\n"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	matches, err := NewLocalExec().GrepFiles("match", root, true, false)
	if err != nil || len(matches) != 1 || filepath.Base(matches[0].Path) != "c-good.txt" || matches[0].Line != "match café" {
		t.Fatalf("one unreadable file disrupted search: %+v, %v", matches, err)
	}
}
