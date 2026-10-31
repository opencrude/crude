//go:build windows

package workspace

import "os"

func openProjectArtifact(root *os.Root, path string) (*os.File, error) {
	return root.Open(path)
}
