package codexio

import (
	"errors"
	"fmt"
	"maps"
	"unicode/utf8"
)

// Mode is the upstream Codex file-update mode, not a stricter replacement
// language. The zero value matches the upstream standalone default.
type Mode int

const (
	NormalizeToLf Mode = iota
	PreserveLineEndings
)

const codexInput = "input"

func (m Mode) String() string {
	switch m {
	case NormalizeToLf:
		return "NormalizeToLf"
	case PreserveLineEndings:
		return "PreserveLineEndings"
	default:
		return fmt.Sprintf("Mode(%d)", m)
	}
}

func (m Mode) valid() bool { return m == NormalizeToLf || m == PreserveLineEndings }

// Apply applies one update body. For complete patches, use Parse and ApplyFiles.
func (m Mode) Apply(input, diff string) (string, error) {
	return m.Update(codexInput, input, diff)
}

// Update derives a file's new contents without I/O. Paths are used in upstream
// diagnostics; the host remains responsible for authorization and mutation.
func (m Mode) Update(path, input, diff string) (string, error) {
	if !m.valid() {
		return "", errors.New("unknown mode")
	}
	if !utf8.ValidString(diff) {
		return "", errors.New("invalid patch: not valid UTF-8")
	}
	chunks, err := parseCodexChunks(path, diffLines(diff))
	if err != nil {
		return "", err
	}
	return applyCodexChunks(input, chunks, path, m)
}

// ApplyBatch applies successive updates to the preceding update's output.
func (m Mode) ApplyBatch(input string, diffs []string) (string, error) {
	if !m.valid() {
		return "", errors.New("unknown mode")
	}
	output := input
	for _, diff := range diffs {
		next, err := m.Apply(output, diff)
		if err != nil {
			return "", err
		}
		output = next
	}
	return output, nil
}

// ApplyFiles applies operations sequentially to a copy. On failure it returns
// the partial state, just as Codex leaves earlier successful operations on disk.
// Map keys are literal paths; filesystem hosts resolve paths before mutation.
func (m Mode) ApplyFiles(files map[string]string, text string) (map[string]string, error) {
	output := maps.Clone(files)
	if output == nil {
		output = map[string]string{}
	}
	if !m.valid() {
		return output, errors.New("unknown mode")
	}
	patch, err := Parse(text)
	if err != nil {
		return output, err
	}
	if len(patch.Operations) == 0 {
		//nolint:staticcheck // Upstream Codex diagnostic.
		return output, errors.New("No files were modified.")
	}
	for _, op := range patch.Operations {
		switch op.Kind {
		case AddFile:
			output[op.Path] = op.Contents
		case DeleteFile:
			if _, ok := output[op.Path]; !ok {
				//nolint:staticcheck // Upstream Codex diagnostic is intentionally byte-preserved.
				return output, fmt.Errorf("Failed to delete file %s", op.Path)
			}
			delete(output, op.Path)
		case UpdateFile:
			old, ok := output[op.Path]
			if !ok {
				//nolint:staticcheck // Upstream Codex diagnostic is intentionally byte-preserved.
				return output, fmt.Errorf("Failed to read file to update %s: file not found", op.Path)
			}
			updated, err := m.Update(op.Path, old, op.Diff)
			if err != nil {
				return output, err
			}
			if op.MoveTo == "" {
				output[op.Path] = updated
			} else {
				output[op.MoveTo] = updated
				delete(output, op.Path)
			}
		}
	}
	return output, nil
}
