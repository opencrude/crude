package tools

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/fileio"
	"crude/logger"
	"crude/textencoding"
)

func TestTextToolsPreserveEncodingWithoutSpecialArguments(t *testing.T) {
	log, err := logger.New(logger.Options{HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	for _, withEditor := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "encoded.txt")
		raw := []byte{0xff, 0xfe, 'o', 0, 'l', 0, 'd', 0, 13, 0, 10, 0}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		var editor *fileio.Editor
		if withEditor {
			editor = fileio.NewEditor("encoding", log)
		}
		for _, encoded := range []bool{false, true} {
			out := ExecuteWithJSONOptions("read_file", map[string]any{"path": path, "encoded": encoded}, nil, editor, false, log, t.Context(), nil)
			if !strings.Contains(out.Content, "UTF-16 LE") || !strings.Contains(out.Content, "old") || strings.ContainsRune(out.Content, 0) {
				t.Fatalf("tool read did not decode: %s", out.Content)
			}
		}
		out := ExecuteWithJSONOptions("edit_file", map[string]any{"path": path, "old_string": "old", "new_string": "café 😀"}, nil, editor, false, log, t.Context(), nil)
		if strings.HasPrefix(out.Content, "Error:") || strings.ContainsRune(out.Content, 0) || !strings.Contains(out.Content, "café 😀") {
			t.Fatalf("tool edit failed or rendered raw bytes: %s", out.Content)
		}
		want, _ := textencoding.EncodeText([]byte("café 😀\r\n"), textencoding.UTF16LE)
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("tool changed format or line endings: %x %v", got, err)
		}
	}
}
