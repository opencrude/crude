package workspace

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"os/exec"
	"path/filepath"
	"strings"
)

// FormatDocument formats an immutable snapshot. No formatter receives write
// access to the source path, and applying its result remains revision-checked.
func FormatDocument(ctx context.Context, path string, data []byte) ([]byte, error) {
	var command string
	var args []string
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return format.Source(data)
	case ".rs":
		command, args = "rustfmt", []string{"--emit", "stdout"}
	case ".py":
		command, args = "black", []string{"--quiet", "--stdin-filename", path, "-"}
	default:
		return data, nil
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = filepath.Dir(path)
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	result, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", command, err, strings.TrimSpace(stderr.String()))
	}
	return result, nil
}
