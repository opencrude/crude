// Copyright 2025 OpenAI
// SPDX-License-Identifier: Apache-2.0
// Ported for Crude from codex-rs/apply-patch/src/file_update.rs at aa380897f67b.
package codexio

import "strings"

// applyNormalized preserves the upstream NormalizeToLf algorithm, including
// its trailing-empty-line sentinel and reverse-order replacement application.
func applyNormalized(contents string, chunks []*codexChunk, path string) (string, error) {
	lines := strings.Split(contents, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	replacements, err := codexReplacements(lines, chunks, path, NormalizeToLf)
	if err != nil {
		return "", err
	}
	for i := len(replacements) - 1; i >= 0; i-- {
		r := replacements[i]
		end := min(r.start+r.oldLen, len(lines))
		next := make([]string, 0, len(lines)-(end-r.start)+len(r.lines))
		next = append(next, lines[:r.start]...)
		next = append(next, r.lines...)
		next = append(next, lines[end:]...)
		lines = next
	}
	if len(lines) == 0 || lines[len(lines)-1] != "" {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n"), nil
}
