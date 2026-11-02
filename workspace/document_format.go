package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"crude/insight/catalog"
)

// FormatDocument formats an immutable snapshot with the formatter that the
// catalog names for the file's language. The formatter reads the text on
// standard input and writes the result on standard output. No formatter
// receives write access to the source path, and applying its result remains
// revision-checked. A file without a catalog formatter is returned unchanged.
func FormatDocument(ctx context.Context, path string, data []byte) ([]byte, error) {
	c := catalog.Current()
	language, ok := c.LanguageForPath(path, data[:min(len(data), 256)])
	if !ok || language.Formatter == nil {
		return data, nil
	}
	formatter := *language.Formatter
	cmd := exec.CommandContext(ctx, formatter.Command, formatter.ArgsFor(path)...)
	// The formatter runs in the file's folder when it exists, so it finds
	// project configuration. An unsaved file has no folder yet.
	if info, err := os.Stat(filepath.Dir(path)); err == nil && info.IsDir() {
		cmd.Dir = filepath.Dir(path)
	}
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	result, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", formatter.Command, err, strings.TrimSpace(stderr.String()))
	}
	return result, nil
}
